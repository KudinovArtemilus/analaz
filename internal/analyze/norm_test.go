package analyze

import (
	"math"
	"slices"
	"testing"
)

// Ожидаемые значения посчитаны analyze.robust_stats (pandas) на тех же данных.
func TestRobustStatsMatchesPandas(t *testing.T) {
	nan := math.NaN()
	base := map[string][]float64{
		"x": {10, 11, 12, 10.5, nan, 30, 11.5, 10.2, 10.8, 11.1}, // выброс 30 и пропуск
		"k": {5, 5, 5, 5, 5, 5, 5, 5, 5, 5},                      // постоянный в базе — нужен «пол» σ
		"n": {3, 4, 3, 5, 4, 3, 4, 4, 3, 4},                      // целочисленный — пол не меньше 1
	}
	extra := map[string][]float64{"x": {12, 13, 9}, "k": {5, 6, 5}, "n": {3, 9, 4}}
	want := map[string][2]float64{"x": {11, 1.1100009999999987}, "k": {5, 1}, "n": {4, 1}}

	for name, b := range base {
		all := append(slices.Clone(b), extra[name]...)
		med, sig := RobustStats(b, all)
		w := want[name]
		if med != w[0] || math.Abs(sig-w[1]) > 1e-12 {
			t.Errorf("%s: медиана %v σ %v, ждали %v %v", name, med, sig, w[0], w[1])
		}
	}
}

func TestUseful(t *testing.T) {
	nan := math.NaN()
	cases := []struct {
		x    []float64
		want bool
	}{
		{[]float64{1, 2, 3}, true},
		{[]float64{5, 5, 5}, false},        // одно значение — не анализируем
		{[]float64{1, 2, nan, nan}, false}, // заполнено ровно половина — мало (нужно > 0,5)
		{[]float64{1, 2, nan}, true},       // 2 из 3
		{[]float64{nan, nan}, false},
	}
	for _, c := range cases {
		if got := useful(c.x); got != c.want {
			t.Errorf("useful(%v) = %v", c.x, got)
		}
	}
}
