package analyze

import (
	"math"
	"testing"
	"time"
)

// Сверка с Python (charts.py разделы 4, 5, 7) на настоящих таблицах OP1/OP2 — отдельно,
// скриптом ref/dump_compare.py: было/стало, 625 ячеек корреляций, точки пар и квартили
// длительности совпали бит в бит. Здесь — правила, которые легко сломать.

func TestSpearmanPairwise(t *testing.T) {
	nan := math.NaN()
	if r := SpearmanPairwise([]float64{1, 2, 3, 4}, []float64{10, 20, 30, 40}); r != 1 {
		t.Errorf("растут вместе: %v", r)
	}
	if r := SpearmanPairwise([]float64{1, 2, 3, 4}, []float64{4, 3, 2, 1}); r != -1 {
		t.Errorf("навстречу: %v", r)
	}
	// NaN: точка выпадает у ОБОИХ рядов, ранги пересчитываются по оставшимся
	if r := SpearmanPairwise([]float64{1, nan, 3, 4}, []float64{5, 100, 7, 8}); r != 1 {
		t.Errorf("с пропуском: %v", r)
	}
	if r := SpearmanPairwise([]float64{1, 1, 1}, []float64{1, 2, 3}); !math.IsNaN(r) {
		t.Errorf("постоянный ряд — NaN, а не %v", r)
	}
	if r := SpearmanPairwise([]float64{1, nan}, []float64{nan, 2}); !math.IsNaN(r) {
		t.Errorf("общих точек меньше двух — NaN, а не %v", r)
	}
	// повторы — средние ранги: x=[1,1,2] → [1.5,1.5,3]
	if r := SpearmanPairwise([]float64{1, 1, 2}, []float64{1, 2, 3}); math.Abs(r-0.8660254037844387) > 1e-15 {
		t.Errorf("повторы: %v", r)
	}
}

func TestCycleDurations(t *testing.T) {
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC) }
	r := &Result{
		Prod: testSet([]time.Time{day(1, 3), day(1, 9), day(1, 20), day(2, 1)}, col{"cycle_s", []float64{40, 42, 50, 41}}),
		Bins: &Bins{Step: 24 * time.Hour},
	}
	b := r.CycleDurations()
	if len(b) != 2 || b[0].N != 3 || b[0].Min != 40 || b[0].Med != 42 || b[0].Max != 50 || b[0].Q1 != 41 || b[0].Q3 != 46 {
		t.Errorf("по дням: %+v", b)
	}
	if !b[1].Start.Equal(day(2, 0)) {
		t.Errorf("начало дня: %v", b[1].Start)
	}
}

func TestBeforeAfterTail(t *testing.T) {
	times := make([]time.Time, 60)
	x := make([]float64, 60)
	for i := range times {
		times[i] = time.Date(2026, 11, 1, 0, i, 0, 0, time.UTC)
		x[i] = float64(i)
	}
	p := testSet(times, col{"hp_min", x})
	r := &Result{Prod: p, Base: p.Rows([]int{0, 1, 2}), Top: []Trend{{Feature: "hp_min"}, {Feature: "nope"}}}
	ba := r.BeforeAfterTop()
	// «стало» — последние max(10, 20%) = 12 циклов: 48…59
	if len(ba) != 1 || len(ba[0].Tail) != 12 || ba[0].Tail[0] != 48 || ba[0].TailMed != 53.5 || ba[0].BaseMed != 1 {
		t.Errorf("было/стало: %+v", ba)
	}
}
