package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/features"
	"github.com/KudinovArtemilus/press-go/internal/lttb"
)

// ---------- хранилище результатов анализа ----------

// analysisStore считает анализ по таблицам признаков features*_OP1.csv.gz и держит
// результат в памяти, пока таблица (или норма) не изменилась. Полный анализ — около
// секунды, поэтому считать на каждый запрос нельзя, а держать — можно (несколько МБ).
type analysisStore struct {
	// dirs — где таблицы признаков (results у run.py) и хранимые нормы norm_OP1.json.
	// Функция, а не строка: с файлом настроек папку меняют в админке, и сервер видит
	// новую сразу, без перезапуска.
	dirs func() (features, norms string)

	mu    sync.Mutex
	cache map[string]*analysisEntry // по операции

	computed atomic.Int32 // сколько раз считали — для тестов и журнала
}

// analysisEntry — результат для одной операции. done закрывается, когда расчёт закончен:
// если пять браузеров одновременно попросят OP1, считать будем один раз, остальные подождут.
type analysisEntry struct {
	path    string
	mod     time.Time // время изменения таблицы, по которому считали
	normMod time.Time
	done    chan struct{}
	res     *analyze.Result
	err     error
	at      time.Time // когда посчитано
}

var (
	reTableOp = regexp.MustCompile(`(?i)^features.*_(OP\d+)\.csv\.gz$`)
	reOp      = regexp.MustCompile(`(?i)^OP\d+(@(dv\d+-dw\d+|rcp\d+-\d+|all))?$`) // OP3@dv860-dw895 — колесо КПС, OP1@rcp60-650 — рецепт пресса
)

