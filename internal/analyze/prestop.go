package analyze

import (
	"math"
	"regexp"
	"slices"
	"time"
)

// 6.2д — «Как ведут себя признаки перед остановами» (charts.py, раздел 6).
//
// 12 самых долгих простоев совмещены по моменту останова (0 = последний цикл перед паузой).
// По каждому — 72 часа рабочих циклов до него, признаки в σ от нормы, сглаженные медианой
// по 5 циклам. Признаки — 4 разных сигнала, которые чаще всего отклонялись перед остановами
// (Precursors). Если линии систематически уходят из полосы ±3σ за N часов до 0 — это и есть
// «как умирает» узел и запас времени на предупреждение.

// PreStopLine — один останов: время до него (часы, ≤ 0) и z каждого признака.
type PreStopLine struct {
	Stop  time.Time // последний цикл перед остановом
	Hours float64   // длительность простоя, ч
	Color int       // номер останова среди 12 самых долгих — для постоянного цвета (как PAL[k] в Python)
	X     []float64 // часы до останова
	Z     [][]float64
}

// PreStops — данные графика: признаки и линии остановов.
type PreStops struct {
	Features []string
	Lines    []PreStopLine
}

const (
	preStopTop    = 12 // самых долгих простоев
	preStopFeats  = 4  // признаков (разных сигналов)
	preStopWindow = 72 * time.Hour
	preStopSmooth = 5 // медиана по 5 циклам
)

var reSigSuffix = regexp.MustCompile(`_(mean|min|max|p95|rms|absmax|active)$`)

// BeforeStops — nil, если остановов или предвестников нет.
func (r *Result) BeforeStops() *PreStops {
	if r.Stops == nil || len(r.Stops.Stops) == 0 || len(r.Stops.Precursors) == 0 {
		return nil
	}
	// 4 признака — по одному на сигнал: из hp_min и hp_mean остаётся тот, что отклонялся чаще
	out := &PreStops{}
	seen := map[string]bool{}
	var idx []int // номера признаков в Cols.Eng / Norms
	for _, c := range r.Stops.Precursors {
		k := reSigSuffix.ReplaceAllString(c.Feature, "")
		if !seen[k] {
			seen[k] = true
			if j := slices.Index(r.Cols.Eng, c.Feature); j >= 0 {
				out.Features = append(out.Features, c.Feature)
				idx = append(idx, j)
			}
		}
		if len(out.Features) == preStopFeats {
			break
		}
	}
	p := r.Prod
	for k, s := range r.Stops.Stops[:min(preStopTop, len(r.Stops.Stops))] {
		// рабочие циклы за 72 ч до останова включительно: (ts − 72ч, ts]
		var rows []int
		for i, t := range p.Times {
			if !t.After(s.From) && t.After(s.From.Add(-preStopWindow)) {
				rows = append(rows, i)
			}
		}
		if len(rows) < 3 {
			continue // цвет k при этом пропускается — как в Python
		}
		line := PreStopLine{Stop: s.From, Hours: s.Hours, Color: k}
		for _, i := range rows {
			line.X = append(line.X, p.Times[i].Sub(s.From).Seconds()/3600)
		}
		for _, j := range idx {
			col, n := p.Col(r.Cols.Eng[j]), r.Norms[j]
			z := make([]float64, len(rows))
			for q, i := range rows {
				z[q] = (col[i] - n.Median) / n.Sigma
			}
			line.Z = append(line.Z, TrailingMedian(z, preStopSmooth))
		}
		out.Lines = append(out.Lines, line)
	}
	return out
}

// TrailingMedian — pandas rolling(w, min_periods=1).median(): окно из w последних значений
// (текущее и до w−1 предыдущих), NaN пропускаются; в окне нет чисел — NaN.
// При чётном числе значений — среднее двух средних, как pandas.
func TrailingMedian(x []float64, w int) []float64 {
	out := make([]float64, len(x))
	win := make([]float64, 0, w)
	for i := range x {
		win = win[:0]
		for _, v := range x[max(0, i-w+1) : i+1] {
			if !math.IsNaN(v) {
				win = append(win, v)
			}
		}
		out[i] = math.NaN()
		if m := len(win); m > 0 {
			slices.Sort(win)
			if m%2 == 1 {
				out[i] = win[m/2]
			} else {
				out[i] = (win[m/2-1] + win[m/2]) / 2
			}
		}
	}
	return out
}
