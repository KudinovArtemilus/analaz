package analyze

import (
	"math"
	"testing"
	"time"
)

func TestGridStep(t *testing.T) {
	nan := math.NaN()
	for _, c := range []struct {
		name string
		x    []float64
		want float64
	}{
		{"запаздывание с шагом 5 мс", []float64{0, 0, 5, 10, 0, 20, nan}, 5},
		{"секунды с шумом вычислений", []float64{0.8, 0.805, 0.8200000000000001, 0.78}, 0.005},
		{"целые бары", []float64{-10, -11, -9, -10}, 1},
		{"два уровня — не сетка", []float64{5, 20, 5, 20}, 0},
		{"непрерывный признак", []float64{1.234567, 2.345678, 0.987654, 3.14159}, 0},
		{"постоянный", []float64{75, 75, 75}, 0},
	} {
		if got := gridStep(c.x); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: шаг %v, ждали %v", c.name, got, c.want)
		}
	}
}

// TestStepFloors — запаздывание (0 мс в норме, иногда на точку позже) не даёт тревогу,
// а настоящий рост (на десятки шагов) — даёт.
func TestStepFloors(t *testing.T) {
	n := 100
	times := make([]time.Time, n)
	lag, dt, slow, dur := make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n)
	for i := range n {
		times[i] = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute)
		dt[i] = 20
		if i%10 == 0 {
			lag[i] = 20 // на одну точку позже
		}
		dur[i] = 1.5
	}
	d := testSet(times, col{"dt_ms", dt}, col{"x_lag_ms", lag}, col{"y_lag", slow}, col{"step_s", dur})
	fl := StepFloors(d, []string{"x_lag_ms", "y_lag", "step_s", "нет_такого"})
	if fl["x_lag_ms"] != 20 || fl["y_lag"] != 20 || math.Abs(fl["step_s"]-0.02) > 1e-12 {
		t.Fatalf("полы: %v", fl)
	}
	if _, ok := fl["нет_такого"]; ok {
		t.Fatal("пол у отсутствующего признака")
	}
	ns := []Norm{{Feature: "x_lag_ms", Median: 0, Sigma: 1}, {Feature: "other", Sigma: 0.5}, {Feature: "y_lag", Sigma: 50}}
	ApplyStepFloors(ns, fl)
	if ns[0].Sigma != 20 || ns[1].Sigma != 0.5 || ns[2].Sigma != 50 {
		t.Fatalf("σ после пола: %+v", ns)
	}
	// точка позже — z = 1 (не отклонение); рост на 145 мс при норме 0 — z ≈ 7
	if z := (20 - ns[0].Median) / ns[0].Sigma; z > 1 {
		t.Errorf("одна точка: z = %v", z)
	}
	if z := (145 - ns[0].Median) / ns[0].Sigma; z < 7 {
		t.Errorf("настоящий рост: z = %v", z)
	}
	m := floorSigmaMap(map[string]float64{"x_lag_ms": 0.1, "other": 2}, fl)
	if m["x_lag_ms"] != 20 || m["other"] != 2 {
		t.Errorf("карта σ: %v", m)
	}
}
