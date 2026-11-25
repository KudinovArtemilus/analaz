package analyze

import (
	"testing"
	"time"
)

func TestSuggestNormPeriods(t *testing.T) {
	// 3 суток по 48 циклов через 30 мин; во вторые сутки — два останова по 2 часа
	var times []time.Time
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	for d := range 3 {
		at := t0.Add(time.Duration(d) * 24 * time.Hour)
		for i := range 40 {
			if d == 1 && (i == 10 || i == 25) {
				at = at.Add(2 * time.Hour)
			}
			times = append(times, at)
			at = at.Add(30 * time.Minute)
		}
	}
	n := len(times)
	r := &Result{Opts: DefaultOptions(), Prod: testSet(times, col{"x", make([]float64, n)}),
		Anom: &Anomalies{Flag: make([]bool, n)}}
	for i := 80; i < 90; i++ { // в третьи сутки 10 аномальных
		r.Anom.Flag[i] = true
	}

	ps := SuggestNormPeriods(r, 30, 3)
	if len(ps) != 3 {
		t.Fatalf("периодов %d: %+v", len(ps), ps)
	}
	// спокойнее всего первые сутки (0 остановов, 0 аномалий), потом третьи (аномалии), потом вторые (остановы)
	if !ps[0].From.Equal(times[0]) || ps[0].Stops != 0 || ps[0].AnomPct != 0 {
		t.Errorf("первый: %+v", ps[0])
	}
	if !ps[1].From.Equal(times[80]) || ps[1].AnomPct != 25 {
		t.Errorf("второй: %+v", ps[1])
	}
	if !ps[2].From.Equal(times[40]) || ps[2].Stops != 2 {
		t.Errorf("третий: %+v", ps[2])
	}
	// нужно больше циклов, чем в сутках, — период растягивается на двое суток и не пересекается с другим
	ps = SuggestNormPeriods(r, 60, 3)
	if len(ps) != 1 || ps[0].Days != 2 || ps[0].Cycles != 80 {
		t.Errorf("двое суток: %+v", ps)
	}
	if SuggestNormPeriods(r, 500, 3) != nil {
		t.Error("циклов меньше нужного — подсказок быть не должно")
	}
}
