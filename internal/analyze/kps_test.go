package analyze

import (
	"strings"
	"testing"
	"time"
)

// КПС: клапан левой оси валка диска стал хуже — «проблема» по росту и по напарнику (правая ось);
// клапан, который хуже напарника с самого начала, — «наблюдать: так и в норме, проверить один раз»;
// узлы пресса в таблице КПС не появляются.
func TestEvaluateKps(t *testing.T) {
	n := 100
	mk := func(level, late float64) []float64 {
		x := make([]float64, n)
		for i := range x {
			x[i] = level + float64(i%5)*0.002
			if i >= 80 {
				x[i] = late + float64(i%5)*0.002
			}
		}
		return x
	}
	P := testSet(everyMin(n, time.Hour),
		col{"kps_wrf_l_y11_1_err_rms", mk(0.10, 0.40)}, // левый: был как правый, стал в 4 раза хуже
		col{"kps_wrf_r_y21_1_err_rms", mk(0.10, 0.10)},
		col{"kps_srf_l_y11_1_err_rms", mk(0.20, 0.20)}, // левый наклонный: всегда вдвое хуже правого
		col{"kps_srf_r_y21_1_err_rms", mk(0.10, 0.10)},
		col{"kps_p1_1_swivel_err_active", mk(1.0, 1.0)},
		col{"kps_p1_2_swivel_err_active", mk(1.0, 1.0)},
	)
	B := firstRows(P, 20)
	sig := map[string]float64{}
	for _, c := range P.Cols {
		_, sig[c] = RobustStats(B.Col(c), P.Col(c))
	}
	diag, _ := Evaluate(P, func(f string) float64 { return NanMedian(B.Col(f)) }, sig, float64(n-1), nil)
	got := map[string]Diag{}
	for _, d := range diag {
		got[d.Name] = d
		if !strings.HasPrefix(d.Group, "КПС") {
			t.Errorf("в таблице КПС узел пресса: %s", d.Name)
		}
	}
	wl := got["Подача валка диска (WRF) левый"]
	if wl.Status != Alarm || !strings.Contains(strings.Join(wl.Facts, "|"), "хуже напарника (Подача валка диска (WRF) правый) в 3.9 раза") ||
		!strings.Contains(strings.Join(wl.Facts, "|"), "отличие появилось недавно") {
		t.Errorf("WRF левый: %+v", wl)
	}
	if wr := got["Подача валка диска (WRF) правый"]; wr.Status != OK {
		t.Errorf("WRF правый: %+v", wr)
	}
	sl := got["Подача наклонного валка (SRF) левый"]
	if sl.Status != Warn || !strings.Contains(strings.Join(sl.Facts, "|"), "и в норме было так же") {
		t.Errorf("SRF левый (так всегда): %+v", sl)
	}
	if p := got["Насос КПС P1.1"]; p.Status != OK {
		t.Errorf("P1.1: %+v", p)
	}
	if diag[0].Name != "Подача валка диска (WRF) левый" {
		t.Errorf("первым должен быть WRF левый, а не %q", diag[0].Name)
	}
}

// Узлы КПС: у каждого показателя есть описание (подпись на странице), напарник — существующий узел.
func TestKpsUnitsDescribed(t *testing.T) {
	names := map[string]bool{}
	for _, u := range KpsUnits() {
		names[u.Name] = true
	}
	for _, u := range KpsUnits() {
		for _, in := range u.Ind {
			if _, ok := Descs[in.Feat]; !ok {
				t.Errorf("%s: нет описания %s", u.Name, in.Feat)
			}
		}
		if u.PairFeat != "" {
			if _, ok := Descs[u.PairOther]; !ok {
				t.Errorf("%s: напарник %s без описания", u.Name, u.PairOther)
			}
		}
	}
	if len(KpsUnits()) != 13+5+3+2 { // 13 осей, 5 приводов (MRD, SRD Л/П по клапанам; WRD Л/П по моменту), 3 насоса, аккумулятор, ПЛК
		t.Errorf("узлов КПС %d", len(KpsUnits()))
	}
	if Subsystem("kps_step13_s") != "КПС: шаги программы" || !strings.Contains(Human("kps_step13_s"), "шага программы 13") {
		t.Errorf("шаги: %q %q", Subsystem("kps_step13_s"), Human("kps_step13_s"))
	}
}

// Усилие оси — показатель «внутри колеса»: на одном колесе рост усилия — «проблема»,
// при смене колеса (смешанные данные) он не оценивается, хоть усилие и выросло.
func TestPerRegimeIndicators(t *testing.T) {
	n := 100
	force, dv, dw := make([]float64, n), make([]float64, n), make([]float64, n)
	for i := range force {
		force[i], dv[i], dw[i] = 800+float64(i%5), 860, 895
		if i >= 80 {
			force[i] = 1200 + float64(i%5)
		}
	}
	eval := func(P *Dataset) Diag {
		B := firstRows(P, 20)
		sig := map[string]float64{}
		for _, c := range P.Cols {
			_, sig[c] = RobustStats(B.Col(c), P.Col(c))
		}
		diag, _ := Evaluate(P, func(f string) float64 { return NanMedian(B.Col(f)) }, sig, float64(n-1), nil)
		for _, d := range diag {
			if d.Name == "Подача главного валка (MRF)" {
				return d
			}
		}
		return Diag{}
	}
	one := testSet(everyMin(n, time.Hour), col{"kps_mrf_force_max", force}, col{"kps_wheel_dv", dv}, col{"kps_wheel_dw", dw})
	if d := eval(one); d.Status != Alarm {
		t.Errorf("одно колесо, усилие +50%%: %+v", d)
	}
	dv2 := append([]float64{}, dv...)
	for i := 80; i < n; i++ {
		dv2[i] = 1164 // в конце — другое колесо: рост усилия от колеса
	}
	mixed := testSet(everyMin(n, time.Hour), col{"kps_mrf_force_max", force}, col{"kps_wheel_dv", dv2}, col{"kps_wheel_dw", dw})
	if d := eval(mixed); d.Status != OK || len(d.Facts) != 0 {
		t.Errorf("смешанные колёса — усилие не оценивается: %+v", d)
	}
	if r := Regimes(mixed); len(r) != 2 || r[0].Key != "dv860-dw895" || r[0].Cycles != 80 || r[1].Label != "колесо DV 1164 / DW 895" {
		t.Errorf("режимы: %+v", r)
	}
	if b, rg := SplitOp("op3@DV860-DW895"); b != "OP3" || rg != "dv860-dw895" || JoinOp(b, rg) != "OP3@dv860-dw895" {
		t.Errorf("SplitOp: %s %s", b, rg)
	}
}
