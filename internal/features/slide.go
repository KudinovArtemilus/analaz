package features

import (
	"math"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// Имена каналов ползуна.
const (
	chSlidePos   = "slide:average val.encoder [mm]"
	chSlideForce = "slide:total force [kN]"
	chSlideDev   = "slide:deviation left-right [mm]"
)

// Slide — признаки ползуна: усилие, работа, ход, скорости, дрейф наверху, перекос.
// Перенос блока «слайд (ползун)» из extract_features.py строка в строку —
// порядок и условия сохранены, чтобы результаты совпали.
func Slide(f *shp.File, dt float64, out Features) {
	pos, okPos := f.Analog(chSlidePos)
	force, okF := f.Analog(chSlideForce)

	if okPos && okF && len(pos) >= 2 {
		v := Gradient(pos, dt) // мм/с, отрицательная = вниз

		// press = F > PRESS_KN; сравнение с NaN в Go, как и в numpy, даёт false
		press := make([]bool, len(force))
		nPress := 0
		for i, x := range force {
			press[i] = x > PressKN
			if press[i] {
				nPress++
			}
		}

		out["force_max_kN"] = NanMax(force)
		out["press_time_s"] = float64(nPress) * dt

		var impulse float64 // np.nansum(F[press]) * dt
		for i, p := range press {
			if p && !math.IsNaN(force[i]) {
				impulse += force[i]
			}
		}
		out["force_impulse_kNs"] = impulse * dt

		// работа: сумма F * (путь вниз) там, где идёт прессование и ползун движется вниз
		var work float64
		for i := 1; i < len(pos); i++ {
			dpos := -(pos[i] - pos[i-1])
			if press[i] && dpos > 0 {
				if w := force[i] * dpos; !math.IsNaN(w) {
					work += w
				}
			}
		}
		out["work_kJ"] = work / 1000 // кН·мм / 1000 = кДж

		top, bottom := NanMax(pos), NanMin(pos)
		out["pos_top_mm"] = top
		out["pos_bottom_mm"] = bottom
		out["stroke_mm"] = top - bottom
		out["v_down_max"] = -NanMin(v)
		out["v_up_max"] = NanMax(v)

		moving := 0
		for _, x := range v {
			if math.Abs(x) > 5 {
				moving++
			}
		}
		out["slide_moving_s"] = float64(moving) * dt

		if nPress > 0 {
			out["press_start_s"] = float64(FirstTrue(press)) * dt
			if i := NanArgMax(force); i >= 0 {
				out["pos_at_force_max_mm"] = pos[i]
			}
			// время нарастания усилия: от 10% до 90% максимума
			fm := out["force_max_kN"]
			i10 := FirstTrue(above(force, 0.1*fm))
			i90 := FirstTrue(above(force, 0.9*fm))
			out["force_rise_s"] = float64(i90-i10) * dt
		}

		// дрейф ползуна в покое наверху (утечки через уплотнения)
		var tx, ty []float64
		for i := range pos {
			if pos[i] > top-5 && math.Abs(v[i]) < 2 && !press[i] {
				tx = append(tx, float64(i)*dt)
				ty = append(ty, pos[i])
			}
		}
		if float64(len(tx)) > 3.0/dt { // не меньше 3 с в покое
			out["slide_drift_top_mm_s"] = Slope(tx, ty)
		}
	}

	if dev, ok := f.Analog(chSlideDev); ok {
		abs := make([]float64, len(dev))
		for i, x := range dev {
			abs[i] = math.Abs(x) // |NaN| = NaN, NanMax его пропустит
		}
		out["lr_dev_absmax_mm"] = NanMax(abs)

		if okF {
			var sel []float64
			for i, x := range force {
				if x > PressKN {
					sel = append(sel, dev[i])
				}
			}
			if len(sel) > 0 {
				out["lr_dev_press_mean_mm"] = NanMean(sel)
			}
		}
	}
}

// above — маска x > thr.
func above(x []float64, thr float64) []bool {
	m := make([]bool, len(x))
	for i, v := range x {
		m[i] = v > thr
	}
	return m
}
