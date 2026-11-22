// shpinfo — показывает, что внутри .shp файла.
//
//	go run ./cmd/shpinfo файл.shp                   # сводка + проверка шага записи
//	go run ./cmd/shpinfo -stats файл.shp            # + min/mean/max всех аналоговых каналов
//	go run ./cmd/shpinfo -ch "slide:total force [kN]" файл.shp
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

func main() {
	stats := flag.Bool("stats", false, "печатать min/mean/max каждого аналогового канала")
	chName := flag.String("ch", "", "показать один аналоговый канал")
	tz := flag.Int("tz", 5, "часовой пояс времени в имени файла, часы от UTC")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "использование: shpinfo [-stats] [-ch имя] [-tz 5] файл.shp ...")
		os.Exit(2)
	}
	loc := time.FixedZone(fmt.Sprintf("UTC%+d", *tz), *tz*3600)

	for _, path := range flag.Args() {
		t0 := time.Now()
		f, err := shp.Read(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ошибка:", err)
			continue
		}
		readTime := time.Since(t0)
		name := shp.ParseName(path, loc)

		fmt.Printf("Файл:        %s\n", path)
		if name.HasIDs || name.Op != "" {
			fmt.Printf("Из имени:    %s, заготовка %d, плавка %d\n", name.Op, name.Piece, name.Heat)
		}
		fmt.Printf("Каналы:      %d дискретных, %d аналоговых\n", len(f.DigNames), len(f.AnaNames))
		fmt.Printf("Отсчётов:    %d, длительность %.1f с (по счётчику)\n", f.NumSamples(), f.Duration().Seconds())
		fmt.Printf("Прочитано за %v\n", readTime.Round(time.Microsecond))
		fmt.Printf("Целостность: %s\n", f.Damage)
		printTiming(f, name, loc)

		if *chName != "" {
			x, ok := f.Analog(*chName)
			if !ok {
				fmt.Printf("канал %q не найден\n", *chName)
			} else {
				mn, mean, mx, nan := describe(x)
				fmt.Printf("%s: min %.4g  mean %.4g  max %.4g  NaN %d\n", *chName, mn, mean, mx, nan)
			}
		}
		if *stats {
			fmt.Printf("%-4s %-45s %14s %14s %14s %6s\n", "#", "канал", "min", "mean", "max", "NaN")
			for j, name := range f.AnaNames {
				// по индексу, а не по имени: у дублей имени Analog() вернул бы первый канал
				mn, mean, mx, nan := describe(shp.Clean(f.Ana[j]))
				fmt.Printf("%-4d %-45.45s %14.6g %14.6g %14.6g %6d\n", j, name, mn, mean, mx, nan)
			}
		}
		fmt.Println()
	}
}

// printTiming — три независимые проверки шага записи. Если все три сходятся, шаг известен точно.
func printTiming(f *shp.File, name shp.Name, loc *time.Location) {
	fmt.Println("--- Шаг записи (три проверки) ---")
	n := f.NumSamples()
	if n < 2 {
		fmt.Println("  мало отсчётов")
		return
	}

	// 1) счётчик в начале каждой записи
	fmt.Printf("  1. Счётчик: первый %d, последний %d → шаг %v\n", f.Index[0], f.Index[n-1], f.Step())

	// 2) бит «clock pulse 10 Hz»: период 100 мс → сколько отсчётов между фронтами
	found := false
	for j, bn := range f.DigNames {
		if !strings.Contains(strings.ToLower(bn), "clock") {
			continue
		}
		found = true
		per := medianRisePeriod(f.Dig[j])
		if per > 0 {
			fmt.Printf("  2. Бит %q: %.0f отсчётов на период → при 10 Гц шаг %.1f мс\n", bn, per, 100/per)
		} else {
			fmt.Printf("  2. Бит %q не переключается\n", bn)
		}
	}
	if !found {
		fmt.Println("  2. Бита clock в файле нет")
	}

	// 3) время в имени файла против заголовка
	hdr := f.FileTime.In(loc)
	fmt.Printf("  3. Заголовок (старт): %s; имя файла: ", hdr.Format("15:04:05"))
	if name.Time.IsZero() {
		fmt.Println("времени нет")
	} else {
		end := hdr.Add(f.Duration())
		fmt.Printf("%s; старт + длительность = %s (разница %+.0f с)\n",
			name.Time.Format("15:04:05"), end.Format("15:04:05"), name.Time.Sub(end).Seconds())
	}
}

// medianRisePeriod — медиана числа отсчётов между передними фронтами бита.
func medianRisePeriod(b []bool) float64 {
	var rises []int
	for i := 1; i < len(b); i++ {
		if b[i] && !b[i-1] {
			rises = append(rises, i)
		}
	}
	if len(rises) < 3 {
		return 0
	}
	d := make([]int, len(rises)-1)
	for i := range d {
		d[i] = rises[i+1] - rises[i]
	}
	slices.Sort(d)
	return float64(d[len(d)/2])
}

// describe — min, среднее, max без NaN и число NaN.
func describe(x []float64) (mn, mean, mx float64, nan int) {
	mn, mx = math.Inf(1), math.Inf(-1)
	var sum float64
	for _, v := range x {
		if math.IsNaN(v) {
			nan++
			continue
		}
		sum += v
		mn = math.Min(mn, v)
		mx = math.Max(mx, v)
	}
	if n := len(x) - nan; n > 0 {
		return mn, sum / float64(n), mx, nan
	}
	return math.NaN(), math.NaN(), math.NaN(), nan
}
