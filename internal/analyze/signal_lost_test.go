package analyze

import (
	"testing"
	"time"
)

func TestFindSignalLost(t *testing.T) {
	n := 60
	live := func(base float64) []float64 {
		x := make([]float64, n)
		for i := range x {
			x[i] = base + float64(i%5)*0.01
		}
		return x
	}
	dead := make([]float64, n)
	dead[20] = 175489            // повреждённый .shp: в канал давления попал номер заготовки
	setMax := make([]float64, n) // уставка −10…0 В: max = 0 законно, но std живой
	off := make([]float64, n)    // насос 2 выключен у всех — режим, не отказ
	d := testSet(everyMin(n, time.Minute),
		col{"casedrain_md1_mean", live(0.03)}, col{"casedrain_md2_mean", live(0.5)},
		col{"casedrain_md3_mean", live(0.5)}, col{"casedrain_md5_mean", dead},
		col{"a022_slide_12Y008_1_set_V__max", setMax}, col{"a022_slide_12Y008_1_set_V__std", live(2)},
		col{"a023_slide_12Y009_1_set_V__max", live(3)}, col{"a024_slide_12Y010_1_set_V__max", live(3)},
		col{"pump21_press_max", off}, col{"pump22_press_max", off}, col{"pump31_press_max", off},
	)
	got := FindSignalLost(d)
	if len(got) != 1 || got[0].Feature != "casedrain_md5_mean" {
		t.Fatalf("ждали только casedrain_md5_mean, получили %+v", got)
	}
	// одиночный «живой» цикл 20 серию нулей не прерывает — ноль с начала данных
	if !got[0].FromStart || got[0].ZeroShare != 1 || len(got[0].Peers) != 3 {
		t.Errorf("%+v", got[0])
	}
}

func TestZeroSinceRealRecovery(t *testing.T) {
	x := []float64{1, 2, 3, 0, 0, 0} // работал три цикла подряд, потом пропал
	d := testSet(everyMin(len(x), time.Minute), col{"m", x})
	since, fromStart := zeroSince(d, "m")
	if fromStart || !since.Equal(d.Times[3]) {
		t.Errorf("ноль с %v (с начала: %v), ждали цикл 3", since, fromStart)
	}
}
