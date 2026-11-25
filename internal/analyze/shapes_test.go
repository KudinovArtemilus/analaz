package analyze

import (
	"fmt"
	"testing"
	"time"
)

// TestShapePicks — типичный цикл суток — ближайший к медиане усилия; среди «предпочтительных»
// (в архиве на диске), если они есть; больше max суток — равномерно; негодный файл — пропуск.
func TestShapePicks(t *testing.T) {
	var times []time.Time
	var force []float64
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for day := range 10 {
		for k, f := range []float64{20000, 21000, 25000, 22000, 30000} { // медиана — 22000 (k = 3)
			times = append(times, t0.Add(time.Duration(day)*24*time.Hour+time.Duration(k)*time.Hour))
			force = append(force, f+float64(day))
		}
	}
	d := testSet(times, col{"force_max_kN", force})
	for i := range d.File {
		d.File[i] = fmt.Sprintf("d%02d_k%d.shp", i/5, i%5)
	}
	r := &Result{Prod: d, Bins: &Bins{Step: 24 * time.Hour}}

	p := r.ShapePicks(0, nil, nil)
	if len(p) != 10 || p[0].File != "d00_k3.shp" || !p[0].Start.Equal(t0) {
		t.Fatalf("без предпочтений: %+v", p[:min(2, len(p))])
	}
	// в архиве — только k1 (21000): берём его, хотя k3 ближе к медиане
	p = r.ShapePicks(0, func(f string) bool { return f[4:] == "k1.shp" }, nil)
	if p[0].File != "d00_k1.shp" {
		t.Errorf("предпочтительный: %s", p[0].File)
	}
	// не больше 4 суток — равномерно: 0, 3, 6, 9
	p = r.ShapePicks(4, nil, nil)
	if len(p) != 4 || p[1].File != "d03_k3.shp" || p[3].File != "d09_k3.shp" {
		t.Errorf("равномерно: %v", p)
	}
	// файла 5-х суток нет — сутки пропускаются
	p = r.ShapePicks(0, nil, func(f string) bool { return f[:3] != "d05" })
	if len(p) != 9 {
		t.Errorf("без пропавшего файла: %d суток", len(p))
	}
}
