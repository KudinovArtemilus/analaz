package features

import (
	"math"
	"slices"
)

// Аналоги функций numpy, которые пропускают NaN (np.nanmax, np.nansum ...).
// Все возвращают NaN, если в срезе нет ни одного числа, — как numpy (там ещё печатается warning).

// NanMax — максимум без NaN.
func NanMax(x []float64) float64 {
	m, ok := math.Inf(-1), false
	for _, v := range x {
		if !math.IsNaN(v) && (!ok || v > m) {
			m, ok = v, true
		}
	}
	if !ok {
		return math.NaN()
	}
	return m
}

// NanMin — минимум без NaN.
func NanMin(x []float64) float64 {
	m, ok := math.Inf(1), false
	for _, v := range x {
		if !math.IsNaN(v) && (!ok || v < m) {
			m, ok = v, true
		}
	}
	if !ok {
		return math.NaN()
	}
	return m
}

// NanSum — сумма без NaN. Пустая сумма = 0, как np.nansum.
func NanSum(x []float64) float64 {
	var s float64
	for _, v := range x {
		if !math.IsNaN(v) {
			s += v
		}
	}
	return s
}

// NanMean — среднее без NaN.
func NanMean(x []float64) float64 {
	var s float64
	n := 0
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

// NanPercentile — перцентиль без NaN, как np.nanpercentile(x, p) с методом по умолчанию
// (линейная интерполяция между соседними значениями отсортированного ряда).
func NanPercentile(x []float64, p float64) float64 {
	v := make([]float64, 0, len(x))
	for _, a := range x {
		if !math.IsNaN(a) {
			v = append(v, a)
		}
	}
	if len(v) == 0 {
		return math.NaN()
	}
	slices.Sort(v)
	pos := p / 100 * float64(len(v)-1) // дробная позиция в отсортированном ряду
	lo := int(math.Floor(pos))
	if lo >= len(v)-1 {
		return v[len(v)-1]
	}
	return lerp(v[lo], v[lo+1], pos-float64(lo))
}

// lerp — линейная интерполяция ровно как в numpy (_lerp): от ближнего конца.
// a + (b−a)·t и b − (b−a)·(1−t) математически равны, но в float64 могут отличаться
// в последнем знаке; numpy берёт второе при t ≥ 0,5 — повторяем, чтобы совпадать бит в бит.
func lerp(a, b, t float64) float64 {
	d := b - a
	if t >= 0.5 {
		return b - d*(1-t)
	}
	return a + d*t
}

// RMS — среднеквадратичное по конечным значениям, как rms() в extract_features.py.
func RMS(x []float64) float64 {
	var s float64
	n := 0
	for _, v := range x {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			s += v * v
			n++
		}
	}
	if n == 0 {
		return math.NaN()
	}
	return math.Sqrt(s / float64(n))
}

// NanArgMax — индекс первого максимума без NaN (-1, если чисел нет).
func NanArgMax(x []float64) int {
	best, bi := math.Inf(-1), -1
	for i, v := range x {
		if !math.IsNaN(v) && (bi < 0 || v > best) {
			best, bi = v, i
		}
	}
	return bi
}

// FirstTrue — индекс первого true; 0, если true нет. Именно так ведёт себя
// np.argmax(маска): для маски из одних false он возвращает 0, а не ошибку.
func FirstTrue(m []bool) int {
	for i, b := range m {
		if b {
			return i
		}
	}
	return 0
}

// Gradient — производная как np.gradient(x, dt):
// внутри центральная разность (x[i+1]-x[i-1])/2dt, на краях — односторонняя.
func Gradient(x []float64, dt float64) []float64 {
	n := len(x)
	g := make([]float64, n)
	if n < 2 {
		for i := range g {
			g[i] = math.NaN()
		}
		return g
	}
	g[0] = (x[1] - x[0]) / dt
	g[n-1] = (x[n-1] - x[n-2]) / dt
	for i := 1; i < n-1; i++ {
		g[i] = (x[i+1] - x[i-1]) / (2 * dt)
	}
	return g
}

// Slope — наклон прямой по методу наименьших квадратов, как np.polyfit(x, y, 1)[0].
func Slope(x, y []float64) float64 {
	n := float64(len(x))
	if len(x) < 2 || len(x) != len(y) {
		return math.NaN()
	}
	var mx, my float64
	for i := range x {
		mx += x[i]
		my += y[i]
	}
	mx /= n
	my /= n
	var sxy, sxx float64
	for i := range x {
		dx := x[i] - mx
		sxy += dx * (y[i] - my)
		sxx += dx * dx
	}
	if sxx == 0 {
		return math.NaN()
	}
	return sxy / sxx
}
