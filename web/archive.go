package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/archive"
)

// archiveRunner — архивация по расписанию (этап 8): раз в N минут берёт результат анализа
// каждой операции каждого включённого профиля, отбирает важные циклы (archive.Select) и копирует
// их из папок профиля в архив профиля: <папка данных>\archive\<id профиля>. Потолок в настройках —
// общий на все архивы. Настройки читаются на каждом проходе — правка действует со следующего
// прохода без перезапуска.
type archiveRunner struct {
	s       *Server
	mu      sync.Mutex
	arcs    map[string]*archive.Archive // по id профиля
	roots   map[string]string           // папка архива по id профиля
	max     int64
	running bool
	st      archiveStatus
}

// archiveStatus — итог последнего прохода (для «Настроек» и журнала).
type archiveStatus struct {
	At      time.Time
	Took    time.Duration
	Saved   int   // сохранено за проход
	SavedGz int64 // их размер в архиве
	Known   int   // уже были в архиве
	Missing int   // отобраны, но в буфере их уже нет
	NoRel   int   // файл не в папках профиля — путь в архиве не построить
	Errors  []string
	Full    bool
	Root    string // общая папка архивов (<папка данных>\archive)
	Used    int64  // занято всеми архивами
	Max     int64
	Files   int
	Per     []archiveProfile // по профилям
	Note    string           // почему архив не работает (нет папки и т. п.)
}

type archiveProfile struct {
	Name  string
	Root  string
	Files int
	Used  int64
}

func newArchiveRunner(s *Server) *archiveRunner {
	return &archiveRunner{s: s, arcs: map[string]*archive.Archive{}, roots: map[string]string{}}
}

// forProfile — архив профиля (nil, если архив не задан или профиля нет). Открывается при первом
// обращении — чтобы карточка цикла видела архив и до первого прохода.
func (ar *archiveRunner) forProfile(id string) *archive.Archive {
	if ar == nil || id == "" {
		return nil
	}
	ar.mu.Lock()
	defer ar.mu.Unlock()
	if ar.arcs[id] == nil {
		ar.open()
	}
	return ar.arcs[id]
}

// open — открыть архивы всех включённых профилей по текущим настройкам (под ar.mu).
func (ar *archiveRunner) open() {
	if ar.s.cfg == nil {
		return
	}
	c := ar.s.cfg.Get()
	if c.DataDir == "" {
		ar.arcs, ar.roots = map[string]*archive.Archive{}, map[string]string{}
		return
	}
	ar.max = int64(c.Archive.MaxGB * 1e9)
	for _, p := range c.EnabledProfiles() {
		root := filepath.Join(c.DataDir, "archive", p.ID)
		if a := ar.arcs[p.ID]; a != nil && ar.roots[p.ID] == root {
			continue
		}
		a, err := archive.Open(root, ar.max)
		if err != nil {
			log.Printf("архив %s: %v", p.ID, err)
			delete(ar.arcs, p.ID)
			continue
		}
		ar.arcs[p.ID], ar.roots[p.ID] = a, root
	}
	ar.limit()
}

// limit — общий потолок: каждому архиву разрешено столько, сколько не заняли остальные (под ar.mu).
func (ar *archiveRunner) limit() {
	var total int64
	for _, a := range ar.arcs {
		total += a.Used()
	}
	for _, a := range ar.arcs {
		a.SetMax(ar.max - (total - a.Used()))
	}
}

// Status — копия статуса с текущей занятостью.
func (ar *archiveRunner) Status() archiveStatus {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	st := ar.st
	st.Max, st.Used, st.Files, st.Per = ar.max, 0, 0, nil
	names := map[string]string{}
	if ar.s.cfg != nil {
		c := ar.s.cfg.Get()
		for _, p := range c.EnabledProfiles() {
			names[p.ID] = p.Name
		}
		if c.DataDir != "" {
			st.Root = filepath.Join(c.DataDir, "archive")
		}
	}
	ids := make([]string, 0, len(ar.arcs))
	for id := range ar.arcs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := ar.arcs[id]
		n := len(a.Entries())
		st.Used += a.Used()
		st.Files += n
		name := names[id]
		if name == "" {
			name = id
		}
		st.Per = append(st.Per, archiveProfile{Name: name, Root: ar.roots[id], Files: n, Used: a.Used()})
	}
	return st
}

// Start — проход сейчас и потом каждые every. Возвращается сразу.
func (ar *archiveRunner) Start(every time.Duration) {
	go func() {
		for {
			ar.Run()
			time.Sleep(every)
		}
	}()
}

