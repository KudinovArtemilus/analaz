package analyze

import (
	"math"
	"sort"
	"time"
)

// Новое правило (в Python-отчёте его нет): «отрезок работы вне нормы».
//
// Отрезок — работа между двумя остановами. Если признак весь отрезок держится далеко от
// нормы — это режим, а не единичный выброс (аномалии ловят выбросы относительно соседних
// циклов и такой режим не видят: внутри отрезка все циклы одинаково «плохие»).
// Так нашлось, что перед остановами 17–18.11 давление насосов 6 было −6σ всю смену.
//
// Правило:
//   - отрезки — паузы между циклами больше порога останова (тот же, что у FindStops);
//   - рабочих циклов в отрезке ≥ 20;
//   - по каждому инженерному признаку z = (медиана за отрезок − норма) / σ; вне нормы — |z| ≥ 6;
//     порядок — по убыванию |z| (при равных — порядок признаков);
//   - «с пуска» — медиана первых 10 рабочих циклов отрезка тоже за 6σ в ту же сторону;
//   - «с какого времени» — первый цикл, где медиана последних 5 циклов ушла за 6σ в ту же сторону.
//
// Эталон на pandas — ref/dump_runs.py; совпадение с Go проверено бит в бит.

const (
	RunMinCycles = 20
	RunZ         = 6.0
	RunStartN    = 10
)

// OffRun — отрезок работы и признаки, которые весь отрезок были вне нормы.
type OffRun struct {
	From, To time.Time // первый и последний цикл отрезка (все циклы, не только рабочие)
	Cycles   int       // рабочих циклов
	Off      []RunFeature
}

// RunFeature — признак вне нормы за отрезок.
type RunFeature struct {
	Feature   string
	Z         float64   // медиана за отрезок, σ от нормы
	Z0        float64   // медиана первых 10 рабочих циклов
	FromStart bool      // вне нормы уже с пуска
	Since     time.Time // первый цикл, где медиана последних 5 ушла за 6σ (нулевое — не уходила)
}

// OffRuns — все отрезки с ≥ 20 рабочими циклами, по времени; Off пустой — отрезок в норме.
func (r *Result) OffRuns() []OffRun {
	all, p := r.All, r.Prod
	if all == nil || all.Len() == 0 || r.Stops == nil {
		return nil
	}
	// границы отрезков: паузы больше порога
	cuts := []int{0}
	for i := 1; i < all.Len(); i++ {
		if all.Times[i].Sub(all.Times[i-1]).Seconds()/60 > r.Stops.GapThrMin {
			cuts = append(cuts, i)
		}
	}
	cuts = append(cuts, all.Len())

	var out []OffRun
	for k := 0; k+1 < len(cuts); k++ {
		t0, t1 := all.Times[cuts[k]], all.Times[cuts[k+1]-1]
		var rows []int
		for i, t := range p.Times {
			if !t.Before(t0) && !t.After(t1) {
				rows = append(rows, i)
			}
		}
		if len(rows) < RunMinCycles {
			continue
		}
		run := OffRun{From: t0, To: t1, Cycles: len(rows)}
		for j, f := range r.Cols.Eng {
			col, n := p.Col(f), r.Norms[j]
			z := make([]float64, len(rows))
			for q, i := range rows {
				z[q] = (col[i] - n.Median) / n.Sigma
			}
			// медиана z = z медианы (σ > 0, деление монотонно), но Python считает
			// (медиана − норма)/σ — повторяем ровно его порядок действий ради бита в бит
			zr := (NanMedian(pick(col, rows)) - n.Median) / n.Sigma
			if !(math.Abs(zr) >= RunZ) {
				continue
			}
			z0 := (NanMedian(pick(col, rows[:min(RunStartN, len(rows))])) - n.Median) / n.Sigma
			rf := RunFeature{Feature: f, Z: zr, Z0: z0,
				FromStart: math.Abs(z0) >= RunZ && math.Signbit(z0) == math.Signbit(zr)}
			for q, v := range TrailingMedian(z, 5) {
				if math.Abs(v) >= RunZ && math.Signbit(v) == math.Signbit(zr) {
					rf.Since = p.Times[rows[q]]
					break
				}
			}
			run.Off = append(run.Off, rf)
		}
		sort.SliceStable(run.Off, func(a, b int) bool { return math.Abs(run.Off[a].Z) > math.Abs(run.Off[b].Z) })
		out = append(out, run)
	}
	return out
}

func pick(x []float64, idx []int) []float64 {
	out := make([]float64, len(idx))
	for k, i := range idx {
		out[k] = x[i]
	}
	return out
}
