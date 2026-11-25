package web

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
	"github.com/KudinovArtemilus/press-go/internal/features"
)

// Страницы анализа (этап 6). Всё считается на сервере и приходит готовым HTML (HTMX);
// TypeScript нужен только для графика признака — «островок» data-analysis-chart.
//
// ISA101: цвет только у отклонений. Узел в норме — серый текст, «наблюдать» — янтарный,
// «проблема» — красный. Ни зелёных лампочек, ни цветных плашек для нормы: глаз оператора
// должен цепляться только за то, что требует внимания.

// unitFeatures — какие признаки показывать на графике по щелчку на узле.
var unitFeatures = func() map[string][]string {
	m := map[string][]string{}
	for _, u := range analyze.Units() {
		for _, in := range u.Ind {
			m[u.Name] = append(m[u.Name], in.Feat)
		}
		if u.SpikeFeat != "" {
			m[u.Name] = append(m[u.Name], u.SpikeFeat)
		}
	}
	return m
}()

type featLink struct{ Feature, Name string }

type unitView struct {
	unitJSON
	Feats []featLink
}

type anomView struct {
	Time, File, Open string // Open — путь для просмотра цикла (пусто, если файл вне папки данных)
	Score            float64
	Reasons          []reasonJSON
}

// culpritView — признак и сколько раз он «виноват» (в аномалиях или перед остановами).
type culpritView struct {
	Feature, Name, Subsystem string
	Count                    int
	Pct                      float64
}

type stopView struct {
	From, Resume string
	Hours        float64
	Before       []reasonJSON // Value — медиана 50 циклов до останова, Rm — норма
}

// regimeView — колесо (КПС) или рецепт (пресс) для выбора на странице и его норма.
type regimeView struct {
	Op, Label  string
	Cycles     int
	From, To   string
	On         bool
	Norm       string // «сохранена …» или «пока нет …»
	NormStored bool
}

func (s *Server) regimeViews(base, cur string, regs []analyze.Regime) []regimeView {
	var out []regimeView
	for _, rg := range regs {
		v := regimeView{Op: analyze.JoinOp(base, rg.Key), Label: rg.Label, Cycles: rg.Cycles,
			From: rg.From.Format("02.01 15:04"), To: rg.To.Format("02.01 15:04"), On: rg.Key == cur}
		if ns, err := analyze.LoadNorm(s.an.normPath(v.Op)); err == nil {
			v.NormStored = true
			v.Norm = fmt.Sprintf("норма сохранена: %s — %s, %d циклов", ns.BaseFrom.Format("02.01 15:04"), ns.BaseTo.Format("02.01 15:04"), ns.BaseCycles)
			if ns.Note != "" {
				v.Norm += " · " + ns.Note
			}
		} else if rg.Cycles >= analyze.AutoNormCycles {
			v.Norm = fmt.Sprintf("норма сохраняется (циклов %d ≥ %d) — обновите страницу через минуту", rg.Cycles, analyze.AutoNormCycles)
		} else {
			what := "колеса"
			if analyze.IsRecipe(rg.Key) {
				what = "рецепта"
			}
			v.Norm = fmt.Sprintf("нормы пока нет: сохранится сама, когда будет %d рабочих циклов (сейчас %d); до тех пор — первые 20%% циклов этого %s",
				analyze.AutoNormCycles, rg.Cycles, what)
		}
		out = append(out, v)
	}
	return out
}

// ensureWheelNorms — у колёс, где циклов уже хватает, а нормы ещё нет, — посчитать анализ
// колеса в фоне: при этом сохраняется его норма (analysisStore.autoNorm). Так норма появляется,
// даже если страницу этого колеса никто не открывал.
func (s *Server) ensureWheelNorms(base string, regs []analyze.Regime) {
	for _, rg := range regs {
		op := analyze.JoinOp(base, rg.Key)
		if rg.Cycles < analyze.AutoNormCycles {
			continue
		}
		if _, err := os.Stat(s.an.normPath(op)); err == nil {
			continue
		}
		go s.an.get(op) //nolint:errcheck — ошибки пишет autoNorm в журнал; одновременный второй расчёт get не запустит
	}
}

