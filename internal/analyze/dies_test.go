package analyze

import (
	"math"
	"testing"
	"time"
)

func TestDecodeChange(t *testing.T) {
	for _, c := range []struct {
		day, tod float64
		ok       bool
		want     string
	}{
		{261105, 82817, true, "2026-11-05 08:28:17"},
		{261105, 13119, true, "2026-11-05 01:31:19"}, // 01:31:19 без ведущего нуля
		{261112, 261112, false, ""},                  // «26 часов» — мусор повреждённого файла; time.Date перенёс бы на завтра
		{260230, 120000, false, ""},                  // 30 февраля
		{261305, 120000, false, ""},                  // 13-й месяц
		{math.NaN(), 120000, false, ""},
		{261105.5, 120000, false, ""},
	} {
		ct, ok := decodeChange(c.day, c.tod)
		if ok != c.ok || ok && ct.Format("2006-01-02 15:04:05") != c.want {
			t.Errorf("decodeChange(%v, %v) = %v %v", c.day, c.tod, ct, ok)
		}
	}
}

func TestDieInstalls(t *testing.T) {
	at := func(h int) time.Time {
		return time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC).Add(time.Duration(h) * time.Hour)
	}
	var times []time.Time
	var day, tod, ds, f []float64
	// установка A (замена 09.11 23:00): 40 циклов, в конце значение растёт; установка B (замена 11.11 15:00): 5 циклов;
	// один цикл с мусором (26 часов) и один, где время замены ПОЗЖЕ цикла, — отброшены
	for i := 0; i < 40; i++ {
		v := 100.0 + float64(i%3)
		if i >= 32 {
			v = 150
		}
		times, day, tod, ds, f = append(times, at(i)), append(day, 261109), append(tod, 230000), append(ds, float64(i+1)), append(f, v)
	}
	for i := 0; i < 5; i++ {
		times, day, tod, ds, f = append(times, at(40+i)), append(day, 261111), append(tod, 150000), append(ds, float64(i+1)), append(f, 100)
	}
	times, day, tod, ds, f = append(times, at(50)), append(day, 261112), append(tod, 261112), append(ds, 261112), append(f, 100)
	times, day, tod, ds, f = append(times, at(51)), append(day, 261130), append(tod, 120000), append(ds, 3), append(f, 100)
	p := testSet(times,
		col{"a124_DieTimeOfChange_Day__min", day}, col{"a124_DieTimeOfChange_Day__max", day},
		col{"a125_DieTimeOfChange_Time__min", tod}, col{"a125_DieTimeOfChange_Time__max", tod},
		col{"die_strokes", ds}, col{"hp_min", f})
	r := &Result{Prod: p, Cols: Columns{Eng: []string{"hp_min"}}, Norms: []Norm{{Median: 100, Sigma: 1}}}
	in := r.DieInstalls()
	if len(in) != 2 || in[0].Cycles != 40 || in[1].Cycles != 5 || in[0].Strokes != 40 || !in[1].Change.Equal(time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("установки: %+v", in)
	}
	// у A: база — первые 10, «сейчас» — последние 10 (8 из них по 150) → сильный дрейф вверх
	if !in[0].HasDrift || len(in[0].Drift) != 1 || in[0].Drift[0].Z < 3 || in[0].Drift[0].Now != 150 {
		t.Errorf("дрейф A: %+v", in[0].Drift)
	}
	if in[1].HasDrift { // 5 циклов — дрейф не считаем
		t.Errorf("у короткой установки дрейф считаться не должен")
	}
	if h := r.DieHealth(in); h == nil || len(h.Keys) != 2 || h.Cycles[0] != 40 {
		t.Errorf("карта по установкам: %+v", h)
	}
}

func TestModeOfAndRecipe(t *testing.T) {
	nan := math.NaN()
	col := []float64{60, 60, 50, nan, 50, 70}
	if m := modeOf(col, []int{0, 1, 2, 3, 4, 5}); m != 50 { // 60 и 50 по два раза — меньшее, как pandas
		t.Errorf("мода при равенстве: %v", m)
	}
	if m := modeOf(col, []int{3}); !math.IsNaN(m) {
		t.Errorf("одни NaN — NaN: %v", m)
	}
	a, b := Recipe{60, 650, 0}, Recipe{45, 655, 49}
	in := []DieInstall{{Recipe: b, Cycles: 30}, {Recipe: a, Cycles: 20}, {Recipe: a, Cycles: 20}}
	if m := MainRecipe(in); m != a || m.String() != "60 / 650 / 0" {
		t.Errorf("основной рецепт — по числу циклов: %v", m)
	}
	if s := (Recipe{nan, 650, 0}).String(); s != "— / 650 / 0" {
		t.Errorf("NaN в рецепте: %q", s)
	}
}
