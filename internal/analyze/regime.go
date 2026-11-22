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
// В имени операции режим пишется через @: «OP3@dv860-dw895». Без @ — все колёса вместе, как раньше.
// У пресса режимов нет (нет kps_wheel_*) — у него ничего не меняется.

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

// RegimeKey — режим цикла i ("" — режима нет: не КПС или размеры не записаны).
func RegimeKey(d *Dataset, i int) string {
	dv, dw := d.Col("kps_wheel_dv"), d.Col("kps_wheel_dw")
	if dv == nil || dw == nil || math.IsNaN(dv[i]) || math.IsNaN(dw[i]) {
		return ""
	}
	return fmt.Sprintf("dv%d-dw%d", int(math.Round(dv[i])), int(math.Round(dw[i])))
}

// RegimeLabel — «колесо DV 860 / DW 895» по ключу.
func RegimeLabel(key string) string {
	var dv, dw int
	if _, err := fmt.Sscanf(key, "dv%d-dw%d", &dv, &dw); err != nil {
		return key
	}
	return fmt.Sprintf("колесо DV %d / DW %d", dv, dw)
}

// Regimes — режимы в данных в порядке первого появления (по времени).
func Regimes(d *Dataset) []Regime {
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
