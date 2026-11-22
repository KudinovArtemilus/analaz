package analyze

import (
	"math"
	"slices"
	"sort"
)

// Параметры поиска аномалий — как в analyze.py.
const (
	AnomWindow     = 51  // окно скользящей медианы, циклов (±25 соседей)
	AnomMinPeriods = 10  // меньше заполненных соседей — медиана не считается
	AnomClip       = 100 // |z| обрезается сверху, чтобы один сломанный признак не «перекрикивал» всё
	AnomTopK       = 5   // сколько признаков показывать в объяснении
)

// Anomalies — результат поиска локальных выбросов по инженерным признакам.
//
// Идея: цикл сравнивается не с нормой базового периода, а с СОСЕДНИМИ циклами
// (скользящая медиана). Медленный износ сдвигает и соседей, поэтому не даёт ложных
// аномалий — им занимаются тренды. А резкий одиночный скачок виден сразу.
type Anomalies struct {
	Features []string    // инженерные признаки, по которым искали
	Rm       [][]float64 // Rm[признак][цикл] — скользящая медиана соседей
	MaxZ     []float64   // самый большой |z| цикла (NaN, если у цикла нет ни одного z)
	Score    []float64   // среднее трёх самых больших |z| — для сортировки списка
	Flag     []bool      // MaxZ > порога
	Culprits []Culprit   // какие признаки чаще всего виноваты
}

// Culprit — признак и число циклов, где его |z| превысил порог.
type Culprit struct {
	Feature string
	Cycles  int
}

// Reason — один признак в объяснении «что не так» с циклом.
type Reason struct {
	Feature string
	Value   float64 // значение в цикле
	Rm      float64 // медиана соседних циклов
	Z       float64 // (Value − Rm) / σ, со знаком и без обрезки
}

// DetectAnomalies повторяет блок «АНОМАЛИИ» из analyze.py (без IsolationForest).
// prod — рабочие циклы по времени, sigma — робастная σ каждого признака из eng.
func DetectAnomalies(prod *Dataset, eng []string, sigma []float64, zThr float64) *Anomalies {
	n := prod.Len()
	a := &Anomalies{
		Features: eng,
		Rm:       make([][]float64, len(eng)),
		MaxZ:     make([]float64, n),
		Score:    make([]float64, n),
		Flag:     make([]bool, n),
	}
	for i := range a.MaxZ {
		a.MaxZ[i] = math.NaN()
	}
	// zl[цикл] — |z| всех признаков цикла (NaN → 0) для среднего трёх наибольших
	zl := make([][]float64, n)
	for i := range zl {
		zl[i] = make([]float64, len(eng))
	}
	cnt := make([]int, len(eng))

	for j, name := range eng {
		x := prod.Col(name)
		rm := RollingMedian(x, AnomWindow, AnomMinPeriods)
		a.Rm[j] = rm
		for i := range n {
			z := math.Abs((x[i] - rm[i]) / sigma[j])
			if math.IsNaN(z) {
				continue
			}
			z = math.Min(z, AnomClip)
			zl[i][j] = z
			if math.IsNaN(a.MaxZ[i]) || z > a.MaxZ[i] {
				a.MaxZ[i] = z
			}
			if z > zThr {
				cnt[j]++
			}
		}
	}
	for i, row := range zl {
		a.Score[i] = top3Mean(row)
		a.Flag[i] = a.MaxZ[i] > zThr // NaN > порога → false, как в pandas
	}
	for j, c := range cnt {
		if c > 0 {
			a.Culprits = append(a.Culprits, Culprit{eng[j], c})
		}
	}
	sort.SliceStable(a.Culprits, func(p, q int) bool { return a.Culprits[p].Cycles > a.Culprits[q].Cycles })
	return a
}

// Explain — до k признаков с наибольшим |z| цикла i, у которых |z| > порог/2 (explain() в Python).
func (a *Anomalies) Explain(prod *Dataset, sigma []float64, i, k int, zThr float64) []Reason {
	var r []Reason
	for j, name := range a.Features {
		v, rm := prod.Col(name)[i], a.Rm[j][i]
		z := (v - rm) / sigma[j]
		if !math.IsNaN(z) {
			r = append(r, Reason{name, v, rm, z})
		}
	}
	sort.SliceStable(r, func(p, q int) bool { return math.Abs(r[p].Z) > math.Abs(r[q].Z) })
	r = r[:min(k, len(r))]
	return slices.DeleteFunc(r, func(x Reason) bool { return !(math.Abs(x.Z) > zThr/2) })
}

// Order — номера аномальных циклов, самые сильные (по Score) первыми.
func (a *Anomalies) Order() []int {
	var idx []int
	for i, f := range a.Flag {
		if f {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(p, q int) bool { return a.Score[idx[p]] > a.Score[idx[q]] })
	return idx
}

// top3Mean — np.sort(row)[-3:].mean(): сложение в порядке возрастания, как в numpy.
func top3Mean(row []float64) float64 {
	if len(row) == 0 {
		return math.NaN()
	}
	s := slices.Clone(row)
	slices.Sort(s)
	s = s[max(0, len(s)-3):]
	sum := 0.0
	for _, v := range s {
		sum += v
	}
	return sum / float64(len(s))
}

// RollingMedian повторяет pandas rolling(w, center=True, min_periods=mp).median():
// окно [i−w/2, i+w/2] обрезается краями ряда, NaN пропускаются, и если в окне
// меньше mp чисел — результат NaN.
//
// Окно хранится отсортированным: при сдвиге одно значение вставляется и одно
// удаляется двоичным поиском. Это O(w) на шаг вместо O(w·log w) на полную сортировку.
func RollingMedian(x []float64, w, mp int) []float64 {
	n, half := len(x), w/2
	out := make([]float64, n)
	win := make([]float64, 0, w)
	add := func(v float64) {
		if !math.IsNaN(v) {
			k, _ := slices.BinarySearch(win, v)
			win = slices.Insert(win, k, v)
		}
	}
	remove := func(v float64) {
		if !math.IsNaN(v) {
			k, _ := slices.BinarySearch(win, v)
			win = slices.Delete(win, k, k+1)
		}
	}
	for i := 0; i < min(half, n); i++ { // заполняем правую половину окна для i = 0
		add(x[i])
	}
	for i := range n {
		if r := i + half; r < n {
			add(x[r])
		}
		if l := i - half - 1; l >= 0 {
			remove(x[l])
		}
		m := len(win)
		switch {
		case m < mp || m == 0:
			out[i] = math.NaN()
		case m%2 == 1:
			out[i] = win[m/2]
		default:
			out[i] = (win[m/2-1] + win[m/2]) / 2
		}
	}
	return out
}
