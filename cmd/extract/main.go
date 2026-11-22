// extract — признаки по всем циклам в папке, замена extract_features.py.
//
//	extract D:\press --op OP1 -o features_OP1                 # всё
//	extract D:\press --op OP1 -o features_OP1 --every 20      # быстрая проба: каждый 20-й файл
//	extract D:\press --op OP1 --from 2026-11-01 --to 2026-11-18 -j 3
//
// Пишет features_OP1.csv.gz — его читают analyze.py и daily.py без изменений.
// Можно прервать (Ctrl+C) и запустить снова: обработанные файлы пропускаются.
//
// Сервер с файлом настроек делает то же сам, раз в 5 минут (web/extract.go, код общий —
// internal/extract). Утилита нужна для разовых пересчётов и проб (--every, --from/--to).
// Не запускайте её в ту же таблицу features_OP1.csv.gz, пока работает сервер: пишут
// оба, и строки последнего прохода одного из них пропадут (сервер их досчитает, если файлы
// ещё в буфере). В другую таблицу (-o features_quick_OP1) — можно: сервер перенесёт новые строки сам.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/extract"
)

func main() {
	out := flag.String("o", "features", "имя выходного файла без расширения")
	jobs := flag.Int("j", 2, "сколько файлов обрабатывать одновременно (HDD и сеть: 2–3, SSD: больше)")
	op := flag.String("op", "", "только эта операция, напр. OP1")
	from := flag.String("from", "", "с даты ГГГГ-ММ-ДД")
	to := flag.String("to", "", "по дату ГГГГ-ММ-ДД включительно")
	every := flag.Int("every", 1, "брать каждый N-й файл (быстрый обзор)")
	limit := flag.Int("limit", 0, "обработать только N файлов (для пробы)")
	tz := flag.Int("tz", 5, "часовой пояс времени в имени файла, часы от UTC")
	verbose := flag.Bool("v", false, "печатать каждый файл")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "использование: extract [флаги] папка_с_shp")
		flag.PrintDefaults()
	}
	// Флаги в Go пишутся до позиционных аргументов. Чтобы можно было писать как в Python
	// (extract D:\press --op OP1), переставляем папку в конец сами.
	flag.CommandLine.Parse(reorderArgs(os.Args[1:]))
	if flag.NArg() != 1 || *jobs < 1 || *every < 1 {
		flag.Usage()
		os.Exit(2)
	}
	root := flag.Arg(0)
	loc := time.FixedZone(fmt.Sprintf("UTC%+d", *tz), *tz*3600)
	outPath := *out + ".csv.gz"

	if err := run(root, outPath, *jobs, *every, *limit, *verbose, batch.Filter{From: *from, To: *to, Op: *op}, loc); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func run(root, outPath string, jobs, every, limit int, verbose bool, flt batch.Filter, loc *time.Location) error {
	// --- что уже посчитано ---
	parquet := strings.TrimSuffix(outPath, ".csv.gz") + ".parquet"
	if _, err := os.Stat(parquet); err == nil {
		// Go пишет .csv.gz, а run.py и load_existing предпочитают .parquet —
		// старый Python-файл перекрыл бы новые данные. Откладываем его в сторону.
		if err := os.Rename(parquet, parquet+".python"); err != nil {
			return err
		}
		fmt.Printf("Найден %s от Python — переименован в %s.python, признаки будут в %s\n", parquet, parquet, outPath)
	}
	table, err := batch.ReadCSVGz(outPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		table = batch.NewTable()
	case errors.Is(err, batch.ErrOldStep):
		if err := os.Rename(outPath, outPath+".old_dt10"); err != nil {
			return err
		}
		fmt.Printf("Старый файл признаков (шаг 10 мс) переименован в %s.old_dt10 — считаю заново.\n", outPath)
		table = batch.NewTable()
	case err != nil:
		return err
	}

	// --- считаем (общий код с сервером) ---
	st, err := extract.Update(table, outPath, extract.Options{
		Roots: []string{root}, Filter: flt, Every: every, Limit: limit, Jobs: jobs, Loc: loc,
		Log: func(format string, a ...any) { fmt.Printf(format, a...) }, Verbose: verbose,
	})
	if err != nil {
		return err
	}

	if st.Damaged > 0 {
		fmt.Printf("Файлов с повреждёнными записями: %d — испорченные куски пропущены, циклы помечены колонкой shp_damaged\n", st.Damaged)
	}
	if len(st.Errors) > 0 {
		errPath := strings.TrimSuffix(outPath, ".csv.gz") + "_errors.txt"
		fh, err := os.OpenFile(errPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer fh.Close()
		for _, e := range st.Errors {
			fmt.Fprintf(fh, "%v\n\n", e.Err)
		}
		fmt.Printf("Ошибок: %d (см. %s)\n", len(st.Errors), errPath)
	}
	return nil
}

// reorderArgs переносит позиционные аргументы (папку) в конец, чтобы флаги можно было
// писать и после неё. Значение флага (--op OP1) остаётся рядом со своим флагом.
func reorderArgs(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") || name == "v" {
			continue // --op=OP1 или булев флаг без значения
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, pos...)
}
