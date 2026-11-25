package analyze

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/features"
)

// 6.2г — «Было / стало», «Что меняется вместе», «Связи и производительность»
// (charts.py, разделы 4, 5, 7). Здесь только расчёт; картинки рисует браузер.

// BeforeAfter — значения признака в базовом периоде и в последних 20% рабочих циклов.
type BeforeAfter struct {
	Feature    string
	Base, Tail []float64 // без NaN
	BaseMed    float64   // медианы — для подписи «было → стало»
	TailMed    float64
}

// BeforeAfterTop — «было / стало» для 9 признаков с самым сильным трендом (Ttop[:9]).
// «Стало» — последние max(10, 20%) рабочих циклов, как в Python.
func (r *Result) BeforeAfterTop() []BeforeAfter {
	n := r.Prod.Len()
	from := n - min(n, max(10, int(float64(n)*0.2)))
	var out []BeforeAfter
	for _, t := range r.Top {
		if len(out) == 9 {
			break
		}
		col := r.Prod.Col(t.Feature)
		if col == nil {
			continue
		}
		var base []float64
		if r.Base != nil {
			base = dropNaN(r.Base.Col(t.Feature))
		}
		tail := dropNaN(col[from:])
		out = append(out, BeforeAfter{t.Feature, base, tail, NanMedian(base), NanMedian(tail)})
	}
	return out
}

func dropNaN(x []float64) []float64 {
	out := make([]float64, 0, len(x))
	for _, v := range x {
		if !math.IsNaN(v) {
			out = append(out, v)
		}
	}
	return out
}

// CorrMatrix — корреляция Спирмена между медианами за интервал (G) для 25 самых
// «трендовых» инженерных признаков. Rho[i][j] — NaN, если общих интервалов меньше двух
// или признак постоянный.
type CorrMatrix struct {
	Features []string
	Rho      [][]float64
}

// CorrPinned — признаки, которые есть в матрице всегда, даже если они не среди «трендовых»:
// перекос ползуна (= перекос верхнего штампа относительно нижнего) и давление зажима штампа —
// чтобы видеть, меняются ли они вместе. Нет у машины такого признака — его нет и в матрице.
var CorrPinned = []string{"lr_dev_absmax_mm", "lr_dev_press_mean_mm",
	"dieclamp_c1_min", "dieclamp_c2_min", "dieclamp_lower_min"}

// Correlations — как в charts.py: cc = первые 25 признаков T (по score), которые инженерные;
// после них — CorrPinned, которых среди них нет. Матрица — только если признаков ≥ 3 и
// интервалов ≥ 5. nil — не строим.
func (r *Result) Correlations() *CorrMatrix {
	var idx []int // номера признаков в Bins.Med
	var names []string
	for _, t := range r.Trends {
		if len(idx) == 25 {
			break
		}
		if j := slices.Index(r.Cols.Eng, t.Feature); j >= 0 {
			idx = append(idx, j)
			names = append(names, t.Feature)
		}
	}
	for _, f := range CorrPinned {
		if j := slices.Index(r.Cols.Eng, f); j >= 0 && !slices.Contains(names, f) && anyFinite(r.Bins.Med[j]) {
			idx = append(idx, j)
			names = append(names, f)
		}
	}
	if len(idx) < 3 || len(r.Bins.Starts) < 5 {
		return nil
	}
	m := &CorrMatrix{Features: names, Rho: make([][]float64, len(idx))}
	for a := range idx {
		m.Rho[a] = make([]float64, len(idx))
	}
	for a, ja := range idx {
		for b := 0; b <= a; b++ {
			v := SpearmanPairwise(r.Bins.Med[ja], r.Bins.Med[idx[b]])
			m.Rho[a][b], m.Rho[b][a] = v, v
		}
	}
	return m
}

// SpearmanPairwise — Спирмен двух рядов по общим (оба не NaN) точкам, как
// pandas DataFrame.corr(method="spearman"): ранги считаются заново внутри общих точек
// (средние для повторов), затем Пирсон по рангам.
func SpearmanPairwise(x, y []float64) float64 {
	var xs, ys []float64
	for i := range x {
		if !math.IsNaN(x[i]) && !math.IsNaN(y[i]) {
			xs, ys = append(xs, x[i]), append(ys, y[i])
		}
	}
	if len(xs) < 2 {
		return math.NaN()
	}
	rx, ry := AverageRanks(xs), AverageRanks(ys)
	var mx, my float64
	for i := range rx {
		mx += rx[i]
		my += ry[i]
	}
	// ранги — кратны 0,5, их сумма точна в любом порядке сложения: совпадает с numpy
	mx /= float64(len(rx))
	my /= float64(len(ry))
	var cov, sx, sy float64
	for i := range rx {
		vx, vy := rx[i]-mx, ry[i]-my
		cov += vx * vy
		sx += vx * vx
		sy += vy * vy
	}
	d := math.Sqrt(sx * sy)
	if d == 0 {
		return math.NaN()
	}
	return cov / d
}

