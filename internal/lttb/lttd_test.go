package lttb

import (
	"math"
	"testing"
)

func TestDownsampleKeepsPeak(t *testing.T) {
	n := 8125
	x, y := make([]float64, n), make([]float64, n)
	for i := range x {
		x[i] = float64(i) * 0.005
		y[i] = math.Sin(float64(i) / 400)
	}
	y[5000] = 25 // узкий пик в одну точку — как удар усилия

	ox, oy := Downsample(x, y, 1000)
	if len(ox) != 1000 || len(oy) != 1000 {
		t.Fatalf("ждали 1000 точек, получили %d", len(ox))
	}
	if ox[0] != x[0] || ox[len(ox)-1] != x[n-1] {
		t.Error("первая и последняя точки должны сохраниться")
	}
	peak := false
	for _, v := range oy {
		if v == 25 {
			peak = true
		}
	}
	if !peak {
		t.Error("пик потерян — ради этого LTTB и нужен")
	}
	for i := 1; i < len(ox); i++ {
		if ox[i] <= ox[i-1] {
			t.Fatalf("время должно строго расти: %v, %v", ox[i-1], ox[i])
		}
	}
}

func TestDownsampleNaNAndShort(t *testing.T) {
	ox, _ := Downsample([]float64{0, 1, 2, 3}, []float64{1, math.NaN(), 3, math.Inf(1)}, 1000)
	if len(ox) != 2 { // NaN и Inf отброшены, короткий ряд возвращается как есть
		t.Errorf("ждали 2 точки, получили %v", ox)
	}
}
