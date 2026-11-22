package analyze

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Журнал изменений — что менялось в работе машины, одной лентой: смена колеса, замена валков,
// изменение рецепта и заданий, шаг записи регистратора, паузы; у пресса — замены штампа и рецепт.
// Нужен, чтобы отклонение в анализе сразу можно было сопоставить с событием: «давление выросло
// 16.11 07:21» — а в журнале «задание угла насоса P2.1 50 → 59» в ту же минуту.
//
// Изменение засчитывается, только если новое значение держится JournalPersist циклов подряд:
// один выброс — не событие (выбросы — в аномалиях).

const (
	JournalGap     = 2 * time.Hour // пауза между циклами длиннее — событие «пауза»
	JournalPersist = 3             // новое значение держится столько циклов — засчитываем
	setpointWin    = 10            // задание: медиана 10 циклов до против 10 после
	setpointRel    = 0.10          // и разница больше 10% уровня
)

// Event — одно событие журнала.
type Event struct {
	At   time.Time
	Kind string // wheel, roll, recipe, setpoint, step, gap, die
	Text string
	File string // цикл, с которого началось (для ссылки «открыть цикл»); "" — нет
}

// kpsRollNames — валки КПС по ключу счётчика.
var kpsRollNames = map[string]string{
	"mr": "главный (MR)", "wrl": "диска левый (WRL)", "wrr": "диска правый (WRR)",
	"srl": "наклонный левый (SRL)", "srr": "наклонный правый (SRR)",
	"ucr": "верхний центрирующий (UCR)", "lcr": "нижний центрирующий (LCR)",
	"grl": "направляющий левый (GRL)", "grr": "направляющий правый (GRR)",
}

// kpsRecipeNames — размеры колеса из рецепта КПС (кроме DV/DW — по ним колесо). Перевод — мой,
// по немецким названиям каналов.
var kpsRecipeNames = map[string]string{
	"d1i": "внутренний диаметр обода D1i", "dai": "наружный диаметр ступицы Dai",
	"brv": "ширина обода заготовки BRV", "brw": "ширина обода готового колеса BRW",
	"mkv": "торец — круг катания MKV", "bsnv": "толщина диска у ступицы BSNV",
	"ssv": "торец — диск SSV", "bsr": "толщина диска у обода BSR", "alpha": "угол поверхности катания",
}

// Journal — события по циклам r.All, новые — первыми. Смену колеса видно только в общем
// результате операции (без Options.Regime): у результата одного колеса r.All — лишь его циклы.
func (r *Result) Journal() []Event {
	d := r.All
	if d == nil || d.Len() == 0 {
		return nil
	}
	var ev []Event
	add := func(i int, kind, text string) {
		ev = append(ev, Event{At: d.Times[i], Kind: kind, Text: text, File: d.File[i]})
	}

	// паузы
	for i := 1; i < d.Len(); i++ {
		if g := d.Times[i].Sub(d.Times[i-1]); g > JournalGap {
			add(i, "gap", fmt.Sprintf("Пауза: нет циклов %s (%s — %s)", DurText(g), d.Times[i-1].Format("02.01 15:04"), d.Times[i].Format("02.01 15:04")))
		}
	}
	// шаг записи регистратора
	if x := d.Col("dt_ms"); x != nil {
		for _, c := range persistentChanges(x, JournalPersist) {
			add(c.i, "step", fmt.Sprintf("Регистратор: шаг записи %s → %s мс", fmtNum(c.from), fmtNum(c.to)))
		}
	}
	// колесо (КПС): номер колеса по порядку появления — и та же проверка «сменилось и держится»
	var keys []string
	code := map[string]float64{}
	wheel := make([]float64, d.Len())
	for i := range wheel {
		k := RegimeKey(d, i)
		if k == "" {
			wheel[i] = math.NaN()
			continue
		}
		if _, ok := code[k]; !ok {
			code[k] = float64(len(keys))
			keys = append(keys, k)
		}
		wheel[i] = code[k]
	}
	if len(keys) > 1 {
		lbl := func(c float64) string { return strings.TrimPrefix(RegimeLabel(keys[int(c)]), "колесо ") }
		for _, c := range persistentChanges(wheel, JournalPersist) {
			add(c.i, "wheel", fmt.Sprintf("Колесо: %s → %s", lbl(c.from), lbl(c.to)))
		}
	}
	// рецепт колеса (КПС), кроме DV/DW
	for key, name := range kpsRecipeNames {
		if x := d.Col("kps_wheel_" + key); x != nil {
			for _, c := range persistentChanges(x, JournalPersist) {
				add(c.i, "recipe", fmt.Sprintf("Рецепт: %s %s → %s", name, fmtNum(c.from), fmtNum(c.to)))
			}
		}
	}
	// замены валков (КПС): поменялась отметка замены — или сбросился счётчик ходов
	for key, name := range kpsRollNames {
		strokes := d.Col("kps_roll_" + key + "_strokes")
		mark := d.Col("kps_roll_" + key + "_change")
		var at []int
		if mark != nil {
			for _, c := range persistentChanges(mark, JournalPersist) {
				at = append(at, c.i)
			}
		} else if strokes != nil {
			for i := 1; i < len(strokes); i++ {
				if strokes[i] < strokes[i-1]-1 { // счётчик пошёл заново
					at = append(at, i)
				}
			}
		}
		for _, i := range at {
			t := "Замена валка: " + name
			if strokes != nil {
				if before := lastFiniteBefore(strokes, i); finite(before) && finite(strokes[i]) {
					t += fmt.Sprintf(" — ходов до замены %s, после %s", fmtNum(before), fmtNum(strokes[i]))
				}
			}
			add(i, "roll", t)
		}
	}
	// задания (КПС): задание угла насосов — уровень до и после
	for _, c := range d.Cols {
		if !strings.HasPrefix(c, "kps_") || !(strings.HasSuffix(c, "_swivel_set_max") || strings.HasSuffix(c, "_swivel_set_min")) {
			continue
		}
		for _, s := range setpointSteps(d.Col(c)) {
			add(s.i, "setpoint", fmt.Sprintf("Задание: %s %s → %s", Human(c), fmtNum(s.from), fmtNum(s.to)))
		}
	}
	// пресс: замены штампа и рецепт штампа
	inst := r.DieInstalls()
	sort.Slice(inst, func(a, b int) bool { return inst[a].From.Before(inst[b].From) })
	for k, in := range inst {
		t := fmt.Sprintf("Замена штампа (рецепт %s), рабочих циклов %d", in.Recipe, in.Cycles)
		if k > 0 && in.Recipe != inst[k-1].Recipe {
			t += fmt.Sprintf(" — рецепт %s → %s", inst[k-1].Recipe, in.Recipe)
		}
		at := in.Change
		ev = append(ev, Event{At: at, Kind: "die", Text: t, File: r.Prod.File[in.rows[0]]})
	}

	sort.SliceStable(ev, func(a, b int) bool { return ev[a].At.After(ev[b].At) })
	return ev
}

