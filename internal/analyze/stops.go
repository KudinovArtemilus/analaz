package analyze

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Параметры остановов — как в analyze.py.
const (
	StopGapMin    = 30.0 // пауза между циклами, мин, считающаяся остановом
	StopBefore    = 50   // сколько рабочих циклов до останова смотреть
	StopZ         = 3.0  // |z| медианы этих циклов, начиная с которого признак «отклонялся»
	StopReasonTop = 5
)

// Stop — останов пресса и что отклонялось от нормы перед ним (возможные предвестники).
type Stop struct {
	From, Resume time.Time
	Hours        float64  // простой, ч, округлён до 0,01 как round(x, 2) в Python
	Before       []Reason // признаки с |z| > 3 за 50 циклов до останова, сильнейшие первыми (Value = медиана, Rm = норма)
}

// StopsResult — все остановы и сводка «какой признак чаще всего отклонялся перед остановом».
type StopsResult struct {
	TypicalGapMin float64 // обычный интервал между циклами, мин
	GapThrMin     float64 // порог останова, мин (поднимается, если обработана выборка --every)
	Stops         []Stop  // по длительности простоя, самые долгие первыми
	Precursors    []Culprit
}

// FindStops повторяет блок «ОСТАНОВЫ и предвестники» из analyze.py.
// all — все циклы (и нерабочие тоже: останов — это пауза в записи вообще),
// prod — рабочие циклы, med/sig — норма инженерных признаков eng.
func FindStops(all, prod *Dataset, eng []string, med, sig []float64, gapMin float64) *StopsResult {
	r := &StopsResult{}
	n := all.Len()
	gaps := make([]float64, max(0, n-1)) // gaps[i-1] — пауза перед циклом i, мин
	for i := 1; i < n; i++ {
		gaps[i-1] = all.Times[i].Sub(all.Times[i-1]).Seconds() / 60
	}
	if len(gaps) > 0 {
		r.TypicalGapMin = NanMedian(gaps)
	}
	// если обработана выборка (--every N), обычный интервал большой — порог поднимаем
	r.GapThrMin = math.Max(gapMin, 4*r.TypicalGapMin)

	pre := make([]int, len(eng))
	for k, g := range gaps {
		if !(g > r.GapThrMin) {
			continue
		}
		tStop, tResume := all.Times[k], all.Times[k+1]
		// рабочие циклы не позже начала останова, последние 50
		end := sort.Search(prod.Len(), func(i int) bool { return prod.Times[i].After(tStop) })
		start := max(0, end-StopBefore)
		if end-start < 5 {
			continue
		}
		var zs []Reason
		for j, name := range eng {
			m := NanMedian(prod.Col(name)[start:end])
			if z := (m - med[j]) / sig[j]; !math.IsNaN(z) {
				zs = append(zs, Reason{Feature: name, Value: m, Rm: med[j], Z: z})
			}
		}
		sort.SliceStable(zs, func(a, b int) bool { return math.Abs(zs[a].Z) > math.Abs(zs[b].Z) })
		var before []Reason
		for i, z := range zs {
			if math.Abs(z.Z) > StopZ {
				pre[indexOf(eng, z.Feature)]++
				if i < StopReasonTop { // как zb.index[:5] в Python: из первых пяти — только сильные
					before = append(before, z)
				}
			}
		}
		r.Stops = append(r.Stops, Stop{From: tStop, Resume: tResume, Hours: round2(g / 60), Before: before})
	}
	sort.SliceStable(r.Stops, func(a, b int) bool { return r.Stops[a].Hours > r.Stops[b].Hours })
	for j, c := range pre {
		if c > 0 {
			r.Precursors = append(r.Precursors, Culprit{eng[j], c})
		}
	}
	sort.SliceStable(r.Precursors, func(a, b int) bool { return r.Precursors[a].Cycles > r.Precursors[b].Cycles })
	return r
}

// round2 — round(x, 2) из Python: точное десятичное округление двоичного значения
// (math.Round(x*100)/100 иногда ошибается на краях вроде 2.675).
func round2(x float64) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 2, 64), 64)
	return v
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// SensorIssue — подозрение на неисправность датчика или канала.
type SensorIssue struct {
	Feature string // колонка таблицы (…__std или …__nan)
	Signal  string // понятное название
	Problem string
	Base    float64 // значение в базовом периоде
	Now     float64 // значение в последних 20% циклов
}

// notMeasured — каналы, которые не являются измерением (уставки, режимы, расчёты),
// для них «не меняется» — норма. Список из analyze.py.
var notMeasured = regexp.MustCompile(`(?i)Date|Day|Time|Soll|Anteil|Set|set_|Mode|mode|Tune|CMD|Cmd|Target|Calc|Needed|Piece|Heat|Stokes|Which|Flow|Out|Dev|Lim|Part|Filter|Local|State|Encoder_?Val|_s$`)

var reStdCol = regexp.MustCompile(`^a\d{3}_.*__std$`)

// SensorHealth повторяет блок «ЗДОРОВЬЕ ДАТЧИКОВ» из analyze.py:
//   - замер: в базе канал «дышал» (std > 0), а в последний период стоит (std = 0);
//   - NaN/мусор: в последний период больше 1% отсчётов негодные.
//
// Ограничение метода: если датчик сломался ДО базового периода, он «стоит» и в базе,
// и такой отказ здесь не виден (так с MD5) — для этого нужна хранимая норма (3а+)
// или сравнение с соседними каналами.
func SensorHealth(d, prod, base *Dataset) []SensorIssue {
	return SensorHealthBy(d, prod,
		func(c string) float64 { return NanMedian(base.Col(c)) },
		func(c string) float64 { return NanMean(base.Col(c)) })
}

// SensorHealthBy — то же, но значения базы берутся из функций: baseMed — медиана колонки
// в норме, baseMean — среднее (для доли NaN). Так работает и с хранимой нормой.
func SensorHealthBy(d, prod *Dataset, baseMed, baseMean func(string) float64) []SensorIssue {
	tail := prod.Rows(lastN(prod.Len(), max(10, int(float64(prod.Len())*0.2))))
	var out []SensorIssue
	for _, c := range d.Cols {
		if !reStdCol.MatchString(c) || notMeasured.MatchString(strings.Replace(c, "__std", "", 1)) {
			continue
		}
		b, t := baseMed(c), NanMedian(tail.Col(c))
		scale := 0.0
		if mc := strings.Replace(c, "__std", "__mean", 1); d.Col(mc) != nil {
			scale = math.Abs(baseMed(mc))
		}
		if b > 0 && t == 0 && b > 0.002*(scale+1e-9) {
			out = append(out, SensorIssue{c, strings.Replace(Human(c), " [std]", "", 1), "замер (не меняется) в последний период", b, t})
		}
	}
	for _, c := range d.Cols {
		if !strings.HasSuffix(c, "__nan") {
			continue
		}
		if t := NanMean(tail.Col(c)); t > 0.01 {
			out = append(out, SensorIssue{c, Human(c), "NaN/мусор в данных", baseMean(c), t})
		}
	}
	return out
}

// lastN — номера последних k строк из n.
func lastN(n, k int) []int {
	k = min(k, n)
	idx := make([]int, k)
	for i := range idx {
		idx[i] = n - k + i
	}
	return idx
}

// NanMean — среднее без NaN (pandas .mean()); пусто → NaN.
func NanMean(x []float64) float64 {
	s, n := 0.0, 0
	for _, v := range x {
		if !math.IsNaN(v) {
			s += v
			n++
		}
	}
	if n == 0 {
		return math.NaN()
	}
	return s / float64(n)
}
