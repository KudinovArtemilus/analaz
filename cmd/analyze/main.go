// analyze — анализ таблицы признаков (перенос analyze.py и daily.py): норма, аномалии,
// тренды, остановы, датчики, диагноз по узлам, отчёт за сутки.
//
//	analyze features_OP1.csv.gz --op OP1 -o analyze_go_OP1 [--day last]
//
// Норма. По умолчанию — как в Python: первые 20% рабочих циклов (--baseline-frac) или период
// --baseline-start / --baseline-end. Её можно сохранить и дальше оценивать по ней:
//
//	analyze f.csv.gz --op OP1 --baseline-start 2026-11-07 --baseline-end 2026-11-09 --save-norm norm_OP1.json
//	analyze f.csv.gz --op OP1 --norm norm_OP1.json --day last
//
// Циклы из повреждённых .shp по умолчанию выбрасываются (--damaged keep — оставить, как Python):
// помеченные extract (shp_damaged > 0) и с физически невозможными значениями (≥ 5 признаков на ≥ 1000σ
// от соседей, из них ≥ 2 инертных: температура, уровень, дренаж, аккумулятор). Сбои пресса не трогаются.
//
// В папку -o пишет:
//
//	norm.csv              медиана, робастная σ и квантили нормы по каждому признаку
//	anomalies.csv         аномальные циклы, сильнейшие сверху, с объяснением «что не так»
//	anomaly_reasons.csv   то же объяснение построчно (признак, значение, соседи, z) — для сверки и графиков
//	anomaly_cycles.csv    max|z| и score КАЖДОГО рабочего цикла
//	culprits.csv          какие признаки чаще всего делают цикл аномальным
//	trends.csv            тренд каждого признака: ρ Спирмена, сдвиг от нормы в σ, score
//	trends_top.csv        значимые тренды для отчёта (|ρ| > 0,6 и |сдвиг| > 3σ), по одному на сигнал
//	stops.csv             остановы (паузы между циклами) и что отклонялось за 50 циклов до них
//	stop_precursors.csv   какие признаки чаще всего отклонялись перед остановами
//	sensors.csv           подозрения на датчики: «замер» (не меняется) и NaN/мусор
//	signal_lost.csv       каналы с ровным нулём, когда однотипные соседи работают (новое, в Python нет)
//	diagnosis.txt         диагноз по узлам: светофор, факты, прогноз, причина, что проверить
//
// С --day last|ГГГГ-ММ-ДД|all ещё отчёт за сутки (daily.py) в папку -o\daily:
//
//	diagnosis_ГГГГ-ММ-ДД_OP1.txt   итог суток и диагноз по узлам
//	day_anomalies_….csv            аномальные циклы суток
//	day_vs_norm_….csv              ключевые показатели: сутки против нормы
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
	"github.com/KudinovArtemilus/press-go/internal/batch"
)

// options — все флаги командной строки.
type options struct {
	path, op, out, day string
	frac, zThr, gapMin float64
	baseFrom, baseTo   string
	normPath, saveNorm string
	damaged, note      string
}

