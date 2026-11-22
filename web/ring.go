package web

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/ring"
)

// Захват кольцевых буферов (профили с галочкой «кольцевой буфер», пресс 5): каждые ringEvery
// новые циклы кольца копируются в <папка данных>\ring\<id> под постоянными именами, а признаки,
// архив и «Циклы» берут их оттуда (config.Dirs). Раз в час — чистка копий старше RingKeep.

const ringEvery = 30 * time.Second // кольцо пресса 5 — ~6 ч работы, цикл ~1,5 мин: запас огромный

type ringRunner struct {
	s *Server

	run     sync.Mutex               // один проход за раз; caps и pruneAt — только под ним
	caps    map[string]*ring.Capture // по папке-источнику: помнит виденные файлы между проходами
	pruneAt time.Time

	mu     sync.Mutex             // только состояние: первый проход копирует всё кольцо (сотни МБ
	status map[string]*ringStatus // по сети), страница настроек не должна ждать его
}

// ringStatus — что показать в настройках у профиля.
type ringStatus struct {
	Last     time.Time // последний проход
	Copied   int       // скопировано с запуска сервера
	LastNew  time.Time // когда скопирован последний новый цикл
	Err      string    // папка не открылась
	BadFiles []string  // файлы, которые не прочитались в последнем проходе
	Pruned   int       // удалено старых копий с запуска
}

func newRingRunner(s *Server) *ringRunner {
	return &ringRunner{s: s, caps: map[string]*ring.Capture{}, status: map[string]*ringStatus{}}
}

// Start — проход сейчас и потом каждые every.
func (rr *ringRunner) Start(every time.Duration) {
	go func() {
		for {
			rr.Run(time.Now())
			time.Sleep(every)
		}
	}()
}

// Run — один проход по всем включённым кольцевым профилям.
func (rr *ringRunner) Run(now time.Time) {
	rr.run.Lock()
	defer rr.run.Unlock()
	c := rr.s.cfg.Get()
	prune := now.Sub(rr.pruneAt) >= time.Hour
	for _, p := range c.EnabledProfiles() {
		if !p.Ring || len(p.Ops) != 1 {
			continue
		}
		var errText string
		var bad []string
		copied, pruned := 0, 0
		loc := time.FixedZone(fmt.Sprintf("UTC%+d", p.TZ), p.TZ*3600)
		for i, src := range p.Sources {
			dst := c.RingDir(p, i)
			cp := rr.caps[src]
			if cp == nil || cp.Dst != dst || cp.Op != p.Ops[0] {
				cp = &ring.Capture{Src: src, Dst: dst, Op: p.Ops[0], Loc: loc}
				rr.caps[src] = cp
			}
			res, err := cp.Run()
			if err != nil {
				errText = err.Error()
				continue
			}
			if res.New > 0 {
				copied += res.New
				log.Printf("кольцо %s: скопировано новых циклов %d (папка %s)", p.Name, res.New, src)
			}
			bad = append(bad, res.Errors...)
			if prune {
				if n, err := ring.Prune(dst, c.Archive.RingKeep(), now); err == nil && n > 0 {
					pruned += n
					log.Printf("кольцо %s: удалено старых копий %d (скопированы больше %d сут назад)", p.Name, n, int(c.Archive.RingKeep().Hours()/24))
				}
			}
		}
		rr.mu.Lock()
		st := rr.status[p.ID]
		if st == nil {
			st = &ringStatus{}
			rr.status[p.ID] = st
		}
		st.Last, st.Err, st.BadFiles = now, errText, bad
		st.Copied += copied
		st.Pruned += pruned
		if copied > 0 {
			st.LastNew = now
		}
		rr.mu.Unlock()
	}
	if prune {
		rr.pruneAt = now
	}
}

// Status — копия состояния профиля (nil — кольцо ещё не захватывалось).
func (rr *ringRunner) Status(id string) *ringStatus {
	if rr == nil {
		return nil
	}
	rr.mu.Lock()
	defer rr.mu.Unlock()
	st := rr.status[id]
	if st == nil {
		return nil
	}
	cp := *st
	cp.BadFiles = append([]string(nil), st.BadFiles...)
	return &cp
}

// StartRing — включить захват кольцевых буферов (из main, если есть настройки).
func (s *Server) StartRing() {
	if s.ring != nil {
		s.ring.Start(ringEvery)
	}
}