// ScatterPair — облако «признак против признака» по всем рабочим циклам, цвет — время.
type ScatterPair struct {
	X, Y   string    // признаки
	Title  string    // подпись графика
	XS, YS []float64 // значения (NaN оставлены: браузер их пропустит)
	Days   []float64 // время цикла в днях от первого рабочего цикла
}

// pairs — пары из charts.py, раздел 7.
var scatterPairs = []struct{ x, y, title string }{
	{"force_max_kN", "work_kJ", "Усилие vs работа"},
	{"die_strokes", "pos_bottom_mm", "Ходов штампа vs нижняя точка (износ штампа)"},
	{"temp_bottom_mean", "press_time_s", "Температура масла vs время прессования"},
	{"force_max_kN", "hp_drop", "Усилие vs просадка давления"},
	// перекос штампа и его зажим: есть ли связь (пресс 10 — два контура, пресс 5 — нижний штамп)
	{"lr_dev_absmax_mm", "dieclamp_c1_min", "Перекос ползуна vs зажим штампа, контур 1"},
	{"lr_dev_absmax_mm", "dieclamp_c2_min", "Перекос ползуна vs зажим штампа, контур 2"},
	{"lr_dev_absmax_mm", "dieclamp_lower_min", "Перекос ползуна vs зажим нижнего штампа"},
}

// Scatter — пары, у которых оба признака есть и хоть где-то не NaN.
func (r *Result) Scatter() []ScatterPair {
	p := r.Prod
	if p.Len() == 0 {
		return nil
	}
	t0 := p.Times[0]
	for _, t := range p.Times { // min, как P['t_start'].min()
		if t.Before(t0) {
			t0 = t
		}
	}
	days := make([]float64, p.Len())
	for i, t := range p.Times {
		days[i] = t.Sub(t0).Seconds() / 86400
	}
	var out []ScatterPair
	for _, sp := range scatterPairs {
		x, y := p.Col(sp.x), p.Col(sp.y)
		if x == nil || y == nil || !anyFinite(x) || !anyFinite(y) {
			continue
		}
		// ρ по циклам — прямой ответ «связаны ли»: |ρ| < 0,2 — связи почти нет, > 0,5 — заметная
		title := sp.title
		if rho, n := SpearmanPairwise(x, y), finitePairs(x, y); !math.IsNaN(rho) {
			title += fmt.Sprintf(" · ρ = %+.2f по %d циклам", rho, n)
		}
		out = append(out, ScatterPair{sp.x, sp.y, title, x, y, days})
	}
	return out
}

// CycleBox — длительность цикла по дням (часам): пять чисел для «ящика с усами».
type CycleBox struct {
	Start time.Time
	N     int
	Min   float64
	Q1    float64
	Med   float64
	Q3    float64
	Max   float64
}

// CycleDurations — cycle_s рабочих циклов по дням (по часам, если период короче 5 дней —
// тот же шаг, что у Bins). Квартили — линейная интерполяция, как pandas/numpy.
// Усы — от минимума до максимума: в charts.py ящик без точек (boxpoints=False).
func (r *Result) CycleDurations() []CycleBox {
	col := r.Prod.Col("cycle_s")
	if col == nil {
		return nil
	}
	step := r.Bins.Step
	groups := map[time.Time][]float64{}
	var keys []time.Time
	for i, t := range r.Prod.Times {
		if math.IsNaN(col[i]) {
			continue
		}
		k := floorTo(t, step)
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], col[i])
	}
	slices.SortFunc(keys, func(a, b time.Time) int { return a.Compare(b) })
	out := make([]CycleBox, 0, len(keys))
	for _, k := range keys {
		v := groups[k]
		slices.Sort(v)
		out = append(out, CycleBox{k, len(v), v[0], quantileSorted(v, 0.25), quantileSorted(v, 0.5), quantileSorted(v, 0.75), v[len(v)-1]})
	}
	return out
}

// floorTo — начало суток или часа (как dt.floor('D'/'h'); время в таблице — местное «как записано»).
func floorTo(t time.Time, step time.Duration) time.Time {
	if step >= 24*time.Hour {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	}
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}

// quantileSorted — квартиль, линейная интерполяция как pandas .quantile() (numpy «linear»,
// та же функция, что для σ нормы — совпадает бит в бит).
func quantileSorted(v []float64, q float64) float64 {
	return features.NanPercentile(v, q*100)
}

// finitePairs — сколько циклов, где оба значения есть.
func finitePairs(x, y []float64) int {
	n := 0
	for i := range x {
		if !math.IsNaN(x[i]) && !math.IsNaN(y[i]) {
			n++
		}
	}
	return n
}
