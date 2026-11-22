package analyze

import (
	"math"
	"testing"
	"time"
)

// col — колонка для testSet.
type col struct {
	name string
	x    []float64
}

// testSet собирает Dataset из колонок (порядок колонок сохраняется).
func testSet(times []time.Time, cols ...col) *Dataset {
	d := &Dataset{Times: times, Op: make([]string, len(times)), File: make([]string, len(times)), index: map[string]int{}}
	for _, c := range cols {
		d.index[c.name] = len(d.Cols)
		d.Cols = append(d.Cols, c.name)
		d.Data = append(d.Data, c.x)
	}
	return d
}

func firstRows(d *Dataset, k int) *Dataset {
	idx := make([]int, k)
	for i := range idx {
		idx[i] = i
	}
	return d.Rows(idx)
}

func everyMin(n int, step time.Duration) []time.Time {
	t := make([]time.Time, n)
	for i := range t {
		t[i] = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * step)
	}
	return t
}

func TestFindStops(t *testing.T) {
	n := 100
	times := everyMin(n, 5*time.Minute)
	for i := 60; i < n; i++ { // после 60-го цикла пресс стоял 3 ч 05 мин
		times[i] = times[i].Add(3 * time.Hour)
	}
	x := make([]float64, n)
	for i := range x {
		x[i] = 10
		if i >= 40 && i < 60 { // перед остановом признак ушёл вверх
			x[i] = 20
		}
	}
	d := testSet(times, col{"hp_drop", x})
	r := FindStops(d, d, []string{"hp_drop"}, []float64{10}, []float64{1}, StopGapMin)
	if r.TypicalGapMin != 5 || r.GapThrMin != 30 {
		t.Errorf("обычный интервал %v, порог %v (ждали 5 и 30)", r.TypicalGapMin, r.GapThrMin)
	}
	if len(r.Stops) != 1 {
		t.Fatalf("остановов %d, ждали 1", len(r.Stops))
	}
	s := r.Stops[0]
	if s.Hours != 3.08 || !s.From.Equal(times[59]) || !s.Resume.Equal(times[60]) {
		t.Errorf("останов %+v (ждали 3.08 ч между циклами 59 и 60)", s)
	}
	// медиана последних 50 циклов: 30 по 10 и 20 по 20 → 10, z = 0 — «ничего заметного»
	if len(s.Before) != 0 || len(r.Precursors) != 0 {
		t.Errorf("предвестники %+v / %+v", s.Before, r.Precursors)
	}
}

func TestRound2LikePython(t *testing.T) {
	// round(2.675, 2) в Python = 2.67: двоичное 2.675 чуть меньше 2.675
	for in, want := range map[float64]float64{2.675: 2.67, 3.0833333: 3.08, 57.199999: 57.2, 0.005: 0.01} {
		if got := round2(in); got != want {
			t.Errorf("round2(%v) = %v, ждали %v", in, got, want)
		}
	}
}

func TestSensorHealth(t *testing.T) {
	n := 50
	live, frozen, nanShare := make([]float64, n), make([]float64, n), make([]float64, n)
	for i := range n {
		live[i] = 0.5
		frozen[i] = 0.5
		if i >= 40 {
			frozen[i] = 0 // последние 20% — замер
			nanShare[i] = 0.05
		}
	}
	d := testSet(everyMin(n, time.Minute),
		col{"a010_HP_Pressure__std", live}, col{"a011_LP_Pressure__std", frozen},
		col{"a012_Pump_Set_Value__std", frozen}, // уставка — «не меняется» не проблема
		col{"a011_LP_Pressure__mean", make([]float64, n)}, col{"a011_LP_Pressure__nan", nanShare})
	got := SensorHealth(d, d, firstRows(d, 10))
	if len(got) != 2 || got[0].Feature != "a011_LP_Pressure__std" || got[1].Feature != "a011_LP_Pressure__nan" {
		t.Fatalf("получили %+v", got)
	}
	if got[0].Signal != "LP Pressure" || math.Abs(got[1].Now-0.05) > 1e-12 || got[1].Base != 0 {
		t.Errorf("детали: %+v", got)
	}
}