func main() {
	var o options
	flag.StringVar(&o.op, "op", "", "только эта операция, напр. OP1; с колесом/рецептом — OP3@dv860-dw895, OP1@rcp60-650")
	flag.StringVar(&o.out, "o", "analyze_go", "папка для результатов")
	flag.Float64Var(&o.frac, "baseline-frac", 0.2, "доля первых по времени рабочих циклов как «норма»")
	flag.StringVar(&o.baseFrom, "baseline-start", "", "норма с этого момента (ГГГГ-ММ-ДД или ГГГГ-ММ-ДД ЧЧ:ММ)")
	flag.StringVar(&o.baseTo, "baseline-end", "", "норма по этот момент включительно; дата без времени = полночь, как в Python")
	flag.StringVar(&o.normPath, "norm", "", "оценивать по хранимой норме из этого файла (JSON)")
	flag.StringVar(&o.saveNorm, "save-norm", "", "сохранить посчитанную норму в этот файл (JSON)")
	flag.StringVar(&o.note, "note", "", "пояснение к сохраняемой норме: почему этот период хороший")
	flag.StringVar(&o.damaged, "damaged", "drop", "циклы из повреждённых .shp: drop — выбросить, keep — оставить все (как Python)")
	flag.Float64Var(&o.zThr, "z", 6.0, "порог робастного z для аномалии")
	flag.Float64Var(&o.gapMin, "gap-min", analyze.StopGapMin, "пауза между циклами (мин), считающаяся остановом")
	flag.StringVar(&o.day, "day", "", "отчёт за сутки: last — последние в данных, ГГГГ-ММ-ДД или all — за каждые")
	flag.CommandLine.Parse(reorder(os.Args[1:]))
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "использование: analyze features.csv.gz [--op OP1] [-o папка] [--norm norm.json | --save-norm norm.json] [--day last]")
		os.Exit(2)
	}
	o.path = flag.Arg(0)
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	if o.normPath != "" && (o.saveNorm != "" || o.baseFrom != "" || o.baseTo != "") {
		return fmt.Errorf("--norm нельзя вместе с --save-norm / --baseline-*: норма либо берётся из файла, либо считается")
	}
	if o.damaged != "drop" && o.damaged != "keep" {
		return fmt.Errorf("--damaged: ждали drop или keep, получили %q", o.damaged)
	}
	opts := analyze.DefaultOptions()
	opts.BaselineFrac, opts.Z, opts.GapMin = o.frac, o.zThr, o.gapMin
	opts.Op, opts.Regime = analyze.SplitOp(o.op) // OP3@dv860-dw895 — колесо КПС, OP1@rcp60-650 — рецепт пресса
	if opts.Regime == "all" {
		opts.Regime = ""
	}
	opts.KeepDamaged, opts.Source, opts.Note = o.damaged == "keep", o.path, o.note
	var err error
	if opts.BaseFrom, err = analyze.ParseWhen(o.baseFrom); err != nil {
		return err
	}
	if opts.BaseTo, err = analyze.ParseWhen(o.baseTo); err != nil {
		return err
	}
	if o.normPath != "" {
		if opts.Norm, err = analyze.LoadNorm(o.normPath); err != nil {
			return err
		}
	}

	t, err := batch.ReadCSVGz(o.path)
	if err != nil {
		return err
	}
	d, err := analyze.FromTable(t)
	if err != nil {
		return err
	}
	r, err := analyze.Run(d, opts)
	if err != nil {
		return err
	}
	report(r, o)
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return err
	}
	if o.saveNorm != "" {
		if err := r.Norm.Save(o.saveNorm); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Норма сохранена ->", o.saveNorm)
	}
	if err := writeAll(r, o.out); err != nil {
		return err
	}
	if o.day != "" {
		if err := runDaily(r, o.day, filepath.Join(o.out, "daily")); err != nil {
			return err
		}
	}
	fmt.Fprintln(os.Stderr, "Результаты ->", o.out)
	return nil
}

// report — итог анализа в консоль.
func report(r *analyze.Result, o options) {
	if r.Damaged > 0 {
		fmt.Fprintf(os.Stderr, "Выброшено циклов из повреждённых .shp (shp_damaged): %d (--damaged keep — оставить)\n", r.Damaged)
	}
	if len(r.Garbage) > 0 {
		fmt.Fprintf(os.Stderr, "Выброшено циклов с мусорными значениями (похоже на повреждённый .shp — проверьте shpinfo): %d\n", len(r.Garbage))
		for k, x := range r.Garbage {
			fmt.Fprintf(os.Stderr, "  %s  %s: %d признаков ≥ %dσ (из них %d — температура/уровень/дренаж/аккумулятор), худший %s (%.0fσ)\n",
				r.GarbageT[k].Format("02.01 15:04:05"), baseName(r.GarbageF[k]), x.Feats, analyze.GarbageZ, x.Inert, x.Worst, x.Z)
		}
	}
	ns := r.Norm
	if r.Stored {
		fmt.Fprintf(os.Stderr, "Норма из %s: %s — %s, %d циклов", o.normPath,
			ns.BaseFrom.Format("02.01.2006 15:04"), ns.BaseTo.Format("02.01.2006 15:04"), ns.BaseCycles)
		if ns.Note != "" {
			fmt.Fprintf(os.Stderr, " (%s)", ns.Note)
		}
		fmt.Fprintln(os.Stderr)
		if r.Dropped > 0 {
			fmt.Fprintf(os.Stderr, "  %d признаков есть в данных, но нет в норме — пропущены\n", r.Dropped)
		}
	} else {
		fmt.Fprintf(os.Stderr, "База: %s — %s (%d рабочих циклов)\n",
			ns.BaseFrom.Format("02.01 15:04"), ns.BaseTo.Format("02.01 15:04"), ns.BaseCycles)
		if o.saveNorm != "" && ns.BaseCycles < 30 {
			fmt.Fprintf(os.Stderr, "ВНИМАНИЕ: в норме всего %d циклов — σ и квантили будут неустойчивы, лучше ≥ 100\n", ns.BaseCycles)
		}
	}
	fmt.Fprintf(os.Stderr, "Циклов %d, рабочих %d; инженерных %d, сырых %d, дискретных %d\n",
		r.All.Len(), r.Prod.Len(), len(r.Cols.Eng), len(r.Cols.Raw), len(r.Cols.Dig))
	fmt.Fprintf(os.Stderr, "Аномальных циклов: %d из %d (порог z > %g)\n", len(r.Order), r.Prod.Len(), o.zThr)
	for _, c := range r.Anom.Culprits[:min(5, len(r.Anom.Culprits))] {
		fmt.Fprintf(os.Stderr, "  %-40s %d циклов\n", c.Feature, c.Cycles)
	}
	step := "суткам"
	if r.Bins.Step < 24*time.Hour {
		step = "часам"
	}
	fmt.Fprintf(os.Stderr, "Тренды: %d интервалов по %s, значимых %d:\n", len(r.Bins.Starts), step, len(r.Top))
	for _, t := range r.Top[:min(5, len(r.Top))] {
		fmt.Fprintf(os.Stderr, "  %-40s ρ=%+.2f сдвиг %+.1fσ\n", t.Feature, t.Rho, t.Shift)
	}
	if r.Stops.GapThrMin > o.gapMin {
		fmt.Fprintf(os.Stderr, "Интервал между циклами ~%.0f мин (похоже на выборку) — порог останова поднят до %.0f мин\n", r.Stops.TypicalGapMin, r.Stops.GapThrMin)
	}
	fmt.Fprintf(os.Stderr, "Остановов: %d. Подозрений на датчики: %d\n", len(r.Stops.Stops), len(r.Sensors))
	seen := map[string]bool{}
	for _, l := range r.Lost {
		if k := analyze.SignalKey(l.Feature); !seen[k] {
			seen[k] = true
			when := "с " + l.Since.Format("02.01 15:04")
			if l.FromStart {
				when = "с начала данных"
			}
			fmt.Fprintf(os.Stderr, "  СИГНАЛ ПРОПАЛ: %-45s ровно 0 %s, соседи работают\n", k, when)
		}
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, strings.SplitN(r.Text, "\n\n", 3)[1]) // строки ПРОБЛЕМА / НАБЛЮДАТЬ / НОРМА
}