// tables — по каждой операции самая свежая таблица в папке.
func (a *analysisStore) tables() (map[string]string, error) {
	dir, _ := a.dirs()
	if dir == "" {
		return nil, errors.New("папка таблиц признаков не указана — «Настройки» → «Хранение» → «Таблицы признаков»")
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("папка таблиц признаков недоступна (%s): %w", dir, err)
	}
	out := map[string]string{}
	mods := map[string]time.Time{}
	for _, e := range ents {
		m := reTableOp.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		op := strings.ToUpper(m[1])
		if info.ModTime().After(mods[op]) {
			out[op], mods[op] = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	return out, nil
}

// normPath — файл хранимой нормы: norm_OP1.json; у колеса КПС — norm_OP3@dv860-dw895.json.
func (a *analysisStore) normPath(op string) string {
	feats, norms := a.dirs()
	if norms == "" {
		norms = feats // по умолчанию нормы лежат рядом с таблицами
	}
	if norms == "" {
		return ""
	}
	return filepath.Join(norms, "norm_"+analyze.JoinOp(analyze.SplitOp(op))+".json")
}

// opKey — имя операции для кэша и файлов: OP3, OP3@dv860-dw895; «@all» — то же, что без колеса.
func opKey(op string) string {
	base, regime := analyze.SplitOp(op)
	if regime == "all" {
		regime = ""
	}
	return analyze.JoinOp(base, regime)
}

// get — результат для операции: из памяти, если таблица и норма не менялись, иначе пересчёт.
func (a *analysisStore) get(op string) (*analyze.Result, error) {
	op = opKey(op)
	base, _ := analyze.SplitOp(op)
	tabs, err := a.tables()
	if err != nil {
		return nil, err
	}
	path, ok := tabs[base]
	if !ok {
		dir, _ := a.dirs()
		return nil, fmt.Errorf("нет таблицы признаков для %s в %s", base, dir)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var normMod time.Time
	if np := a.normPath(op); np != "" {
		if ns, err := os.Stat(np); err == nil {
			normMod = ns.ModTime()
		}
	}

	a.mu.Lock()
	e := a.cache[op]
	if e == nil || e.path != path || !e.mod.Equal(st.ModTime()) || !e.normMod.Equal(normMod) {
		e = &analysisEntry{path: path, mod: st.ModTime(), normMod: normMod, done: make(chan struct{})}
		a.cache[op] = e
		go a.compute(op, e) // считает одна горутина; мьютекс на время расчёта не держим
	}
	a.mu.Unlock()

	<-e.done
	return e.res, e.err
}

func (a *analysisStore) compute(op string, e *analysisEntry) {
	a.computed.Add(1)
	defer close(e.done)
	defer func() { // паника в расчёте не должна ронять сервер
		if r := recover(); r != nil {
			e.err = fmt.Errorf("сбой анализа %s: %v", op, r)
		}
	}()
	opts := analyze.DefaultOptions()
	opts.Op, opts.Regime = analyze.SplitOp(op)
	opts.Source = e.path
	if !e.normMod.IsZero() {
		ns, err := analyze.LoadNorm(a.normPath(op))
		if err != nil {
			e.err = err
			return
		}
		opts.Norm = ns
	}
	t, err := batch.ReadCSVGz(e.path)
	if err != nil {
		e.err = err
		return
	}
	d, err := analyze.FromTable(t)
	if err != nil {
		e.err = err
		return
	}
	e.res, e.err = analyze.Run(d, opts)
	e.at = time.Now()
	if e.err == nil && opts.Regime != "" && opts.Norm == nil {
		a.autoNorm(op, d, opts, e)
	}
}

// autoNorm — у колеса набралось AutoNormCycles рабочих циклов, а нормы ещё нет: сохранить его норму
// по первым AutoNormCycles циклам и пересчитать по ней. Дальше норма не ползёт: файл не меняется,
// пока его не заменит человек (утилитой analyze --save-norm). Существующий файл не перезаписывается.
func (a *analysisStore) autoNorm(op string, d *analyze.Dataset, opts analyze.Options, e *analysisEntry) {
	p := e.res.Prod
	path := a.normPath(op)
	if p.Len() < analyze.AutoNormCycles || path == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		return
	}
	o := opts
	o.BaseFrom, o.BaseTo = p.Times[0], p.Times[analyze.AutoNormCycles-1]
	o.Note = fmt.Sprintf("авто: первые %d рабочих циклов (%s)", analyze.AutoNormCycles, analyze.RegimeLabel(opts.Regime))
	nr, err := analyze.Run(d, o)
	if err != nil {
		log.Printf("норма %s: %v", op, err)
		return
	}
	if err := nr.Norm.Save(path); err != nil {
		log.Printf("норма %s: не записать %s: %v", op, path, err)
		return
	}
	log.Printf("норма %s сохранена: %s, %s — %s, %d циклов", op, filepath.Base(path),
		nr.Norm.BaseFrom.Format("02.01 15:04"), nr.Norm.BaseTo.Format("02.01 15:04"), nr.Norm.BaseCycles)
	o = opts
	o.Norm = nr.Norm
	if res, err := analyze.Run(d, o); err == nil {
		e.res = res
	}
	if st, err := os.Stat(path); err == nil {
		a.mu.Lock()
		e.normMod = st.ModTime() // чтобы get не пересчитывал ещё раз из-за нового файла
		a.mu.Unlock()
	}
}

// ---------- JSON ----------

// num — число для JSON: NaN и ±Inf становятся null (стандартный JSON их не умеет).
type num = analyze.JF

const tf = "2006-01-02 15:04:05"

type opJSON struct {
	Op      string `json:"op"`
	File    string `json:"file"`
	Updated string `json:"updated"`
}

type unitJSON struct {
	Name     string   `json:"name"`
	Group    string   `json:"group"`
	Status   string   `json:"status"` // ok, warn, alarm
	Label    string   `json:"label"`  // НОРМА, НАБЛЮДАТЬ, ПРОБЛЕМА
	Facts    []string `json:"facts"`
	Forecast []string `json:"forecast"`
	Cause    string   `json:"cause"`
	Check    string   `json:"check"`
}

var statusKey = map[analyze.Status]string{analyze.OK: "ok", analyze.Warn: "warn", analyze.Alarm: "alarm"}

type summaryJSON struct {
	Op       string `json:"op"`
	File     string `json:"file"`
	Period   string `json:"period"`
	Computed string `json:"computed"`
	Cycles   int    `json:"cycles"`
	Prod     int    `json:"production"`
	Norm     struct {
		Stored bool   `json:"stored"`
		From   string `json:"from"`
		To     string `json:"to"`
		Cycles int    `json:"cycles"`
		Note   string `json:"note,omitempty"`
	} `json:"norm"`
	ShortData bool `json:"short_data"` // меньше недели — прогнозы предварительные
	Counts    struct {
		Alarm int `json:"alarm"`
		Warn  int `json:"warn"`
		OK    int `json:"ok"`
	} `json:"counts"`
	Units     []unitJSON `json:"units"`
	Notes     []string   `json:"notes"`
	Anomalies int        `json:"anomalies"`
	Trends    int        `json:"trends"`
	Stops     []string   `json:"stops"`
	Lost      []lostJSON `json:"signal_lost"`
	Sensors   []sensJSON `json:"sensors"`
	Dropped   []dropJSON `json:"dropped"` // выброшенные повреждённые циклы
	Damaged   int        `json:"damaged"`
}

type lostJSON struct {
	Signal    string `json:"signal"`
	Feature   string `json:"feature"`
	Name      string `json:"name"`
	Since     string `json:"since"`
	FromStart bool   `json:"from_start"`
}

type sensJSON struct {
	Feature string `json:"feature"`
	Signal  string `json:"signal"`
	Problem string `json:"problem"`
	Base    num    `json:"base"`
	Now     num    `json:"now"`
}

type dropJSON struct {
	Time  string `json:"time"`
	File  string `json:"file"`
	Feats int    `json:"feats"`
	Worst string `json:"worst"`
}

func summarize(op string, r *analyze.Result, at time.Time, human func(string) string) summaryJSON {
	var s summaryJSON
	s.Op, s.File, s.Period = op, filepath.Base(r.Opts.Source), r.Period
	s.Computed = at.Format("02.01.2006 15:04:05")
	s.Cycles, s.Prod = r.All.Len(), r.Prod.Len()
	s.Norm.Stored, s.Norm.Cycles, s.Norm.Note = r.Stored, r.Norm.BaseCycles, r.Norm.Note
	s.Norm.From, s.Norm.To = r.Norm.BaseFrom.Format(tf), r.Norm.BaseTo.Format(tf)
	s.ShortData = r.SpanH < 24*7
	for _, d := range r.Diag {
		switch d.Status {
		case analyze.Alarm:
			s.Counts.Alarm++
		case analyze.Warn:
			s.Counts.Warn++
		default:
			s.Counts.OK++
		}
		s.Units = append(s.Units, unitJSON{d.Name, d.Group, statusKey[d.Status], analyze.StatusText[d.Status],
			nonNil(d.Facts), nonNil(d.Forecast), d.Cause, d.Check})
	}
	s.Notes, s.Stops = nonNil(r.Notes), nonNil(r.StopRows)
	s.Anomalies, s.Trends = len(r.Order), len(r.Top)
	seen := map[string]bool{}
	s.Lost = []lostJSON{}
	for _, l := range r.Lost {
		k := analyze.SignalKey(l.Feature)
		if seen[k] {
			continue // один физический канал — одна строка (max/mean/min/std одного датчика)
		}
		seen[k] = true
		s.Lost = append(s.Lost, lostJSON{k, l.Feature, human(l.Feature), l.Since.Format(tf), l.FromStart})
	}
	s.Sensors = []sensJSON{}
	for _, x := range r.Sensors {
		// название — из «Настроек», если задано; « [std]» убираем, как анализ в своём описании
		s.Sensors = append(s.Sensors, sensJSON{x.Feature, strings.Replace(human(x.Feature), " [std]", "", 1), x.Problem, num(x.Base), num(x.Now)})
	}
	s.Dropped = []dropJSON{}
	for k, g := range r.Garbage {
		s.Dropped = append(s.Dropped, dropJSON{r.GarbageT[k].Format(tf), filepath.Base(strings.ReplaceAll(r.GarbageF[k], `\`, "/")), g.Feats, g.Worst})
	}
	s.Damaged = r.Damaged
	return s
}

type reasonJSON struct {
	Feature string `json:"feature"`
	Name    string `json:"name"`
	Value   num    `json:"value"`
	Rm      num    `json:"neighbours"`
	Z       num    `json:"z"`
}

type anomalyJSON struct {
	Time    string       `json:"time"`
	File    string       `json:"file"`
	Score   num          `json:"score"`
	Reasons []reasonJSON `json:"reasons"`
}

type trendJSON struct {
	Feature   string `json:"feature"`
	Name      string `json:"name"`
	Subsystem string `json:"subsystem"`
	Why       string `json:"why,omitempty"`
	Rho       num    `json:"rho"`
	Shift     num    `json:"shift_sigma"`
	Score     num    `json:"score"`
}

// featureJSON — ряд одного признака по циклам для графика: значения, медиана соседей,
// коридор нормы и аномальные точки. Время — секунды от 1970 (по часам пресса).
type featureJSON struct {
	Feature   string `json:"feature"`
	Name      string `json:"name"`
	Subsystem string `json:"subsystem"`
	Norm      struct {
		Median num `json:"median"`
		Sigma  num `json:"sigma"`
		P5     num `json:"p5"`
		P95    num `json:"p95"`
	} `json:"norm"`
	T      []float64 `json:"t"`
	V      []num     `json:"v"`
	RmT    []float64 `json:"rm_t,omitempty"`
	Rm     []num     `json:"rm,omitempty"`
	AnomT  []float64 `json:"anom_t"`
	AnomV  []num     `json:"anom_v"`
	Points int       `json:"points"` // сколько циклов было до прореживания
}

// ---------- обработчики ----------

func (s *Server) analysisOps(w http.ResponseWriter, r *http.Request) {
	tabs, err := s.an.tables()
	if err != nil {
		jsonError(w, err, http.StatusInternalServerError)
		return
	}
	var out []opJSON
	for op, p := range tabs {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		out = append(out, opJSON{op, filepath.Base(p), st.ModTime().Format("02.01.2006 15:04")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Op < out[j].Op })
	writeJSON(w, map[string]any{"ops": nonNil(out)})
}

// result — результат по операции из пути /api/analysis/{op}/…; при ошибке уже ответил клиенту.
func (s *Server) result(w http.ResponseWriter, r *http.Request) (*analyze.Result, time.Time, bool) {
	op := r.PathValue("op")
	if !reOp.MatchString(op) {
		jsonError(w, fmt.Errorf("операция %q: ждали OP1, OP2…", op), http.StatusBadRequest)
		return nil, time.Time{}, false
	}
	res, err := s.an.get(op)
	if err != nil {
		jsonError(w, err, http.StatusNotFound)
		return nil, time.Time{}, false
	}
	s.an.mu.Lock()
	at := s.an.cache[opKey(op)].at
	s.an.mu.Unlock()
	return res, at, true
}

func (s *Server) analysisSummary(w http.ResponseWriter, r *http.Request) {
	res, at, ok := s.result(w, r)
	if !ok {
		return
	}
	writeJSON(w, summarize(strings.ToUpper(r.PathValue("op")), res, at, s.human))
}

func (s *Server) analysisAnomalies(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.result(w, r)
	if !ok {
		return
	}
	limit := clampInt(r.URL.Query().Get("limit"), 100, 1, 5000)
	out := []anomalyJSON{}
	for k, i := range res.Order[:min(limit, len(res.Order))] {
		a := anomalyJSON{res.Prod.Times[i].Format(tf), filepath.Base(strings.ReplaceAll(res.Prod.File[i], `\`, "/")), num(res.Anom.Score[i]), []reasonJSON{}}
		for _, x := range res.Reasons[k] {
			a.Reasons = append(a.Reasons, reasonJSON{x.Feature, s.human(x.Feature), num(x.Value), num(x.Rm), num(x.Z)})
		}
		out = append(out, a)
	}
	writeJSON(w, map[string]any{"total": len(res.Order), "cycles": res.Prod.Len(), "anomalies": out})
}

func (s *Server) analysisTrends(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.result(w, r)
	if !ok {
		return
	}
	list := res.Top
	if r.URL.Query().Get("all") == "1" {
		list = res.Trends
	}
	out := []trendJSON{}
	for _, t := range list {
		out = append(out, trendJSON{t.Feature, s.human(t.Feature), analyze.Subsystem(t.Feature),
			analyze.Descs[t.Feature].Why, num(t.Rho), num(t.Shift), num(t.Score)})
	}
	step := "day"
	if res.Bins.Step < 24*time.Hour {
		step = "hour"
	}
	writeJSON(w, map[string]any{"step": step, "intervals": len(res.Bins.Starts), "trends": out})
}

func (s *Server) analysisFeature(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.result(w, r)
	if !ok {
		return
	}
	f := r.URL.Query().Get("f")
	x := res.Prod.Col(f)
	if x == nil {
		jsonError(w, fmt.Errorf("признака %q нет в таблице", f), http.StatusNotFound)
		return
	}
	n := clampInt(r.URL.Query().Get("n"), 1500, 200, 5000)
	t := make([]float64, res.Prod.Len())
	for i, tm := range res.Prod.Times {
		t[i] = float64(tm.Unix())
	}
	out := featureJSON{Feature: f, Name: s.human(f), Subsystem: analyze.Subsystem(f), Points: len(x), AnomT: []float64{}, AnomV: []num{}}
	out.Norm.Median = num(res.Norm.Median(f))
	if fn, ok := res.Norm.Features[f]; ok {
		out.Norm.Sigma, out.Norm.P5, out.Norm.P95 = fn.Sigma, fn.Q[1], fn.Q[3]
	} else {
		out.Norm.Sigma, out.Norm.P5, out.Norm.P95 = num(math.NaN()), num(math.NaN()), num(math.NaN())
	}
	tt, vv := lttb.Downsample(t, x, n)
	out.T, out.V = tt, toNum(vv)
	if j := indexOf(res.Anom.Features, f); j >= 0 { // инженерный: есть медиана соседей и свои аномалии
		rt, rv := lttb.Downsample(t, res.Anom.Rm[j], n)
		out.RmT, out.Rm = rt, toNum(rv)
		sig := res.SigEng[j]
		for i := range x {
			if z := math.Abs((x[i] - res.Anom.Rm[j][i]) / sig); z > res.Opts.Z && res.Anom.Flag[i] {
				out.AnomT, out.AnomV = append(out.AnomT, t[i]), append(out.AnomV, num(x[i]))
			}
		}
	}
	writeJSON(w, out)
}

func (s *Server) analysisDay(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.result(w, r)
	if !ok {
		return
	}
	days := analyze.Days(res.All)
	day := days[len(days)-1]
	if q := r.URL.Query().Get("d"); q != "" && q != "last" {
		t, err := time.Parse("2006-01-02", q)
		if err != nil {
			jsonError(w, fmt.Errorf("d=%q: ждали ГГГГ-ММ-ДД", q), http.StatusBadRequest)
			return
		}
		day = t
	}
	dr, err := res.Daily(day)
	if err != nil {
		jsonError(w, err, http.StatusNotFound)
		return
	}
	var units []unitJSON
	for _, d := range dr.Diag {
		units = append(units, unitJSON{d.Name, d.Group, statusKey[d.Status], analyze.StatusText[d.Status], nonNil(d.Facts), nonNil(d.Forecast), d.Cause, d.Check})
	}
	var dayList []string
	for _, d := range days {
		dayList = append(dayList, d.Format("2006-01-02"))
	}
	writeJSON(w, map[string]any{
		"day": dr.Day.Format("2006-01-02"), "days": dayList,
		"cycles": dr.Cycles, "production": dr.Production, "down_h": num(dr.DownH), "die_changes": dr.DieChanges,
		"norm": dr.BaseNote, "short_data": dr.Short, "units": nonNil(units), "notes": nonNil(dr.Notes), "stops": nonNil(dr.Stops),
		"anomalies": dayAnoms(dr.Anomalies, s.human), "rows": dayRows(dr.Rows, s.human), "text": dr.Text, // text — как diagnosis.txt у Python, без своих названий
	})
}

func dayAnoms(a []analyze.DayAnomaly, human func(string) string) []map[string]any {
	out := []map[string]any{}
	for _, x := range a {
		out = append(out, map[string]any{"time": x.Time.Format("15:04:05"), "file": x.File, "z": num(x.Z), "what": analyze.DayReasonText(x.Reasons, human)})
	}
	return out
}

func dayRows(rows []analyze.DayRow, human func(string) string) []map[string]string {
	out := []map[string]string{}
	for _, x := range rows {
		out = append(out, map[string]string{"mark": x.Mark, "name": human(x.Feature), "feature": x.Feature, "value": x.Value, "median": x.Median, "range": x.Range, "z": x.Z})
	}
	return out
}

// healthJSON — карта здоровья подсистем для ECharts (6.2б).
type healthJSON struct {
	Step       string   `json:"step"` // day или hour
	Subsystems []string `json:"subsystems"`
	Bins       []string `json:"bins"`
	Z          [][]num  `json:"z"`     // [подсистема][интервал], null — нет данных
	Index      []num    `json:"index"` // худшая подсистема за интервал
	AnomBins   []string `json:"anom_bins"`
	Anom       [][]int  `json:"anom"` // аномальные циклы [подсистема][интервал]
	Max        num      `json:"zmax"` // верх шкалы цвета: max(10, 98-й перцентиль)
}

func (s *Server) analysisHealth(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.result(w, r)
	if !ok {
		return
	}
	h := res.HealthMap()
	lay := "2006-01-02"
	step := "day"
	if h.Step < 24*time.Hour {
		lay, step = "2006-01-02 15:00", "hour"
	}
	out := healthJSON{Step: step, Subsystems: h.Subsystems, Index: toNum(h.Index), Anom: h.Anom}
	var all []float64
	for _, row := range h.Z {
		out.Z = append(out.Z, toNum(row))
		for _, v := range row {
			if !math.IsNaN(v) {
				all = append(all, v)
			}
		}
	}
	for _, b := range h.Bins {
		out.Bins = append(out.Bins, b.Format(lay))
	}
	for _, b := range h.AnomBins {
		out.AnomBins = append(out.AnomBins, b.Format(lay))
	}
	// как в charts.py: шкала до max(10, p98), чтобы один выброс не «выбелил» всю карту
	out.Max = num(10)
	if len(all) > 0 {
		if p := features.NanPercentile(all, 98); p > 10 {
			out.Max = num(p)
		}
	}
	writeJSON(w, out)
}

// groupsJSON — группы однотипных узлов (6.2в, charts.py раздел 3).
type groupsJSON struct {
	Step   string      `json:"step"` // day или hour
	Bins   []string    `json:"bins"`
	Groups []groupJSON `json:"groups"`
}

type groupJSON struct {
	Title string          `json:"title"`
	Note  string          `json:"note"`
	Out   int             `json:"out"` // сколько линий в последнем интервале за ±3σ — для пометки в списке
	Feats []groupFeatJSON `json:"feats"`
}

type groupFeatJSON struct {
	Feature string `json:"feature"`
	Name    string `json:"name"` // короткая подпись (Short): в группе линии различаются узлом
	Val     []num  `json:"val"`  // медиана за интервал, реальные единицы
	Z       []num  `json:"z"`    // она же в σ от нормы
	Last    num    `json:"last"` // z в последнем интервале с данными
}

func (s *Server) analysisGroups(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.result(w, r)
	if !ok {
		return
	}
	lay, step := "2006-01-02", "day"
	if res.Bins.Step < 24*time.Hour {
		lay, step = "2006-01-02 15:00", "hour"
	}
	out := groupsJSON{Step: step, Groups: []groupJSON{}}
	for _, b := range res.Bins.Starts {
		out.Bins = append(out.Bins, b.Format(lay))
	}
	for _, g := range res.GroupData() {
		gj := groupJSON{Title: g.Title, Note: g.Note}
		for k, f := range g.Feats {
			last := lastFinite(g.Z[k])
			if math.Abs(last) > 3 {
				gj.Out++
			}
			gj.Feats = append(gj.Feats, groupFeatJSON{Feature: f, Name: analyze.Short(s.human(f)),
				Val: toNum(g.Val[k]), Z: toNum(g.Z[k]), Last: num(last)})
		}
		out.Groups = append(out.Groups, gj)
	}
	writeJSON(w, out)
}

// compareJSON — 6.2г: было/стало, что меняется вместе, связи, длительность цикла.
type compareJSON struct {
	BeforeAfter []histJSON    `json:"before_after"`
	Corr        *corrJSON     `json:"corr"`
	Pairs       []pairJSON    `json:"pairs"`
	Step        string        `json:"step"`
	Cycle       []cycleBoxRow `json:"cycle"`
}

type histJSON struct {
	Feature string  `json:"feature"`
	Name    string  `json:"name"`
	Edges   []num   `json:"edges"` // границы столбиков, их на один больше, чем столбиков
	Base    []num   `json:"base"`  // доля циклов базового периода в столбике (0…1)
	Tail    []num   `json:"tail"`  // то же для последних 20%
	BaseN   int     `json:"base_n"`
	TailN   int     `json:"tail_n"`
	BaseMed num     `json:"base_med"`
	TailMed num     `json:"tail_med"`
	Clipped float64 `json:"clipped"` // доля значений за краями, собранных в крайние столбики
}

type corrJSON struct {
	Features []string `json:"features"`
	Names    []string `json:"names"` // для осей
	Full     []string `json:"full"`  // полные — для подсказки
	Rho      [][]num  `json:"rho"`
}

type pairJSON struct {
	Title string `json:"title"`
	XName string `json:"x_name"`
	YName string `json:"y_name"`
	X     []num  `json:"x"`
	Y     []num  `json:"y"`
	Days  []num  `json:"days"`
}

type cycleBoxRow struct {
	Start string `json:"start"`
	N     int    `json:"n"`
	Box   [5]num `json:"box"` // минимум, Q1, медиана, Q3, максимум
}

func (s *Server) analysisCompare(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.result(w, r)
	if !ok {
		return
	}
	out := compareJSON{Step: "day", BeforeAfter: []histJSON{}, Pairs: []pairJSON{}, Cycle: []cycleBoxRow{}}
	lay := "2006-01-02"
	if res.Bins.Step < 24*time.Hour {
		out.Step, lay = "hour", "2006-01-02 15:00"
	}
	short := func(f string) string { return analyze.Short(s.human(f)) }
	for _, b := range res.BeforeAfterTop() {
		h := histogram(b.Base, b.Tail, 30)
		h.Feature, h.Name = b.Feature, s.human(b.Feature) // полное: у «было/стало» нет группы, которая бы пояснила признак
		h.BaseN, h.TailN, h.BaseMed, h.TailMed = len(b.Base), len(b.Tail), num(b.BaseMed), num(b.TailMed)
		out.BeforeAfter = append(out.BeforeAfter, h)
	}
	if c := res.Correlations(); c != nil {
		cj := &corrJSON{Features: c.Features}
		for k, f := range c.Features {
			// на осях — полное описание (обрезается по ширине): короткие у разных признаков
			// одного узла совпадают («Центрирующий цил. прав.» — и ошибка, и скорость)
			cj.Names = append(cj.Names, s.human(f))
			cj.Full = append(cj.Full, s.human(f))
			cj.Rho = append(cj.Rho, toNum(c.Rho[k]))
		}
		out.Corr = cj
	}
	for _, p := range res.Scatter() {
		out.Pairs = append(out.Pairs, pairJSON{Title: p.Title, XName: short(p.X), YName: short(p.Y),
			X: toNum(p.XS), Y: toNum(p.YS), Days: toNum(p.Days)})
	}
	for _, b := range res.CycleDurations() {
		out.Cycle = append(out.Cycle, cycleBoxRow{Start: b.Start.Format(lay), N: b.N,
			Box: [5]num{num(b.Min), num(b.Q1), num(b.Med), num(b.Q3), num(b.Max)}})
	}
	writeJSON(w, out)
}

// histogram — общие столбики для «было» и «стало» (сравнивать можно только на одной сетке).
// Диапазон — от 0,5-го до 99,5-го процентиля обоих наборов вместе: один случайный выброс
// не сжимает весь «горб» в один столбик. Значения за краями собраны в крайние столбики,
// их доля — в Clipped. Высота столбика — доля циклов набора (как histnorm="probability").
func histogram(base, tail []float64, bins int) histJSON {
	all := append(append([]float64{}, base...), tail...)
	h := histJSON{Edges: []num{}, Base: []num{}, Tail: []num{}}
	if len(all) == 0 {
		return h
	}
	lo, hi := features.NanPercentile(all, 0.5), features.NanPercentile(all, 99.5)
	if hi <= lo { // все значения одинаковые — один столбик
		lo, hi, bins = lo-0.5, hi+0.5, 1
	}
	width := (hi - lo) / float64(bins)
	for k := 0; k <= bins; k++ {
		h.Edges = append(h.Edges, num(lo+float64(k)*width))
	}
	clipped := 0
	count := func(x []float64) []num {
		c := make([]float64, bins)
		for _, v := range x {
			k := int(math.Floor((v - lo) / width))
			if v < lo || v > hi {
				clipped++
			}
			c[min(max(k, 0), bins-1)]++
		}
		out := make([]num, bins)
		for k := range c {
			if len(x) > 0 {
				out[k] = num(c[k] / float64(len(x)))
			}
		}
		return out
	}
	h.Base, h.Tail = count(base), count(tail)
	h.Clipped = float64(clipped) / float64(len(all))
	return h
}

// preStopsJSON — 6.2д: признаки перед 12 самыми долгими остановами.
type preStopsJSON struct {
	Features []preFeatJSON `json:"features"`
	Lines    []preLineJSON `json:"lines"`
}

type preFeatJSON struct {
	Feature string `json:"feature"`
	Name    string `json:"name"`
}

type preLineJSON struct {
	Label string  `json:"label"` // «18.11 16:12 (4.3 ч)» — как в легенде Python
	Color int     `json:"color"`
	X     []num   `json:"x"` // часы до останова (≤ 0)
	Z     [][]num `json:"z"` // [признак][цикл], σ от нормы, медиана по 5 циклам
}

func (s *Server) analysisPreStops(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.result(w, r)
	if !ok {
		return
	}
	out := preStopsJSON{Features: []preFeatJSON{}, Lines: []preLineJSON{}}
	if ps := res.BeforeStops(); ps != nil {
		for _, f := range ps.Features {
			out.Features = append(out.Features, preFeatJSON{f, s.human(f)})
		}
		for _, l := range ps.Lines {
			pl := preLineJSON{Label: fmt.Sprintf("%s (%.1f ч)", l.Stop.Format("02.01 15:04"), l.Hours), Color: l.Color, X: toNum(l.X)}
			for _, z := range l.Z {
				pl.Z = append(pl.Z, toNum(z))
			}
			out.Lines = append(out.Lines, pl)
		}
	}
	writeJSON(w, out)
}

// groupHealthJSON — карта здоровья по плавкам (без h) или по заготовкам плавки h.
type groupHealthJSON struct {
	Heat       int      `json:"heat"` // −1 — карта по плавкам
	Subsystems []string `json:"subsystems"`
	Keys       []int    `json:"keys"`
	Labels     []string `json:"labels"`          // подпись столбца: время первого цикла
	Names      []string `json:"names,omitempty"` // подписи столбцов на оси (у установок штампа — время замены); пусто — номера
	Cycles     []int    `json:"cycles"`
	Anom       []int    `json:"anom"`
	Z          [][]num  `json:"z"`
	Index      []num    `json:"index"`
	ZMax       float64  `json:"zmax"`
}

// analysisHeats — /api/analysis/{op}/heats — по плавкам; ?h=67563 — по заготовкам плавки.
func (s *Server) analysisHeats(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.result(w, r)
	if !ok {
		return
	}
	out := groupHealthJSON{Heat: -1, ZMax: 10, Subsystems: []string{}, Keys: []int{}, Labels: []string{}, Cycles: []int{}, Anom: []int{}, Z: [][]num{}, Index: []num{}}
	var g *analyze.GroupHealth
	if hs := r.URL.Query().Get("h"); hs != "" {
		h, err := strconv.Atoi(hs)
		if err != nil || h < 0 {
			jsonError(w, fmt.Errorf("плавка %q: ждали число", hs), http.StatusBadRequest)
			return
		}
		out.Heat, g = h, res.PieceHealth(h)
	} else {
		g = res.HeatHealth()
	}
	if g != nil {
		heat := out.Heat
		out = groupHealthFrom(g)
		out.Heat = heat
	}
	writeJSON(w, out)
}

// groupHealthFrom — JSON карты по столбцам (плавки, заготовки, установки штампа).
// Шкала цвета — как у дневной карты (charts.py): max(10, 98-й перцентиль), чтобы одинаковый
// цвет на всех картах значил одно и то же.
func groupHealthFrom(g *analyze.GroupHealth) groupHealthJSON {
	out := groupHealthJSON{Heat: -1, ZMax: 10, Subsystems: []string{}, Keys: []int{}, Labels: []string{}, Cycles: []int{}, Anom: []int{}, Z: [][]num{}, Index: []num{}}
	if g == nil {
		return out
	}
	out.Subsystems, out.Keys, out.Cycles, out.Anom, out.Index = g.Subsystems, g.Keys, g.Cycles, g.Anom, toNum(g.Index)
	for k := range g.Keys {
		out.Labels = append(out.Labels, g.From[k].Format("02.01 15:04"))
	}
	var all []float64
	for _, row := range g.Z {
		out.Z = append(out.Z, toNum(row))
		all = append(all, row...)
	}
	if p := features.NanPercentile(all, 98); p > 10 {
		out.ZMax = p
	}
	return out
}

// analysisDies — карта «подсистема × установка штампа» (И1).
func (s *Server) analysisDies(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.result(w, r)
	if !ok {
		return
	}
	inst := res.DieInstalls()
	out := groupHealthFrom(res.DieHealth(inst))
	for _, k := range out.Keys {
		out.Names = append(out.Names, "с "+inst[k].Change.Format("02.01 15:04"))
	}
	writeJSON(w, out)
}

// lastFinite — последнее значение без NaN (NaN, если таких нет).
func lastFinite(x []float64) float64 {
	for i := len(x) - 1; i >= 0; i-- {
		if !math.IsNaN(x[i]) {
			return x[i]
		}
	}
	return math.NaN()
}

// ---------- вспомогательное ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// заголовок уже отправлен — остаётся только записать в журнал
		fmt.Fprintln(os.Stderr, "json:", err)
	}
}

func jsonError(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func toNum(x []float64) []num {
	out := make([]num, len(x))
	for i, v := range x {
		out[i] = num(v)
	}
	return out
}

// nonNil — пустой список вместо nil, чтобы в JSON было [] а не null: фронтенду не нужна лишняя проверка.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func clampInt(s string, def, lo, hi int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return min(max(v, lo), hi)
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

var errNoAnalysis = errors.New("анализ не включён: укажите папку таблиц признаков в «Настройках» (или флаг -features)")

// EnableAnalysis включает анализ по таблицам признаков из dir (results у run.py).
// normDir — папка с хранимыми нормами norm_OP1.json (пусто — норма считается, как в Python).
func (s *Server) EnableAnalysis(dir, normDir string) error {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("папка таблиц признаков недоступна: %s", dir)
	}
	s.an = &analysisStore{dirs: func() (string, string) { return dir, normDir }, cache: map[string]*analysisEntry{}}
	return nil
}

// analysisRoutes — маршруты API анализа. Без -features отвечают 503 с подсказкой.
func (s *Server) analysisRoutes(mux *http.ServeMux) {
	h := func(f http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.an == nil {
				jsonError(w, errNoAnalysis, http.StatusServiceUnavailable)
				return
			}
			f(w, r)
		}
	}
	mux.HandleFunc("GET /api/analysis", h(s.analysisOps))
	mux.HandleFunc("GET /api/analysis/{op}", h(s.analysisSummary))
	mux.HandleFunc("GET /api/analysis/{op}/anomalies", h(s.analysisAnomalies))
	mux.HandleFunc("GET /api/analysis/{op}/trends", h(s.analysisTrends))
	mux.HandleFunc("GET /api/analysis/{op}/feature", h(s.analysisFeature))
	mux.HandleFunc("GET /api/analysis/{op}/day", h(s.analysisDay))
	mux.HandleFunc("GET /api/analysis/{op}/health", h(s.analysisHealth))
	mux.HandleFunc("GET /api/analysis/{op}/groups", h(s.analysisGroups))
	mux.HandleFunc("GET /api/analysis/{op}/compare", h(s.analysisCompare))
	mux.HandleFunc("GET /api/analysis/{op}/shapes", h(s.analysisShapes))
	mux.HandleFunc("GET /api/analysis/{op}/prestops", h(s.analysisPreStops))
	mux.HandleFunc("GET /api/analysis/{op}/heats", h(s.analysisHeats))
	mux.HandleFunc("GET /api/analysis/{op}/dies", h(s.analysisDies))
}
