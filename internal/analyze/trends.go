package analyze

import (
	"math"
	"regexp"
	"slices"
	"sort"
	"time"
)

// Параметры трендов — как в analyze.py.
const (
	TrendRhoMin   = 0.6 // |ρ| Спирмена: насколько монотонно меняется признак
	TrendShiftMin = 3.0 // насколько ушёл от нормы в конце периода, σ
	TrendShiftCap = 50  // выше — не важно, насколько: иначе один сломанный датчик забивает весь список
	TrendTop      = 30  // сколько трендов показывать (один на сигнал)
)

// Trend — медленное устойчивое изменение признака за период (деградация, износ).
type Trend struct {
	Feature string
	Rho     float64 // Спирмен между номером интервала и медианой: +1 растёт всегда, −1 падает всегда
	Shift   float64 // медиана последних 20% интервалов в σ нормы: где признак сейчас относительно базы
	Score   float64 // |ρ|·min(|Shift|, 50) — для сортировки
}

// Bins — медианы признаков по интервалам времени (resample(freq).median() в Python).
type Bins struct {
	Step   time.Duration // сутки, если данных ≥ 5 суток, иначе час
	Starts []time.Time   // начало каждого непустого интервала
	Med    [][]float64   // Med[признак][интервал]
}

// Resample делит рабочие циклы на интервалы (сутки или часы) и берёт медиану каждого признака.
// Интервалы без единого цикла выбрасываются (dropna(how="all")).
func Resample(prod *Dataset, feats []string) *Bins {
	b := &Bins{Step: time.Hour, Med: make([][]float64, len(feats))}
	n := prod.Len()
	if n == 0 {
		return b
	}
	if prod.Times[n-1].Sub(prod.Times[0]).Hours()/24 >= 5 {
		b.Step = 24 * time.Hour
	}
	// циклы отсортированы по времени, поэтому интервал — непрерывный кусок [from, to)
	var cuts []int
	for i := 0; i < n; {
		start := prod.Times[i].Truncate(b.Step)
		j := i
		for j < n && prod.Times[j].Truncate(b.Step).Equal(start) {
			j++
		}
		b.Starts = append(b.Starts, start)
		cuts = append(cuts, i)
		i = j
	}
	cuts = append(cuts, n)
	for f, name := range feats {
		x := prod.Col(name)
		m := make([]float64, len(b.Starts))
		for k := range b.Starts {
			m[k] = NanMedian(x[cuts[k]:cuts[k+1]])
		}
		b.Med[f] = m
	}
	// интервал, где ВСЕ признаки NaN, pandas тоже выбрасывает
	keep := make([]int, 0, len(b.Starts))
	for k := range b.Starts {
		for f := range feats {
			if !math.IsNaN(b.Med[f][k]) {
				keep = append(keep, k)
				break
			}
		}
	}
	if len(keep) < len(b.Starts) {
		starts := make([]time.Time, len(keep))
		for i, k := range keep {
			starts[i] = b.Starts[k]
		}
		b.Starts = starts
		for f := range b.Med {
			m := make([]float64, len(keep))
			for i, k := range keep {
				m[i] = b.Med[f][k]
			}
			b.Med[f] = m
		}
	}
	return b
}

// Trends повторяет блок «ДЕГРАДАЦИЯ / ТРЕНДЫ» из analyze.py.
// med и sig — норма каждого признака из feats, в том же порядке.
func Trends(b *Bins, feats []string, med, sig []float64) []Trend {
	nTail := max(3, int(float64(len(b.Starts))*0.2))
	var out []Trend
	for f, name := range feats {
		s := make([]float64, 0, len(b.Starts))
		for _, v := range b.Med[f] {
			if z := (v - med[f]) / sig[f]; !math.IsNaN(z) {
				s = append(s, z)
			}
		}
		if len(s) < 5 {
			continue
		}
		rho := Spearman(s)
		shift := NanMedian(s[max(0, len(s)-nTail):])
		capped := math.Abs(shift)
		if !(capped < TrendShiftCap) { // Python min(abs(shift), 50): NaN остаётся NaN
			if !math.IsNaN(capped) {
				capped = TrendShiftCap
			}
		}
		out = append(out, Trend{name, rho, shift, math.Abs(rho) * capped})
	}
	// по score убыванию, NaN в конец (sort_values(ascending=False, kind="stable"))
	sort.SliceStable(out, func(i, j int) bool {
		a, c := out[i].Score, out[j].Score
		if math.IsNaN(c) {
			return !math.IsNaN(a)
		}
		return a > c
	})
	return out
}

var (
	reRawAnalog = regexp.MustCompile(`^a\d{3}_`)
	reSigKey    = regexp.MustCompile(`(__\w+|_(mean|min|max|p95|rms|absmax|active))$`)
)

// SignificantTrends — тренды для отчёта: |ρ| > 0,6 и |сдвиг| > 3σ; инженерные первыми,
// по одному признаку на сигнал (из hp_min и hp_mean остаётся сильнейший), не больше top.
func SignificantTrends(all []Trend, top int) []Trend {
	var eng, raw []Trend
	for _, t := range all {
		if !(math.Abs(t.Rho) > TrendRhoMin && math.Abs(t.Shift) > TrendShiftMin) {
			continue
		}
		if reRawAnalog.MatchString(t.Feature) {
			raw = append(raw, t)
		} else {
			eng = append(eng, t)
		}
	}
	seen := map[string]bool{}
	var out []Trend
	for _, t := range slices.Concat(eng, raw) {
		key := reSigKey.ReplaceAllString(t.Feature, "")
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(out) < top {
			out = append(out, t)
		}
	}
	return out
}

// Spearman — ранговая корреляция ряда с номером точки (0, 1, 2…): насколько ряд монотонен.
// Равные значения получают средний ранг (scipy.stats.rankdata), постоянный ряд → NaN.
func Spearman(y []float64) float64 {
	n := len(y)
	r := AverageRanks(y)
	mx := float64(n-1) / 2 // средний номер точки
	var my float64
	for _, v := range r {
		my += v
	}
	my /= float64(n)
	var sxy, sxx, syy float64
	for i, v := range r {
		dx, dy := float64(i)-mx, v-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if syy == 0 {
		return math.NaN()
	}
	return sxy / math.Sqrt(sxx*syy)
}

// AverageRanks — ранги от 1 до n; у одинаковых значений — средний ранг группы.
func AverageRanks(y []float64) []float64 {
	idx := make([]int, len(y))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return y[idx[a]] < y[idx[b]] })
	r := make([]float64, len(y))
	for i := 0; i < len(idx); {
		j := i
		for j < len(idx) && y[idx[j]] == y[idx[i]] {
			j++
		}
		avg := float64(i+j+1) / 2 // ранги i+1..j → среднее (i+1+j)/2
		for k := i; k < j; k++ {
			r[idx[k]] = avg
		}
		i = j
	}
	return r
}