type trendView struct {
	trendJSON
	Up bool
}

// analysisPage — страница «Анализ» по операции: светофор узлов, сигналы, тренды, аномалии.
func (s *Server) analysisPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Data": s.rootsLabel(), "Enabled": s.an != nil}
	if s.an == nil {
		s.render(w, "analysis.html", data)
		return
	}
	tabs, err := s.an.tables()
	if err != nil {
		s.fail(w, err)
		return
	}
	var ops []string
	for op := range tabs {
		ops = append(ops, op)
	}
	sort.Strings(ops)
	data["Ops"] = ops
	op := r.URL.Query().Get("op")
	if op == "" && len(ops) > 0 {
		op = ops[0]
	}
	if op != "" && !reOp.MatchString(op) {
		data["Op"] = op
		data["Error"] = "операция " + op + ": ждали OP1, OP2…"
		s.render(w, "analysis.html", data)
		return
	}
	base, regime := analyze.SplitOp(op)
	data["BaseOp"] = base
	// колёса (КПС), рецепты (пресс): по умолчанию — то, что делают сейчас, со своей нормой;
	// «@all» — все вместе. У пресса с одним рецептом (OP2) выбирать нечего — всё как раньше.
	if base != "" {
		if mixed, err := s.an.get(base); err == nil && len(mixed.Regimes) > 0 &&
			!(analyze.IsRecipe(mixed.Regimes[0].Key) && len(mixed.Regimes) < 2) {
			if regime == "" {
				cur := mixed.Regimes[0]
				for _, rg := range mixed.Regimes {
					if rg.To.After(cur.To) {
						cur = rg
					}
				}
				regime = cur.Key
			}
			data["Regimes"] = s.regimeViews(base, regime, mixed.Regimes)
			data["AllOp"], data["AllOn"] = base+"@all", regime == "all"
			data["RegimeWord"], data["AllWord"] = "колесо", "все колёса вместе"
			if analyze.IsRecipe(mixed.Regimes[0].Key) {
				data["RegimeWord"], data["AllWord"], data["Recipes"] = "рецепт", "все рецепты вместе", true
			}
			s.ensureWheelNorms(base, mixed.Regimes)
		}
	}
	op = analyze.JoinOp(base, regime)
	data["Op"] = op
	data["CommonNorm"] = regime == "all" && data["Regimes"] != nil // все колёса / рецепты вместе — норма общая
	// чья операция: название машины в шапке; у машин, для которых узлы ещё не описаны
	// (всё, кроме прессов 10 и 5 и КПС), — пометка, что пустой светофор не значит «в норме»
	if s.cfg != nil {
		if p := s.cfg.Get().ProfileFor(base); p != nil {
			data["Machine"] = p.Name
			data["NoUnits"] = p.Kind != "" && p.Kind != "press10" && p.Kind != "press5" && p.Kind != "kps"
		}
	}
	version := features.PressVersion // версия признаков машины: строки старее — посчитаны прежним расчётом
	if s.cfg != nil {
		if p := s.cfg.Get().ProfileFor(base); p != nil {
			version = features.VersionFor(p.Kind)
		}
	}
	if op == "" {
		s.render(w, "analysis.html", data)
		return
	}
	res, err := s.an.get(op)
	if err != nil {
		data["Error"] = err.Error()
		s.render(w, "analysis.html", data)
		return
	}
	s.an.mu.Lock()
	at := s.an.cache[opKey(op)].at
	s.an.mu.Unlock()

	sum := summarize(base, res, at, s.human) // в шапке — операция; колесо — отдельно (RegimeLabel)
	switch {
	case regime == "all" && data["Recipes"] == true:
		data["RegimeLabel"] = "все рецепты"
	case regime == "all":
		data["RegimeLabel"] = "все колёса"
	case regime != "":
		data["RegimeLabel"] = analyze.RegimeLabel(regime)
	}
	var bad, ok []unitView
	for _, u := range sum.Units {
		v := unitView{unitJSON: u}
		for _, f := range unitFeatures[u.Name] {
			if res.Prod.Col(f) != nil {
				v.Feats = append(v.Feats, featLink{f, s.human(f)})
			}
		}
		if u.Status == "ok" {
			ok = append(ok, v)
		} else {
			bad = append(bad, v)
		}
	}
	var anoms []anomView
	for k, i := range res.Order[:min(30, len(res.Order))] {
		a := anomView{Time: res.Prod.Times[i].Format("02.01 15:04:05"), File: baseFile(res.Prod.File[i]),
			Open: s.relData(res.Prod.File[i]), Score: res.Anom.Score[i]}
		for _, x := range res.Reasons[k] {
			a.Reasons = append(a.Reasons, reasonJSON{x.Feature, s.human(x.Feature), num(x.Value), num(x.Rm), num(x.Z)})
		}
		anoms = append(anoms, a)
	}
	var trends []trendView
	for _, t := range res.Top {
		trends = append(trends, trendView{trendJSON{t.Feature, s.human(t.Feature), analyze.Subsystem(t.Feature),
			analyze.Descs[t.Feature].Why, num(t.Rho), num(t.Shift), num(t.Score)}, t.Shift > 0})
	}
	days := analyze.Days(res.All)
	var dayList []string
	for i := len(days) - 1; i >= 0; i-- { // новые сверху
		dayList = append(dayList, days[i].Format("2006-01-02"))
	}
	// какой признак показать на графике сразу: первый у самого тревожного узла
	first := ""
	if len(bad) > 0 && len(bad[0].Feats) > 0 {
		first = bad[0].Feats[0].Feature
	}
	data["S"], data["Bad"], data["OK"] = sum, bad, ok
	data["Anoms"], data["AnomTotal"], data["Trends"] = anoms, len(res.Order), trends
	data["Days"], data["First"] = dayList, first

	// 6.2а — то, что анализ уже посчитал, но страница не показывала
	n := res.Prod.Len()
	var culprits []culpritView
	for _, c := range res.Anom.Culprits[:min(15, len(res.Anom.Culprits))] {
		culprits = append(culprits, culpritView{c.Feature, s.human(c.Feature), analyze.Subsystem(c.Feature), c.Cycles, 100 * float64(c.Cycles) / float64(n)})
	}
	var pre []culpritView
	for _, c := range res.Stops.Precursors[:min(12, len(res.Stops.Precursors))] {
		pre = append(pre, culpritView{c.Feature, s.human(c.Feature), analyze.Subsystem(c.Feature), c.Cycles, 100 * float64(c.Cycles) / float64(max(1, len(res.Stops.Stops)))})
	}
	var stops []stopView
	for _, st := range res.Stops.Stops {
		v := stopView{From: st.From.Format("02.01 15:04"), Resume: st.Resume.Format("02.01 15:04"), Hours: st.Hours}
		for _, r := range st.Before {
			v.Before = append(v.Before, reasonJSON{r.Feature, s.human(r.Feature), num(r.Value), num(r.Rm), num(r.Z)})
		}
		stops = append(stops, v)
	}
	data["Culprits"], data["Pre"], data["StopList"], data["StopThr"] = culprits, pre, stops, res.Stops.GapThrMin
	data["Sensors"] = sum.Sensors

	// новые правила: «не похож на однотипных соседей» и «отрезок работы вне нормы»
	var peers []peerView
	for _, o := range res.PeerOutliers() {
		peers = append(peers, peerView{o.Feature, o.Text(s.human), o.Kind()})
	}
	var runs []runView
	inNorm := 0
	for _, run := range res.OffRuns() {
		if len(run.Off) == 0 {
			inNorm++
			continue
		}
		v := runView{From: run.From.Format("02.01 15:04"), To: run.To.Format("02.01 15:04"), Cycles: run.Cycles, More: max(0, len(run.Off)-6)}
		for _, f := range run.Off[:min(6, len(run.Off))] {
			when := "с пуска"
			switch {
			case f.FromStart:
			case f.Since.IsZero():
				when = "по ходу отрезка"
			default:
				when = "с " + f.Since.Format("02.01 15:04")
			}
			v.Feats = append(v.Feats, runFeatView{f.Feature, s.human(f.Feature), num(f.Z), when, f.FromStart})
		}
		runs = append(runs, v)
	}
	data["Peers"], data["Runs"], data["RunsInNorm"] = peers, runs, inNorm

	// И1: установки штампа — чем отличаются и что ушло за время работы каждого
	var dies []dieView
	inst := res.DieInstalls()
	main := analyze.MainRecipe(inst)
	for _, d := range inst {
		v := dieView{Change: d.Change.Format("02.01 15:04"), From: d.From.Format("02.01 15:04"), To: d.To.Format("02.01 15:04"),
			Cycles: d.Cycles, Strokes: num(d.Strokes), HasDrift: d.HasDrift, More: max(0, len(d.Drift)-3),
			Recipe: d.Recipe.String(), OtherRecipe: d.Recipe != main}
		for _, f := range dieKey {
			kv := dieKeyView{Name: s.human(f), Value: num(d.Median(res.Prod, f)), Z: num(math.NaN())}
			if j := slices.Index(res.Cols.Eng, f); j >= 0 {
				kv.Z = num((float64(kv.Value) - res.Norms[j].Median) / res.Norms[j].Sigma)
			}
			v.Key = append(v.Key, kv)
		}
		for _, x := range d.Drift[:min(3, len(d.Drift))] {
			v.Drift = append(v.Drift, dieDriftView{x.Feature, s.human(x.Feature), num(x.Base), num(x.Now), num(x.Z)})
		}
		dies = append(dies, v)
	}
	data["Dies"], data["DieKey"], data["MainRecipe"] = dies, dieKeyNames(s), main.String()

	// К4б: журнал изменений — по всем циклам операции, новые сверху. У колеса res — только его
	// циклы, а смена колеса видна лишь по всем вместе: берём общий результат операции
	jr := res
	if regime != "" && regime != "all" {
		if m, err := s.an.get(base); err == nil {
			jr = m
		}
	}
	data["Journal"], data["JournalMore"], data["JournalTail"] = s.journalViews(jr, wallNow())
	data["OldFeat"], data["FeatVer"] = oldFeatRows(jr, version), version // по всей операции, как и журнал
	s.render(w, "analysis.html", data)
}

