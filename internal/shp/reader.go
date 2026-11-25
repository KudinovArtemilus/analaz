// Package shp читает файлы самописца пресса (.shp).
//
// Формат (разобран вручную, см. Py_analiz/shpreader.py):
//
//	0x08  uint64  время старта, Windows FILETIME (UTC, шаги по 100 нс с 1601 г.)
//	0x10  uint32  число дискретных каналов (обычно 256)
//	0x2c  uint32  число аналоговых каналов (обычно 307)
//	0x37c         имена каналов: блоки по 0x100 байт, имя до 0x84 байт, latin1, \0 в конце
//	далее         записи фиксированного размера, по одной каждые 10 мс:
//	              счётчик uint32 | биты дискретных (младший бит первым) | float32 × nAna
//
// Все числа little-endian.
package shp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/netio"
)

const (
	offFileTime = 0x08
	offNumDig   = 0x10
	offNumAna   = 0x2c
	nameStart   = 0x37c
	nameStep    = 0x100
	nameMaxLen  = 0x84
	headerLen   = 0x38

	maxChannels = 10000 // защита от мусорного заголовка
)

// ErrTooShort возвращается, если файл короче заголовка.
var ErrTooShort = errors.New("shp: файл слишком короткий")

// File — содержимое одного .shp файла (один цикл пресса).
//
// Данные хранятся по каналам (column-major): Ana[канал][отсчёт].
// Признаки почти всегда считаются по одному каналу целиком,
// поэтому так удобнее и быстрее, чем по строкам.
type File struct {
	Path     string
	Header   []byte    // первые 0x38 байт — как header в Python
	FileTime time.Time // время из заголовка (UTC); zero, если поле пустое
	DigNames []string
	AnaNames []string
	Index    []uint32    // счётчик записи
	Dig      [][]bool    // Dig[канал][отсчёт]
	Ana      [][]float32 // Ana[канал][отсчёт]

	// Damage — повреждения записей (съехавшие записи, обрыв, пропуски); nil у исправного файла.
	// Испорченные куски в Index/Dig/Ana не попадают — см. integrity.go.
	Damage *Damage

	anaIdx map[string]int
	digIdx map[string]int
	press5 bool // файл пресса 5: его каналы доступны и под именами пресса 10 (press5.go)
}

// NumSamples — число записей (отсчётов) в файле.
func (f *File) NumSamples() int { return len(f.Index) }

// Step — шаг записи по счётчику Index (счётчик — миллисекунды от старта записи).
// Берётся медиана разностей соседних отсчётов: один сбойный отсчёт её не сдвинет.
//
// Внимание: в Python-версии шаг зашит константой DT = 0.01 (10 мс),
// а по счётчику в файлах ноября получается 5 мс. Шаг не угадываем — читаем из файла.
func (f *File) Step() time.Duration {
	n := len(f.Index)
	if n < 2 {
		return 0
	}
	d := make([]uint32, 0, n-1)
	for i := 1; i < n; i++ {
		d = append(d, f.Index[i]-f.Index[i-1])
	}
	slices.Sort(d)
	return time.Duration(d[len(d)/2]) * time.Millisecond
}

// Duration — длительность цикла по счётчику: от первого до последнего отсчёта плюс один шаг.
func (f *File) Duration() time.Duration {
	n := len(f.Index)
	if n < 2 {
		return 0
	}
	return time.Duration(f.Index[n-1]-f.Index[0])*time.Millisecond + f.Step()
}

// Times — время каждого отсчёта в секундах от начала записи (как Time() в shpscan).
func (f *File) Times() []float64 {
	t := make([]float64, len(f.Index))
	for i, v := range f.Index {
		t[i] = float64(v-f.Index[0]) / 1000
	}
	return t
}

// Read читает и разбирает файл с диска.
func Read(path string) (*File, error) {
	data, err := netio.ReadFile(path) // с сетевой папки — не быстрее заданной скорости (не забить линию машины)
	if err != nil {
		return nil, err
	}
	f, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	f.Path = path
	return f, nil
}

