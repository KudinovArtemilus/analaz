package analyze

import (
	"math"
	"slices"
	"strings"
)

// Пол σ «по шагу записи».
//
// Регистратор пишет точку раз в 5 или 20 мс, поэтому время (запаздывание, длительность шага)
// бывает только кратным этому шагу: 0, 5, 10 мс… Если в норме привод отвечал за 0 мс, разброс
// нормы почти нулевой, и цикл «на одну точку позже» давал z в десятки — ложная тревога.
// Разница меньше шага записи — это точность измерения, а не неисправность, поэтому σ признака
// не может быть меньше его шага:
//   - признак времени (…_lag, …_ms — миллисекунды, …_s — секунды): шаг записи из колонки dt_ms;
//   - любой другой признак, если его значения лежат на сетке (не меньше 3 уровней,
//     все разности кратны наименьшей): шаг сетки — например, датчик, отдающий целые бары.
// У непрерывного признака сетки нет — пол не действует, анализ как раньше.

// StepFloorMinLevels — сколько разных значений нужно, чтобы поверить в сетку:
// у двух уровней (0 и 100) «шаг» — это просто их разность, а не точность датчика.
const StepFloorMinLevels = 3

// StepFloors — пол σ для каждого признака из feats по циклам d (рабочие циклы анализа).
// Признаки без пола в карту не попадают.
func StepFloors(d *Dataset, feats []string) map[string]float64 {
	out := map[string]float64{}
	dtMs := NanMedian(d.Col("dt_ms"))
	for _, f := range feats {
		x := d.Col(f)
		if x == nil {
			continue
		}
		fl := gridStep(x)
		if finite(dtMs) && dtMs > 0 {
			switch timeUnit(f) {
			case "ms":
				fl = math.Max(fl, dtMs)
			case "s":
				fl = math.Max(fl, dtMs/1000)
			}
		}
		if fl > 0 {
			out[f] = fl
		}
	}
	return out
}

// ApplyStepFloors поднимает σ норм до пола (in place: r.Norms — копия хранимой нормы,
// сама хранимая норма и её файл не меняются).
func ApplyStepFloors(ns []Norm, floors map[string]float64) {
	for j := range ns {
		if fl, ok := floors[ns[j].Feature]; ok && ns[j].Sigma < fl {
			ns[j].Sigma = fl
		}
	}
}

// floorSigmaMap — то же для карты σ (диагноз по узлам). Возвращает новую карту.
func floorSigmaMap(sig map[string]float64, floors map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(sig))
	for k, v := range sig {
		if fl, ok := floors[k]; ok && v < fl {
			v = fl
		}
		out[k] = v
	}
	return out
}

// timeUnit — единица признака времени по имени: "ms", "s" или "".
func timeUnit(f string) string {
	switch {
	case strings.HasSuffix(f, "_ms"), strings.HasSuffix(f, "_lag"):
		return "ms"
	case strings.HasSuffix(f, "_s"):
		return "s"
	}
	return ""
}

// gridStep — шаг сетки значений x или 0, если сетки нет.
func gridStep(x []float64) float64 {
	var u []float64
	scale := 0.0
	for _, v := range x {
		if finite(v) {
			u = append(u, v)
			scale = math.Max(scale, math.Abs(v))
		}
	}
	if len(u) < StepFloorMinLevels {
		return 0
	}
	slices.Sort(u)
	tol := 1e-9 * math.Max(1, scale) // разности меньше этого — шум вычислений, одно и то же значение
	lv := u[:1]
	for _, v := range u[1:] {
		if v-lv[len(lv)-1] > tol {
			lv = append(lv, v)
		}
	}
	if len(lv) < StepFloorMinLevels {
		return 0
	}
	step := math.Inf(1)
	for i := 1; i < len(lv); i++ {
		step = math.Min(step, lv[i]-lv[i-1])
	}
	if step <= 1e-6*math.Max(1, scale) { // слишком мелкий «шаг» — признак непрерывный
		return 0
	}
	for i := 1; i < len(lv); i++ {
		k := (lv[i] - lv[i-1]) / step
		if math.Abs(k-math.Round(k)) > 0.01 {
			return 0
		}
	}
	return step
}
