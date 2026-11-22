package analyze

import (
	"testing"
	"time"
)

func TestPieceAndHeat(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 11, 16, 8, m, 0, 0, time.UTC) }
	times := []time.Time{at(0), at(1), at(2), at(3)}
	all := testSet(times,
		col{"fn_piece", []float64{101, 102, 103, 104}},
		col{"fn_heat", []float64{7, 7, 8, 7}},
		col{"hp_min", []float64{270, 280, 290, 300}},
		col{"force_max_kN", []float64{25000, 25000, 100, 25000}}) // 103 — нерабочий цикл
	prod := all.Rows([]int{0, 1, 3})
	r := &Result{All: all, Prod: prod, Cols: Columns{Eng: []string{"hp_min"}}, Norms: []Norm{{Median: 280, Sigma: 10}},
		SigEng: []float64{10}, Anom: &Anomalies{Features: []string{"hp_min"}, Rm: [][]float64{{280, 280, 280}},
			Score: []float64{1, 0, 2}, Flag: []bool{false, false, true}, MaxZ: []float64{1, 0, 2}},
		Stops: &StopsResult{GapThrMin: 20}}
	r.Opts.Z = 6

	p := r.Piece(104)
	if len(p) != 1 || !p[0].Production || !p[0].Anomalous || p[0].Heat != 7 {
		t.Fatalf("заготовка 104: %+v", p)
	}
	var hp *PieceValue
	for k := range p[0].Key {
		if p[0].Key[k].Feature == "hp_min" {
			hp = &p[0].Key[k]
		}
	}
	if hp == nil || hp.Value != 300 || hp.Norm != 280 || hp.Z != 2 {
		t.Errorf("ключевой показатель: %+v", hp)
	}
	if p := r.Piece(103); len(p) != 1 || p[0].Production || p[0].Key != nil {
		t.Errorf("нерабочий цикл — без сравнения с нормой: %+v", p)
	}
	if p := r.Piece(999); p != nil {
		t.Errorf("нет такой — nil: %+v", p)
	}
	if h := r.Heat(7); len(h) != 3 || h[0].Piece != 101 || h[2].Piece != 104 {
		t.Errorf("плавка 7: %+v", h)
	}
}
