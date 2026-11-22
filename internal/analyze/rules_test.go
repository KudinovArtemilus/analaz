package analyze

import (
	"math"
	"testing"
	"time"
)

// Новые правила (в Python их нет). Эталоны на pandas — ref/dump_peers.py и ref/dump_runs.py,
// на настоящих таблицах OP1/OP2 совпадение с Go бит в бит. Здесь — поведение на простых данных.

func peerResult(md [6]float64, baseMD1 float64, lost bool) *Result {
	times := make([]time.Time, 50)
	cols := make([]col, 6)
	for k := range cols {
		cols[k] = col{name: "casedrain_md" + string(rune('1'+k)) + "_mean", x: make([]float64, 50)}
	}
	for i := range times {
		times[i] = time.Date(2026, 11, 1, 0, i, 0, 0, time.UTC)
		for k := range cols {
			v := md[k] + float64(i%3)*0.001
			if k == 0 && i < 10 { // база — первые 10 циклов
				v = baseMD1
			}
			cols[k].x[i] = v
		}
	}
	p := testSet(times, cols...)
	r := &Result{Prod: p, Base: p.Rows(rangeIdx(0, 10))}
	if lost {
		r.Lost = []SignalLost{{Feature: "casedrain_md6_mean"}}
	}
	return r
}

func TestPeerOutliers(t *testing.T) {
	// MD1 в 15 раз ниже соседей и так было всегда
	o := peerResult([6]float64{0.03, 0.48, 0.49, 0.5, 0.51, 0.6}, 0.03, false).PeerOutliers()
	if len(o) != 1 || o[0].Feature != "casedrain_md1_mean" || !o[0].Always || o[0].Z > -5 {
		t.Fatalf("MD1 всегда: %+v", o)
	}
	// в норме MD1 был как все — «стал отличаться»
	if o := peerResult([6]float64{0.03, 0.48, 0.49, 0.5, 0.51, 0.6}, 0.49, false).PeerOutliers(); len(o) != 1 || o[0].Always {
		t.Errorf("MD1 стал: %+v", o)
	}
	// отличался и в норме, но отрыв вырос больше чем в 1,5 раза — «давно, но хуже»
	if o := peerResult([6]float64{0.03, 0.48, 0.49, 0.5, 0.51, 0.6}, 0.3, false).PeerOutliers(); len(o) != 1 || o[0].Kind() != "worse" {
		t.Errorf("давно, но хуже: %+v", o)
	}
	// мёртвый MD6 (пропал) не участвует: иначе раздул бы разброс
	if o := peerResult([6]float64{0.03, 0.48, 0.49, 0.5, 0.51, 0}, 0.03, true).PeerOutliers(); len(o) != 1 || o[0].N != 5 {
		t.Errorf("без пропавшего: %+v", o)
	}
	// разброс есть, но меньше 30% — не «чужак», даже при большом z
	if o := peerResult([6]float64{0.6, 0.48, 0.49, 0.5, 0.51, 0.5}, 0.6, false).PeerOutliers(); len(o) != 0 {
		t.Errorf("+20%% — не чужак: %+v", o)
	}
}

func TestOffRuns(t *testing.T) {
	at := func(m int) time.Time {
		return time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC).Add(time.Duration(m) * time.Minute)
	}
	var times []time.Time
	var x []float64
	// отрезок 1: 30 циклов в норме; пауза 3 ч; отрезок 2: 30 циклов, первые 12 в норме, потом −8σ
	for i := 0; i < 30; i++ {
		times, x = append(times, at(i)), append(x, 0)
	}
	for i := 0; i < 30; i++ {
		v := 0.0
		if i >= 12 {
			v = -8
		}
		times, x = append(times, at(210+i)), append(x, v)
	}
	p := testSet(times, col{"hp_min", x})
	r := &Result{All: p, Prod: p, Cols: Columns{Eng: []string{"hp_min"}}, Norms: []Norm{{Median: 0, Sigma: 1}},
		Stops: &StopsResult{GapThrMin: 20}}
	runs := r.OffRuns()
	if len(runs) != 2 || len(runs[0].Off) != 0 || len(runs[1].Off) != 1 {
		t.Fatalf("отрезки: %+v", runs)
	}
	f := runs[1].Off[0]
	// медиана 30 значений (12 нулей и 18 по −8) = −8; первые 10 — 0: не с пуска;
	// медиана последних 5 впервые −8 на 15-м цикле отрезка (3 из 5 уже −8)
	if f.Z != -8 || f.FromStart || !f.Since.Equal(at(210+14)) || math.IsNaN(f.Z0) {
		t.Errorf("признак: %+v", f)
	}
}