// journalMax — столько событий журнала на странице; остальное — «и ещё N».
const journalMax = 100

// journalView — строка журнала изменений.
type journalView struct {
	At, Kind, KindName, Text, Open string
}

var journalKinds = map[string]string{
	"wheel": "колесо", "roll": "валок", "recipe": "рецепт", "setpoint": "задание",
	"step": "запись", "gap": "пауза", "die": "штамп",
}

// journalViews — журнал для страницы и «хвост»: если последний цикл старше паузы журнала,
// отдельной строкой «нет новых циклов с …» — иначе молчание регистратора не видно.
func (s *Server) journalViews(res *analyze.Result, now time.Time) (out []journalView, more int, tail string) {
	ev := res.Journal()
	if d := res.All; d != nil && d.Len() > 0 {
		last := d.Times[d.Len()-1]
		for _, t := range d.Times {
			if t.After(last) {
				last = t
			}
		}
		if ago := now.Sub(last); ago > analyze.JournalGap {
			tail = "Нет новых циклов с " + last.Format("02.01 15:04") + " (" + analyze.DurText(ago) + " назад)"
		}
	}
	if len(ev) > journalMax {
		ev, more = ev[:journalMax], len(ev)-journalMax
	}
	for _, e := range ev {
		v := journalView{At: e.At.Format("02.01 15:04"), Kind: e.Kind, KindName: journalKinds[e.Kind], Text: e.Text}
		if e.File != "" {
			v.Open = s.relData(e.File)
		}
		out = append(out, v)
	}
	return out, more, tail
}

