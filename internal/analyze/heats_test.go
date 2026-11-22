package analyze

import (
	"math"
	"slices"
	"testing"
	"time"
)

func TestHeatAndPieceHealth(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 11, 16, 8, m, 0, 0, time.UTC) }
	// плавка 7: заготовки 101, 102 (в норме); плавка 5 — раньше по времени: 103 (hp_min −6σ), 104
	p := testSet([]time.Time{at(10), at(11), at(0), at(1)},
		col{"fn_piece", []float64{101, 102, 103, 104}},
		col{"fn_heat", []float64{7, 7, 5, 5}},
		col{"hp_min", []float64{280, 280, 220, 280}})
	r := &Result{Prod: p, Cols: Columns{Eng: []string{"hp_min"}}, Norms: []Norm{{Median: 280, Sigma: 10}},
		Anom: &Anomalies{Flag: []bool{false, false, true, false}}}

	h := r.HeatHealth()
	// по времени: сначала плавка 5 (08:00), потом 7 (08:10)
	if !slices.Equal(h.Keys, []int{5, 7}) || !slices.Equal(h.Cycles, []int{2, 2}) || !slices.Equal(h.Anom, []int{1, 0}) {
		t.Fatalf("плавки: %+v", h)
	}
	// медиана hp_min по плавке 5 = (220+280)/2 = 250 → |z| = 3; одна подсистема — она же индекс
	if h.Z[0][0] != 3 || h.Index[0] != 3 || h.Z[0][1] != 0 {
		t.Errorf("z плавок: %v, индекс %v", h.Z, h.Index)
	}
	ph := r.PieceHealth(5)
	if !slices.Equal(ph.Keys, []int{103, 104}) || ph.Z[0][0] != 6 || ph.Z[0][1] != 0 {
		t.Errorf("заготовки плавки 5: %+v", ph)
	}
	if r.PieceHealth(99) != nil {
		t.Error("нет такой плавки — nil")
	}
	if math.IsNaN(h.Index[1]) {
		t.Error("индекс плавки 7 должен быть 0, а не NaN")
	}
}
