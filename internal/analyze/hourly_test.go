package analyze

import (
	"testing"
	"time"
)

// «Циклов в час»: часы подряд с нулями в простое; «по часам»: пустые часы пропущены.
// Сверка с Python (daily.py) на настоящих таблицах — ref/dump_hourly.py: бит в бит.
func TestHourly(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 10, h, m, 0, 0, time.UTC) }
	day := testSet([]time.Time{at(8, 0), at(8, 30), at(11, 10)},
		col{"cycle_s", []float64{40, 42, 44}}, col{"hp_min", []float64{270, 280, 290}})
	ph := perHour(day)
	if len(ph) != 4 || ph[0].N != 2 || ph[1].N != 0 || ph[2].N != 0 || ph[3].N != 1 {
		t.Errorf("циклов в час: %+v", ph)
	}
	q := func(string) (float64, float64, float64, bool) { return 1, 2, 3, true }
	p := dayPlots(day, q)
	var hp *DayPlot
	for i := range p {
		if p[i].Feature == "hp_min" {
			hp = &p[i]
		}
	}
	if hp == nil || len(hp.HourT) != 2 || hp.HourMed[0] != 275 || hp.HourMed[1] != 290 || hp.Lo != 1 || hp.Hi != 3 {
		t.Errorf("по часам: %+v", hp)
	}
}
