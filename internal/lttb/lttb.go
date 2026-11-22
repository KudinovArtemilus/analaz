// Package lttb прореживает временной ряд для графика алгоритмом
// Largest-Triangle-Three-Buckets (Steinarsson, 2013).
//
// Зачем: в цикле 8125 точек, а на экране ширина графика ~1000 пикселей.
// Брать каждую 8-ю точку нельзя — потеряются пики (максимум усилия, провал давления).
// LTTB делит ряд на корзины и из каждой берёт точку, образующую самый большой
// треугольник с соседями, — именно пики и изломы. Форма кривой сохраняется.
package lttb

import "math"

// Downsample возвращает не больше n точек. Первая и последняя точки сохраняются всегда.
// Точки с NaN/Inf пропускаются (на графике будет прямой отрезок через разрыв).
func Downsample(x, y []float64, n int) ([]float64, []float64) {
	idx := Index(x, y, n)
	outX := make([]float64, len(idx))
	outY := make([]float64, len(idx))
	for k, i := range idx {
		outX[k], outY[k] = x[i], y[i]
	}
	return outX, outY
}

// Index — то же, что Downsample, но возвращает номера выбранных точек в исходном ряду.
// Нужно, чтобы взять ОДНИ И ТЕ ЖЕ моменты времени у нескольких каналов (задание и факт
// на одном графике должны иметь общую ось X).
func Index(x, y []float64, n int) []int {
	// сначала отбрасываем нечисловые значения — в треугольниках они дали бы NaN-площадь
	ok := make([]int, 0, len(x))
	for i := range x {
		if finite(x[i]) && finite(y[i]) {
			ok = append(ok, i)
		}
	}
	if n >= len(ok) || n < 3 {
		return ok
	}
	xs := func(k int) float64 { return x[ok[k]] }
	ys := func(k int) float64 { return y[ok[k]] }

	out := make([]int, 0, n)
	out = append(out, ok[0])

	// n-2 корзины между первой и последней точкой
	every := float64(len(ok)-2) / float64(n-2)
	a := 0 // номер (среди ok) точки, выбранной в предыдущей корзине
	for i := 0; i < n-2; i++ {
		// среднее следующей корзины — «третья вершина» треугольника
		nextStart := int(math.Floor(float64(i+1)*every)) + 1
		nextEnd := min(int(math.Floor(float64(i+2)*every))+1, len(ok))
		var avgX, avgY float64
		for j := nextStart; j < nextEnd; j++ {
			avgX += xs(j)
			avgY += ys(j)
		}
		cnt := float64(max(nextEnd-nextStart, 1))
		avgX /= cnt
		avgY /= cnt

		// в текущей корзине ищем точку с наибольшей площадью треугольника
		start := int(math.Floor(float64(i)*every)) + 1
		end := int(math.Floor(float64(i+1)*every)) + 1
		best, bestArea := start, -1.0
		for j := start; j < end; j++ {
			area := math.Abs((xs(a)-avgX)*(ys(j)-ys(a)) - (xs(a)-xs(j))*(avgY-ys(a)))
			if area > bestArea {
				best, bestArea = j, area
			}
		}
		out = append(out, ok[best])
		a = best
	}
	return append(out, ok[len(ok)-1])
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
