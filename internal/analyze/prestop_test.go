package analyze

import (
	"math"
	"slices"
	"testing"
	"time"
)

// Сверка с Python (charts.py раздел 6, сортировки устойчивые) на настоящих таблицах —
// отдельно, скриптом ref/dump_prestop.py: OP1 3990 и OP2 3805 значений бит в бит.

func TestTrailingMedian(t *testing.T) {
	nan := math.NaN()
	got := TrailingMedian([]float64{5, 1, nan, 3, 9, 7, 2}, 5)
	// окна: [5] [5 1] [5 1] [5 1 3] [5 1 3 9] [1 3 9 7] [3 9 7 2]
	want := []float64{5, 3, 3, 3, 4, 5, 5}
	if !slices.Equal(got, want) {
		t.Errorf("медиана назад: %v, ждали %v", got, want)
	}
	if v := TrailingMedian([]float64{nan}, 5)[0]; !math.IsNaN(v) {
		t.Errorf("одни NaN — NaN, а не %v", v)
	}
}

func TestBeforeStops(t *testing.T) {
	at := func(h float64) time.Time {
		return time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC).Add(time.Duration(h * float64(time.Hour)))
	}
	// циклы каждый час, 0…100 ч; остановы после циклов 80 ч, 1 ч и 50 ч
	var times []time.Time
	var x []float64
	for h := 0; h <= 100; h++ {
		times = append(times, at(float64(h)))
		x = append(x, float64(h))
	}
	p := testSet(times, col{"hp_min", x}, col{"hp_mean", x})
	r := &Result{
		Prod:  p,
		Cols:  Columns{Eng: []string{"hp_min", "hp_mean"}},
		Norms: []Norm{{Median: 0, Sigma: 1}, {Median: 0, Sigma: 1}},
		Stops: &StopsResult{
			Stops: []Stop{{From: at(80), Hours: 5}, {From: at(1), Hours: 3}, {From: at(50), Hours: 2}},
			// hp_min и hp_mean — один сигнал: берётся только первый (чаще отклонялся)
			Precursors: []Culprit{{"hp_mean", 4}, {"hp_min", 4}},
		},
	}
	ps := r.BeforeStops()
	if !slices.Equal(ps.Features, []string{"hp_mean"}) {
		t.Errorf("признаки: %v (один сигнал — один признак)", ps.Features)
	}
	// останов на 1 ч: в окне 2 цикла (<3) — пропущен, но его цвет (1) не достаётся следующему
	if len(ps.Lines) != 2 || ps.Lines[0].Color != 0 || ps.Lines[1].Color != 2 {
		t.Fatalf("линии: %d, цвета %v", len(ps.Lines), []int{ps.Lines[0].Color})
	}
	l := ps.Lines[0]
	// окно (8 ч, 80 ч] — 72 цикла, последний — ровно в момент останова (x = 0)
	if len(l.X) != 72 || l.X[0] != -71 || l.X[71] != 0 {
		t.Errorf("окно: %d точек, %v … %v", len(l.X), l.X[0], l.X[len(l.X)-1])
	}
	if l.Z[0][0] != 9 || l.Z[0][71] != 78 { // медиана последних 5: 9 (одна точка), 76..80 → 78
		t.Errorf("z: %v … %v", l.Z[0][0], l.Z[0][71])
	}
}
