package analyze

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestCorrPinnedAndScatterRho — перекос ползуна и зажим штампа есть в матрице корреляций,
// даже если они не среди «трендовых», а у облака «перекос vs зажим» в подписи — ρ по циклам.
func TestCorrPinnedAndScatterRho(t *testing.T) {
	n := 240 // 10 суток по циклу в час
	cols := []col{}
	mk := func(name string, f func(i int) float64) {
		x := make([]float64, n)
		for i := range x {
			x[i] = f(i)
		}
		cols = append(cols, col{name, x})
	}
	mk("force_max_kN", func(i int) float64 { return 26000 + float64(i%5)*10 })
	mk("dt_ms", func(int) float64 { return 5 })
	// три признака с явным трендом — они займут «трендовые» места
	mk("md1_mean", func(i int) float64 { return 10 + float64(i)/10 })
	mk("hp_min", func(i int) float64 { return 280 - float64(i)/20 })
	mk("temp_bottom_mean", func(i int) float64 { return 40 + float64(i)/30 })
	// перекос и зажим контура 2: связаны (общая волна), без тренда
	mk("lr_dev_absmax_mm", func(i int) float64 { return 1 + 0.3*math.Sin(float64(i)/7) })
	mk("dieclamp_c2_min", func(i int) float64 { return 120 + 5*math.Sin(float64(i)/7) + float64(i%3) })
	d := testSet(everyMin(n, time.Hour), cols...)
	r, err := Run(d, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	c := r.Correlations()
	if c == nil || !slices.Contains(c.Features, "lr_dev_absmax_mm") || !slices.Contains(c.Features, "dieclamp_c2_min") {
		t.Fatalf("в матрице нет перекоса или зажима: %v", c)
	}
	var title string
	for _, p := range r.Scatter() {
		if p.X == "lr_dev_absmax_mm" && p.Y == "dieclamp_c2_min" {
			title = p.Title
		}
	}
	if !strings.Contains(title, "Перекос ползуна vs зажим штампа, контур 2 · ρ = +0.") || !strings.Contains(title, "по 240 циклам") {
		t.Errorf("подпись облака: %q", title)
	}
}
