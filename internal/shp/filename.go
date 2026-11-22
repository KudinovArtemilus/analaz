package shp

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Name — то, что можно узнать из имени файла, не открывая его.
//
//	26.176613.067890_OP1_20261118_045619.shp
//	│  │      │      │   └─ дата и время, МЕСТНОЕ (не UTC!)
//	│  │      │      └─ операция
//	│  │      └─ номер плавки
//	│  └─ номер заготовки
//	└─ префикс (похоже на год: 26)
type Name struct {
	Op     string    // "OP1"; пусто, если в имени нет
	Prefix int       // 26
	Piece  int       // номер заготовки
	Heat   int       // номер плавки
	Time   time.Time // время из имени; zero, если в имени нет
	HasIDs bool      // удалось ли разобрать префикс.заготовка.плавка
}

var (
	reIDs  = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)_`)
	reOp   = regexp.MustCompile(`(OP\d+)`)
	reTime = regexp.MustCompile(`(\d{8})_(\d{6})`)
)

// ParseName разбирает имя файла (путь тоже можно — берётся только имя).
// loc — часовой пояс, в котором записано время в имени (на заводе UTC+5).
// Ничего не найдено — это не ошибка: поля просто останутся пустыми, как в Python.
func ParseName(path string, loc *time.Location) Name {
	if loc == nil {
		loc = time.UTC // nil — «пояс не важен»; ParseInLocation с nil упал бы
	}
	// Не filepath.Base: он режет путь по правилам ТЕКУЩЕЙ ОС, и на Linux
	// путь D:\press\...\файл.shp целиком остался бы «именем». Режем по обоим слэшам.
	base := path
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		base = path[i+1:]
	}
	var n Name

	if m := reIDs.FindStringSubmatch(base); m != nil {
		// Atoi не упадёт: регулярка пропускает только цифры
		n.Prefix, _ = strconv.Atoi(m[1])
		n.Piece, _ = strconv.Atoi(m[2])
		n.Heat, _ = strconv.Atoi(m[3]) // "067890" → 67890, как int() в Python
		n.HasIDs = true
	}
	if m := reOp.FindStringSubmatch(base); m != nil {
		n.Op = m[1]
	}
	if m := reTime.FindStringSubmatch(base); m != nil {
		// Раскладка времени в Go — это "образец" 2006-01-02 15:04:05, а не %Y%m%d.
		if t, err := time.ParseInLocation("20060102150405", m[1]+m[2], loc); err == nil {
			n.Time = t
		}
	}
	return n
}

// StartTime — время начала цикла по тому же правилу, что file_time() в Python:
// сначала из имени файла, если его там нет — из заголовка.
func StartTime(f *File, loc *time.Location) time.Time {
	if n := ParseName(f.Path, loc); !n.Time.IsZero() {
		return n.Time
	}
	return f.FileTime.In(loc)
}
