package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/archive"
	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/extract"
	"github.com/KudinovArtemilus/press-go/internal/features"
)

// featRunner — признаки по расписанию (этап 8): раз в N минут смотрит в папки всех включённых
// профилей, считает признаки по новым .shp и дописывает таблицу features_<OP>.csv.gz в папке таблиц.
// Анализ пересчитывается сам (видит, что таблица изменилась), архив отбирает уже по свежим данным.
//
// Проход в две фазы: сначала новые файлы всех машин, потом история (пропуски, которые буфер
// скоро удалит). Иначе новая машина ждала бы, пока другая досчитает свой буфер за часы.
//
// Папки-источники только читаются. Чужие таблицы (features_quick_OP1 от утилиты extract)
// не меняются: их строки один раз переносятся в features_<OP>.csv.gz, чтобы история не пропала.
type featRunner struct {
	s       *Server
	mu      sync.Mutex
	running bool
	started time.Time
	doing   string // что делается сейчас — для «Настроек» во время прохода
	st      featStatus
	done    map[string]featDone           // по операции: что уже в таблице
	failed  map[string]time.Time          // файл → когда не прочитался (не повторяем каждые 5 минут)
	merged  map[string]time.Time          // чужая таблица → её время, когда переносили (второй раз ту же не читаем)
	now     func() time.Time              // для тестов
	perPass int                           // featPerPass; меньше — только в тестах
	onApply func(op, phase string, n int) // для тестов: вызывается перед подсчётом
}

// featDone — множество посчитанных файлов и время таблицы, по которому оно снято:
// таблицу целиком (сотни МБ) читаем, только когда есть что добавить.
type featDone struct {
	mod   time.Time
	files map[string]bool
	stale map[string]bool // посчитаны старой версией признаков (по имени файла) — пересчитать
}

// doneOf — что посчитано и что пересчитать, по таблице.
func doneOf(t *batch.Table, mod time.Time, version int) featDone {
	return featDone{mod: mod, files: t.Files(), stale: t.Stale(version)}
}

const (
	featPerPass  = 500             // не больше файлов операции за проход: остальные машины не ждут часами
	featJobs     = 2               // файлов одновременно: буфер — сетевая папка, больше не быстрее
	featMinAge   = 2 * time.Minute // моложе — регистратор, возможно, ещё пишет
	featRetryErr = time.Hour       // файл с ошибкой пробуем снова не раньше
)

// featStatus — итог последнего законченного прохода и, если идёт, текущий (для «Настроек»).
type featStatus struct {
	At    time.Time
	Took  time.Duration
	Ops   []featOpStatus
	Note  string // почему не работает
	Every time.Duration

	Running bool      // проход идёт прямо сейчас
	Started time.Time // когда начался текущий
	Doing   string    // что делается: «Пресс 10 · OP1, новые: 230 из 500»
}

type featOpStatus struct {
	Profile string // название профиля («Пресс 10», «КПС»)
	Op      string
	Table   string
	Rows    int // строк в таблице после прохода
	Added   int
	Todo    int // осталось посчитать (после этого прохода)
	Young   int
	Damaged int
	Redone  int    // пересчитано по новой версии признаков
	Stale   int    // ещё ждут пересчёта (файл есть в папках или в архиве)
	Archive int    // из них — из архива: в папках файла уже нет
	Lost    int    // старой версии, а файла нет ни в папках, ни в архиве — так и останутся
	Merged  int    // перенесено строк из другой таблицы (features_quick…)
	From    string // откуда перенесены
	Errors  []string
	Err     string
}

func newFeatRunner(s *Server) *featRunner {
	return &featRunner{s: s, done: map[string]featDone{}, failed: map[string]time.Time{}, merged: map[string]time.Time{}, now: time.Now, perPass: featPerPass}
}

// Status — копия статуса: последний законченный проход плюс что делается сейчас.
func (fr *featRunner) Status() featStatus {
	if fr == nil {
		return featStatus{Note: "без файла настроек"}
	}
	fr.mu.Lock()
	defer fr.mu.Unlock()
	st := fr.st
	st.Running, st.Started, st.Doing = fr.running, fr.started, fr.doing
	return st
}

func (fr *featRunner) setDoing(s string) {
	fr.mu.Lock()
	fr.doing = s
	fr.mu.Unlock()
}

// Start — проход сейчас и потом каждые every. Возвращается сразу.
func (fr *featRunner) Start(every time.Duration) {
	fr.mu.Lock()
	fr.st.Every = every
	fr.mu.Unlock()
	go func() {
		for {
			fr.Run()
			time.Sleep(every)
		}
	}()
}