// wallNow — «сейчас» по настенным часам: время циклов в таблице — местное без пояса (как в имени
// файла), поэтому и «сейчас» берём так же, иначе «N назад» съедет на разницу с UTC.
func wallNow() time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), n.Hour(), n.Minute(), n.Second(), 0, time.UTC)
}

// dieKey — показатели, по которым видно, что штампы разные (геометрия и нагрузка).
var dieKey = []string{"force_max_kN", "pos_bottom_mm", "work_kJ", "press_time_s"}

func dieKeyNames(s *Server) []string {
	out := make([]string, len(dieKey))
	for k, f := range dieKey {
		out[k] = s.human(f)
	}
	return out
}

// dieView — строка таблицы установок штампа.
type dieView struct {
	Change, From, To string
	Recipe           string // «60 / 650 / 0»: скорость опускания / нижние цилиндры / SCu
	OtherRecipe      bool   // не основной рецепт операции — похоже, другое колесо
	Cycles           int
	Strokes          num
	Key              []dieKeyView // медиана за установку и её z от ОБЩЕЙ нормы
	HasDrift         bool         // дрейф считался (циклов ≥ 30)
	Drift            []dieDriftView
	More             int
}

type dieKeyView struct {
	Name  string
	Value num
	Z     num
}

type dieDriftView struct {
	Feature, Name string
	Base, Now, Z  num
}

