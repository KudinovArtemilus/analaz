package features

import (
	"math"
	"testing"
)

var nan = math.NaN()

func TestNanFuncs(t *testing.T) {
	x := []float64{nan, 3, -1, nan, 7, 7}
	if NanMax(x) != 7 || NanMin(x) != -1 || NanSum(x) != 16 || NanMean(x) != 4 {
		t.Errorf("max %v min %v sum %v mean %v", NanMax(x), NanMin(x), NanSum(x), NanMean(x))
	}
	if NanArgMax(x) != 4 { // первый из двух максимумов, как np.nanargmax
		t.Errorf("argmax %d", NanArgMax(x))
	}
	all := []float64{nan, nan}
	if !math.IsNaN(NanMax(all)) || !math.IsNaN(NanMean(all)) || NanSum(all) != 0 || NanArgMax(all) != -1 {
		t.Error("для одних NaN ждали NaN / 0 / -1")
	}
}

func TestFirstTrue(t *testing.T) {
	if FirstTrue([]bool{false, true, true}) != 1 || FirstTrue([]bool{false, false}) != 0 {
		t.Error("FirstTrue должен вести себя как np.argmax по маске")
	}
}

func TestGradient(t *testing.T) {
	g := Gradient([]float64{0, 1, 4, 9}, 0.5) // x²; края — односторонние разности
	want := []float64{2, 4, 8, 10}            // сверено с np.gradient([0,1,4,9], 0.5)
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("Gradient = %v, ждали %v", g, want)
		}
	}
}

func TestSlope(t *testing.T) {
	if s := Slope([]float64{0, 1, 2, 3}, []float64{1, 3, 5, 7}); math.Abs(s-2) > 1e-12 {
		t.Errorf("наклон %v, ждали 2", s)
	}
	if !math.IsNaN(Slope([]float64{1, 1}, []float64{2, 3})) {
		t.Error("вертикальные точки: ждали NaN")
	}
}

func TestNanPercentile(t *testing.T) {
	// сверено с np.nanpercentile([nan, 1, 2, 3, 4, 10], 95) = 8.8 и [..], 50) = 3
	x := []float64{nan, 10, 1, 3, 2, 4}
	if p := NanPercentile(x, 95); math.Abs(p-8.8) > 1e-12 {
		t.Errorf("p95 = %v, ждали 8.8", p)
	}
	if p := NanPercentile(x, 50); p != 3 {
		t.Errorf("p50 = %v, ждали 3", p)
	}
	if !math.IsNaN(NanPercentile([]float64{nan}, 50)) {
		t.Error("ждали NaN")
	}
}

func TestRMS(t *testing.T) {
	if r := RMS([]float64{3, -4, nan, math.Inf(1)}); math.Abs(r-math.Sqrt(12.5)) > 1e-12 {
		t.Errorf("RMS = %v", r)
	}
}
