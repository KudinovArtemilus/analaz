package web

import (
	"net/http"
	"os"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
	"github.com/KudinovArtemilus/press-go/internal/lttb"
	"github.com/KudinovArtemilus/press-go/internal/netio"
)

// 6.2е — форма цикла (charts.py, cycle_shapes): типичный цикл каждого дня наложением
// (синие — начало периода, красные — конец) и самые сильные аномалии против типичного.
// Кривые читаются из самих .shp — из архива на диске, если цикл там есть, иначе из буфера
// регистратора по сети (с ограничением скорости netio). Поэтому типичный цикл дня выбирается
// в первую очередь среди архивных, а на чтение отпущено не больше shapesBudget.

// shapeChannels — каналы, как CYCLE_CH в charts.py; у дней — первые четыре.
var shapeChannels = []struct{ Name, Label string }{
	{"slide:average val.encoder [mm]", "Позиция ползуна, мм"},
	{"slide:total force [kN]", "Усилие, кН"},
	{"hyd:high pressure [bar]", "Высокое давление, бар"},
	{"slide:deviation left-right [mm]", "Перекос Л-П, мм"},
	{"slide: speed [mm/s]", "Скорость ползуна, мм/с"},
	{"Pump 1.1 actPivoting", "Угол насоса 1.1, %"},
}

const (
	shapeDayChannels = 4
	shapeAnomalies   = 6
	shapePoints      = 500              // точек на кривую (LTTB): форма видна, ответ — не мегабайты
	shapesBudget     = 30 * time.Second // дольше не читаем: страница не должна висеть на сети
)

type shapeCurve struct {
	T []float64 `json:"t"`
	Y []num     `json:"y"`
}

type shapeCycle struct {
	Label  string                 `json:"label"` // день (час) или время цикла
	Rel    string                 `json:"rel"`   // адрес для «открыть цикл»
	Curves map[string]*shapeCurve `json:"curves"`
}

type shapesJSON struct {
	Step      string       `json:"step"` // day | hour
	Channels  []string     `json:"channels"`
	Labels    []string     `json:"labels"`
	Days      []shapeCycle `json:"days"`
	Ref       *shapeCycle  `json:"ref"` // типичный цикл (середина периода) — для сравнения с аномалиями
	Anomalies []shapeCycle `json:"anomalies"`
	Skipped   int          `json:"skipped"` // не успели прочитать за shapesBudget (сеть)
	Missing   int          `json:"missing"` // файла уже нет ни в буфере, ни в архиве
}

func (s *Server) analysisShapes(w http.ResponseWriter, r *http.Request) {
	res, _, ok := s.result(w, r)
	if !ok {
		return
	}
	out := shapesJSON{Step: "day", Days: []shapeCycle{}, Anomalies: []shapeCycle{}}
	lay := "02.01"
	if res.Bins.Step < 24*time.Hour {
		out.Step, lay = "hour", "02.01 15:00"
	}
	for _, c := range shapeChannels {
		out.Channels, out.Labels = append(out.Channels, c.Name), append(out.Labels, c.Label)
	}
	local := func(file string) bool { // читать с диска (архив, копии кольца), а не по сети
		root, rel := s.rootOf(file)
		if rel == "" {
			return false
		}
		if a := s.arch.forProfile(root.Profile); a != nil && a.Has(archRel(root, rel)) {
			return true
		}
		return netio.Host(file) == "" && exists(file)
	}
	avail := func(file string) bool {
		if local(file) {
			return true
		}
		if s.relData(file) == "" || !exists(file) {
			out.Missing++
			return false
		}
		return true
	}
	deadline := time.Now().Add(shapesBudget)
	load := func(file, label string, nch int) (*shapeCycle, bool) {
		if time.Now().After(deadline) {
			out.Skipped++
			return nil, false
		}
		rel := s.relData(file)
		f, err := s.open(rel)
		if err != nil {
			out.Missing++
			return nil, false
		}
		t := f.Times()
		c := &shapeCycle{Label: label, Rel: rel, Curves: map[string]*shapeCurve{}}
		for _, ch := range shapeChannels[:nch] {
			y, ok := f.Analog(ch.Name) // у пресса 5 — его каналы под именами пресса 10
			if !ok || len(y) != len(t) {
				continue
			}
			tt, yy := lttb.Downsample(t, y, shapePoints)
			c.Curves[ch.Name] = &shapeCurve{T: tt, Y: toNum(yy)}
		}
		return c, len(c.Curves) > 0
	}

	picks := res.ShapePicks(analyze.ShapeMaxDays, local, avail)
	for _, p := range picks {
		if c, ok := load(p.File, p.Start.Format(lay), shapeDayChannels); ok {
			out.Days = append(out.Days, *c)
		}
	}
	if len(picks) > 0 {
		if an := res.ShapeAnomalies(shapeAnomalies); len(an) > 0 {
			ref := picks[len(picks)/2] // как в Python: типичный цикл из середины периода
			if c, ok := load(ref.File, "типичный цикл "+ref.Time.Format("02.01 15:04"), len(shapeChannels)); ok {
				out.Ref = c
				for _, a := range an {
					if !avail(a.File) {
						continue
					}
					if c, ok := load(a.File, a.Time.Format("02.01 15:04:05"), len(shapeChannels)); ok {
						out.Anomalies = append(out.Anomalies, *c)
					}
				}
			}
		}
	}
	writeJSON(w, out)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