// featJob — одна операция в проходе: что посчитать, и куда.
type featJob struct {
	name, op string // профиль и операция
	version  int    // версия признаков машины
	target   string
	opt      extract.Options
	todo     []string // сначала новые, потом история
	fresh    int      // сколько в начале todo новых
	taken    int      // взято в этом проходе
	table    *batch.Table
	st       *featOpStatus
}

// Run — один проход по всем включённым профилям. true — проход сделан; false — уже шёл другой
// (тогда ничего не делает: кнопка во время планового прохода второй параллельно не запустит).
func (fr *featRunner) Run() bool {
	fr.mu.Lock()
	if fr.running {
		fr.mu.Unlock()
		return false
	}
	fr.running, fr.started, fr.doing = true, fr.now(), "смотрю папки…"
	every := fr.st.Every
	fr.mu.Unlock()

	st := featStatus{At: fr.now(), Every: every}
	defer func() {
		st.Took = time.Since(st.At).Round(time.Millisecond)
		fr.mu.Lock()
		fr.st, fr.running, fr.doing = st, false, ""
		fr.mu.Unlock()
	}()

	if fr.s.cfg == nil {
		st.Note = "без файла настроек"
		return true
	}
	c := fr.s.cfg.Get()
	profiles := c.EnabledProfiles()
	dir := ""
	if fr.s.an != nil {
		dir, _ = fr.s.an.dirs() // та же папка, что у анализа (в т. ч. из -features)
	}
	switch {
	case dir == "":
		st.Note = "папка таблиц признаков не задана — «Хранение» → «Таблицы признаков»"
		return true
	case len(profiles) == 0:
		st.Note = "нет включённого профиля"
		return true
	}

	// --- что где посчитать: все операции всех включённых профилей ---
	// Таблицы — в одной папке, по операции; операция у включённых профилей одна на всех (config.Validate).
	n := 0
	for _, p := range profiles {
		n += max(len(p.Ops), 1)
	}
	st.Ops = make([]featOpStatus, 0, n) // ёмкость сразу на все: указатели на элементы не «переедут»
	var jobs []*featJob
	for i, p := range profiles {
		roots := c.Dirs(p) // у кольцевого буфера — папка с копиями, не само кольцо
		if len(roots) == 0 && i == 0 {
			roots = []string{fr.s.data} // папка из -data при запуске относится к первому профилю
		}
		if len(roots) == 0 {
			st.Ops = append(st.Ops, featOpStatus{Profile: p.Name, Op: strings.Join(p.Ops, ", "),
				Err: "у профиля не указаны папки с .shp — «Настройки» → профиль"})
			continue
		}
		loc := time.FixedZone(fmt.Sprintf("UTC%+d", p.TZ), p.TZ*3600)
		for _, op := range p.Ops {
			fr.setDoing(p.Name + " · " + op + ": смотрю папку…")
			st.Ops = append(st.Ops, featOpStatus{Profile: p.Name, Op: op})
			if j := fr.plan(p.Name, op, features.VersionFor(p.Kind), dir, roots, loc, &st.Ops[len(st.Ops)-1]); j != nil {
				jobs = append(jobs, j)
			}
		}
	}
	// --- фаза 1: новые файлы всех машин; фаза 2: история, в остаток лимита ---
	for _, j := range jobs {
		fr.apply(j, j.todo[:min(j.fresh, fr.perPass)], "новые")
	}
	for _, j := range jobs {
		rest := j.todo[j.taken:]
		fr.apply(j, rest[:min(len(rest), fr.perPass-j.taken)], "история")
	}
	for _, j := range jobs {
		if j.table != nil && j.st.Err == "" { // перенесли строки, а файлов не было — всё равно сохранить
			if err := extract.Save(j.table, j.target); err != nil {
				j.st.Err = fmt.Sprintf("не записать %s: %v", j.target, err)
				fr.forget(j.op)
			} else {
				fr.remember(j.op, doneOf(j.table, modTime(j.target), j.version))
			}
			j.table = nil
		}
		j.st.Todo = len(j.todo) - j.taken
		j.st.Stale = max(j.st.Stale-j.st.Redone, 0)
		fr.logOp(*j.st)
	}
	return true
}

