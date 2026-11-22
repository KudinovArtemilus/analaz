// Package web — веб-интерфейс press-go: HTML-страницы (Go-шаблоны + HTMX)
// и JSON для графиков (их рисует TypeScript + uPlot).
//
// Шаблоны, стили и собранный app.js вшиты в программу через embed,
// поэтому press-go остаётся одним .exe без папок рядом.
package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/config"
	"github.com/KudinovArtemilus/press-go/internal/features"
	"github.com/KudinovArtemilus/press-go/internal/lttb"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// spare — пустые резервные слоты регистратора (как SKIP_NAME в Python).
var spare = regexp.MustCompile(`(?i)(^|:)\s*spare\s*$`)

// каналы, которые показываем при открытии цикла, если они есть в файле
var defaultChannels = []string{
	"slide:total force [kN]",
	"slide:average val.encoder [mm]",
	"hyd:high pressure [bar]",
}

// Server — веб-сервер над папкой с .shp.
type Server struct {
	data  string // корневая папка с .shp
	loc   *time.Location
	tmpl  *template.Template
	files *shpCache

	listMu   sync.Mutex
	list     []fileItem
	listTime time.Time

	an   *analysisStore // nil — анализ не включён (нет -features)
	cfg  *config.Store  // nil — без файла настроек: нет профилей, псевдонимов и админки
	arch *archiveRunner // nil — без настроек архива нет (папка архива задаётся в «Настройках»)
	feat *featRunner    // признаки по расписанию; nil — без настроек
}

// New готовит сервер: разбирает шаблоны один раз при старте.
// Ошибка в шаблоне — это ошибка программиста, лучше узнать о ней сразу, а не на первом запросе.
func New(data string, loc *time.Location) (*Server, error) {
	abs, err := filepath.Abs(data)
	if err != nil {
		return nil, err
	}
	t, err := template.New("").Funcs(template.FuncMap{
		"sec":  func(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) },
		"ms":   func(d time.Duration) int64 { return d.Milliseconds() },
		"v":    fmtNum,
		"join": strings.Join,
		"kinds": func() map[string]string {
			return map[string]string{"press10": "Пресс 10 (OP1/OP2)", "press5": "Пресс 5", "kps": "КПС (прокатный стан, OP3)", "other": "Другая машина"}
		},
		"z":           fmtZ,
		"zcls":        zClass,
		"gb":          func(b int64) float64 { return float64(b) / 1e9 },
		"featPerPass": func() int { return featPerPass },
	}).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{data: abs, loc: loc, tmpl: t, files: newShpCache(6)}, nil
}

// Handler — все маршруты. С Go 1.22 метод и путь пишутся прямо в шаблоне маршрута.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index) // {$} — только сам «/», а не всё подряд
	mux.HandleFunc("GET /files", s.fileList)
	mux.HandleFunc("GET /cycle", s.cycle)
	mux.HandleFunc("GET /cycle/chart", s.chart)
	mux.HandleFunc("GET /api/series", s.series)
	mux.HandleFunc("GET /api/digital", s.digital)
	mux.HandleFunc("GET /analysis", s.analysisPage)
	mux.HandleFunc("GET /piece", s.piecePage) // И3: паспорт заготовки
	mux.HandleFunc("GET /analysis/day", s.analysisDayPage)
	mux.HandleFunc("GET /analysis/chart", s.analysisChart)
	mux.HandleFunc("GET /analysis/norm", s.analysisNorm)
	mux.HandleFunc("GET /analysis/health/cell", s.analysisHealthCell)
	s.analysisRoutes(mux)
	s.adminRoutes(mux)
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	return logRequests(mux)
}