// peerView — строка «не похож на однотипных соседей»; Kind — new, worse, always, nobase (см. PeerOutlier.Kind).
type peerView struct {
	Feature, Text, Kind string
}

// runView — отрезок работы между остановами, где признаки весь отрезок были вне нормы.
type runView struct {
	From, To string
	Cycles   int
	Feats    []runFeatView
	More     int // ещё столько признаков не показано
}

type runFeatView struct {
	Feature, Name string
	Z             num
	When          string // «с пуска», «с 16.11 20:46»
	FromStart     bool
}

// analysisDayPage — фрагмент «отчёт за сутки» для HTMX.
func (s *Server) analysisDayPage(w http.ResponseWriter, r *http.Request) {
	if s.an == nil {
		s.fail(w, errNoAnalysis)
		return
	}
	op := strings.ToUpper(r.URL.Query().Get("op"))
	if !reOp.MatchString(op) {
		s.fail(w, errBadOp(op))
		return
	}
	res, err := s.an.get(op)
	if err != nil {
		s.fail(w, err)
		return
	}
	days := analyze.Days(res.All)
	day := days[len(days)-1]
	if q := r.URL.Query().Get("d"); q != "" {
		if day, err = time.Parse("2006-01-02", q); err != nil {
			s.fail(w, errBadDay(q))
			return
		}
	}
	dr, err := res.Daily(day)
	if err != nil {
		s.fail(w, err)
		return
	}
	var bad []unitJSON
	nOK := 0
	for _, d := range dr.Diag {
		if d.Status == analyze.OK {
			nOK++
			continue
		}
		bad = append(bad, unitJSON{d.Name, d.Group, statusKey[d.Status], analyze.StatusText[d.Status], d.Facts, d.Forecast, d.Cause, d.Check})
	}
	// названия признаков — с учётом «Настроек» (анализ кладёт описание по умолчанию)
	rows := make([]analyze.DayRow, len(dr.Rows))
	for k, row := range dr.Rows {
		row.Name = s.human(row.Feature)
		rows[k] = row
	}
	anoms := make([]analyze.DayAnomaly, min(20, len(dr.Anomalies)))
	for k := range anoms {
		a := dr.Anomalies[k]
		a.What = analyze.DayReasonText(a.Reasons, s.human)
		anoms[k] = a
	}
	s.render(w, "analysis_day.html", map[string]any{
		"Op": op, "Day": dr.Day.Format("02.01.2006"), "R": dr, "Bad": bad, "NOK": nOK,
		"Anoms": anoms, "Rows": rows,
		"Hourly": s.hourlyJSON(dr),
	})
}

// dayHourlyJSON — «Сутки по часам» и «Циклов в час» (6.2з). Кладётся прямо во фрагмент
// отчёта за сутки (<script type="application/json">) — второй запрос не нужен.
// Время — часы от начала суток (0…24): браузер не будет переводить его в свой часовой пояс,
// на графике ровно то время, что записал регистратор.
type dayHourlyJSON struct {
	Plots   []dayPlotJSON `json:"plots"`
	PerHour [][2]float64  `json:"per_hour"` // [час от начала суток, циклов]
}

type dayPlotJSON struct {
	Name string `json:"name"`
	X    []num  `json:"x"`
	V    []num  `json:"v"`
	HX   []num  `json:"hx"` // середина часа — точка медианы
	HM   []num  `json:"hm"`
	Lo   num    `json:"lo"` // полоса нормы 5…95%; null — нет
	Hi   num    `json:"hi"`
}