func (fr *featRunner) logOp(r featOpStatus) {
	if r.Err != "" {
		log.Printf("признаки %s %s: %s", r.Profile, r.Op, r.Err)
		return
	}
	if r.Added == 0 && r.Redone == 0 && r.Merged == 0 && len(r.Errors) == 0 {
		return
	}
	msg := fmt.Sprintf("признаки %s %s: добавлено %d", r.Profile, r.Op, r.Added)
	if r.Redone > 0 {
		msg += fmt.Sprintf(", пересчитано по новой версии %d (ждут ещё %d)", r.Redone, r.Stale)
	}
	if r.Lost > 0 {
		msg += fmt.Sprintf(", старой версии без файла (ни в буфере, ни в архиве) %d", r.Lost)
	}
	if r.Damaged > 0 {
		msg += fmt.Sprintf(" (повреждённых %d)", r.Damaged)
	}
	if r.Merged > 0 {
		msg += fmt.Sprintf(", перенесено из %s %d", r.From, r.Merged)
	}
	if len(r.Errors) > 0 {
		msg += fmt.Sprintf(", ошибок %d", len(r.Errors))
	}
	log.Printf("%s, осталось посчитать %d, в таблице %d", msg, r.Todo, r.Rows)
}

// plan — что нового у операции: перенос из чужой таблицы и список файлов. nil — делать нечего
// (или ошибка — она уже в st).
func (fr *featRunner) plan(name, op string, version int, dir string, roots []string, loc *time.Location, st *featOpStatus) *featJob {
	target := filepath.Join(dir, "features_"+op+".csv.gz")
	st.Table = target

	// Есть ли таблица этой операции новее нашей (утилиту extract запустили руками) —
	// её строки переносим. С первого раза это features_quick_OP1: история не пропадает.
	var other string
	tmod := modTime(target)
	if tabs, err := fr.s.an.tables(); err == nil {
		o := tabs[op]
		fr.mu.Lock()
		seen, was := fr.merged[o]
		fr.mu.Unlock()
		if o != "" && !samePath(o, target) && modTime(o).After(tmod) && !(was && seen.Equal(modTime(o))) {
			other = o
		}
	}

	// что уже посчитано — из памяти, если таблица не менялась
	fr.mu.Lock()
	d, ok := fr.done[op]
	fr.mu.Unlock()
	var table *batch.Table
	if !ok || !d.mod.Equal(tmod) || other != "" {
		var err error
		if table, err = readOrNew(target); err != nil {
			st.Err = err.Error()
			return nil
		}
		if other != "" {
			o, err := batch.ReadCSVGz(other)
			if err != nil {
				st.Err = fmt.Sprintf("не прочитать %s: %v", other, err)
				return nil
			}
			st.Merged, st.From = table.Merge(o), filepath.Base(other)
			fr.mu.Lock()
			fr.merged[other] = modTime(other)
			fr.mu.Unlock()
		}
		d = doneOf(table, tmod, version)
		if st.Merged == 0 {
			fr.remember(op, d)
		}
	}
	st.Rows = len(d.files)

	now := fr.now()
	arcs := &archLookup{s: fr.s, by: map[string]*archive.Archive{}}
	opt := extract.Options{
		Roots: roots, Filter: batch.Filter{Op: op}, Jobs: featJobs, Loc: loc, FreshFirst: true, MinAge: featMinAge,
		Stale:       d.stale,
		InArchive:   func(file string) bool { _, _, ok := arcs.find(file); return ok },
		ReadArchive: arcs.read,
		Skip: func(p string) bool {
			fr.mu.Lock()
			defer fr.mu.Unlock()
			t, bad := fr.failed[p]
			return bad && now.Sub(t) < featRetryErr
		},
	}
	todo, ps, err := extract.Plan(d.files, opt)
	if err != nil {
		st.Err = fmt.Sprintf("папка с .shp недоступна: %v", err)
		return nil
	}
	st.Todo, st.Young, st.Stale, st.Archive, st.Lost = ps.Todo, ps.Young, ps.Stale, ps.Archive, ps.Lost
	if len(todo) == 0 && st.Merged == 0 {
		return nil
	}
	if st.Merged == 0 {
		table = nil // таблицу прочитаем, когда дойдёт очередь: держать в памяти все сразу незачем
	}
	return &featJob{name: name, op: op, version: version, target: target, opt: opt, todo: todo, fresh: ps.Fresh, table: table, st: st}
}

