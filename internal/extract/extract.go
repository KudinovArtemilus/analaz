// Package extract — дописать в таблицу признаков циклы, которых в ней ещё нет.
// Общий код утилиты extract и сервера (этап 8): утилита запускается руками,
// сервер зовёт то же самое по расписанию.
//
// Таблица только дописывается: строки уже посчитанных циклов остаются, даже когда
// буфер удалил сами .shp, — так копится история. Папки-источники только читаются.
package extract

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/features"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// Result — что получилось по одному файлу.
type Result struct {
	Damage string // описание повреждения записей, если есть
	Path   string // как нашли при обходе
	File   string // абсолютный путь — так он хранится в колонке file
	Op     string
	TStart string
	F      features.Features
	Err    error
}

// ArchivePrefix — пометка в списке todo: файла в папках уже нет, цикл берётся из архива
// (archive:<путь из таблицы>). Нужна для пересчёта старой версии признаков после того,
// как буфер регистратора файл удалил.
const ArchivePrefix = "archive:"

// Process — признаки одного файла. Паника (выход за границы массива на битом файле)
// не должна уронить обработку тысяч файлов: ловим её и превращаем в ошибку этого файла.
func Process(path string, loc *time.Location) (res Result) {
	res.Path = path
	defer func() {
		if p := recover(); p != nil {
			res.Err = fmt.Errorf("%s: паника: %v\n%s", path, p, debug.Stack())
		}
	}()
	f, err := shp.Read(path)
	if err != nil {
		res.Err = err
		return res
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		res.Err = fmt.Errorf("%s: %w", path, err)
		return res
	}
	return compute(res, f, abs, loc)
}

// ProcessData — признаки цикла, прочитанного не из папки (из архива): file — путь цикла
// из таблицы, под ним строка и заменяется.
func ProcessData(file string, data []byte, loc *time.Location) (res Result) {
	res.Path = ArchivePrefix + file
	defer func() {
		if p := recover(); p != nil {
			res.Err = fmt.Errorf("%s: паника: %v\n%s", file, p, debug.Stack())
		}
	}()
	f, err := shp.Parse(data)
	if err != nil {
		res.Err = fmt.Errorf("%s (из архива): %w", file, err)
		return res
	}
	return compute(res, f, file, loc)
}

func compute(res Result, f *shp.File, abs string, loc *time.Location) Result {
	path := res.Path
	f.Path = abs
	res.File = abs
	res.Op = shp.ParseName(abs, loc).Op
	t := shp.StartTime(f, loc)
	if t.Year() < 2000 {
		// ни в имени, ни в заголовке нормального времени нет: без времени цикл
		// не встанет на ось времени и испортит тренды и остановы — лучше честная ошибка
		res.Err = fmt.Errorf("%s: не удалось определить время цикла (в имени и в заголовке)", path)
		return res
	}
	res.TStart = t.Format("2006-01-02 15:04:05")
	res.F = features.Compute(f)
	if f.Damage.Serious() {
		res.Damage = f.Damage.String()
	}
	return res
}

// Options — что и как обрабатывать.
type Options struct {
	Roots  []string     // папки с .shp (обходятся рекурсивно)
	Filter batch.Filter // даты и операция
	Every  int          // каждый N-й файл (1 — все)
	Limit  int          // не больше N файлов за вызов (0 — без предела)
	Jobs   int          // файлов одновременно
	Loc    *time.Location

	// FreshFirst — сначала файлы новее последнего посчитанного (то, что пришло с прошлого раза),
	// потом пропуски в старых (их буфер скоро удалит). Без флага — просто по времени, как Python.
	FreshFirst bool
	// MinAge — не брать файлы моложе этого: регистратор, возможно, ещё пишет их.
	MinAge time.Duration
	// Stale — циклы (по имени файла, batch.FileName), посчитанные старой версией признаков:
	// их надо пересчитать, хоть они и в таблице. Идут в историю (после новых); строка заменяется.
	Stale map[string]bool
	// InArchive и ReadArchive — архив сохранённых циклов (сервер): цикл старой версии признаков,
	// которого в папках уже нет, пересчитывается из архива. nil — архива нет.
	InArchive   func(file string) bool
	ReadArchive func(file string) ([]byte, error)
	// Skip — не брать этот файл (сервер не повторяет недавние ошибки каждые 5 минут).
	Skip func(path string) bool
	// SaveEvery — сохранять таблицу каждые N добавленных циклов (0 — 500).
	SaveEvery int
	// NoSave — Apply таблицу не записывает: запишет вызывающий (сервер пишет её не чаще раза
	// в пару минут: таблица пресса на 10 тыс. циклов — около 85 МБ, запись — десятки секунд).
	NoSave bool
	// Progress — сколько файлов из todo уже обработано (после каждого файла); nil — не звать.
	Progress func(done, total int)
	// Log — сообщения о ходе; nil — молчать. Verbose — ещё и по каждому файлу.
	Log     func(format string, a ...any)
	Verbose bool
}

