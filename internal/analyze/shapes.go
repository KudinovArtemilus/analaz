package analyze

import (
	"math"
	"sort"
	"time"
)

// 6.2е — форма цикла (charts.py, cycle_shapes). Здесь только выбор циклов; сами кривые
// читает сервер из .shp (буфер регистратора или архив).

// ShapePick — типичный цикл интервала: ближайший к медиане максимального усилия.
type ShapePick struct {
	Start time.Time // начало интервала (суток или часа — как у Bins)
	File  string
	Time  time.Time // время цикла
}

// ShapeMaxDays — не больше стольких типичных циклов (равномерно по периоду), как max_days в Python.
const ShapeMaxDays = 30

// ShapePicks — по одному типичному циклу на интервал (сутки; час, если период короче 5 дней).
// prefer — какие файлы брать в первую очередь (сервер: те, что лежат в архиве на диске, — их
// читать быстрее, чем по сети). Если среди файлов интервала нет «предпочтительных», берётся
// ближайший к медиане из всех. Больше max интервалов — равномерная выборка, как np.linspace.
// Если файл не годится (ok вернул false), интервал пропускается, как в Python (os.path.exists).
func (r *Result) ShapePicks(max int, prefer, ok func(file string) bool) []ShapePick {
	p := r.Prod
	force := p.Col("force_max_kN")
	if force == nil || p.Len() == 0 {
		return nil
	}
	step := r.Bins.Step
	if step <= 0 {
		step = 24 * time.Hour
	}
	groups := map[time.Time][]int{}
	var keys []time.Time
	for i, t := range p.Times {
		if math.IsNaN(force[i]) {
			continue
		}
		k := floorTo(t, step)
		if _, seen := groups[k]; !seen {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], i)
	}
	sort.Slice(keys, func(a, b int) bool { return keys[a].Before(keys[b]) })
	var picks []ShapePick
	for _, k := range keys {
		idx := groups[k]
		vals := make([]float64, len(idx))
		for j, i := range idx {
			vals[j] = force[i]
		}
		med := median(vals)
		best := func(only bool) int {
			b, bd := -1, math.Inf(1)
			for _, i := range idx {
				if only && (prefer == nil || !prefer(p.File[i])) {
					continue
				}
				if d := math.Abs(force[i] - med); d < bd { // первый при равенстве — как idxmin
					b, bd = i, d
				}
			}
			return b
		}
		i := best(true)
		if i < 0 {
			i = best(false)
		}
		picks = append(picks, ShapePick{Start: k, File: p.File[i], Time: p.Times[i]})
	}
	if max > 0 && len(picks) > max {
		sel := make([]ShapePick, 0, max)
		for j := range max {
			sel = append(sel, picks[int(float64(j)*float64(len(picks)-1)/float64(max-1))])
		}
		picks = sel
	}
	if ok != nil {
		out := picks[:0]
		for _, pk := range picks {
			if ok(pk.File) {
				out = append(out, pk)
			}
		}
		picks = out
	}
	return picks
}

// ShapeAnomalies — файлы k самых сильных аномальных циклов (как an_rows[:6] в Python).
func (r *Result) ShapeAnomalies(k int) []ShapePick {
	var out []ShapePick
	for _, i := range r.Order {
		if len(out) == k {
			break
		}
		out = append(out, ShapePick{File: r.Prod.File[i], Time: r.Prod.Times[i]})
	}
	return out
}

func median(x []float64) float64 {
	s := append([]float64(nil), x...)
	sort.Float64s(s)
	n := len(s)
	if n == 0 {
		return math.NaN()
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