// ---------- страницы ----------

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	// ?open=путь — сразу открыть цикл (ссылка со страницы «Анализ» на аномальный цикл);
	// &feat=признак (несколько) — показать на графике каналы, из которых эти признаки посчитаны
	q := r.URL.Query()
	// машины для выбора над списком — если их больше одной (новые сверху у всех вместе, и у машины,
	// которая давно не писала файлов, они иначе тонут под тысячами циклов другой)
	type machine struct{ ID, Name string }
	var ms []machine
	if s.cfg != nil {
		for _, p := range s.cfg.Get().EnabledProfiles() {
			ms = append(ms, machine{p.ID, p.Name})
		}
	}
	if len(ms) < 2 {
		ms = nil
	}
	s.render(w, "layout.html", map[string]any{"Machines": ms, "Data": s.rootsLabel(), "Open": q.Get("open"), "Feat": q["feat"], "Analysis": s.an != nil})
}

type fileItem struct {
	Rel, RelQ string // адрес для ?f= (см. roots.go); RelQ — он же, закодированный для URL
	Name, Op  string
	Machine   string // название профиля
	Prof      string // id профиля — для выбора машины над списком
	Time      string
	sortKey   string
}

// fileList — список циклов (фрагмент для HTMX), новые сверху, с поиском по имени.
func (s *Server) fileList(w http.ResponseWriter, r *http.Request) {
	items, err := s.listFiles()
	if err != nil {
		s.fail(w, err)
		return
	}
	const page = 300 // за раз: больше на экран не нужно, дальше — «показать ещё»
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	m := r.URL.Query().Get("m") // машина (id профиля); пусто — все
	off, _ := strconv.Atoi(r.URL.Query().Get("o"))
	off = max(off, 0)
	var out []fileItem
	found := 0 // подходит под машину и поиск
	for _, it := range items {
		if (m != "" && it.Prof != m) || (q != "" && !strings.Contains(strings.ToLower(it.Name), q)) {
			continue
		}
		if found >= off && len(out) < page {
			out = append(out, it)
		}
		found++
	}
	next := ""
	if off+len(out) < found {
		next = "/files?" + url.Values{"q": {q}, "m": {m}, "o": {strconv.Itoa(off + page)}}.Encode()
	}
	// продолжение (o > 0) приходит без шапки и дописывается вместо кнопки «показать ещё»
	s.render(w, "files.html", map[string]any{"Items": out, "Shown": off + len(out), "Found": found, "Next": next, "More": off > 0})
}

// listFiles обходит папки всех включённых профилей не чаще раза в 30 с: на сетевой папке
// с тысячами файлов обход занимает секунды, а список запрашивается при каждом вводе в поиске.
// Недоступная папка одного профиля не прячет файлы остальных.
func (s *Server) listFiles() ([]fileItem, error) {
	s.listMu.Lock()
	defer s.listMu.Unlock()
	if time.Since(s.listTime) < 30*time.Second && s.list != nil {
		return s.list, nil
	}
	var items []fileItem
	var errs []error
	for _, root := range s.roots() {
		paths, err := batch.ListFiles(root.Dir, batch.Filter{})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, p := range paths {
			rel, err := filepath.Rel(root.Dir, p)
			if err != nil {
				continue
			}
			q := qualify(root, filepath.ToSlash(rel))
			n := shp.ParseName(p, s.loc)
			it := fileItem{Rel: q, RelQ: url.QueryEscape(q), Name: filepath.Base(p), Op: n.Op, Machine: root.Name, Prof: root.Profile}
			it.sortKey = filepath.Base(p)
			if !n.Time.IsZero() {
				it.Time = n.Time.Format("02.01 15:04:05")
				it.sortKey = n.Time.UTC().Format("20060102150405") + it.sortKey
			}
			items = append(items, it)
		}
	}
	if len(items) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	// новые сверху — по времени из имени, через все папки сразу
	sort.SliceStable(items, func(a, b int) bool { return items[a].sortKey > items[b].sortKey })
	s.list, s.listTime = items, time.Now()
	return items, nil
}

type channelItem struct {
	Index   int
	Name    string // как в файле .shp
	Alias   string // своё название из админки ("" — не задано)
	Checked bool
	Signal  Signal
}

type channelGroup struct {
	Name     string
	Open     bool // группа раскрыта, если в ней есть выбранный канал или проблема сигнала
	Problems int  // сколько каналов группы с подозрением на неисправность сигнала
	Channels []channelItem
}