// apply — посчитать files и дописать таблицу операции.
func (fr *featRunner) apply(j *featJob, files []string, phase string) {
	if len(files) == 0 || j.st.Err != "" {
		return
	}
	if j.table == nil {
		t, err := readOrNew(j.target)
		if err != nil {
			j.st.Err = err.Error()
			return
		}
		j.table = t
	}
	opt := j.opt
	label := fmt.Sprintf("%s · %s, %s", j.name, j.op, phase)
	opt.Progress = func(done, total int) { fr.setDoing(fmt.Sprintf("%s: %d из %d", label, done, total)) }
	fr.setDoing(fmt.Sprintf("%s: 0 из %d", label, len(files)))
	if fr.onApply != nil {
		fr.onApply(j.op, phase, len(files))
	}
	as, err := extract.Apply(j.table, j.target, files, extract.Stats{}, opt)
	j.taken += len(files)
	if err != nil {
		j.st.Err = fmt.Sprintf("не записать %s: %v", j.target, err)
		fr.forget(j.op) // таблица на диске могла остаться старой — в следующий раз перечитаем
		j.table = nil
		return
	}
	if as.Saves == 0 && j.st.Merged > 0 { // всё с ошибками, а перенос был — сохранить перенесённое
		if err := extract.Save(j.table, j.target); err != nil {
			j.st.Err = fmt.Sprintf("не записать %s: %v", j.target, err)
			fr.forget(j.op)
			j.table = nil
			return
		}
	}
	j.st.Added += as.Added
	j.st.Redone += as.Redone
	j.st.Damaged += as.Damaged
	j.st.Rows = j.table.Len()
	now := fr.now()
	fr.mu.Lock()
	for _, e := range as.Errors {
		fr.failed[e.Path] = now
	}
	fr.mu.Unlock()
	for _, e := range as.Errors {
		if len(j.st.Errors) < 5 {
			j.st.Errors = append(j.st.Errors, firstLine(e.Err.Error()))
		}
	}
	fr.remember(j.op, doneOf(j.table, modTime(j.target), j.version))
	j.table = nil // следующую фазу — с диска: все таблицы сразу в памяти не держим
}

// archLookup — где в архиве цикл из таблицы признаков. Архив профиля открывается один раз
// на проход: циклов старой версии без файла в буфере бывают тысячи.
type archLookup struct {
	s  *Server
	mu sync.Mutex
	by map[string]*archive.Archive // id профиля → архив (nil — архива нет)
}

func (l *archLookup) find(file string) (*archive.Archive, string, bool) {
	root, rel := l.s.rootOf(file)
	if rel == "" {
		return nil, "", false
	}
	l.mu.Lock()
	a, ok := l.by[root.Profile]
	if !ok {
		a = l.s.arch.forProfile(root.Profile)
		l.by[root.Profile] = a
	}
	l.mu.Unlock()
	ar := archRel(root, rel)
	if a == nil || !a.Has(ar) {
		return nil, "", false
	}
	return a, ar, true
}

// read — содержимое цикла из архива (распакованное).
func (l *archLookup) read(file string) ([]byte, error) {
	a, ar, ok := l.find(file)
	if !ok {
		return nil, errors.New("в архиве нет")
	}
	return a.Read(ar)
}

func (fr *featRunner) remember(op string, d featDone) {
	fr.mu.Lock()
	fr.done[op] = d
	fr.mu.Unlock()
}

func (fr *featRunner) forget(op string) {
	fr.mu.Lock()
	delete(fr.done, op)
	fr.mu.Unlock()
}

// readOrNew — таблица с диска или пустая, если её ещё нет. Старую (шаг 10 мс) сервер
// не переименовывает и не пересчитывает сам — это решает человек утилитой extract.
func readOrNew(path string) (*batch.Table, error) {
	t, err := batch.ReadCSVGz(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return batch.NewTable(), nil
	case errors.Is(err, batch.ErrOldStep):
		return nil, fmt.Errorf("%s — старая таблица (шаг 10 мс): пересчитайте утилитой extract, она отложит старый файл", filepath.Base(path))
	case err != nil:
		return nil, err
	}
	return t, nil
}

func modTime(p string) time.Time {
	if st, err := os.Stat(p); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

func samePath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// adminFeatRun — кнопка «Посчитать сейчас» (только с этого компьютера). Если проход уже идёт —
// говорим об этом, а не молчим: второй параллельно не запускается.
func (s *Server) adminFeatRun(w http.ResponseWriter, r *http.Request) {
	if s.feat.Status().Running {
		http.Redirect(w, r, "/admin?saved=feat-busy#features", http.StatusSeeOther)
		return
	}
	go s.feat.Run()
	http.Redirect(w, r, "/admin?saved=feat-run#features", http.StatusSeeOther)
}

// StartFeatures — включить подсчёт признаков по расписанию (из main, если есть настройки).
func (s *Server) StartFeatures(every time.Duration) {
	if s.feat != nil {
		s.feat.Start(every)
	}
}