func (s *Server) hourlyJSON(dr *analyze.DayReport) dayHourlyJSON {
	h := func(t time.Time) float64 { return t.Sub(dr.Day).Hours() }
	out := dayHourlyJSON{Plots: []dayPlotJSON{}, PerHour: [][2]float64{}}
	for _, p := range dr.Hourly {
		pj := dayPlotJSON{Name: s.human(p.Feature), V: toNum(p.V), HM: toNum(p.HourMed), Lo: num(p.Lo), Hi: num(p.Hi)}
		for _, t := range p.T {
			pj.X = append(pj.X, num(h(t)))
		}
		for _, t := range p.HourT {
			pj.HX = append(pj.HX, num(h(t)+0.5))
		}
		out.Plots = append(out.Plots, pj)
	}
	for _, c := range dr.PerHour {
		out.PerHour = append(out.PerHour, [2]float64{h(c.Hour), float64(c.N)})
	}
	return out
}

// analysisChart — фрагмент с «островком» графика признака; данные TypeScript возьмёт из API.
func (s *Server) analysisChart(w http.ResponseWriter, r *http.Request) {
	op := strings.ToUpper(r.URL.Query().Get("op"))
	f := r.URL.Query().Get("f")
	if !reOp.MatchString(op) || f == "" {
		s.fail(w, errBadOp(op))
		return
	}
	s.render(w, "analysis_chart.html", map[string]any{"Op": op, "F": f, "Name": s.human(f)})
}