// Parse разбирает содержимое .shp, уже загруженное в память.
func Parse(d []byte) (*File, error) {
	if len(d) < nameStart {
		return nil, ErrTooShort
	}
	le := binary.LittleEndian
	nDig := int(le.Uint32(d[offNumDig:]))
	nAna := int(le.Uint32(d[offNumAna:]))
	nCh := nDig + nAna

	if nDig%8 != 0 {
		return nil, fmt.Errorf("shp: число дискретных каналов %d не кратно 8", nDig)
	}
	if nCh == 0 || nCh > maxChannels {
		return nil, fmt.Errorf("shp: подозрительное число каналов в заголовке: дискретных %d, аналоговых %d", nDig, nAna)
	}
	start := nameStart + nCh*nameStep // здесь начинаются записи
	if start > len(d) {
		return nil, fmt.Errorf("shp: файл обрывается раньше, чем кончаются имена каналов (по заголовку каналов: %d)", nCh)
	}

	f := &File{
		Header:   append([]byte(nil), d[:headerLen]...), // копия, чтобы не держать весь файл
		FileTime: fileTime(le.Uint64(d[offFileTime:])),
		DigNames: make([]string, nDig),
		AnaNames: make([]string, nAna),
		anaIdx:   make(map[string]int, nAna),
		digIdx:   make(map[string]int, nDig),
	}

	// ---- имена каналов ----
	for i := 0; i < nCh; i++ {
		o := nameStart + i*nameStep
		name := latin1(d[o : o+nameMaxLen])
		if i < nDig {
			f.DigNames[i] = name
			if _, ok := f.digIdx[name]; !ok { // как setdefault в Python: побеждает первый
				f.digIdx[name] = i
			}
		} else {
			f.AnaNames[i-nDig] = name
			if _, ok := f.anaIdx[name]; !ok {
				f.anaIdx[name] = i - nDig
			}
		}
	}

	f.addAliases()

	// ---- записи ----
	digBytes := nDig / 8
	recLen := 4 + digBytes + 4*nAna
	// Смещение каждой исправной записи. В целом файле это просто start + s*recLen,
	// но если записи где-то съехали, испорченный кусок будет пропущен (integrity.go).
	offs, dmg := recordOffsets(d, start, recLen)
	f.Damage = dmg
	n := len(offs)

	f.Index = make([]uint32, n)
	f.Dig = make([][]bool, nDig)
	for ch := range f.Dig {
		f.Dig[ch] = make([]bool, n)
	}
	f.Ana = make([][]float32, nAna)
	for ch := range f.Ana {
		f.Ana[ch] = make([]float32, n)
	}

	// Файл хранит данные по строкам (запись = все каналы в момент времени), а нам нужны
	// столбцы (канал = все моменты). Наивная перекладка «строка за строкой» на каждой записи
	// пишет в 563 разных массива — процессор всё время промахивается мимо кэша.
	// Поэтому идём блоками по 256 записей (~320 КБ — помещается в кэш L2):
	// внутри блока — канал за каналом, и запись в каждый массив идёт подряд.
	const block = 256
	for s0 := 0; s0 < n; s0 += block {
		s1 := min(s0+block, n)
		for s := s0; s < s1; s++ {
			f.Index[s] = le.Uint32(d[offs[s]:])
		}
		for ch := 0; ch < nDig; ch++ {
			// бит ch лежит в байте ch/8, позиция ch%8, младший бит первым
			off, shift, col := 4+ch>>3, ch&7, f.Dig[ch]
			for s := s0; s < s1; s++ {
				col[s] = d[offs[s]+off]>>shift&1 == 1
			}
		}
		for ch := 0; ch < nAna; ch++ {
			off, col := 4+digBytes+4*ch, f.Ana[ch]
			for s := s0; s < s1; s++ {
				col[s] = math.Float32frombits(le.Uint32(d[offs[s]+off:]))
			}
		}
	}
	return f, nil
}

// Analog возвращает аналоговый канал по имени как float64,
// заменяя мусор (Inf, NaN, |x| >= 1e12) на NaN — как ch() в extract_features.py.
// Каждый вызов возвращает новую копию: её можно спокойно менять.
func (f *File) Analog(name string) ([]float64, bool) {
	j, ok := f.anaIdx[name]
	if !ok {
		return nil, false
	}
	return Clean(f.Ana[j]), true
}

// AnalogIndex — номер аналогового канала по имени (для ссылок «признак → канал на графике»).
func (f *File) AnalogIndex(name string) (int, bool) {
	j, ok := f.anaIdx[name]
	return j, ok
}

// Clean переводит канал в float64 и заменяет мусор (Inf, NaN, |x| >= 1e12) на NaN.
func Clean(src []float32) []float64 {
	out := make([]float64, len(src))
	for i, v := range src {
		x := float64(v)
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) >= 1e12 {
			x = math.NaN()
		}
		out[i] = x
	}
	return out
}

// Digital возвращает дискретный канал по имени.
func (f *File) Digital(name string) ([]bool, bool) {
	j, ok := f.digIdx[name]
	if !ok {
		return nil, false
	}
	return f.Dig[j], true
}

// latin1 обрезает строку по первому \0, переводит latin1 → UTF-8 и убирает пробелы по краям.
func latin1(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c) // в latin1 байт == код символа Unicode
	}
	return strings.TrimSpace(string(r))
}

// fileTime переводит Windows FILETIME (100 нс с 01.01.1601 UTC) в time.Time.
// Через time.Duration считать нельзя: он переполняется после ~292 лет, а с 1601 г. прошло больше.
func fileTime(ft uint64) time.Time {
	if ft == 0 {
		return time.Time{}
	}
	const ticks1601to1970 = 116444736000000000 // 100-нс тиков между 1601 и 1970
	unix100ns := int64(ft) - ticks1601to1970
	return time.Unix(0, unix100ns*100).UTC()
}

// HeaderTime — время начала записи из заголовка (UTC), без разбора остального файла.
// Нужно, когда в имени файла времени нет (кольцевой буфер регистратора: actual.shp, old01.shp…).
func HeaderTime(d []byte) (time.Time, error) {
	if len(d) < offFileTime+8 {
		return time.Time{}, ErrTooShort
	}
	t := fileTime(binary.LittleEndian.Uint64(d[offFileTime:]))
	if t.IsZero() {
		return t, errors.New("shp: в заголовке нет времени")
	}
	return t, nil
}
