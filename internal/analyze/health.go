package analyze

import (
	"math"
	"slices"
	"time"
)

// Health — карта здоровья подсистем (charts.py, разделы 1 и 2): насколько каждая подсистема
// в каждый день (или час) отличается от нормы и сколько в ней аномальных циклов.
type Health struct {
	Step       time.Duration
	Subsystems []string // в порядке первого появления среди инженерных признаков
	Bins       []time.Time
	// Z[подсистема][интервал] — среднее трёх самых больших |z| медиан признаков подсистемы
	// за интервал (NaN — нет данных). Один сильно ушедший признак не тонет среди двадцати нормальных,
	// а один шумный не красит всю подсистему: нужны хотя бы три.
	Z [][]float64
	// Index[интервал] — худшая подсистема за интервал: «общий индекс нездоровья пресса».
	Index []float64
	// Anom — аномальные циклы по подсистемам (resample без выброса пустых интервалов, как в Python):
	// цикл засчитывается подсистеме, если хотя бы один её признак вышел за порог.
	AnomBins []time.Time
	Anom     [][]int // [подсистема][интервал]
}

// HealthMap считает карту здоровья по результату анализа.
func (r *Result) HealthMap() *Health {
	eng := r.Cols.Eng
	h := &Health{Step: r.Bins.Step, Bins: r.Bins.Starts}
	// инженерные признаки — первые в r.Bins.Med (Resample считался по eng + raw)
	subOf := make([]int, len(eng))
	for j, c := range eng {
		s := Subsystem(c)
		k := slices.Index(h.Subsystems, s)
		if k < 0 {
			k = len(h.Subsystems)
			h.Subsystems = append(h.Subsystems, s)
		}
		subOf[j] = k
	}
	nb := len(h.Bins)
	h.Z = make([][]float64, len(h.Subsystems))
	for k := range h.Z {
		h.Z[k] = make([]float64, nb)
	}
	h.Index = make([]float64, nb)
	vals := make([][]float64, len(h.Subsystems))
	for b := range nb {
		for k := range vals {
			vals[k] = vals[k][:0]
		}
		for j := range eng {
			z := math.Abs((r.Bins.Med[j][b] - r.Norms[j].Median) / r.Norms[j].Sigma)
			if !math.IsNaN(z) {
				vals[subOf[j]] = append(vals[subOf[j]], z)
			}
		}
		h.Index[b] = math.NaN()
		for k, v := range vals {
			h.Z[k][b] = topMeanNoNaN(v, 3)
			if z := h.Z[k][b]; !math.IsNaN(z) && (math.IsNaN(h.Index[b]) || z > h.Index[b]) {
				h.Index[b] = z
			}
		}
	}

	// аномальные циклы по подсистемам: все интервалы от первого до последнего, пустые — нули
	p := r.Prod
	if p.Len() == 0 {
		return h
	}
	first, last := p.Times[0].Truncate(h.Step), p.Times[p.Len()-1].Truncate(h.Step)
	for t := first; !t.After(last); t = t.Add(h.Step) {
		h.AnomBins = append(h.AnomBins, t)
	}
	h.Anom = make([][]int, len(h.Subsystems))
	for k := range h.Anom {
		h.Anom[k] = make([]int, len(h.AnomBins))
	}
	hit := make([]bool, len(h.Subsystems))
	for i := range p.Len() {
		clear(hit)
		for j, c := range eng {
			z := math.Min(math.Abs((p.Col(c)[i]-r.Anom.Rm[j][i])/r.SigEng[j]), AnomClip)
			if z > r.Opts.Z {
				hit[subOf[j]] = true
			}
		}
		b := int(p.Times[i].Truncate(h.Step).Sub(first) / h.Step)
		for k, x := range hit {
			if x {
				h.Anom[k][b]++
			}
		}
	}
	return h
}

// topMeanNoNaN — среднее k самых больших значений (np.sort(x)[-k:].mean()); пусто → NaN.
// Складываем в порядке возрастания, как numpy, чтобы совпасть до последнего знака.
func topMeanNoNaN(x []float64, k int) float64 {
	if len(x) == 0 {
		return math.NaN()
	}
	s := slices.Clone(x)
	slices.Sort(s)
	s = s[max(0, len(s)-k):]
	sum := 0.0
	for _, v := range s {
		sum += v
	}
	return sum / float64(len(s))
}
