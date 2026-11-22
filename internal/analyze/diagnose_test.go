package analyze

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

// Ожидаемые значения — из diagnose.py (_robust_slope, die_change_mask, _fmt) и pandas corr(method="spearman").
func TestDiagnoseHelpersMatchPython(t *testing.T) {
	tt, y := make([]float64, 200), make([]float64, 200)
	for i := range tt {
		tt[i] = float64(i) * 0.5
		y[i] = math.Sin(tt[i])*3 + tt[i]*0.2
	}
	y[17] = math.NaN()
	// 199 точек > 150 — проверяется и равномерная выборка np.linspace(...).astype(int)
	if s := RobustSlope(tt, y); math.Abs(s-0.19891758628977926) > 1e-15 {
		t.Errorf("RobustSlope (выборка) = %v", s)
	}
	if s := RobustSlope([]float64{0, 1, 2, 3, 4, 5}, []float64{1, 3, 2, 5, 4, 9}); s != 4.0/3 {
		t.Errorf("RobustSlope = %v, ждали 1.3333333333333333", s)
	}
	if got := DieChanges([]float64{1000, 1050, 5, 60, 30, 200, 90}); !slices.Equal(got, []int{2, 6}) {
		t.Errorf("DieChanges = %v, ждали [2 6]", got)
	}
	for v, want := range map[float64]string{
		math.NaN(): "—", 1234.5: "1234 бар", 12.345: "12.3 бар", 0.12345: "0.12 бар",
		0.012345: "0.012 бар", -150.5: "-150 бар", 0: "0.000 бар",
	} {
		if got := fmtv(v, "бар"); got != want {
			t.Errorf("fmtv(%v) = %q, ждали %q", v, got, want)
		}
	}
	nan := math.NaN()
	if r := SpearmanXY([]float64{1, 2, nan, 4, 5, 6}, []float64{2, 1, 3, nan, 6, 5}); math.Abs(r-0.6) > 1e-12 {
		t.Errorf("SpearmanXY = %v, ждали 0.6", r)
	}
}

// Датчик дренажа MD5 показывает 0 при живых соседях — узел «наблюдать» с подозрением на датчик,
// а насос с вдвое большим дренажем, чем у остальных, — «проблема».
func TestEvaluateCasedrainPeers(t *testing.T) {
	n := 40
	var cols []col
	for k, level := range []float64{0.5, 0.5, 0.5, 0.5, 0, 1.4} { // MD1…MD6: MD5 мёртв, MD6 ~в 2,7 раза выше
		x := make([]float64, n)
		for i := range x {
			x[i] = level
			if level > 0 {
				x[i] += float64(i%4) * 0.01
			}
		}
		cols = append(cols, col{"casedrain_md" + string(rune('1'+k)) + "_mean", x})
	}
	P := testSet(everyMin(n, time.Hour), cols...)
	B := firstRows(P, 10)
	diag, _ := Evaluate(P, func(f string) float64 { return NanMedian(B.Col(f)) }, map[string]float64{}, float64(n-1), nil)
	got := map[string]Diag{}
	for _, d := range diag {
		got[d.Name] = d
	}
	if d := got["Насос / привод MD5"]; d.Status != Warn || !strings.HasPrefix(d.Cause, "Подозрение на ДАТЧИК") {
		t.Errorf("MD5: %+v", d)
	}
	if d := got["Насос / привод MD6"]; d.Status != Alarm || len(d.Facts) == 0 || !strings.HasPrefix(d.Facts[0], "хуже соседних в 2.7 раза") {
		t.Errorf("MD6: %+v", d)
	}
	if d := got["Насос / привод MD1"]; d.Status != OK {
		t.Errorf("MD1: %+v", d)
	}
	if diag[0].Status != Alarm { // сначала проблемы
		t.Errorf("порядок: первым %q", diag[0].Name)
	}
}
