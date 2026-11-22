package analyze

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Режим — «что катаем»: у КПС это тип колеса по заданным размерам DV/DW (рецепт, в цикле постоянен).
// Усилия, давления, обороты, длительности шагов у разных колёс разные, и общая норма смешивает
// их: смена колеса выглядит как поломка. Поэтому анализ можно считать внутри одного колеса —
// со своей нормой (её сервер сохраняет сам, когда у колеса набралось AutoNormCycles циклов).
//
// У пресса режим — рецепт: скорость опускания ползуна и позиция нижних цилиндров (RecipeChannels).
// У OP1 их три (60/650, 45/655, 50/645), и усилие у них разное: 26,6 / 21,8 / 17,7 МН. Позиция SCu
// в ключ не входит: у OP2 она меняется внутри одной установки штампа (0, 41…59) — это не рецепт.
//
// В имени операции режим пишется через @: «OP3@dv860-dw895», «OP1@rcp60-650». Без @ — все вместе,
// как раньше: там анализ пресса не изменился ни в чём.

// AutoNormCycles — после стольких рабочих циклов колеса сервер сохраняет его норму (первые столько).
// Переменная — только чтобы тесты не создавали по 200 циклов.
var AutoNormCycles = 200

// Regime — один режим (колесо) в данных.
type Regime struct {
	Key    string // dv860-dw895
	Label  string // «колесо DV 860 / DW 895»
	From   time.Time
	To     time.Time
	Cycles int
}

// RegimeMinCycles — режим с меньшим числом циклов не показывается отдельно: это единичный
// сбойный цикл (повреждённый .shp, наладка), а не рецепт.
const RegimeMinCycles = 3

// RegimeKey — режим цикла i ("" — режима нет: размеры колеса или рецепт не записаны).
func RegimeKey(d *Dataset, i int) string {
	if dv, dw := d.Col("kps_wheel_dv"), d.Col("kps_wheel_dw"); dv != nil && dw != nil {
		if math.IsNaN(dv[i]) || math.IsNaN(dw[i]) {
			return ""
		}
		return fmt.Sprintf("dv%d-dw%d", int(math.Round(dv[i])), int(math.Round(dw[i])))
	}
	sp, lc := d.Col(RecipeChannels[0]), d.Col(RecipeChannels[1])
	if sp == nil || lc == nil {
		return ""
	}
	v, c := math.Round(sp[i]), math.Round(lc[i])
	if !(v >= 1 && v <= 500 && c >= 100 && c <= 3000) { // NaN и мусор повреждённых записей (233254 / −1576)
		return ""
	}
	return fmt.Sprintf("rcp%d-%d", int(v), int(c))
}

// IsRecipe — режим пресса (рецепт), а не колесо КПС.
func IsRecipe(key string) bool { return strings.HasPrefix(key, "rcp") }

// RegimeLabel — «колесо DV 860 / DW 895», «рецепт 60 / 650» по ключу.
func RegimeLabel(key string) string {
	var a, b int
	if _, err := fmt.Sscanf(key, "dv%d-dw%d", &a, &b); err == nil {
		return fmt.Sprintf("колесо DV %d / DW %d", a, b)
	}
	if _, err := fmt.Sscanf(key, "rcp%d-%d", &a, &b); err == nil {
		return fmt.Sprintf("рецепт %d / %d", a, b)
	}
	return key
}

// Regimes — режимы в данных в порядке первого появления (по времени); режимы меньше
// RegimeMinCycles циклов не показываются.
func Regimes(d *Dataset) []Regime {
	all := regimesAll(d)
	out := all[:0]
	for _, r := range all {
		if r.Cycles >= RegimeMinCycles {
			out = append(out, r)
		}
	}
	return out
}

func regimesAll(d *Dataset) []Regime {
	var out []Regime
	at := map[string]int{}
	for i := range d.Len() {
		k := RegimeKey(d, i)
		if k == "" {
			continue
		}
		j, ok := at[k]
		if !ok {
			j = len(out)
			at[k] = j
			out = append(out, Regime{Key: k, Label: RegimeLabel(k), From: d.Times[i]})
		}
		out[j].To = d.Times[i]
		out[j].Cycles++
	}
	return out
}

// OnlyRegime — циклы одного режима.
func (d *Dataset) OnlyRegime(key string) *Dataset {
	return d.Where(func(i int) bool { return RegimeKey(d, i) == key })
}

// mixedRegimes — в данных больше одного режима (колёса смешаны).
func mixedRegimes(d *Dataset) bool {
	first := ""
	for i := range d.Len() {
		if k := RegimeKey(d, i); k != "" {
			if first == "" {
				first = k
			} else if k != first {
				return true
			}
		}
	}
	return false
}

// SplitOp — «OP3@dv860-dw895» → «OP3», «dv860-dw895». Регистр: операция — заглавными, режим — строчными.
func SplitOp(op string) (base, regime string) {
	base, regime, _ = strings.Cut(op, "@")
	return strings.ToUpper(base), strings.ToLower(regime)
}

// JoinOp — обратное SplitOp.
func JoinOp(base, regime string) string {
	if regime == "" {
		return strings.ToUpper(base)
	}
	return strings.ToUpper(base) + "@" + strings.ToLower(regime)
}
