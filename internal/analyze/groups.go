package analyze

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Group — физическая группа однотипных признаков (GROUPS в charts.py): шесть дренажей
// насосов, двенадцать насосов, одиннадцать сервоклапанов… На одном графике сразу видно,
// какой узел отбился от остальных: у однотипных узлов линии должны идти рядом.
type Group struct {
	Title, Note string
	Feats       []string
}

// Groups — перенос GROUPS из charts.py (порядок и тексты те же), потом группы КПС.
// Группы без данных GroupData пропускает: у пресса не видно групп КПС, и наоборот.
var Groups = append(pressGroups, kpsGroups()...)

var pressGroups = []Group{
	{"Дренаж корпусов насосов MD1–MD6", "Главный индикатор износа аксиально-поршневых насосов: рост = внутренние утечки.",
		feats("casedrain_md%d_mean", 1, 6)},
	{"Насосы: ошибка угла наклона под нагрузкой", "Насколько фактический угол люльки не успевает за заданием. Рост = износ регулятора.",
		pumps("pump%d%d_pivot_err_active")},
	{"Насосы: запаздывание угла (мс)", "Рост = регулятор стал медленнее.", pumps("pump%d%d_pivot_lag")},
	{"Насосы: ошибка клапана управления", "Рост = загрязнение/износ клапана регулятора.", pumps("pump%d%d_valve_err_rms")},
	{"Насосы: макс. давление за цикл, бар", "", pumps("pump%d%d_press_max")},
	{"Сервоклапаны: ошибка задание-золотник (RMS)", "Рост = загрязнение масла, износ золотника, заедание.", servos("_err_rms")},
	{"Сервоклапаны: запаздывание (мс)", "Рост = медленный отклик.", servos("_lag")},
	{"Масло: температура, °C", "Рост при той же нагрузке = охлаждение или внутренние утечки (греют масло).",
		[]string{"temp_bottom_mean", "temp_upper_mean"}},
	{"Масло: уровни баков", "Медленное падение = внешняя утечка.", []string{"level_bottom_mean", "level_upper_mean"}},
	{"Высокое давление: минимум и просадка, бар", "Просадка растёт = насосы/аккумуляторы не держат.",
		[]string{"hp_min", "hp_drop", "hp_mean"}},
	{"Аккумуляторы и зажим штампа, бар", "Падение минимумов = азот/заряд/утечка контура.",
		[]string{"accu_rv1_min", "accu_rv3_min", "dieclamp_c1_min", "dieclamp_c2_min", "accu_rv2_min", "dieclamp_lower_min"}}, // два последних — пресс 5
	{"Ползун: перекос лево-право, мм", "Рост = износ направляющих, разбаланс.", []string{"lr_dev_absmax_mm", "lr_dev_press_mean_mm"}},
	{"Ползун: времена, с", "Рост при той же работе = гидравлика теряет производительность.",
		[]string{"press_time_s", "force_rise_s", "press_start_s", "slide_moving_s"}},
	{"Ползун: скорости, мм/с", "", []string{"v_down_max", "v_up_max"}},
	{"Ползун: дрейф в покое наверху, мм/с", "Ползун «сползает» при закрытых клапанах = утечка через уплотнения/клапаны.",
		[]string{"slide_drift_top_mm_s"}},
	{"Центрирующие/нижние цилиндры: ошибка позиции (RMS)", "Рост = износ/утечки/клапан.",
		[]string{"CCylLeft_posdev_rms", "CCylRight_posdev_rms", "LCylLeft_posdev_rms", "LCylRight_posdev_rms"}},
	{"Выталкиватели и MB", "", []string{"bej_speed_max", "sej_speed_max", "MB_vel_err_rms"}},
	{"Частотник привода насосов: ошибка оборотов", "Рост = двигатель/насос тяжелее крутится.", []string{"fc_speed_err_rms"}},
}

func feats(format string, from, to int) []string {
	var out []string
	for k := from; k <= to; k++ {
		out = append(out, fmt.Sprintf(format, k))
	}
	return out
}

func pumps(format string) []string {
	var out []string
	for a := 1; a <= 6; a++ {
		for b := 1; b <= 2; b++ {
			out = append(out, fmt.Sprintf(format, a, b))
		}
	}
	return out
}

// servos — признаки 11 сервоклапанов в порядке charts.py.
func servos(suffix string) []string {
	var out []string
	for _, k := range []string{"v12Y102", "v12Y069_21", "v12Y069_22", "v20Y019", "v31Y102",
		"vCCylL_Y22_2", "vCCylR_Y22_1", "vLCylL_Y21_2", "vLCylR_Y21_1", "vMB_Y1", "vMB_Y2", "v12Y069_2"} { // последний — пресс 5
		out = append(out, k+suffix)
	}
	return out
}

// GroupSeries — данные одной группы для графика: по каждому признаку медиана за интервал
// в реальных единицах (Val) и она же в σ от нормы (Z). Интервалы — r.Bins.Starts.
type GroupSeries struct {
	Group
	Val [][]float64 // [признак][интервал]
	Z   [][]float64
}

// GroupData — группы, у которых есть хоть один признак с данными
// (в Python: c in G.columns and G[c].notna().any()). Признаки без данных из группы убираются.
func (r *Result) GroupData() []GroupSeries {
	var out []GroupSeries
	for _, g := range Groups {
		gs := GroupSeries{Group: Group{Title: g.Title, Note: g.Note}}
		for _, f := range g.Feats {
			j := slices.Index(r.Cols.Eng, f) // Bins.Med и Norms: сначала eng, потом raw
			if j < 0 || !anyFinite(r.Bins.Med[j]) {
				continue
			}
			val := r.Bins.Med[j]
			z := make([]float64, len(val))
			for b, v := range val {
				z[b] = (v - r.Norms[j].Median) / r.Norms[j].Sigma
			}
			gs.Feats = append(gs.Feats, f)
			gs.Val = append(gs.Val, val)
			gs.Z = append(gs.Z, z)
		}
		if len(gs.Feats) > 0 {
			out = append(out, gs)
		}
	}
	return out
}

func anyFinite(x []float64) bool {
	for _, v := range x {
		if !math.IsNaN(v) {
			return true
		}
	}
	return false
}

// Short — короткая подпись для легенды (_short в charts.py): длинное описание
// обрезается до части перед двоеточием («Насос 6.2: ошибка…» → «Насос 6.2»), — в группе
// все линии про одно и то же, различаются только узлом.
func Short(human string) string {
	if len([]rune(human)) > 40 {
		if k := strings.Index(human, ":"); k >= 0 {
			return human[:k]
		}
	}
	return human
}
