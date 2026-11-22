package features

import (
	"math"
	"testing"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// kpsCycle — синтетический цикл КПС: оси, приводы, насосы, рецепт колеса, шаги программы.
// Золотник отстаёт от команды на 60 мс, насос — на 100 мс; шаги: 10 с шаг 13, 20 с шаг 30, 10 с шаг 61.
func kpsCycle(stepMs int) *shp.File {
	names := []string{
		"Vorformdurchmesser DV", "Fertigformdurchmesser DW", "MRF_Force_F", "MRFPr_Head_p",
		"MRF_Prop_Y1.1_U", "MRF_Prop_Y1.1_FU", "PumpSwivelP2_1_SetVal", "PumpSwivelP2_1_ActVal",
		"HydAccuPressure_p", "ActualStep", "MR_StokesSinceChange", "MR_Date", "MR_Time",
	}
	cmd := func(t float64) float64 { return 3 * math.Sin(2*math.Pi*t/4) } // команда клапана, период 4 с
	sw := func(t float64) float64 { return 40 + 30*math.Sin(2*math.Pi*t/8) }
	fn := func(ch int, t float64) float64 {
		switch names[ch] {
		case "Vorformdurchmesser DV":
			return 860
		case "Fertigformdurchmesser DW":
			return 895
		case "MRF_Force_F":
			return 800 * math.Exp(-(t-20)*(t-20)/50)
		case "MRFPr_Head_p":
			return 100 + t
		case "MRF_Prop_Y1.1_U":
			return cmd(t)
		case "MRF_Prop_Y1.1_FU":
			return cmd(t - 0.060)
		case "PumpSwivelP2_1_SetVal":
			return sw(t)
		case "PumpSwivelP2_1_ActVal":
			return sw(t - 0.100)
		case "HydAccuPressure_p":
			return 280 - 5*math.Sin(2*math.Pi*t/40)
		case "ActualStep":
			switch {
			case t < 10:
				return 13
			case t < 30:
				return 30
			default:
				return 61
			}
		case "MR_StokesSinceChange":
			return 548
		case "MR_Date":
			return 27105
		case "MR_Time":
			return 134609
		}
		return 0
	}
	f, err := shp.Parse(shp.SyntheticSignals([]string{"Y1", "Y2", "Y3", "Y4", "Y5", "Y6", "Y7", "Y8"}, names, fn, 40, stepMs))
	if err != nil {
		panic(err)
	}
	return f
}

// Тот же цикл, записанный с шагом 20 мс и 5 мс, даёт те же признаки: времена — в секундах,
// а не в отсчётах; у КПС шаг записи менялся, и признаки «в отсчётах» были бы несравнимы.
func TestKpsStepIndependent(t *testing.T) {
	a, b := Compute(kpsCycle(20)), Compute(kpsCycle(5))
	if a["dt_ms"] != 20 || b["dt_ms"] != 5 {
		t.Fatalf("шаг: %v %v", a["dt_ms"], b["dt_ms"])
	}
	near := func(name string, tol float64) {
		t.Helper()
		x, y := a[name], b[name]
		if math.IsNaN(x) || math.IsNaN(y) || math.Abs(x-y) > tol {
			t.Errorf("%s: шаг 20 мс %v, шаг 5 мс %v (допуск %v)", name, x, y, tol)
		}
	}
	near("kps_wheel_dv", 0)
	near("kps_wheel_dw", 0)
	near("kps_mrf_force_max", 0.5)
	near("kps_mrf_y1_1_err_rms", 0.01)
	near("kps_mrf_y1_1_lag_ms", 20)    // разрешение — шаг записи: 60 мс при 20 мс — 60, при 5 мс — 60
	near("kps_p2_1_swivel_lag_ms", 20) // 100 мс
	near("kps_p2_1_swivel_err_active", 0.05)
	near("kps_accu_drop", 0.05)
	near("kps_step13_s", 0.02) // 10 с
	near("kps_step30_s", 0.02) // 20 с
	near("kps_step61_s", 0.02)
	near("kps_roll_mr_strokes", 0)
	near("kps_roll_mr_change", 0)
	// запаздывания — те, что заданы в сигналах
	if v := b["kps_mrf_y1_1_lag_ms"]; v != 60 {
		t.Errorf("запаздывание клапана при 5 мс: %v, должно 60", v)
	}
	if v := b["kps_p2_1_swivel_lag_ms"]; v != 100 {
		t.Errorf("запаздывание насоса при 5 мс: %v, должно 100", v)
	}
	if v := a["kps_step30_s"]; math.Abs(v-20) > 0.02 {
		t.Errorf("шаг программы 30: %v с, должно 20", v)
	}
}

// У файла пресса признаков КПС нет — его таблица не меняется.
func TestKpsNotForPress(t *testing.T) {
	f, err := shp.Parse(shp.Synthetic([]string{"Y1", "Y2", "Y3", "Y4", "Y5", "Y6", "Y7", "Y8"}, []string{"slide:total force [kN]", "MRF_Force_F"}, 50, 0))
	if err != nil {
		t.Fatal(err)
	}
	for k := range Compute(f) {
		if len(k) > 4 && k[:4] == "kps_" {
			t.Fatalf("у пресса признак КПС: %s", k)
		}
	}
}

// У каждого признака КПС есть каналы-источники («открыть цикл» показывает нужные сигналы),
// и каналы — реальные имена из списков КПС. Файл — со всеми каналами из списков.
func TestKpsSourcesCoverAll(t *testing.T) {
	var names []string
	seen := map[string]bool{}
	addName := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, a := range KpsAxes {
		for _, n := range []string{a.force, a.head, a.ring, a.v} {
			addName(n)
		}
		for _, v := range a.valves {
			addName(v.u)
			addName(v.fu)
		}
	}
	for _, d := range KpsDrives {
		for _, n := range []string{d.rev, d.in, d.out, d.torque} {
			addName(n)
		}
		for _, v := range d.valves {
			addName(v.u)
			addName(v.fu)
		}
	}
	for _, p := range KpsPumps {
		for _, n := range []string{p.set, p.act, p.press, p.vu, p.vfu} {
			addName(n)
		}
	}
	for _, w := range KpsWheel {
		addName(w.ch)
	}
	for _, r := range KpsRolls {
		addName(r.prefix + "_StokesSinceChange")
		addName(r.date)
		addName(r.prefix + "_Time")
	}
	for _, n := range []string{"HydAccuPressure_p", "Diameter_Laser", "Diameter_CR", "DiffDiameter_Laser_CR", "S7 Actual Cycle Time (ms)", "ActualStep"} {
		addName(n)
	}
	fn := func(ch int, t float64) float64 { return float64(ch) + math.Sin(t*float64(ch+1)) }
	f, err := shp.Parse(shp.SyntheticSignals([]string{"Y1", "Y2", "Y3", "Y4", "Y5", "Y6", "Y7", "Y8"}, names, fn, 10, 20))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for k := range Compute(f) {
		if len(k) < 4 || k[:4] != "kps_" {
			continue
		}
		n++
		chs := Sources(k)
		if len(chs) == 0 {
			t.Errorf("у %s нет каналов", k)
		}
		for _, c := range chs {
			if !seen[c] {
				t.Errorf("%s: канал %q не из списков КПС", k, c)
			}
		}
	}
	if n < 180 { // все, кроме фаз программы: ActualStep в синтетике не целый
		t.Fatalf("признаков КПС всего %d", n)
	}
}
