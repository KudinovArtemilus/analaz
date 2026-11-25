package analyze

import (
	"math"
	"strings"
	"testing"
	"time"
)

// Зажим держал 130 бар; потом «спокойные» сутки, когда он уже 95 — если взять их за норму,
// занижение станет «нормой». Проверка периода должна это показать: хуже начала данных,
// хуже прежней нормы, ниже паспортного минимума. Температура внутри периода плывёт вверх.
func TestCheckBase(t *testing.T) {
	n := 400
	times := everyMin(n, 10*time.Minute)
	clamp, temp := make([]float64, n), make([]float64, n)
	for i := range n {
		clamp[i] = 130 + float64(i%3)
		temp[i] = 40 + float64(i%2)*0.2
		if i >= 200 {
			clamp[i] = 95 + float64(i%3)
			temp[i] = 40 + float64(i-200)*0.05 + float64(i%2)*0.2 // +10 °C за период
		}
	}
	P := testSet(times, col{"dt_ms", constCol(n, 5)}, col{"dieclamp_c1_min", clamp}, col{"temp_bottom_mean", temp})
	r := &Result{Prod: P, Base: P.Rows(rangeIdx(200, n))}
	prev := &NormSet{BaseFrom: LocalTime{times[0]}, Features: map[string]*FeatureNorm{
		"dieclamp_c1_min": {Median: 131, Sigma: 1}}}
	lim := map[string]Limit{"dieclamp_c1_min": {Min: 110, Max: math.NaN(), Note: "паспорт"}}

	got := map[string]string{}
	for _, c := range r.CheckBase(prev, lim) {
		got[c.Kind+" "+c.Label] = c.Text
	}
	want := map[string]string{
		"passport Зажим штампа (контуры 1 и 2): давление зажима, контур 1, минимум": "меньше паспортного минимума 110 бар (паспорт)",
		"prev Зажим штампа (контуры 1 и 2): давление зажима, контур 1, минимум":     "в прежней норме (01.11) 131 бар (-27%)",
		"start Зажим штампа (контуры 1 и 2): давление зажима, контур 1, минимум":    "в начале данных (01.11) 131 бар",
		"trend Масло: температура и уровень: температура масла, нижний бак":         "внутри периода плывёт: в начале 41.3 °C, в конце 48.8 °C за 33 ч (+18%)",
		"start Масло: температура и уровень: температура масла, нижний бак":         "в начале данных (01.11) 40.1 °C (+12%)",
	}
	for k, w := range want {
		if !strings.Contains(got[k], w) {
			t.Errorf("%s: %q, ждали …%s…", k, got[k], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("лишние находки: %v", got)
	}

	// хороший период (начало данных) — находок нет
	r.Base = P.Rows(rangeIdx(0, 200))
	if cs := r.CheckBase(nil, lim); len(cs) != 0 {
		t.Errorf("хороший период: %+v", cs)
	}
}

// Паспортный предел — «проблема», даже если норма снята, когда давление уже было занижено.
func TestEvaluatePassportLimit(t *testing.T) {
	n := 40
	x := make([]float64, n)
	for i := range x {
		x[i] = 95 + float64(i%3)
	}
	P := testSet(everyMin(n, time.Hour), col{"dieclamp_lower_min", x})
	med := func(string) float64 { return 96 } // норма — тоже 96: по норме всё «хорошо»
	diag, _ := Evaluate(P, med, map[string]float64{"dieclamp_lower_min": 1}, float64(n-1), nil, nil)
	for _, d := range diag {
		if d.Name == "Зажим нижнего штампа" && d.Status != OK {
			t.Fatalf("без предела: %+v", d)
		}
	}
	lim := map[string]Limit{"dieclamp_lower_min": {Min: 110, Max: math.NaN()}}
	diag, _ = Evaluate(P, med, map[string]float64{"dieclamp_lower_min": 1}, float64(n-1), nil, lim)
	for _, d := range diag {
		if d.Name == "Зажим нижнего штампа" {
			if d.Status != Alarm || len(d.Facts) == 0 || !strings.Contains(d.Facts[0], "меньше паспортного минимума 110 бар") {
				t.Fatalf("с пределом: %+v", d)
			}
			return
		}
	}
	t.Fatal("нет узла «Зажим нижнего штампа»")
}

func constCol(n int, v float64) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = v
	}
	return x
}
