package features

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// Sources — из каких каналов посчитан признак: имена каналов в файле.
// Нужно, чтобы ссылка «открыть цикл» со страницы «Анализ» сразу показала на графике
// именно те сигналы, из-за которых цикл попал в аномальные.
//
// Таблица держится рядом с кодом признаков и опирается на те же списки (servos,
// simpleChannels, ch*-константы). Тест TestSourcesCoverAll считает признаки настоящего
// файла и проверяет, что у КАЖДОГО признака из каналов источник найден, — так новый
// признак без записи здесь не пройдёт незамеченным.
//
// Для сырых признаков a017_<канал>__max источник — канал с номером 017; его имя
// берётся из файла (см. ChannelIndexes), поэтому здесь они не разбираются.
func Sources(feature string) []string {
	switch feature {
	case "force_max_kN", "press_time_s", "force_impulse_kNs", "force_rise_s":
		return []string{chSlideForce}
	case "work_kJ", "pos_at_force_max_mm", "press_start_s":
		return []string{chSlideForce, chSlidePos}
	case "pos_top_mm", "pos_bottom_mm", "stroke_mm", "v_down_max", "v_up_max",
		"slide_moving_s", "slide_drift_top_mm_s":
		return []string{chSlidePos}
	case "lr_dev_absmax_mm", "lr_dev_press_mean_mm":
		return []string{chSlideDev}
	case "hp_mean", "hp_min", "hp_drop":
		return []string{chHighPressure}
	case "hp_recover_s":
		return []string{chHighPressure, chSlideForce}
	case "fc_speed_err_rms":
		return []string{"Oildr:FrequenceConverter NSoll", "Oildr:FrequenceConverter NIst"}
	case "MB_vel_err_rms":
		return []string{"MB SetDynVel", "MB ActVel"}
	case "bej_speed_max":
		return []string{"bed ejector speed [mm/s]"}
	case "sej_speed_max":
		return []string{"slide ejector speed [mm/s]"}
	case "bej_force_max":
		return []string{"BEj:netto force [kN]"}
	case "scu_force_max":
		return []string{"SCu:netto force [kN]"}
	case "piece_id":
		return []string{"PartTracking_PieceId_Consecutiv"}
	case "heat_id":
		return []string{"PartTracking_PieceId_HeatID"}
	case "die_strokes":
		return []string{"Die.StokesSinceChange"}
	case "which_op_max":
		return []string{"Which OP is Pressing"}
	}

	if chs := KpsSources(feature); chs != nil { // КПС (OP3)
		return chs
	}

	// префикс + окончание: lp_mean, dieclamp_c2_min, ...
	for _, c := range simpleChannels {
		if cutAny(feature, c.key+"_", "mean", "min", "max") {
			return []string{c.name}
		}
	}
	for _, s := range servos {
		if cutAny(feature, s.key+"_", "err_rms", "err_absmax", "lag") {
			return []string{s.set, s.act}
		}
	}
	if m := rePump.FindStringSubmatch(feature); m != nil {
		p := "Pump " + m[1] + "." + m[2] + " "
		switch m[3] {
		case "valve_err_rms":
			return []string{p + "svValve", p + "avValve"}
		case "pivot_err_rms", "pivot_lag", "pivot_err_active":
			return []string{p + "setPivoting", p + "actPivoting"}
		case "press_max":
			return []string{p + "actPressure"}
		}
	}
	if m := reDrain.FindStringSubmatch(feature); m != nil {
		return []string{"Oildr: MD" + m[1] + " Casedrain Pressure"}
	}
	if m := reCyl.FindStringSubmatch(feature); m != nil {
		ch := map[string]string{"posdev_rms": "lvPosCtrlDev", "posdev_absmax": "lvPosCtrlDev",
			"force_absmax": "Force", "speed_absmax": "Speed"}[m[3]]
		return []string{m[1] + " " + m[2] + " " + ch}
	}
	return nil
}

var (
	rePump  = regexp.MustCompile(`^pump([1-6])([12])_(valve_err_rms|pivot_err_rms|pivot_lag|pivot_err_active|press_max)$`)
	reDrain = regexp.MustCompile(`^casedrain_md([1-6])_(mean|p95)$`)
	reCyl   = regexp.MustCompile(`^(CCyl|LCyl)(Left|Right)_(posdev_rms|posdev_absmax|force_absmax|speed_absmax)$`)
	reRaw   = regexp.MustCompile(`^a(\d{3})_(.+)__[a-z]+$`)
)

// cutAny — feature = prefix + одно из окончаний.
func cutAny(feature, prefix string, ends ...string) bool {
	rest, ok := strings.CutPrefix(feature, prefix)
	if !ok {
		return false
	}
	for _, e := range ends {
		if rest == e {
			return true
		}
	}
	return false
}

// ChannelIndexes — номера аналоговых каналов файла для списка признаков, без повторов,
// в порядке признаков. Неизвестные признаки и отсутствующие в файле каналы пропускаются.
func ChannelIndexes(f *shp.File, feats []string) []int {
	var out []int
	seen := map[int]bool{}
	add := func(j int) {
		if !seen[j] {
			seen[j] = true
			out = append(out, j)
		}
	}
	for _, ft := range feats {
		if m := reRaw.FindStringSubmatch(ft); m != nil {
			// a017_<имя>: номер — позиция канала в файле. Сверяем имя, чтобы ссылка
			// на файл с другим набором каналов не подсветила чужой сигнал.
			j, _ := strconv.Atoi(m[1])
			if j < len(f.AnaNames) && safe(f.AnaNames[j]) == m[2] {
				add(j)
			}
			continue
		}
		for _, name := range Sources(ft) {
			if j, ok := f.AnalogIndex(name); ok {
				add(j)
			}
		}
	}
	return out
}