// cycle — карточка цикла: сведения о файле, выбор каналов и область графиков.
func (s *Server) cycle(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("f")
	f, err := s.open(rel)
	if err != nil {
		s.fail(w, err)
		return
	}
	// по умолчанию ничего не отмечено: позиция, усилие и давление и так всегда видны
	// в обзоре сверху. Каналы по умолчанию — только для файлов без каналов обзора (другой пресс).
	var checked []int
	if len(overviewIdx(f)) == 0 {
		checked = defaultSelection(f)
	}
	// пришли со страницы «Анализ»: вместо каналов по умолчанию — каналы виновников аномалии
	feats := r.URL.Query()["feat"]
	// (каналы, которые уже есть в обзоре сверху, второй раз не рисуем — отметка встанет там)
	if idx := features.ChannelIndexes(f, feats); len(idx) > 0 {
		ov := overviewIdx(f)
		checked = slices.DeleteFunc(idx, func(j int) bool { return slices.Contains(ov, j) })
	}
	groups, problems := groupChannels(f, checked, s.alias)
	n := shp.ParseName(f.Path, s.loc)
	s.render(w, "cycle.html", map[string]any{
		"Rel": rel, "File": filepath.Base(f.Path), "Name": n,
		"Start":    shp.StartTime(f, s.loc).Format("02.01.2006 15:04:05"),
		"Duration": f.Duration(), "Step": f.Step(), "Samples": f.NumSamples(),
		"NAna": len(f.AnaNames), "NDig": len(f.DigNames), "Damage": f.Damage,
		"Groups": groups, "Problems": problems, "Chart": chartData(rel, checked, feats, overviewIdx(f)), "Feat": feats,
	})
}

// chart — только область графиков: HTMX подменяет её при смене выбранных каналов.
func (s *Server) chart(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("f")
	f, err := s.open(rel)
	if err != nil {
		s.fail(w, err)
		return
	}
	var idx []int
	for _, v := range r.URL.Query()["ch"] {
		if i, err := strconv.Atoi(v); err == nil {
			idx = append(idx, i)
		}
	}
	s.render(w, "chart.html", chartData(rel, idx, r.URL.Query()["feat"], overviewIdx(f)))
}

// overview — обзор цикла (как «Обзор цикла» в plot_cycle.py): эти каналы показываются
// всегда сверху, что бы ни было отмечено, — по ним видно, где в цикле прессование.
var overview = []string{
	"slide:average val.encoder [mm]",  // позиция ползуна
	"slide:total force [kN]",          // усилие
	"hyd:high pressure [bar]",         // высокое давление
	"slide:deviation left-right [mm]", // перекос
}

// overviewIdx — номера каналов обзора, которые есть в файле.
func overviewIdx(f *shp.File) []int {
	var idx []int
	for _, name := range overview {
		if j, ok := f.AnalogIndex(name); ok {
			idx = append(idx, j)
		}
	}
	return idx
}

// chartData — данные «островка» графиков: выбранные каналы, признаки, места которых
// отметить на графиках, и каналы обзора цикла.
func chartData(rel string, idx []int, feat []string, ov []int) map[string]any {
	return map[string]any{"Rel": rel, "Ch": joinInts(idx), "N": len(idx), "Feat": strings.Join(feat, ","), "Ov": joinInts(ov)}
}

