package analyze

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/features"
)

// Ожидаемые значения — scipy.stats.spearmanr(np.arange(n), y) и rankdata(y).
func TestSpearmanMatchesScipy(t *testing.T) {
	for _, c := range []struct {
		y     []float64
		rho   float64
		ranks []float64
	}{
		{[]float64{1, 2, 3, 4, 5}, 1, []float64{1, 2, 3, 4, 5}},
		{[]float64{5, 4, 3, 2, 1}, -1, []float64{5, 4, 3, 2, 1}},
		{[]float64{1, 3, 2, 2, 5, 4}, 0.753702346348183, []float64{1, 4, 2.5, 2.5, 6, 5}},
		{[]float64{0, 0, 1, 0, 0, 7}, 0.50709255283711, []float64{2.5, 2.5, 5, 2.5, 2.5, 6}},
		{[]float64{2, 2, 2, 2, 2}, math.NaN(), []float64{3, 3, 3, 3, 3}},
	} {
		if r := AverageRanks(c.y); !slices.Equal(r, c.ranks) {
			t.Errorf("ранги %v = %v, ждали %v", c.y, r, c.ranks)
		}
		got := Spearman(c.y)
		if !(math.Abs(got-c.rho) < 1e-12 || math.IsNaN(got) && math.IsNaN(c.rho)) {
			t.Errorf("Spearman(%v) = %v, ждали %v", c.y, got, c.rho)
		}
	}
}

// Сутки, если данных ≥ 5 суток, иначе часы; пустые интервалы и интервалы, где все признаки NaN, выбрасываются.
func TestResample(t *testing.T) {
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 30, 0, 0, time.UTC) }
	nan := math.NaN()
	d := &Dataset{
		Cols: []string{"x"}, index: map[string]int{"x": 0},
		Times: []time.Time{day(1, 1), day(1, 23), day(3, 5), day(4, 0), day(6, 2)},
		Data:  [][]float64{{1, 4, 7, nan, 9}},
	}
	d.File = make([]string, d.Len())
	b := Resample(d, []string{"x"})
	if b.Step != 24*time.Hour {
		t.Fatalf("шаг %v, ждали сутки", b.Step)
	}
	wantStart := []time.Time{day(1, 0).Truncate(24 * time.Hour), day(3, 0).Truncate(24 * time.Hour), day(6, 0).Truncate(24 * time.Hour)}
	if !slices.EqualFunc(b.Starts, wantStart, time.Time.Equal) || !slices.Equal(b.Med[0], []float64{2.5, 7, 9}) {
		t.Errorf("интервалы %v, медианы %v", b.Starts, b.Med[0])
	}

	d.Times = []time.Time{day(1, 1), day(1, 1), day(1, 3), day(1, 3), day(1, 4)}
	if b := Resample(d, []string{"x"}); b.Step != time.Hour || len(b.Starts) != 3 {
		t.Errorf("короткий период: шаг %v, интервалов %d (ждали час и 3)", b.Step, len(b.Starts))
	}
}

func TestNanMedianAndPercentileLikeNumpy(t *testing.T) {
	// np.median и np.percentile: значения выбраны так, что «наивная» формула дала бы другой последний знак
	// (a+b)/2 = 20.69691213136711, а b−(b−a)·0,5 = 20.696912131367114 — pandas даёт первое
	if m := NanMedian([]float64{34.44228640964949, 1, math.NaN(), 6.951537853084733, 50}); m != 20.69691213136711 {
		t.Errorf("NanMedian = %v, ждали 20.69691213136711", m)
	}
	x := []float64{0.1, 0.7, 0.2, 1e16}
	if p := features.NanPercentile(x, 90); p != 7000000000000002.0 {
		t.Errorf("percentile 90 = %v, numpy даёт 7000000000000002", p)
	}
	if p := features.NanPercentile(x, 30); p != 0.19 {
		t.Errorf("percentile 30 = %v, numpy даёт 0.19", p)
	}
}

func TestSignificantTrendsOnePerSignal(t *testing.T) {
	all := []Trend{
		{"a017_HP__max", 0.9, 10, 9},
		{"hp_min", -0.8, -8, 6.4},
		{"hp_mean", -0.7, -6, 4.2},        // тот же сигнал hp — отбрасывается
		{"pump11_pivot_lag", 0.5, 20, 10}, // ρ мал — не значим
		{"a017_HP__min", 0.9, 5, 4.5},     // тот же сигнал a017_HP
	}
	got := SignificantTrends(all, 30)
	var names []string
	for _, x := range got {
		names = append(names, x.Feature)
	}
	if want := []string{"hp_min", "a017_HP__max"}; !slices.Equal(names, want) {
		t.Errorf("получили %v, ждали %v (инженерные первыми, по одному на сигнал)", names, want)
	}
}