type change struct {
	i        int
	from, to float64
}

// persistentChanges — где значение сменилось и новое держится persist циклов подряд.
// Сравнение — точное (рецепт, шаг, отметка замены — значения постоянные, а не измеренные).
func persistentChanges(x []float64, persist int) []change {
	var out []change
	level := math.NaN()
	for i := 0; i < len(x); i++ {
		v := x[i]
		if !finite(v) || v == level {
			continue
		}
		ok := true
		for k, seen := i+1, 1; seen < persist; k++ {
			if k >= len(x) {
				ok = false
				break
			}
			if !finite(x[k]) {
				continue
			}
			if x[k] != v {
				ok = false
				break
			}
			seen++
		}
		if !ok {
			continue // выброс или конец данных — не событие
		}
		if finite(level) {
			out = append(out, change{i, level, v})
		}
		level = v
	}
	return out
}

// setpointSteps — ступеньки задания: медиана setpointWin циклов до против setpointWin после
// отличается больше чем на setpointRel. Из соседних точек одной ступеньки берётся самая резкая.
func setpointSteps(x []float64) []change {
	var out []change
	n := len(x)
	best, bestD := -1, 0.0
	var bestC change
	flush := func() {
		if best >= 0 {
			out = append(out, bestC)
			best, bestD = -1, 0
		}
	}
	for i := setpointWin; i+setpointWin <= n; i++ {
		a, b := NanMedian(x[i-setpointWin:i]), NanMedian(x[i:i+setpointWin])
		if !finite(a) || !finite(b) {
			continue
		}
		d := math.Abs(b - a)
		if d > setpointRel*math.Max(math.Abs(a), math.Abs(b)) && d > 1e-9 {
			if best >= 0 && i-best > setpointWin {
				flush()
			}
			if d > bestD {
				// ступенька начинается с первого цикла, который ближе к новому уровню, чем к старому
				// (у соседних точек одной ступеньки разница медиан одинакова — точку уточняем по данным)
				near := func(v float64) bool { return finite(v) && math.Abs(v-b) < math.Abs(v-a) }
				j := i
				for j < i+setpointWin-1 && !near(x[j]) {
					j++
				}
				for j > i-setpointWin && j > 0 && near(x[j-1]) {
					j--
				}
				best, bestD, bestC = i, d, change{j, a, b}
			}
		} else if best >= 0 && i-best > setpointWin {
			flush()
		}
	}
	flush()
	return out
}

func lastFiniteBefore(x []float64, i int) float64 {
	for k := i - 1; k >= 0; k-- {
		if finite(x[k]) {
			return x[k]
		}
	}
	return math.NaN()
}

// fmtNum — число без лишних нулей: 20, 141, 59.1.
func fmtNum(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return fmt.Sprintf("%.0f", v)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

// DurText — «26 ч 10 мин», «3 сут 4 ч».
func DurText(d time.Duration) string {
	m := int(d.Round(time.Minute).Minutes())
	switch {
	case m >= 48*60:
		return fmt.Sprintf("%d сут %d ч", m/(24*60), m%(24*60)/60)
	case m >= 60:
		return fmt.Sprintf("%d ч %d мин", m/60, m%60)
	}
	return fmt.Sprintf("%d мин", m)
}