// Mark — где в цикле признак «случился»: отсчёт, каналы, значение и что это за точка.
type Mark struct {
	At    int     // номер отсчёта
	Chans []int   // каналы, на графиках которых ставить отметку
	Value float64 // значение в этой точке (для разницы — |задание − факт|)
	Kind  string  // "max", "min", "absdiff" (наибольшая разница двух каналов), "abs" (наибольшее по модулю)
}

// Locate — точка цикла, где признак принимает своё значение.
//
// Это возможно не для всех признаков. Максимум, минимум, «макс. по модулю» — это одна
// конкретная точка, её и показываем. RMS (среднеквадратичная ошибка) — характеристика
// всего цикла, одной точки у неё нет; для неё показываем пик ошибки — место, которое
// больше всего «тянет» RMS вверх. Средние, запаздывания, длительности, ход — без отметки.
func Locate(f *shp.File, feature string) (Mark, bool) {
	if m := reRaw.FindStringSubmatch(feature); m != nil {
		j, _ := strconv.Atoi(m[1])
		if j >= len(f.AnaNames) || safe(f.AnaNames[j]) != m[2] {
			return Mark{}, false
		}
		x := shp.Clean(f.Ana[j])
		switch {
		case strings.HasSuffix(feature, "__max"):
			return extreme(x, nil, []int{j}, "max")
		case strings.HasSuffix(feature, "__min"):
			return extreme(x, nil, []int{j}, "min")
		}
		return Mark{}, false
	}

	src := Sources(feature)
	if len(src) == 0 {
		return Mark{}, false
	}
	idx := make([]int, len(src))
	for i, name := range src {
		j, ok := f.AnalogIndex(name)
		if !ok {
			return Mark{}, false
		}
		idx[i] = j
	}
	a := shp.Clean(f.Ana[idx[0]])

	switch {
	case feature == "hp_min" || feature == "hp_drop" || feature == "pos_bottom_mm" || strings.HasSuffix(feature, "_min"):
		return extreme(a, nil, idx[:1], "min")
	case feature == "pos_top_mm" || feature == "force_max_kN" ||
		strings.HasSuffix(feature, "_max") && !strings.HasPrefix(feature, "v_"):
		return extreme(a, nil, idx[:1], "max")
	case strings.Contains(feature, "absmax") || strings.HasSuffix(feature, "rms") || strings.HasSuffix(feature, "err_active"):
		var mask []bool
		if strings.HasSuffix(feature, "err_active") { // ошибка «под нагрузкой»: только где задание > 20%
			mask = make([]bool, len(a))
			for i, s := range a {
				mask[i] = s > 20
			}
		}
		if len(idx) == 2 {
			b := shp.Clean(f.Ana[idx[1]])
			d := make([]float64, len(a))
			for i := range a {
				d[i] = math.Abs(a[i] - b[i])
			}
			return extreme(d, mask, idx, "absdiff")
		}
		d := make([]float64, len(a))
		for i := range a {
			d[i] = math.Abs(a[i])
		}
		return extreme(d, mask, idx, "abs")
	}
	return Mark{}, false
}

// extreme — номер наибольшего (kind "min" — наименьшего) значения без NaN, с учётом маски.
func extreme(x []float64, mask []bool, chans []int, kind string) (Mark, bool) {
	best, at := math.NaN(), -1
	for i, v := range x {
		if math.IsNaN(v) || mask != nil && !mask[i] {
			continue
		}
		if at < 0 || (kind == "min" && v < best) || (kind != "min" && v > best) {
			best, at = v, i
		}
	}
	if at < 0 {
		return Mark{}, false
	}
	return Mark{At: at, Chans: chans, Value: best, Kind: kind}, true
}

// Pair — пара «задание / факт» для канала: общий ключ пары и роль канала в ней.
// Каналы одной пары рисуются на одном графике: так ошибка регулятора видна сразу,
// как зазор между линиями, а не сравнением двух отдельных графиков.
//
// Пары берутся из тех же списков, по которым считаются признаки ошибок
// (servos, насосы, MB, частотник), — новый регулятор добавляется в одном месте.
func Pair(name string) (key, role string, ok bool) {
	pairsOnce.Do(buildPairs)
	p, ok := pairs[name]
	return p.key, p.role, ok
}

var (
	pairsOnce sync.Once
	pairs     map[string]struct{ key, role string }
)

func buildPairs() {
	pairs = map[string]struct{ key, role string }{}
	add := func(key, set, act string) {
		pairs[set] = struct{ key, role string }{key, "set"}
		pairs[act] = struct{ key, role string }{key, "act"}
	}
	for _, s := range servos {
		add(s.key, s.set, s.act)
	}
	for a := 1; a <= 6; a++ {
		for b := 1; b <= 2; b++ {
			p := fmt.Sprintf("Pump %d.%d ", a, b)
			add(p+"valve", p+"svValve", p+"avValve")
			add(p+"pivot", p+"setPivoting", p+"actPivoting")
		}
	}
	add("MB_vel", "MB SetDynVel", "MB ActVel")
	add("fc_speed", "Oildr:FrequenceConverter NSoll", "Oildr:FrequenceConverter NIst")
}