// writeAll — все файлы результата в папку out.
func writeAll(r *analyze.Result, out string) error {
	const tf = "2006-01-02 15:04:05"
	prod := r.Prod
	files := []struct {
		name   string
		header []string
		rows   func(row func(...string))
	}{
		{"norm.csv", []string{"feature", "kind", "median", "sigma", "p1", "p5", "p50", "p95", "p99"}, func(row func(...string)) {
			for _, n := range r.Norms {
				row(n.Feature, n.Kind, num(n.Median), num(n.Sigma), num(n.Q[0]), num(n.Q[1]), num(n.Q[2]), num(n.Q[3]), num(n.Q[4]))
			}
		}},
		{"anomaly_cycles.csv", []string{"file", "t_start", "maxz", "score", "anomaly"}, func(row func(...string)) {
			for i := range prod.Len() {
				row(prod.File[i], prod.Times[i].Format(tf), num(r.Anom.MaxZ[i]), num(r.Anom.Score[i]), strconv.FormatBool(r.Anom.Flag[i]))
			}
		}},
		{"anomalies.csv", []string{"время", "файл", "score", "что не так"}, func(row func(...string)) {
			for k, i := range r.Order {
				var parts []string
				for _, x := range r.Reasons[k] {
					parts = append(parts, fmt.Sprintf("%s: %.4g (соседние циклы %.4g, z=%+.1f)", analyze.Human(x.Feature), x.Value, x.Rm, x.Z))
				}
				row(prod.Times[i].Format(tf), baseName(prod.File[i]), strconv.FormatFloat(r.Anom.Score[i], 'f', 1, 64), strings.Join(parts, "; "))
			}
		}},
		{"anomaly_reasons.csv", []string{"file", "feature", "value", "rm", "z"}, func(row func(...string)) {
			for k, i := range r.Order {
				for _, x := range r.Reasons[k] {
					row(prod.File[i], x.Feature, num(x.Value), num(x.Rm), num(x.Z))
				}
			}
		}},
		{"culprits.csv", []string{"feature", "cycles"}, func(row func(...string)) {
			for _, c := range r.Anom.Culprits {
				row(c.Feature, strconv.Itoa(c.Cycles))
			}
		}},
		{"trends.csv", trendHeader, trendRows(r.Trends)},
		{"trends_top.csv", trendHeader, trendRows(r.Top)},
		{"stops.csv", []string{"останов с", "пуск", "простой, ч", "что отклонялось за 50 циклов до"}, func(row func(...string)) {
			for _, s := range r.Stops.Stops {
				var parts []string
				for _, x := range s.Before {
					parts = append(parts, fmt.Sprintf("%s z=%+.1f", analyze.Human(x.Feature), x.Z))
				}
				what := strings.Join(parts, "; ")
				if what == "" {
					what = "ничего заметного"
				}
				row(s.From.Format(tf), s.Resume.Format(tf), num(s.Hours), what)
			}
		}},
		{"stop_precursors.csv", []string{"feature", "описание", "остановов"}, func(row func(...string)) {
			for _, c := range r.Stops.Precursors {
				row(c.Feature, analyze.Human(c.Feature), strconv.Itoa(c.Cycles))
			}
		}},
		{"sensors.csv", []string{"feature", "сигнал", "проблема", "база", "сейчас"}, func(row func(...string)) {
			for _, s := range r.Sensors {
				row(s.Feature, s.Signal, s.Problem, num(s.Base), num(s.Now))
			}
		}},
		{"signal_lost.csv", []string{"feature", "сигнал", "нулей сейчас, %", "у соседей, %", "ноль с", "соседи"}, func(row func(...string)) {
			for _, l := range r.Lost {
				since := l.Since.Format(tf)
				if l.FromStart {
					since = "с начала данных (" + since + ")"
				}
				row(l.Feature, analyze.Human(l.Feature), strconv.FormatFloat(100*l.ZeroShare, 'f', 0, 64),
					strconv.FormatFloat(100*l.PeerShare, 'f', 0, 64), since, strings.Join(l.Peers, " "))
			}
		}},
	}
	for _, f := range files {
		if err := writeCSV(filepath.Join(out, f.name), f.header, f.rows); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(out, "diagnosis.txt"), []byte(r.Text), 0o644)
}

