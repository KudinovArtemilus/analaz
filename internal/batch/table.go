package batch

import (
	"compress/gzip"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/KudinovArtemilus/press-go/internal/features"
)

// Служебные текстовые колонки — как в Python: file, op, t_start. Остальные — числа.
var textCols = []string{"file", "op", "t_start"}

// Table — таблица признаков: строка = цикл, колонка = признак.
//
// Колонки регистрируются по мере появления: у разных циклов набор признаков может
// отличаться (нет канала — нет признака). Недостающее значение = NaN, в CSV — пустая ячейка,
// pandas читает её как NaN.
type Table struct {
	cols []string       // числовые колонки по порядку появления
	idx  map[string]int // имя → номер колонки
	rows []row

	byName map[string]int // имя файла → номер строки; строится при первом Set, nil — не построен
}

type row struct {
	text [3]string // file, op, t_start
	v    []float64 // может быть короче cols — хвост считается NaN
}

// NewTable — пустая таблица.
func NewTable() *Table { return &Table{idx: map[string]int{}} }

// Len — число строк.
func (t *Table) Len() int { return len(t.rows) }

// Files — множество уже обработанных файлов (для продолжения с места остановки).
func (t *Table) Files() map[string]bool {
	m := make(map[string]bool, len(t.rows))
	for _, r := range t.rows {
		m[r.text[0]] = true
	}
	return m
}

// Add добавляет цикл.
func (t *Table) Add(file, op, tStart string, f features.Features) {
	// Ключи сортируем: порядок обхода map в Go случайный, а порядок колонок
	// в файле должен быть одинаковым от запуска к запуску.
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	r := row{text: [3]string{file, op, tStart}}
	for _, k := range keys {
		j := t.col(k)
		for len(r.v) <= j {
			r.v = append(r.v, math.NaN())
		}
		r.v[j] = f[k]
	}
	t.rows = append(t.rows, r)
	if t.byName != nil {
		t.byName[FileName(file)] = len(t.rows) - 1
	}
}

// Set — как Add, но если цикл с тем же файлом (по имени) уже есть — строка заменяется целиком:
// так пересчитываются циклы со старой версией признаков, без дублей в таблице.
func (t *Table) Set(file, op, tStart string, f features.Features) {
	if t.byName == nil {
		t.byName = make(map[string]int, len(t.rows))
		for i, r := range t.rows {
			t.byName[FileName(r.text[0])] = i
		}
	}
	name := FileName(file)
	i, had := t.byName[name]
	n := len(t.rows)
	t.Add(file, op, tStart, f) // новая строка — в конец (Add сам запишет её в индекс)
	if had {
		t.rows[i] = t.rows[n] // …и на место старой
		t.rows = t.rows[:n]
		t.byName[name] = i
	}
}

// Stale — циклы (по имени файла), у которых версия признаков меньше version.
// Нет колонки feat_ver или пусто — версия 0.
func (t *Table) Stale(version int) map[string]bool {
	out := map[string]bool{}
	j, ok := t.idx["feat_ver"]
	for _, r := range t.rows {
		v := 0.0
		if ok && j < len(r.v) && !math.IsNaN(r.v[j]) {
			v = r.v[j]
		}
		if v < float64(version) {
			out[FileName(r.text[0])] = true
		}
	}
	return out
}

func (t *Table) col(name string) int {
	if j, ok := t.idx[name]; ok {
		return j
	}
	t.idx[name] = len(t.cols)
	t.cols = append(t.cols, name)
	return len(t.cols) - 1
}