func joinInts(idx []int) string {
	parts := make([]string, len(idx))
	for i, v := range idx {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

// ---------- данные для графиков ----------

type seriesJSON struct {
	Index int    `json:"index"` // номер канала в файле — к нему привязаны отметки
	Panel string `json:"panel"` // каналы с одинаковым panel рисуются на одном графике
	Role  string `json:"role"`  // "set" — задание, "act" — факт, "" — одиночный канал
	Name  string `json:"name"`
	T     f32s   `json:"t"` // секунды от начала цикла
	V     f32s   `json:"v"`
}

// series — каналы цикла, прорежённые до n точек.
// float32 в JSON — вдвое короче float64, а для графика точности хватает с запасом.
func (s *Server) series(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, err := s.open(q.Get("f"))
	if err != nil {
		s.fail(w, err)
		return
	}
	n, _ := strconv.Atoi(q.Get("n"))
	n = min(max(n, 200), 4000) // защита от n=10000000 в адресной строке

	times := f.Times()
	// t0, t1 — участок цикла при приближении: прореживаем только его, поэтому
	// чем сильнее приближение, тем подробнее линия (вплоть до каждого отсчёта).
	keep := timeWindow(times, q.Get("t0"), q.Get("t1"))
	tw := pick(times, keep)

	// Каналы одной пары «задание / факт» идут на общий график, поэтому им нужны
	// одни и те же моменты времени: берём объединение точек, которые LTTB выбрал
	// для каждого канала пары, — так сохраняются пики и задания, и факта.
	type item struct {
		j           int
		panel, role string
		v           []float64
	}
	var items []item
	panelIdx := map[string][]int{}
	for _, part := range strings.Split(q.Get("ch"), ",") {
		j, err := strconv.Atoi(part)
		if err != nil || j < 0 || j >= len(f.AnaNames) {
			continue
		}
		panel, role, ok := features.Pair(f.AnaNames[j])
		if !ok {
			panel = strconv.Itoa(j)
		}
		v := pick(shp.Clean(f.Ana[j]), keep)
		items = append(items, item{j, panel, role, v})
		panelIdx[panel] = append(panelIdx[panel], lttb.Index(tw, v, n)...)
	}
	var out []seriesJSON
	for _, it := range items {
		idx := panelIdx[it.panel]
		slices.Sort(idx)
		idx = slices.Compact(idx)
		panelIdx[it.panel] = idx
		out = append(out, seriesJSON{Index: it.j, Panel: it.panel, Role: it.role, Name: s.label(f.AnaNames[it.j]),
			T: to32(pick(tw, idx)), V: to32(pick(it.v, idx))})
	}
	// отметки «где в цикле случился признак» — для ссылок со страницы «Анализ»
	var marks []markJSON
	if q.Get("feat") != "" {
		for _, ft := range strings.Split(q.Get("feat"), ",") {
			if m, ok := features.Locate(f, ft); ok {
				marks = append(marks, markJSON{T: times[m.At], Ch: m.Chans, Label: s.human(ft), Kind: m.Kind, Value: m.Value})
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"duration": f.Duration().Seconds(),
		"channels": out,
		"marks":    marks,
	})
}

// timeWindow — номера отсчётов внутри [t0, t1] (с запасом в один отсчёт с каждой стороны,
// чтобы линия доходила до краёв графика). nil — весь цикл.
func timeWindow(times []float64, s0, s1 string) []int {
	t0, err0 := strconv.ParseFloat(s0, 64)
	t1, err1 := strconv.ParseFloat(s1, 64)
	if err0 != nil || err1 != nil || t1 <= t0 {
		return nil
	}
	var keep []int
	for i, t := range times {
		if t >= t0 && t <= t1 {
			if len(keep) == 0 && i > 0 {
				keep = append(keep, i-1)
			}
			keep = append(keep, i)
		} else if len(keep) > 0 && t > t1 {
			keep = append(keep, i)
			break
		}
	}
	return keep
}

// pick — значения по номерам; nil — все.
func pick(x []float64, idx []int) []float64 {
	if idx == nil {
		return x
	}
	out := make([]float64, len(idx))
	for k, i := range idx {
		out[k] = x[i]
	}
	return out
}

// digitalJSON — дискретный канал как список интервалов «включён» [начало, конец] в секундах.
// Интервалы, а не отсчёты: у команды за цикл обычно 1–10 переключений, ответ крошечный
// и точный при любом приближении — догружать при зуме нечего.
type digitalJSON struct {
	Index int          `json:"index"`
	Name  string       `json:"name"`
	On    [][2]float64 `json:"on"`
}

// digital — дискретные сигналы цикла, которые в этом цикле ПЕРЕКЛЮЧАЛИСЬ.
// Постоянно включённые и постоянно выключенные не показываем: о ходе цикла они
// ничего не говорят, а из 256 бит таких большинство (в plot_cycle — так же: только активные,
// здесь ещё строже — без «всегда 1»).
func (s *Server) digital(w http.ResponseWriter, r *http.Request) {
	f, err := s.open(r.URL.Query().Get("f"))
	if err != nil {
		s.fail(w, err)
		return
	}
	times := f.Times()
	step := f.Step().Seconds()
	out := []digitalJSON{}
	for j, name := range f.DigNames {
		if j == 0 || spare.MatchString(name) { // бит 0 — служебный, как в Python
			continue
		}
		if on := onIntervals(f.Dig[j], times, step); on != nil {
			out = append(out, digitalJSON{Index: j, Name: s.label(name), On: on})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// onIntervals — интервалы, где бит включён; nil, если бит за цикл не менялся.
// Конец интервала — начало первого выключенного отсчёта (у последнего — время + шаг):
// так соседние интервалы стыкуются без щелей.
func onIntervals(bits []bool, times []float64, step float64) [][2]float64 {
	if len(bits) == 0 || len(bits) != len(times) {
		return nil
	}
	changed := false
	for _, b := range bits[1:] {
		if b != bits[0] {
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	var on [][2]float64
	start := -1
	for i, b := range bits {
		switch {
		case b && start < 0:
			start = i
		case !b && start >= 0:
			on = append(on, [2]float64{times[start], times[i]})
			start = -1
		}
	}
	if start >= 0 {
		on = append(on, [2]float64{times[start], times[len(times)-1] + step})
	}
	return on
}

// markJSON — отметка на графиках цикла: момент, каналы, подпись.
type markJSON struct {
	T     float64 `json:"t"` // секунды от начала цикла
	Ch    []int   `json:"ch"`
	Label string  `json:"label"`
	Kind  string  `json:"kind"` // max, min, absdiff, abs — см. features.Mark
	Value float64 `json:"value"`
}

// ---------- вспомогательное ----------

// open безопасно превращает адрес из запроса в файл внутри одной из папок с .shp (roots.go).
// Без проверки запрос вида ?f=../../Windows/system.ini читал бы любые файлы с диска.
func (s *Server) open(f string) (*shp.File, error) {
	if f == "" {
		return nil, errors.New("не указан файл")
	}
	root, rel, err := s.resolve(f)
	if err != nil {
		return nil, err
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if rel == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) ||
		!strings.EqualFold(filepath.Ext(clean), ".shp") {
		return nil, fmt.Errorf("недопустимый путь: %s", f)
	}
	path := filepath.Join(root.Dir, clean)
	file, err := s.files.get(path)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return file, err
	}
	// в буфере файла уже нет (буфер хранит ~2 недели) — берём из архива его профиля, если сохранён
	ar := archRel(root, filepath.ToSlash(clean))
	if a := s.arch.forProfile(root.Profile); a != nil && a.Has(ar) {
		return s.files.getWith(path, func() (*shp.File, error) {
			data, err := a.Read(ar)
			if err != nil {
				return nil, err
			}
			f, err := shp.Parse(data)
			if err != nil {
				return nil, err
			}
			f.Path = path // имя файла нужно для номера заготовки и времени
			return f, nil
		})
	}
	return nil, fmt.Errorf("файла уже нет в буфере и он не сохранён в архиве: %s", filepath.Base(path))
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("шаблон %s: %v", name, err)
	}
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	log.Print(err)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, `<div class="error">%s</div>`, template.HTMLEscapeString(err.Error()))
}

// defaultSelection — индексы каналов, открытых по умолчанию; если их нет (другая машина) —
// первые три меняющихся канала.
func defaultSelection(f *shp.File) []int {
	var idx []int
	for _, name := range defaultChannels {
		if j := slices.Index(f.AnaNames, name); j >= 0 {
			idx = append(idx, j)
		}
	}
	for j := range f.AnaNames {
		if len(idx) >= 3 {
			break
		}
		if varies(f.Ana[j]) && !slices.Contains(idx, j) {
			idx = append(idx, j)
		}
	}
	return idx
}

// groupChannels раскладывает каналы по группам по первому слову имени
// ("slide:...", "hyd:...", "Pump 1.1 ..."), чтобы 307 каналов не шли одним списком.
// Показываются ВСЕ каналы, кроме пустых слотов «spare»: неизменный канал может оказаться
// мёртвым датчиком, а прятать неисправность нельзя. Вторым значением — список проблемных.
func groupChannels(f *shp.File, checked []int, label func(string) string) ([]channelGroup, []channelItem) {
	var groups []channelGroup
	var problems []channelItem
	pos := map[string]int{}
	sig := make([]Signal, len(f.AnaNames))
	for j := range f.AnaNames {
		sig[j] = assessSignal(f.Ana[j])
	}
	markPeers(f.AnaNames, sig) // ноль подозрителен, только если однотипные каналы живы
	for j, name := range f.AnaNames {
		if spare.MatchString(name) {
			continue // резервный слот регистратора, а не канал
		}
		g := groupName(name)
		k, ok := pos[g]
		if !ok {
			k = len(groups)
			pos[g] = k
			groups = append(groups, channelGroup{Name: g})
		}
		it := channelItem{Index: j, Name: name, Alias: label(name), Checked: slices.Contains(checked, j), Signal: sig[j]}
		gr := &groups[k]
		gr.Channels = append(gr.Channels, it)
		if it.Signal.Problem() {
			gr.Problems++
			problems = append(problems, it)
		}
		gr.Open = gr.Open || it.Checked || it.Signal.Problem()
	}
	return groups, problems
}

// Группа — первое слово имени до «:», пробела, точки или «_»:
// "CCyl.lvSetDynPos" и "CCyl Left Encoder" → ccyl; "CLP_C1_bar" и "CLP_C2_bar" → clp.
func groupName(name string) string {
	if i := strings.IndexAny(name, ": ._"); i > 0 {
		return strings.ToLower(name[:i])
	}
	return strings.ToLower(name)
}

func varies(x []float32) bool {
	first, seen := float32(0), false
	for _, v := range x {
		if math.IsNaN(float64(v)) || math.Abs(float64(v)) >= 1e12 {
			continue
		}
		if !seen {
			first, seen = v, true
		} else if v != first {
			return true
		}
	}
	return false
}

// f32s — ряд для JSON: float32 вдвое короче float64, а NaN (сбой датчика) пишется
// как null — стандартный JSON не умеет NaN, а uPlot рисует null как разрыв линии.
type f32s []float32

func (a f32s) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, len(a)*8+2)
	b = append(b, '[')
	for i, v := range a {
		if i > 0 {
			b = append(b, ',')
		}
		if v != v { // NaN
			b = append(b, "null"...)
		} else {
			b = strconv.AppendFloat(b, float64(v), 'g', -1, 32)
		}
	}
	return append(b, ']'), nil
}

func to32(x []float64) f32s {
	out := make(f32s, len(x))
	for i, v := range x {
		out[i] = float32(v)
	}
	return out
}

// logRequests пишет в консоль каждый запрос и время ответа — видно, что тормозит.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		next.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			log.Printf("%s %s  %v", r.Method, r.URL.Path, time.Since(t0).Round(time.Millisecond))
		}
	})
}

// zClass — класс строки по отклонению: |z| > alarm — st-alarm, |z| > warn — st-warn, иначе пусто.
// Сравнивать в шаблоне через gt/lt нельзя: в text/template gt — это «не le», и для NaN
// (нормы нет) gt NaN 4.0 даёт true — строка без нормы подсвечивалась красным.
func zClass(z any, warn, alarm float64) string {
	var v float64
	switch x := z.(type) {
	case float64:
		v = x
	case num:
		v = float64(x)
	default:
		return ""
	}
	switch a := math.Abs(v); {
	case math.IsNaN(a):
		return ""
	case a > alarm:
		return "st-alarm"
	case a > warn:
		return "st-warn"
	}
	return ""
}