var trendHeader = []string{"feature", "rho", "shift_sigma", "score", "подсистема", "описание", "смысл роста"}

func trendRows(list []analyze.Trend) func(row func(...string)) {
	return func(row func(...string)) {
		for _, t := range list {
			row(t.Feature, num(t.Rho), num(t.Shift), num(t.Score),
				analyze.Subsystem(t.Feature), analyze.Human(t.Feature), analyze.Descs[t.Feature].Why)
		}
	}
}

// writeCSV пишет таблицу в UTF-8 с BOM (как to_csv(encoding="utf-8-sig") в Python),
// чтобы Excel открывал русские заголовки без «кракозябр». Запятые в тексте экранирует encoding/csv.
func writeCSV(path string, header []string, rows func(row func(...string))) error {
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	bw := bufio.NewWriter(fh)
	bw.WriteString("\ufeff")
	w := csv.NewWriter(bw)
	w.Write(header)
	rows(func(v ...string) { w.Write(v) })
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return bw.Flush()
}

// runDaily — отчёты за сутки (daily.py): последние, заданные или все.
func runDaily(r *analyze.Result, day, dir string) error {
	days := analyze.Days(r.All)
	var todo []time.Time
	switch day {
	case "all":
		todo = days
	case "last":
		todo = days[len(days)-1:]
	default:
		t, err := time.Parse("2006-01-02", day)
		if err != nil {
			return fmt.Errorf("--day: ждали last, all или ГГГГ-ММ-ДД, получили %q", day)
		}
		todo = []time.Time{t}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, t := range todo {
		dr, err := r.Daily(t)
		if err != nil {
			fmt.Fprintln(os.Stderr, "сутки:", err)
			continue
		}
		b := analyze.DayFileBase(dr.Day, r.Opts.Op)
		if err := os.WriteFile(filepath.Join(dir, "diagnosis_"+b+".txt"), []byte(dr.Text), 0o644); err != nil {
			return err
		}
		err = writeCSV(filepath.Join(dir, "day_anomalies_"+b+".csv"), []string{"время", "файл", "z", "что не так"},
			func(row func(...string)) {
				for _, a := range dr.Anomalies {
					row(a.Time.Format("15:04:05"), a.File, fmt.Sprintf("%.0f", a.Z), a.What)
				}
			})
		if err != nil {
			return err
		}
		err = writeCSV(filepath.Join(dir, "day_vs_norm_"+b+".csv"), []string{"", "показатель", "за сутки (медиана)", "норма (медиана)", "норма 5…95%", "z"},
			func(row func(...string)) {
				for _, x := range dr.Rows {
					row(x.Mark, x.Name, x.Value, x.Median, x.Range, x.Z)
				}
			})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Сутки %s: %s\n", dr.Day.Format("02.01.2006"), strings.SplitN(dr.Text, "\n", 2)[0])
	}
	return nil
}

// num — число без потери точности; NaN печатается как "NaN", pandas читает его как пропуск.
func num(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// baseName — имя файла без папки; пути из таблицы бывают и с \\, и с /.
func baseName(p string) string { return p[strings.LastIndexAny(p, `\/`)+1:] }

// reorder переносит путь к файлу в конец, чтобы флаги можно было писать и после него.
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			pos = append(pos, args[i])
			continue
		}
		flags = append(flags, args[i])
		if !strings.Contains(args[i], "=") && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, pos...)
}
