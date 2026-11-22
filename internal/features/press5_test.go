package features

import (
	"math"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// TestPress5 — файл пресса 5: каналы под своими именами дают признаки пресса 10 (усилие, ход,
// гидростанция, клапаны), а каналов, которых у пресса 5 нет (насосы 4–6…), в признаках нет.
func TestPress5(t *testing.T) {
	names := []string{"slide: RV2 accu pressure[bar]", "slide total force [kN]", "slide:average val.encoder [mm]",
		"hyd:03-B135.1 hp [bar]", "slide:12Y102 SetVal_adjust pres", "slide:12Y102 PinF[V]",
		"Pump 1.1 setPivoting", "Pump 1.1 actPivoting", "Pump 4.1 setPivoting", "Pump 4.1 actPivoting",
		"slide:encoder front right [mm]"}
	fn := func(ch int, x float64) float64 {
		switch ch {
		case 0:
			return 220
		case 1: // усилие: импульс до 40 МН в середине цикла
			return 40000 * math.Exp(-(x-5)*(x-5))
		case 2: // ползун: сверху вниз и обратно
			return 2100 - 900*math.Exp(-(x-5)*(x-5)/4)
		case 3:
			return 282 - 3*math.Exp(-(x-5)*(x-5))
		case 4, 6:
			return 50 + 40*math.Sin(x)
		case 5, 7:
			return 50 + 40*math.Sin(x-0.05)
		}
		return 0 // насос 4.1 и энкодер — у пресса 5 их нет: ровный ноль
	}
	f, err := shp.Parse(shp.SyntheticSignals([]string{"Y1", "Y2", "Y3", "Y4", "Y5", "Y6", "Y7", "Y8"}, names, fn, 10, 20))
	if err != nil {
		t.Fatal(err)
	}
	if !f.IsPress5() {
		t.Fatal("файл не узнан как пресс 5")
	}
	out := Compute(f)
	for _, k := range []string{"force_max_kN", "work_kJ", "stroke_mm", "pos_bottom_mm", "hp_min", "accu_rv2_min",
		"v12Y102_err_rms", "pump11_pivot_err_rms"} {
		if v, ok := out[k]; !ok || math.IsNaN(v) {
			t.Errorf("нет признака %s", k)
		}
	}
	if v := out["force_max_kN"]; math.Abs(v-40000) > 100 {
		t.Errorf("усилие %v, ждали ~40000", v)
	}
	if out["feat_ver"] != Press5Version {
		t.Errorf("версия %v", out["feat_ver"])
	}
	for k := range out {
		if strings.HasPrefix(k, "pump41") || strings.Contains(k, "Pump_4_1") || strings.Contains(k, "encoder_front_right") {
			t.Errorf("признак по каналу, которого у пресса 5 нет: %s", k)
		}
	}
	// источники для «открыть цикл»: имя пресса 10 находит канал пресса 5
	if j, ok := f.AnalogIndex(chSlideForce); !ok || f.AnaNames[j] != "slide total force [kN]" {
		t.Errorf("канал усилия под именем пресса 10: %d %v", j, ok)
	}
}
