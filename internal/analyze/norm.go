package analyze

import (
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/KudinovArtemilus/press-go/internal/features"
)

// ProdForceKN — цикл считается рабочим (ковка), если максимальное усилие выше (PROD_FORCE_KN).
const ProdForceKN = 5000.0

// metaCols — служебные колонки: не признаки состояния оборудования (META в Python).
var metaCols = map[string]bool{
	"fn_prefix": true, "fn_piece": true, "fn_heat": true, "n_samples": true,
	"piece_id": true, "heat_id": true, "die_strokes": true, "which_op_max": true,
	"feat_ver": true, // версия признаков (сервер пересчитывает циклы со старой)
}

// IsMeta — служебная колонка. У КПС к ним относятся заданные размеры колеса (рецепт)
// и счётчики валков: это не состояние оборудования, а «что катаем» и «с какой замены»,
// как die_strokes у пресса (по ним — нормы по колесу и валкам и журнал изменений).
func IsMeta(name string) bool {
	return metaCols[name] || strings.HasPrefix(name, "kps_wheel_") || strings.HasPrefix(name, "kps_roll_")
}

var (
	reRawOrDig = regexp.MustCompile(`^[ad]\d{3}_`)
	reRaw      = regexp.MustCompile(`^a\d{3}_.*__(mean|max|min|std)$`)
	reDig      = regexp.MustCompile(`^d\d{3}_`)
)

// Columns — какие признаки анализировать (select_columns в Python):
// заполнены больше чем в половине циклов и принимают больше одного значения.
type Columns struct {
	Eng []string // инженерные: force_max_kN, casedrain_md1_mean…
	Raw []string // сырые статистики каналов: a017_…__max
	Dig []string // дискретные: d012_…__on
}

// SelectColumns повторяет select_columns(df).
func SelectColumns(d *Dataset) Columns {
	var c Columns
	for j, name := range d.Cols {
		if IsMeta(name) || !useful(d.Data[j]) {
			continue
		}
		switch {
		case !reRawOrDig.MatchString(name):
			c.Eng = append(c.Eng, name)
		case reRaw.MatchString(name):
			c.Raw = append(c.Raw, name)
		case reDig.MatchString(name):
			c.Dig = append(c.Dig, name)
		}
	}
	return c
}

// useful — notna().mean() > 0.5 и nunique(dropna) > 1.
func useful(x []float64) bool {
	n, first, many := 0, math.NaN(), false
	for _, v := range x {
		if math.IsNaN(v) {
			continue
		}
		if n == 0 {
			first = v
		} else if v != first {
			many = true
		}
		n++
	}
	return float64(n)/float64(len(x)) > 0.5 && many
}

// Production — рабочие циклы (усилие > 5000 кН). Если их меньше 10 — все циклы, как в Python.
func Production(d *Dataset) *Dataset {
	f := d.Col("force_max_kN")
	p := d.Where(func(i int) bool { return f != nil && f[i] > ProdForceKN })
	if p.Len() < 10 {
		return d
	}
	return p
}

// Baseline — базовый период: первые по времени frac рабочих циклов, но не меньше 10.
func Baseline(p *Dataset, frac float64) *Dataset {
	n := max(10, int(float64(p.Len())*frac))
	n = min(n, p.Len())
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return p.Rows(idx)
}

// Norm — норма одного признака.
type Norm struct {
	Feature string
	Kind    string  // "eng" или "raw"
	Median  float64 // медиана базового периода
	Sigma   float64 // робастная σ с «полом» — делитель для z = (x − median) / σ
	Q       [5]float64
}

// Quantiles — уровни квантилей нормы (normal_profile.csv: p1, p5, median, p95, p99).
var Quantiles = [5]float64{1, 5, 50, 95, 99}

// RobustStats повторяет robust_stats(B, ALL) из analyze.py.
// base — колонка базового периода, all — та же колонка по всем рабочим циклам.
func RobustStats(base, all []float64) (med, sig float64) {
	q := features.NanPercentile
	med = NanMedian(base)

	// σ = max(MAD·1,4826, IQR/1,349); отсутствующее значение считается нулём (fillna(0))
	dev := make([]float64, len(base))
	for i, v := range base {
		dev[i] = math.Abs(v - med)
	}
	mad := zeroIfNaN(NanMedian(dev) * 1.4826)
	iqr := zeroIfNaN((q(base, 75) - q(base, 25)) / 1.349)
	sig = math.Max(mad, iqr)

	// «Пол» σ по всем циклам: чтобы почти постоянный в базе признак не давал z в миллионы.
	// math.Max, как и np.maximum, возвращает NaN, если хоть один аргумент NaN.
	spread := math.Abs(q(all, 95) - q(all, 5))
	floor := math.Max(spread*0.1, math.Abs(q(all, 75)-q(all, 25))/1.349*0.5)
	floor = math.Max(floor, math.Abs(med)*0.01) + 1e-6
	if allIntegers(all) { // целочисленные признаки (запаздывания в отсчётах, счётчики): шаг 1 — это не 100σ
		floor = math.Max(floor, 1.0)
	}
	return med, math.Max(sig, floor)
}

// ComputeNorms — норма каждого инженерного и сырого признака.
func ComputeNorms(base, prod *Dataset, cols Columns) []Norm {
	var out []Norm
	add := func(names []string, kind string) {
		for _, name := range names {
			b, a := base.Col(name), prod.Col(name)
			n := Norm{Feature: name, Kind: kind}
			n.Median, n.Sigma = RobustStats(b, a)
			for k, p := range Quantiles {
				n.Q[k] = features.NanPercentile(b, p)
			}
			out = append(out, n)
		}
	}
	add(cols.Eng, "eng")
	add(cols.Raw, "raw")
	return out
}

func zeroIfNaN(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return v
}

// allIntegers — все непустые значения целые (и есть хотя бы одно).
func allIntegers(x []float64) bool {
	seen := false
	for _, v := range x {
		if math.IsNaN(v) {
			continue
		}
		seen = true
		if v != math.Trunc(v) {
			return false
		}
	}
	return seen
}

// NanMedian — медиана без NaN, как pandas .median(): при чётном числе — (a+b)/2.
// Отличается от NanPercentile(x, 50) только последним знаком, но равные медианы
// должны оставаться равными — от этого зависят ранги в трендах.
func NanMedian(x []float64) float64 {
	v := make([]float64, 0, len(x))
	for _, a := range x {
		if !math.IsNaN(a) {
			v = append(v, a)
		}
	}
	n := len(v)
	if n == 0 {
		return math.NaN()
	}
	slices.Sort(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}