// Run — один проход по всем включённым профилям. Если проход уже идёт — ничего не делает
// (кнопка «Сохранить сейчас» во время планового прохода не запустит второй параллельно).
func (ar *archiveRunner) Run() {
	ar.mu.Lock()
	if ar.running {
		ar.mu.Unlock()
		return
	}
	ar.running = true
	ar.open()
	arcs := make(map[string]*archive.Archive, len(ar.arcs))
	for id, a := range ar.arcs {
		arcs[id] = a
	}
	ar.mu.Unlock()
	defer func() {
		ar.mu.Lock()
		ar.running = false
		ar.mu.Unlock()
	}()

	st := archiveStatus{At: time.Now()}
	defer func() {
		st.Took = time.Since(st.At).Round(time.Millisecond)
		ar.mu.Lock()
		ar.st = st
		ar.mu.Unlock()
		if st.Note == "" {
			log.Printf("архив: сохранено %d (%.1f МБ), уже было %d, пропало из буфера %d, вне папок профилей %d, ошибок %d, %v",
				st.Saved, float64(st.SavedGz)/1e6, st.Known, st.Missing, st.NoRel, len(st.Errors), st.Took)
			if st.NoRel > 0 && st.Saved == 0 && st.Known == 0 {
				log.Printf("архив: ВНИМАНИЕ — ни один отобранный файл не лежит в папках профилей (%s): "+
					"таблицы признаков сняты с другого пути? Пути в таблице и папки профиля должны совпадать", ar.s.rootsLabel())
			}
		}
	}()
	switch {
	case ar.s.cfg == nil || ar.s.cfg.Get().DataDir == "":
		st.Note = "папка данных службы не задана — «Хранение» → «Папка данных службы»"
		return
	case ar.s.an == nil:
		st.Note = "анализ не включён — отбирать нечего"
		return
	case len(arcs) == 0:
		st.Note = "архив не открылся — см. журнал сервера"
		return
	}
	c := ar.s.cfg.Get()
	every := c.Archive.KeepEvery
	tabs, err := ar.s.an.tables()
	if err != nil {
		st.Note = err.Error()
		return
	}
	for _, p := range c.EnabledProfiles() {
		a := arcs[p.ID]
		if a == nil {
			continue
		}
		for _, op := range p.Ops {
			if tabs[op] == "" {
				continue // признаков этой операции ещё нет
			}
			res, err := ar.s.an.get(op)
			if err != nil {
				st.Errors = append(st.Errors, fmt.Sprintf("%s: %v", op, err))
				continue
			}
			if ar.saveOp(a, p.ID, op, archive.Select(res, op, every), &st) {
				return // архивы доросли до потолка
			}
		}
	}
}

// saveOp — сохранить отобранное одной операции в архив её профиля. true — потолок, дальше не идём.
func (ar *archiveRunner) saveOp(a *archive.Archive, profile, op string, picks []archive.Pick, st *archiveStatus) bool {
	for _, p := range picks {
		root, rel := ar.s.rootOf(p.File)
		if rel == "" || root.Profile != profile {
			st.NoRel++
			continue
		}
		key := archRel(root, rel)
		if a.Has(key) {
			st.Known++
			continue
		}
		saved, err := a.Save(p.File, key, op, p.Reasons)
		switch {
		case errors.Is(err, os.ErrNotExist):
			st.Missing++ // буфер уже удалил — не ошибка, просто опоздали
		case errors.Is(err, archive.ErrFull):
			st.Full = true
			return true
		case err != nil:
			if len(st.Errors) < 5 {
				st.Errors = append(st.Errors, err.Error())
			}
		case saved:
			st.Saved++
			st.SavedGz += fileSize(a.Path(key))
			ar.mu.Lock()
			ar.limit() // потолок общий: место, занятое одним архивом, у остальных убывает
			ar.mu.Unlock()
		}
	}
	return false
}

func fileSize(p string) int64 {
	if st, err := os.Stat(p); err == nil {
		return st.Size()
	}
	return 0
}

// adminArchiveRun — кнопка «Сохранить сейчас» (только с этого компьютера).
func (s *Server) adminArchiveRun(w http.ResponseWriter, r *http.Request) {
	go s.arch.Run()
	http.Redirect(w, r, "/admin?saved=archive-run#archive", http.StatusSeeOther)
}

// StartArchive — включить архивацию по расписанию (вызывается из main, если есть настройки).
func (s *Server) StartArchive(every time.Duration) {
	if s.arch != nil {
		s.arch.Start(every)
	}
}
