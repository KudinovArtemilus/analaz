package features

import (
	"math"
	"testing"
)

func TestLagSamples(t *testing.T) {
	n := 2000
	sp, pv := make([]float64, n), make([]float64, n)
	for i := range sp {
		sp[i] = math.Sin(2 * math.Pi * float64(i) * 0.005)
		pv[i] = math.Sin(2 * math.Pi * float64(i-15) * 0.005) // факт отстаёт на 15 отсчётов
	}
	if l := LagSamples(sp, pv, 60); l != 15 {
		t.Errorf("запаздывание %v, ждали 15", l)
	}
	pv[700] = math.NaN()
	if l := LagSamples(sp, pv, 60); !math.IsNaN(l) {
		t.Errorf("с NaN ждали NaN, а не %v (в старом Python было бы 0)", l)
	}
	flat := make([]float64, n)
	if l := LagSamples(sp, flat, 60); !math.IsNaN(l) {
		t.Errorf("постоянный сигнал: ждали NaN, получили %v", l)
	}
}

func TestSafe(t *testing.T) {
	cases := map[string]string{
		"slide:total force [kN]":          "slide_total_force_kN",
		"hyd:level bottom tank =03+44M0-": "hyd_level_bottom_tank_03_44M0",
		"ch é [bar]":                      "ch_bar", // é не латиница и не кириллица → «_»
		"Давление (бар)":                  "Давление_бар",
	}
	for in, want := range cases {
		if got := safe(in); got != want {
			t.Errorf("safe(%q) = %q, ждали %q", in, got, want)
		}
	}
}
