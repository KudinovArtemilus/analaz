package analyze

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/KudinovArtemilus/press-go/internal/features"
)

// Паспортные пределы — абсолютные границы показателя по документации машины или от механиков:
// «давление зажима не ниже 110 бар». Они не зависят от нормы: если норма снята, когда давление
// уже было занижено, сравнение с нормой занижения не увидит, а паспортный предел — увидит.
// Задаются в «Настройках» для каждой машины; по умолчанию их нет.

// Limit — паспортный предел показателя; NaN — границы нет.
type Limit struct {
	Min, Max float64
	Note     string // откуда цифра: «паспорт пресса, раздел 5.3», «механик Иванов»
}

// LimitShare — по скольким циклам сверяем с пределом: 5% худших — это уже не один случайный
// цикл, а заметная часть работы. Сверяется 5-й (для минимума) или 95-й процентиль.
const LimitShare = 5.0

// limitCheck — нарушен ли предел: значение (5-й или 95-й процентиль) и текст. ok=false — не нарушен.
func limitCheck(x []float64, l Limit, unit string) (text string, broken bool) {
	if x == nil {
		return "", false
	}
	if !math.IsNaN(l.Min) {
		if lo := percentile(x, LimitShare); finite(lo) && lo < l.Min {
			return fmt.Sprintf("у %.0f%% циклов ниже %s — меньше паспортного минимума %s%s",
				LimitShare, fmtv(lo, unit), exact(l.Min, unit), limitNote(l)), true
		}
	}
	if !math.IsNaN(l.Max) {
		if hi := percentile(x, 100-LimitShare); finite(hi) && hi > l.Max {
			return fmt.Sprintf("у %.0f%% циклов выше %s — больше паспортного максимума %s%s",
				LimitShare, fmtv(hi, unit), exact(l.Max, unit), limitNote(l)), true
		}
	}
	return "", false
}

// exact — предел как его ввели (110.5, а не округлённое «110»).
func exact(v float64, unit string) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if unit != "" {
		s += " " + unit
	}
	return s
}

func limitNote(l Limit) string {
	if s := strings.TrimSpace(l.Note); s != "" {
		return " (" + s + ")"
	}
	return ""
}

// IndicatorInfo — показатель узла для таблицы паспортных пределов в «Настройках».
type IndicatorInfo struct {
	Feat, Unit, Label string // Label — «Зажим штампа: давление зажима, контур 1, минимум»
	Group             string
	Builtin           string // встроенный порог, если есть: «проблема ниже 100 бар»
	Kps               bool   // показатель КПС
}

// Indicators — все показатели узлов (без повторов) в порядке узлов.
func Indicators() []IndicatorInfo {
	kps := map[string]bool{}
	for _, u := range KpsUnits() {
		for _, in := range u.Ind {
			kps[in.Feat] = true
		}
	}
	seen := map[string]bool{}
	var out []IndicatorInfo
	for _, u := range Units() {
		for _, in := range u.Ind {
			if seen[in.Feat] {
				continue
			}
			seen[in.Feat] = true
			b := ""
			if !math.IsNaN(in.AbsAlarm) {
				dir := "выше"
				if in.Bad == "down" {
					dir = "ниже"
				}
				b = fmt.Sprintf("встроенный: «проблема» %s %s", dir, fmtv(in.AbsAlarm, in.Unit))
			}
			out = append(out, IndicatorInfo{in.Feat, in.Unit, u.Name + ": " + in.Label, u.Group, b, kps[in.Feat]})
		}
	}
	return out
}

// percentile — процентиль без NaN.
func percentile(x []float64, p float64) float64 { return features.NanPercentile(x, p) }
