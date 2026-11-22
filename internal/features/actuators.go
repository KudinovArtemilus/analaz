package features

import (
	"fmt"
	"math"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// servos — сервоклапаны: канал задания, канал положения золотника, префикс признака.
var servos = []struct{ set, act, key string }{
	{"slide:12Y102 set value [V]", "slide:12Y102 pistion pos.[V]", "v12Y102"}, // "pistion" — так в файле
	{"slide:12Y069.21 set value [V]", "slide:12Y069.21 piston pos.[V]", "v12Y069_21"},
	{"slide:12Y069.22 set value [V]", "slide:12Y069.22 piston pos.[V]", "v12Y069_22"},
	{"BEj:20Y019 set value [V]", "BEj:20Y019 piston pos.[V]", "v20Y019"},
	{"SCu:31Y102: set value [V]", "SCu:31Y102:piston pos.[V]", "v31Y102"},
	{"CCyl Left Prop Y22.2 U", "CCyl Left Prop Y22.2 PinF", "vCCylL_Y22_2"},
	{"CCyl Right Prop Y22.1 U", "CCyl Right Prop Y22.1 PinF", "vCCylR_Y22_1"},
	{"LCyl Left Prop Y21.2 U", "LCyl Left Prop Y21.2 PinF", "vLCylL_Y21_2"},
	{"LCyl Right Prop Y21.1 U", "LCyl Right Prop Y21.1 PinF", "vLCylR_Y21_1"},
	{"MB moValve_Y1SetVal", "MB moValve_Y1ActVal", "vMB_Y1"},
	{"MB moValve_Y2SetVal", "MB moValve_Y2ActVal", "vMB_Y2"},
}

// Pumps — 12 регулируемых насосов (1.1 … 6.2): как фактический угол наклона
// и клапан управления успевают за заданием. Рост ошибки и запаздывания = регулятор «устал».
func Pumps(f *shp.File, dt float64, out Features) {
	for a := 1; a <= 6; a++ {
		for b := 1; b <= 2; b++ {
			p := fmt.Sprintf("Pump %d.%d ", a, b)
			key := fmt.Sprintf("pump%d%d", a, b)

			sv, ok1 := f.Analog(p + "svValve")
			av, ok2 := f.Analog(p + "avValve")
			if ok1 && ok2 {
				out[key+"_valve_err_rms"] = RMS(sub(sv, av))
			}

			sp, ok1 := f.Analog(p + "setPivoting")
			ap, ok2 := f.Analog(p + "actPivoting")
			if ok1 && ok2 {
				d := sub(sp, ap)
				out[key+"_pivot_err_rms"] = RMS(d)
				out[key+"_pivot_lag"] = LagSamples(sp, ap, 60) * dt * 1000 // мс

				// ошибка только «под нагрузкой» — где задание угла больше 20%
				var act []float64
				for i, s := range sp {
					if s > 20 {
						act = append(act, d[i])
					}
				}
				if len(act) > 20 {
					out[key+"_pivot_err_active"] = RMS(act)
				} else {
					out[key+"_pivot_err_active"] = math.NaN()
				}
			}

			if pr, ok := f.Analog(p + "actPressure"); ok {
				out[key+"_press_max"] = NanMax(pr)
			}
		}
	}
}

// Servos — сервоклапаны: ошибка «задание − золотник» и запаздывание.
func Servos(f *shp.File, dt float64, out Features) {
	for _, s := range servos {
		sp, ok1 := f.Analog(s.set)
		pv, ok2 := f.Analog(s.act)
		if !ok1 || !ok2 {
			continue
		}
		d := sub(sp, pv)
		out[s.key+"_err_rms"] = RMS(d)
		out[s.key+"_err_absmax"] = NanMax(absAll(d))
		out[s.key+"_lag"] = LagSamples(sp, pv, 60) * dt * 1000 // мс
	}
}

// Cylinders — центрирующие (CCyl) и нижние (LCyl) цилиндры, MB, выталкиватели.
func Cylinders(f *shp.File, out Features) {
	for _, side := range []string{"Left", "Right"} {
		for _, cyl := range []string{"CCyl", "LCyl"} {
			k := cyl + side
			if c, ok := f.Analog(cyl + " " + side + " lvPosCtrlDev"); ok {
				out[k+"_posdev_rms"] = RMS(c)
				out[k+"_posdev_absmax"] = NanMax(absAll(c))
			}
			if c, ok := f.Analog(cyl + " " + side + " Force"); ok {
				out[k+"_force_absmax"] = NanMax(absAll(c))
			}
			if c, ok := f.Analog(cyl + " " + side + " Speed"); ok {
				out[k+"_speed_absmax"] = NanMax(absAll(c))
			}
		}
	}

	mv, ok1 := f.Analog("MB ActVel")
	ms, ok2 := f.Analog("MB SetDynVel")
	if ok1 && ok2 {
		var e []float64
		for i := range mv {
			if math.Abs(mv[i]) > 5 { // только пока MB движется
				e = append(e, ms[i]-mv[i])
			}
		}
		if len(e) > 20 {
			out["MB_vel_err_rms"] = RMS(e)
		} else {
			out["MB_vel_err_rms"] = math.NaN()
		}
	}

	for _, c := range []struct{ name, key string }{
		{"bed ejector speed [mm/s]", "bej_speed_max"},
		{"slide ejector speed [mm/s]", "sej_speed_max"},
		{"BEj:netto force [kN]", "bej_force_max"},
		{"SCu:netto force [kN]", "scu_force_max"},
	} {
		if x, ok := f.Analog(c.name); ok {
			out[c.key] = NanMax(x)
		}
	}
}

// LagSamples — запаздывание факта pv от задания sp, в отсчётах:
// сдвиг L = 0…maxLag, при котором взаимная корреляция максимальна.
// NaN, если в данных есть NaN или один из сигналов почти не меняется.
func LagSamples(sp, pv []float64, maxLag int) float64 {
	n := len(sp)
	if n == 0 || len(pv) != n {
		return math.NaN()
	}
	for i := range sp {
		if !finite(sp[i]) || !finite(pv[i]) {
			return math.NaN()
		}
	}
	a, sa := centered(sp)
	b, sb := centered(pv)
	if sa < 1e-6 || sb < 1e-6 {
		return math.NaN()
	}
	best, bestL := math.Inf(-1), 0
	for L := 0; L <= maxLag && L < n; L++ {
		var c float64
		for i := 0; i+L < n; i++ {
			c += a[i] * b[i+L] // задание в момент i против факта в момент i+L
		}
		if c > best {
			best, bestL = c, L
		}
	}
	return float64(bestL)
}

// centered — x минус среднее и стандартное отклонение (как np.std, ddof=0).
func centered(x []float64) ([]float64, float64) {
	var m float64
	for _, v := range x {
		m += v
	}
	m /= float64(len(x))
	c := make([]float64, len(x))
	var ss float64
	for i, v := range x {
		c[i] = v - m
		ss += c[i] * c[i]
	}
	return c, math.Sqrt(ss / float64(len(x)))
}

func sub(a, b []float64) []float64 {
	d := make([]float64, len(a))
	for i := range a {
		d[i] = a[i] - b[i]
	}
	return d
}

func absAll(x []float64) []float64 {
	y := make([]float64, len(x))
	for i, v := range x {
		y[i] = math.Abs(v)
	}
	return y
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