// Stats — итог вызова.
type Stats struct {
	Found   int      // файлов в папках (после Every)
	Done    int      // уже было в таблице до вызова
	Todo    int      // ещё не посчитано (до Limit)
	Fresh   int      // из них новых (после последнего посчитанного) — в начале списка, если FreshFirst
	Stale   int      // из них — пересчёт старой версии признаков
	Archive int      // из них — из архива (в папках файла уже нет)
	Lost    int      // старой версии, но файла нет ни в папках, ни в архиве: пересчитать нельзя
	Redone  int      // пересчитано (строка заменена) в этот раз
	Taken   int      // взято в работу в этот раз
	Added   int      // добавлено строк
	Damaged int      // из них с повреждёнными записями
	Young   int      // отложено: файл ещё может писаться
	Errors  []Result // файлы с ошибками
	Saves   int      // сколько раз таблица записана
}

// Update дописывает в table циклы из opt.Roots, которых в ней нет, и сохраняет её в outPath
// (каждые SaveEvery циклов и в конце). Если добавить нечего — файл не трогается.
func Update(table *batch.Table, outPath string, opt Options) (Stats, error) {
	todo, st, err := Plan(table.Files(), opt)
	if err != nil || len(todo) == 0 {
		return st, err
	}
	return Apply(table, outPath, todo, st, opt)
}

// Plan — какие файлы посчитать: есть в папках, нет в done (колонка file таблицы).
// Таблица для этого не нужна — сервер держит в памяти только done и читает таблицу,
// лишь когда есть что добавить.
func Plan(done map[string]bool, opt Options) ([]string, Stats, error) {
	var st Stats
	logf := logger(opt)
	every, jobs := max(opt.Every, 1), max(opt.Jobs, 1)
	st.Done = len(done)

	// --- что нужно посчитать ---
	var all []string
	for _, root := range opt.Roots {
		p, err := batch.ListFiles(root, opt.Filter)
		if err != nil {
			return nil, st, err
		}
		all = append(all, p...)
	}
	var files []string
	for i := 0; i < len(all); i += every {
		files = append(files, all[i])
	}
	st.Found = len(files)

	// files отсортированы по времени из имени: «свежие» — после последнего посчитанного
	// «уже посчитан» — по полному пути (как в Python) или по имени файла: имя уникально
	// (заготовка, плавка, операция, дата и время до секунды), а путь к той же папке бывает
	// разным — \\SERVER\share и \\server\share, сетевой путь и подключённый диск. Без этого
	// один цикл попал бы в таблицу дважды под разными путями и исказил анализ.
	doneName := make(map[string]bool, len(done))
	for f := range done {
		doneName[batch.FileName(f)] = true
	}
	lastDone := -1
	isDone := make([]bool, len(files)) // в таблице и пересчитывать не надо
	stale := make([]bool, len(files))  // в таблице, но старой версией — пересчитать
	for i, p := range files {
		if abs, _ := filepath.Abs(p); done[abs] || doneName[batch.FileName(p)] {
			lastDone = i
			if opt.Stale[batch.FileName(p)] {
				stale[i] = true
			} else {
				isDone[i] = true
			}
		}
	}
	// «Молодые» (регистратор, возможно, ещё пишет) бывают только в конце списка: идём от новых
	// к старым и останавливаемся после трёх дописанных подряд. Так stat делается для нескольких
	// файлов, а не для тысяч по сети (у новой таблицы «свежие» — весь буфер). Не на первом
	// дописанном: порядок по имени и порядок записи могут не совпасть (два файла пишутся
	// одновременно), и молодой файл перед дописанным проскочил бы в работу недописанным.
	young := map[int]bool{}
	now := time.Now()
	if opt.MinAge > 0 {
		old := 0
		for i := len(files) - 1; i > lastDone && old < 3; i-- {
			if isDone[i] || stale[i] {
				continue
			}
			fi, err := os.Stat(files[i])
			if err != nil {
				continue
			}
			if now.Sub(fi.ModTime()) < opt.MinAge {
				young[i], old = true, 0
			} else {
				old++
			}
		}
	}
	var fresh, gaps []string
	for i, p := range files {
		if isDone[i] || (opt.Skip != nil && opt.Skip(p)) {
			continue
		}
		if young[i] {
			st.Young++
			continue
		}
		if stale[i] {
			st.Stale++
		}
		if (i > lastDone || !opt.FreshFirst) && !stale[i] {
			fresh = append(fresh, p)
		} else {
			gaps = append(gaps, p) // пропуски истории и пересчёт старой версии — после новых
		}
	}
	// старая версия, а файла в папках уже нет (буфер удалил) — из архива, если цикл там сохранён
	if len(opt.Stale) > 0 {
		inFolders := make(map[string]bool, len(files))
		for _, p := range files {
			inFolders[batch.FileName(p)] = true
		}
		seen := map[string]bool{}
		var arch []string
		for f := range done {
			n := batch.FileName(f)
			if !opt.Stale[n] || inFolders[n] || seen[n] {
				continue
			}
			seen[n] = true
			switch {
			case opt.Skip != nil && opt.Skip(ArchivePrefix+f):
			case opt.InArchive != nil && opt.InArchive(f):
				arch = append(arch, ArchivePrefix+f)
			default:
				st.Lost++
			}
		}
		sort.Strings(arch) // порядок прохода не зависит от порядка map
		st.Stale += len(arch)
		st.Archive = len(arch)
		gaps = append(gaps, arch...)
	}
	todo := append(fresh, gaps...)
	st.Todo, st.Fresh = len(todo), len(fresh)
	if opt.Limit > 0 && len(todo) > opt.Limit {
		todo = todo[:opt.Limit]
	}
	st.Taken = len(todo)
	logf("Найдено файлов: %d, уже обработано: %d, к обработке: %d, потоков: %d\n",
		len(files), len(done), len(todo), jobs)
	return todo, st, nil
}

