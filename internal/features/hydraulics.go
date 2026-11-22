package features

import (
	"math"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

const chHighPressure = "hyd:high pressure [bar]"

// simpleChannels — каналы, по которым считаются только _mean/_min/_max.
// Порядок как в extract_features.py; ключ — префикс имени признака.
var simpleChannels = []struct{ name, key string }{
	{"hyd:low pressure [bar]", "lp"},
	{"hyd:temp bottom tank =03+44M0-B", "temp_bottom"},
	{"hyd:temp upper tank =03+93M0-B1", "temp_upper"},
	{"hyd:level bottom tank =03+44M0-", "level_bottom"},
	{"hyd:level upper tank =03+93M0-B", "level_upper"},
	{"pressure accu ring valve1 [bar]", "accu_rv1"},
	{"pressure accu ring valve3 [bar]", "accu_rv3"},
	// зажим штампа: на этих двух каналах построен диагноз «утечка в контуре 2»
	{"DieCl: reload press. circuit 1", "dieclamp_c1"},
	{"DieCl: reload press. circuit 2", "dieclamp_c2"},
	// пресс 5: аккумулятор кольцевого клапана RV2 и зажим нижнего штампа
	{"slide: RV2 accu pressure[bar]", "accu_rv2"},
	{"28-B013.1 lower die clamp press", "dieclamp_lower"},
}

// Hydraulics — гидростанция (высокое давление: просадка и восстановление),
// низкое давление, баки, аккумуляторы, зажим штампа.
func Hydraulics(f *shp.File, dt float64, out Features) {
	if hp, ok := f.Analog(chHighPressure); ok {
		p95 := NanPercentile(hp, 95)
		out["hp_mean"] = NanMean(hp)
		out["hp_min"] = NanMin(hp)
		out["hp_drop"] = p95 - NanMin(hp) // насколько давление проседает под нагрузкой

		// восстановление: сколько времени после конца прессования давление
		// возвращается к уровню p95 − 2 бар
		if force, ok := f.Analog(chSlideForce); ok {
			last := -1
			for i, x := range force {
				if x > PressKN {
					last = i
				}
			}
			if last >= 0 {
				out["hp_recover_s"] = math.NaN() // так в Python: признак есть, но не посчитан
				for k := last; k < len(hp); k++ {
					if hp[k] >= p95-2 {
						out["hp_recover_s"] = float64(k-last) * dt
						break
					}
				}
			}
		}
	}

	for _, c := range simpleChannels {
		if x, ok := f.Analog(c.name); ok {
			out[c.key+"_mean"] = NanMean(x)
			out[c.key+"_min"] = NanMin(x)
			out[c.key+"_max"] = NanMax(x)
		}
	}
}

// Drains — приводы насосов: давление дренажа корпуса MD1–MD6 (рост = износ насоса)
// и ошибка оборотов частотника.
func Drains(f *shp.File, out Features) {
	for k := 1; k <= 6; k++ {
		name := "Oildr: MD" + string(rune('0'+k)) + " Casedrain Pressure"
		if x, ok := f.Analog(name); ok {
			key := "casedrain_md" + string(rune('0'+k))
			out[key+"_mean"] = NanMean(x)
			out[key+"_p95"] = NanPercentile(x, 95)
		}
	}
	ns, ok1 := f.Analog("Oildr:FrequenceConverter NSoll")
	ni, ok2 := f.Analog("Oildr:FrequenceConverter NIst")
	if ok1 && ok2 {
		d := make([]float64, len(ns))
		for i := range ns {
			d[i] = ns[i] - ni[i]
		}
		out["fc_speed_err_rms"] = RMS(d)
	}
}