// WriteCSVGz записывает таблицу. Сначала во временный файл, потом переименование:
// если программу прервут посреди записи, старый файл останется целым.
// (Python пишет прямо в итоговый файл — прерывание там может испортить все признаки.)
func (t *Table) WriteCSVGz(path string) error {
	tmp := path + ".tmp"
	fh, err := os.Create(tmp)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(fh)
	w := csv.NewWriter(gz)

	head := append(append([]string{}, textCols...), t.cols...)
	if err := w.Write(head); err != nil {
		fh.Close()
		return err
	}
	rec := make([]string, len(head))
	for _, r := range t.rows {
		copy(rec, r.text[:])
		for j := range t.cols {
			v := math.NaN()
			if j < len(r.v) {
				v = r.v[j]
			}
			rec[3+j] = formatFloat(v)
		}
		if err := w.Write(rec); err != nil {
			fh.Close()
			return err
		}
	}
	w.Flush()
	// ошибки закрытия тоже важны: gzip дописывает хвост именно в Close
	if err := errors.Join(w.Error(), gz.Close(), fh.Close()); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// formatFloat — кратчайшая запись, которая читается обратно в то же самое число.
func formatFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return ""
	case math.IsInf(v, 1):
		return "inf"
	case math.IsInf(v, -1):
		return "-inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// ErrOldStep — файл признаков посчитан старой версией (шаг 10 мс, нет колонки dt_ms).
var ErrOldStep = errors.New("в файле признаков нет колонки dt_ms — он посчитан старой версией с шагом 10 мс")

// ReadCSVGz читает таблицу, записанную WriteCSVGz или pandas (df.to_csv(... .csv.gz)).
func ReadCSVGz(path string) (*Table, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	gz, err := gzip.NewReader(fh)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	r := csv.NewReader(gz)
	r.ReuseRecord = true

	head, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%s: заголовок: %w", path, err)
	}
	if !slices.Contains(head, "dt_ms") {
		return nil, ErrOldStep
	}
	t := NewTable()
	textPos := [3]int{-1, -1, -1}
	numPos := make([]int, len(head)) // номер колонки в таблице или -1 для текстовой
	for i, h := range head {
		if k := slices.Index(textCols, h); k >= 0 {
			textPos[k] = i
			numPos[i] = -1
		} else {
			numPos[i] = t.col(h)
		}
	}
	if textPos[0] < 0 {
		return nil, fmt.Errorf("%s: нет колонки file", path)
	}

	for line := 2; ; line++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s, строка %d: %w", path, line, err)
		}
		var rw row
		for k, p := range textPos {
			if p >= 0 {
				rw.text[k] = rec[p]
			}
		}
		rw.v = make([]float64, len(t.cols))
		for i, s := range rec {
			j := numPos[i]
			if j < 0 {
				continue
			}
			if s == "" {
				rw.v[j] = math.NaN()
				continue
			}
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				// pandas пишет True/False для логических колонок — для анализа это 1/0
				switch s {
				case "True":
					v = 1
				case "False":
					v = 0
				default:
					v = math.NaN()
				}
			}
			rw.v[j] = v
		}
		t.rows = append(t.rows, rw)
	}
	return t, nil
}

// Merge дописывает строки o, файлов которых ещё нет в t. Файл узнаётся по полному пути
// или по имени (имя уникально, а путь к той же папке бывает разным: регистр, подключённый
// диск) — так один цикл не попадёт в таблицу дважды. Колонки сопоставляются по имени:
// у таблиц разный порядок колонок — значения не перепутаются. Возвращает, сколько строк
// добавлено. o не меняется.
func (t *Table) Merge(o *Table) int {
	have := t.Files()
	names := make(map[string]bool, len(have))
	for f := range have {
		names[FileName(f)] = true
	}
	pos := make([]int, len(o.cols)) // колонка o → колонка t
	for j, name := range o.cols {
		pos[j] = t.col(name)
	}
	n := 0
	for _, r := range o.rows {
		if have[r.text[0]] || names[FileName(r.text[0])] {
			continue
		}
		have[r.text[0]], names[FileName(r.text[0])] = true, true
		nr := row{text: r.text, v: make([]float64, len(t.cols))}
		for j := range nr.v {
			nr.v[j] = math.NaN()
		}
		for j, v := range r.v {
			nr.v[pos[j]] = v
		}
		t.rows = append(t.rows, nr)
		if t.byName != nil {
			t.byName[FileName(r.text[0])] = len(t.rows) - 1
		}
		n++
	}
	return n
}

// FileName — имя файла без папок, в нижнем регистре, — ключ «тот же цикл» при разных путях.
// Режем по обоим слэшам: в таблице пути Windows, а тесты идут и на Linux.
func FileName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	return strings.ToLower(p)
}

// ---------- доступ для анализа (этап 3) ----------

// Columns — имена числовых колонок в порядке появления.
func (t *Table) Columns() []string { return t.cols }

// Column — колонка целиком по номеру (новый срез; отсутствующие значения = NaN).
func (t *Table) Column(j int) []float64 {
	out := make([]float64, len(t.rows))
	for i, r := range t.rows {
		if j < len(r.v) {
			out[i] = r.v[j]
		} else {
			out[i] = math.NaN()
		}
	}
	return out
}

// Text — текстовая колонка: 0 = file, 1 = op, 2 = t_start.
func (t *Table) Text(k int) []string {
	out := make([]string, len(t.rows))
	for i, r := range t.rows {
		out[i] = r.text[k]
	}
	return out
}