// Apply — посчитать todo (из Plan) и дописать в table, сохраняя в outPath.
func Apply(table *batch.Table, outPath string, todo []string, st Stats, opt Options) (Stats, error) {
	logf := logger(opt)
	jobs, saveEvery := max(opt.Jobs, 1), opt.SaveEvery
	if saveEvery <= 0 {
		saveEvery = 500
	}

	// --- пул воркеров ---
	// paths: главный → воркеры; results: воркеры → главный.
	// В таблицу пишет только главная горутина, поэтому мьютекс не нужен.
	paths := make(chan string)
	results := make(chan Result)
	for w := 0; w < jobs; w++ {
		go func() {
			for p := range paths {
				results <- processAny(p, opt)
			}
		}()
	}
	go func() {
		for _, p := range todo {
			paths <- p
		}
		close(paths) // воркеры выйдут из цикла, когда файлы кончатся
	}()

	t0 := time.Now()
	sinceSave := 0
	var saveErr error
	for k := 1; k <= len(todo); k++ {
		r := <-results // читаем все результаты до конца, даже после ошибки записи: иначе воркеры зависнут
		if r.Err != nil {
			st.Errors = append(st.Errors, r)
			logf("ОШИБКА: %v\n", r.Err) // путь уже внутри ошибки
		} else {
			table.Set(r.File, r.Op, r.TStart, r.F) // старая версия — заменяется, новый — дописывается
			if opt.Stale[batch.FileName(r.File)] {
				st.Redone++
			} else {
				st.Added++
			}
			sinceSave++
			if r.Damage != "" {
				st.Damaged++
				logf("ПОВРЕЖДЁН: %s: %s\n", r.Path, r.Damage)
			}
			if opt.Verbose {
				logf("[%d/%d] OK  %s\n", k, len(todo), r.Path)
			}
		}
		if opt.Progress != nil {
			opt.Progress(k, len(todo))
		}
		if k%100 == 0 || k == len(todo) {
			rate := float64(k) / time.Since(t0).Seconds()
			logf("--- %d/%d  %.1f файл/с  осталось ~%.0f мин\n", k, len(todo), rate, float64(len(todo)-k)/rate/60)
		}
		if !opt.NoSave && saveErr == nil && sinceSave > 0 && (sinceSave >= saveEvery || k == len(todo)) {
			if saveErr = Save(table, outPath); saveErr == nil {
				st.Saves++
				logf("  сохранено -> %s (%d строк)\n", outPath, table.Len())
				sinceSave = 0
			}
		}
	}
	return st, saveErr
}

// processAny — файл из папки или (archive:…) цикл из архива.
func processAny(p string, opt Options) Result {
	f, ok := strings.CutPrefix(p, ArchivePrefix)
	if !ok {
		return Process(p, opt.Loc)
	}
	if opt.ReadArchive == nil {
		return Result{Path: p, Err: fmt.Errorf("%s: архив недоступен", f)}
	}
	data, err := opt.ReadArchive(f)
	if err != nil {
		return Result{Path: p, Err: fmt.Errorf("%s (из архива): %w", f, err)}
	}
	return ProcessData(f, data, opt.Loc)
}

func logger(opt Options) func(string, ...any) {
	if opt.Log == nil {
		return func(string, ...any) {}
	}
	return opt.Log
}

// Save — запись таблицы с повторами. WriteCSVGz пишет во временный файл и переименовывает;
// в Windows переименование не проходит, пока файл открыт на чтение (сервер как раз
// пересчитывает анализ по нему) — это секунды, поэтому просто пробуем ещё.
func Save(t *batch.Table, path string) error {
	var err error
	for try := 0; try < 10; try++ {
		if err = t.WriteCSVGz(path); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return err
}