func baseFile(p string) string { return filepath.Base(strings.ReplaceAll(p, `\`, "/")) }

type pageErr string

func (e pageErr) Error() string { return string(e) }

func errBadOp(op string) error {
	return pageErr("операция «" + op + "»: ждали OP1, OP2…")
}
func errBadDay(d string) error {
	return pageErr("дата «" + d + "»: ждали ГГГГ-ММ-ДД")
}

// fmtNum — число для страницы: чем больше, тем меньше знаков (как в diagnosis.txt).
func fmtNum(v any) string {
	var x float64
	switch t := v.(type) {
	case float64:
		x = t
	case num:
		x = float64(t)
	case int:
		x = float64(t)
	default:
		return fmt.Sprint(v)
	}
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return "—"
	}
	a := math.Abs(x)
	switch {
	case a >= 100:
		return fmt.Sprintf("%.0f", x)
	case a >= 10:
		return fmt.Sprintf("%.1f", x)
	case a >= 0.1:
		return fmt.Sprintf("%.2f", x)
	}
	return fmt.Sprintf("%.3f", x)
}

// fmtZ — z со знаком: +12.3σ.
func fmtZ(v any) string {
	var x float64
	switch t := v.(type) {
	case float64:
		x = t
	case num:
		x = float64(t)
	}
	if math.IsNaN(x) {
		return "—"
	}
	return fmt.Sprintf("%+.1fσ", x)
}

// normRowView — строка профиля нормы.
type normRowView struct {
	Feature, Name, Subsystem, Kind string
	Median, Sigma, P5, P95         num
}

// analysisNorm — фрагмент «профиль нормы»: ключевые показатели или поиск по всем признакам.
func (s *Server) analysisNorm(w http.ResponseWriter, r *http.Request) {
	if s.an == nil {
		s.fail(w, errNoAnalysis)
		return
	}
	op := strings.ToUpper(r.URL.Query().Get("op"))
	if !reOp.MatchString(op) {
		s.fail(w, errBadOp(op))
		return
	}
	s.normFragment(w, r, op, strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))), nil)
}

// normFragment — профиль нормы и (только на этом компьютере) форма «Пересчитать норму».
// msg — итог сохранения: Saved или Err.
func (s *Server) normFragment(w http.ResponseWriter, r *http.Request, op, q string, msg map[string]any) {
	res, err := s.an.get(op)
	if err != nil {
		s.fail(w, err)
		return
	}
	row := func(f string) (normRowView, bool) {
		fn, ok := res.Norm.Features[f]
		if !ok || fn.Kind == "" {
			return normRowView{}, false
		}
		return normRowView{f, s.human(f), analyze.Subsystem(f), fn.Kind, fn.Median, fn.Sigma, fn.Q[1], fn.Q[3]}, true
	}
	var rows []normRowView
	total := 0
	if q == "" {
		for _, f := range analyze.DailyKey { // без поиска — ключевые показатели, как в отчёте за сутки
			if v, ok := row(f); ok {
				rows = append(rows, v)
			}
		}
		total = len(rows)
	} else {
		for _, n := range res.Norms {
			v, ok := row(n.Feature)
			if !ok || !strings.Contains(strings.ToLower(v.Feature+" "+v.Name+" "+v.Subsystem), q) {
				continue
			}
			total++
			if len(rows) < 200 {
				rows = append(rows, v)
			}
		}
	}
	data := map[string]any{"Op": op, "Q": q, "Rows": rows, "Total": total, "All": len(res.Norms),
		"From": res.Norm.BaseFrom.Format("02.01.2006 15:04"), "To": res.Norm.BaseTo.Format("02.01.2006 15:04"), "Cycles": res.Norm.BaseCycles,
		"Note": res.Norm.Note, "Stored": res.Stored, "Local": isLocal(r), "MinCycles": analyze.AutoNormCycles}
	if isLocal(r) {
		data["Suggest"] = normSuggestViews(analyze.SuggestNormPeriods(res, analyze.AutoNormCycles, 3))
		if n := res.Prod.Len(); n > 0 { // границы для полей даты: весь период данных
			data["Min"], data["Max"] = res.Prod.Times[0].Format(normInputTF), res.Prod.Times[n-1].Format(normInputTF)
		}
	}
	for k, v := range msg {
		data[k] = v
	}
	s.render(w, "analysis_norm.html", data)
}

type cellRow struct {
	Feature, Name string
	Value, Norm   num
	Z             float64
}

// analysisHealthCell — щелчок по клетке карты здоровья: признаки подсистемы в этот интервал,
// самые отклонившиеся сверху, со ссылками на график.
func (s *Server) analysisHealthCell(w http.ResponseWriter, r *http.Request) {
	if s.an == nil {
		s.fail(w, errNoAnalysis)
		return
	}
	q := r.URL.Query()
	op := strings.ToUpper(q.Get("op"))
	if !reOp.MatchString(op) {
		s.fail(w, errBadOp(op))
		return
	}
	res, err := s.an.get(op)
	if err != nil {
		s.fail(w, err)
		return
	}
	b, err := strconv.Atoi(q.Get("b"))
	if err != nil || b < 0 || b >= len(res.Bins.Starts) {
		s.fail(w, pageErr("номер интервала вне карты"))
		return
	}
	sub := q.Get("sub")
	var rows []cellRow
	for j, c := range res.Cols.Eng {
		if analyze.Subsystem(c) != sub {
			continue
		}
		v, n := res.Bins.Med[j][b], res.Norms[j]
		z := (v - n.Median) / n.Sigma
		if math.IsNaN(z) {
			continue
		}
		rows = append(rows, cellRow{c, s.human(c), num(v), num(n.Median), z})
	}
	sort.SliceStable(rows, func(i, k int) bool { return math.Abs(rows[i].Z) > math.Abs(rows[k].Z) })
	lay := "02.01.2006"
	if res.Bins.Step < 24*time.Hour {
		lay = "02.01.2006 15:00"
	}
	s.render(w, "analysis_cell.html", map[string]any{"Op": op, "Sub": sub, "When": res.Bins.Starts[b].Format(lay), "Rows": rows})
}

// oldFeatRows — сколько циклов посчитано прежней версией признаков (колонка feat_ver меньше
// текущей; нет колонки — версия 0). Сервер пересчитывает их, пока файл есть в буфере или в архиве;
// остальные так и остаются — анализ должен об этом сказать, а не молчать.
func oldFeatRows(res *analyze.Result, version int) int {
	if version == 0 || res.All == nil {
		return 0
	}
	v := res.All.Col("feat_ver")
	if v == nil {
		return res.All.Len()
	}
	n := 0
	for _, x := range v {
		if !(x >= float64(version)) { // NaN — версия 0
			n++
		}
	}
	return n
}
