package web

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// digits — любые группы цифр: "Oildr: MD5 Casedrain" и "Oildr: MD1 Casedrain" дают один ключ.
var digits = regexp.MustCompile(`\d+`)

// peerKey — ключ однотипности: имя канала, в котором все числа заменены на «#».
func peerKey(name string) string { return digits.ReplaceAllString(name, "#") }

// markPeers дописывает к «нулевым» каналам список однотипных, у которых сигнал есть.
// names и sig — по индексам каналов файла.
func markPeers(names []string, sig []Signal) {
	alive := map[string][]string{}
	for j, n := range names {
		if sig[j].Kind == SignalOK {
			k := peerKey(n)
			alive[k] = append(alive[k], shortName(n))
		}
	}
	for j, n := range names {
		if sig[j].Kind == SignalNoSignal {
			sig[j].Peers = alive[peerKey(n)]
		}
	}
}

// shortName — отличительная часть имени для подписи: "Oildr: MD1 Casedrain Pressure" → "MD1".
func shortName(n string) string {
	for _, w := range strings.FieldsFunc(n, func(r rune) bool { return r == ' ' || r == ':' }) {
		if digits.MatchString(w) {
			return w
		}
	}
	return n
}

func peersText(p []string) string {
	if len(p) > 4 {
		return strings.Join(p[:4], ", ") + "…"
	}
	return strings.Join(p, ", ")
}

// SignalKind — состояние канала в одном цикле.
// Неизменный канал — не «неинтересный»: мёртвый датчик или пропавший сигнал
// часто и есть неисправность (так было с дренажом MD5: с начала ноября ровно 0).
type SignalKind int

const (
	SignalOK       SignalKind = iota // меняется, мусора нет
	SignalConst                      // всё время одно ненулевое значение: уставка или «застывший» датчик
	SignalNoSignal                   // всё время ровно 0 — значение подставлено, а не измерено
	SignalFaulty                     // часть значений — мусор (NaN, Inf, |x| ≥ 1e12)
	SignalEmpty                      // нет ни одного нормального значения
)

// Signal — оценка канала за цикл.
type Signal struct {
	Kind   SignalKind
	Value  float64 // значение для Const/NoSignal
	BadPct float64 // доля мусора, % — для Faulty
	// Peers — однотипные каналы, у которых сигнал есть (MD1, MD2… для MD5).
	// Ноль весь цикл подозрителен только на их фоне: иначе это, скорее всего,
	// неиспользуемый канал (сервисный датчик, неустановленный энкодер).
	Peers []string
}

// Problem — стоит ли подсветить канал как возможную неисправность.
// Ненулевая константа сама по себе не проблема (так выглядят уставки) — её только помечаем.
func (s Signal) Problem() bool {
	return (s.Kind == SignalNoSignal && len(s.Peers) > 0) || s.Kind == SignalFaulty || s.Kind == SignalEmpty
}

// Label — короткая подпись для списка каналов.
func (s Signal) Label() string {
	switch s.Kind {
	case SignalNoSignal:
		if len(s.Peers) > 0 {
			return "нет сигнала: 0 весь цикл, а у однотипных есть (" + peersText(s.Peers) + ")"
		}
		return "0 весь цикл"
	case SignalConst:
		return "не меняется: " + short(s.Value)
	case SignalFaulty:
		return fmt.Sprintf("сбои: %.1f%% значений", s.BadPct)
	case SignalEmpty:
		return "нет данных: только сбойные значения"
	}
	return ""
}

// Class — CSS-класс строки канала.
func (s Signal) Class() string {
	switch {
	case s.Problem():
		return "sig-problem"
	case s.Kind == SignalConst || s.Kind == SignalNoSignal:
		return "sig-const"
	}
	return ""
}

// assessSignal оценивает канал за цикл одним проходом.
func assessSignal(x []float32) Signal {
	var first float32
	good, bad := 0, 0
	same := true
	for _, v := range x {
		if math.Abs(float64(v)) >= 1e12 || math.IsNaN(float64(v)) { // Abs(Inf) тоже ≥ 1e12
			bad++
			continue
		}
		if good == 0 {
			first = v
		} else if v != first {
			same = false
		}
		good++
	}
	switch {
	case good == 0:
		return Signal{Kind: SignalEmpty}
	case bad > 0:
		return Signal{Kind: SignalFaulty, BadPct: 100 * float64(bad) / float64(len(x))}
	case same && first == 0:
		return Signal{Kind: SignalNoSignal}
	case same:
		return Signal{Kind: SignalConst, Value: float64(first)}
	}
	return Signal{Kind: SignalOK}
}

// short — компактная запись числа для подписи: 238.739, 0.0012, 261117.
func short(v float64) string {
	return strconv.FormatFloat(v, 'g', 6, 64)
}
