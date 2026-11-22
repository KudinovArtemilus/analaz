package features

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

var (
	// skipName — каналы, которые не анализируем (SKIP_NAME в Python).
	skipName = regexp.MustCompile(`(?i)spare|^off$|^default$|^$`)
	// unsafeChars — всё, кроме латиницы, кириллицы и цифр (safe() в Python).
	unsafeChars = regexp.MustCompile(`[^0-9A-Za-zА-Яа-я]+`)
)

// safe превращает имя канала в часть имени признака: "slide:total force [kN]" → "slide_total_force_kN".
func safe(name string) string {
	return strings.Trim(unsafeChars.ReplaceAllString(name, "_"), "_")
}

// RawStats — mean/std/min/max каждого аналогового канала и доля мусора (__nan),
// а также доля времени во включённом состоянии и число включений каждого дискретного.
//
// Имена признаков: a000_<имя>__mean, d017_<имя>__on. В Python стоит ('a%03d_%s' % (...))[:60],
// но срез там применяется к кортежу, а не к строке, — то есть имя НЕ обрезается. Повторяем как есть.
func RawStats(f *shp.File, out Features) {
	for j, name := range f.AnaNames {
		if skipName.MatchString(name) || f.Absent(name) { // Absent — канала у этой машины нет (пресс 5: насосы 4–6…)
			continue
		}
		key := fmt.Sprintf("a%03d_%s", j, safe(name))

		// Первый проход: сумма, min, max, число хороших — БЕЗ копирования значений.
		// (Первая версия складывала их в срез через append: 95 МБ выделений на файл.)
		col := f.Ana[j]
		var sum float64
		mn, mx := math.Inf(1), math.Inf(-1)
		good := 0
		for _, v := range col {
			x := float64(v)
			if !good32(x) {
				continue
			}
			sum += x
			// простые сравнения вместо math.Min/Max: те медленнее, потому что
			// отдельно разбирают NaN и −0, а NaN мы уже отфильтровали
			if x < mn {
				mn = x
			}
			if x > mx {
				mx = x
			}
			good++
		}
		if bad := len(col) - good; bad > 0 {
			out[key+"__nan"] = float64(bad) / float64(len(col))
		}
		if good == 0 {
			continue
		}
		mean := sum / float64(good)
		// Второй проход: дисперсия от уже известного среднего — так же, как считает np.std.
		var ss float64
		for _, v := range col {
			if x := float64(v); good32(x) {
				ss += (x - mean) * (x - mean)
			}
		}
		out[key+"__mean"] = mean
		out[key+"__std"] = math.Sqrt(ss / float64(good))
		out[key+"__min"] = mn
		out[key+"__max"] = mx
	}

	n := f.NumSamples()
	for j, name := range f.DigNames {
		if j == 0 || skipName.MatchString(name) { // бит 0 в Python тоже пропускается
			continue
		}
		key := fmt.Sprintf("d%03d_%s", j, safe(name))
		on, rises := 0, 0
		bits := f.Dig[j]
		for i, b := range bits {
			if b {
				on++
				if i > 0 && !bits[i-1] {
					rises++ // фронт 0 → 1
				}
			}
		}
		if n > 0 {
			out[key+"__on"] = float64(on) / float64(n)
		} else {
			out[key+"__on"] = math.NaN()
		}
		out[key+"__rises"] = float64(rises)
	}
}

// good32 — значение из файла пригодно: не NaN, не Inf и не мусор |x| >= 1e12.
// Одно сравнение ловит всё сразу: NaN не меньше ничего, а Inf и мусор больше 1e12.
func good32(x float64) bool {
	return math.Abs(x) < 1e12
}

// Safe — имя канала в виде части имени признака: "slide:total force [kN]" → "slide_total_force_kN".
// Нужно снаружи, чтобы сопоставить признак a017_slide_total_force_kN__max с каналом (и его псевдонимом).
func Safe(name string) string { return safe(name) }
