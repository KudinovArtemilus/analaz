package analyze

import (
	"math"
	"testing"
	"time"
)

// Ожидаемые значения посчитаны pandas: pd.Series(x).rolling(5, center=True, min_periods=2).median()
func TestRollingMedianMatchesPandas(t *testing.T) {
	nan := math.NaN()
	x := []float64{1, nan, 3, 10, 2, nan, nan, nan, 7, 8}
	want := []float64{2, 3, 2.5, 3, 3, 6, 4.5, 7.5, 7.5, 7.5}
	got := RollingMedian(x, 5, 2)
	for i := range want {
		if !(got[i] == want[i] || math.IsNaN(got[i]) && math.IsNaN(want[i])) {
			t.Errorf("[%d] = %v, ждали %v", i, got[i], want[i])
		}
	}
}

// Скачок в одном цикле — аномалия; медленный дрейф всех циклов — нет.
func TestDetectAnomaliesSpikeNotDrift(t *testing.T) {
	n := 120
	drift, spike := make([]float64, n), make([]float64, n)
	for i := range n {
		drift[i] = 100 + float64(i)*0.1 // медленно уходит на 12 единиц = 12σ за период
		spike[i] = 50 + float64(i%3)    // шум 50..52
	}
	spike[60] = 90
	d := &Dataset{Cols: []string{"drift", "spike"}, Data: [][]float64{drift, spike}, index: map[string]int{"drift": 0, "spike": 1}}
	d.File = make([]string, n)
	for i := range n {
		d.Times = append(d.Times, time.Date(2026, 11, 1, 0, i, 0, 0, time.UTC))
	}
	a := DetectAnomalies(d, []string{"drift", "spike"}, []float64{1, 1}, 6)
	for i, f := range a.Flag {
		if f != (i == 60) {
			t.Errorf("цикл %d: аномалия=%v (maxz %.1f)", i, f, a.MaxZ[i])
		}
	}
	r := a.Explain(d, []float64{1, 1}, 60, AnomTopK, 6)
	if len(r) != 1 || r[0].Feature != "spike" || r[0].Z != 39 {
		t.Errorf("объяснение: %+v", r)
	}
	if len(a.Culprits) != 1 || a.Culprits[0] != (Culprit{"spike", 1}) {
		t.Errorf("виновники: %+v", a.Culprits)
	}
}

// Краевой эффект (так же в Python): у первых и последних 25 циклов соседи только с одной
// стороны, поэтому при быстром дрейфе медиана «отстаёт», и крайние циклы выглядят аномальными.
// Тест фиксирует поведение, чтобы при будущем улучшении метода это было осознанным решением.
func TestDetectAnomaliesEdgeEffect(t *testing.T) {
	n := 120
	x := make([]float64, n)
	for i := range n {
		x[i] = float64(i) * 0.5 // дрейф 0,5σ на цикл
	}
	d := &Dataset{Cols: []string{"x"}, Data: [][]float64{x}, index: map[string]int{"x": 0}}
	d.Times, d.File = make([]time.Time, n), make([]string, n)
	a := DetectAnomalies(d, []string{"x"}, []float64{1}, 6)
	if !a.Flag[0] || !a.Flag[n-1] || a.Flag[n/2] {
		t.Errorf("ждали аномалии только на краях: первый %v, последний %v, середина %v", a.Flag[0], a.Flag[n-1], a.Flag[n/2])
	}
}
