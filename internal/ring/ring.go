// Package ring — захват кольцевого буфера регистратора.
//
// Некоторые регистраторы (пресс 5, папка Datalogger) пишут не «файл на цикл с временем в имени»,
// а кольцо: текущий цикл — actual.shp, готовые — old01.shp (самый свежий), old02.shp…, и с каждым
// новым циклом все имена сдвигаются на один, а самый старый затирается. Кольцо держит всего
// несколько часов работы, и одно имя через полторы минуты — уже другой цикл.
//
// Поэтому каждый новый цикл кольца копируется к себе под постоянным именем — с операцией и временем
// начала из заголовка файла (ring_OP4_20261120_174220.shp), как у регистраторов «файл на цикл».
// Дальше признаки, архив и анализ работают с копией как с обычной папкой.
//
// Источник только читается: ничего в нём не переименовывается и не удаляется. Чтобы не мешать
// регистратору сдвигать имена, каждый файл читается целиком одним открытием и только один раз:
// уже виденный файл узнаётся по времени изменения и размеру — при сдвиге они не меняются, меняется
// лишь имя, так что за проход читается только новый old01.
package ring

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// Prefix — начало имени копии; по нему же чистка узнаёт свои файлы и не трогает чужие.
const Prefix = "ring_"

// Capture — захват одной папки-кольца src в папку dst.
type Capture struct {
	Src, Dst string
	Op       string         // операция для имени копии: OP4
	Loc      *time.Location // пояс времени в имени копии (как у других регистраторов — заводское)

	seen map[seenKey]bool
}

type seenKey struct {
	mod  int64
	size int64
}

// Result — итог прохода.
type Result struct {
	New, Seen int      // скопировано новых; уже были
	Errors    []string // файлы, которые не прочитались
}

// IsRingName — имя кольца: без времени в имени (old01.shp). actual.shp — тоже кольцо,
// но его регистратор ещё пишет — см. Skip.
func IsRingName(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".shp") && shp.ParseName(name, time.UTC).Time.IsZero()
}

// Skip — файл, который пишется прямо сейчас: читать его нельзя, он ещё не цикл.
func Skip(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "actual")
}

// Name — имя копии: ring_OP4_20261120_174220.shp (время — в поясе loc).
func Name(op string, start time.Time, loc *time.Location) string {
	return Prefix + strings.ToUpper(op) + "_" + start.In(loc).Format("20060102_150405") + ".shp"
}

// Path — где лежит копия: dst/2026/11/20/ring_OP4_….shp (по дням, как у регистратора пресса 10).
func Path(dst, op string, start time.Time, loc *time.Location) string {
	t := start.In(loc)
	return filepath.Join(dst, t.Format("2006"), t.Format("01"), t.Format("02"), Name(op, start, loc))
}

// Run — один проход: скопировать новые циклы кольца. Свежие — первыми: если проход прервётся,
// потеряется самое старое, которое и так вот-вот затрётся.
func (c *Capture) Run() (Result, error) {
	var res Result
	if c.seen == nil {
		c.seen = map[seenKey]bool{}
	}
	loc := c.Loc
	if loc == nil {
		loc = time.UTC
	}
	ents, err := os.ReadDir(c.Src)
	if err != nil {
		return res, err
	}
	type cand struct {
		name string
		key  seenKey
	}
	var cs []cand
	for _, e := range ents {
		if e.IsDir() || !IsRingName(e.Name()) || Skip(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // файл успели сдвинуть или затереть — в следующий проход
		}
		k := seenKey{info.ModTime().UnixNano(), info.Size()}
		if c.seen[k] {
			res.Seen++
			continue
		}
		cs = append(cs, cand{e.Name(), k})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].key.mod > cs[j].key.mod })
	for _, x := range cs {
		ok, err := c.copyOne(filepath.Join(c.Src, x.name), loc)
		switch {
		case err != nil:
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", x.name, err))
			continue // не помечаем виденным: попробуем ещё раз (если файл ещё в кольце)
		case ok:
			res.New++
		default:
			res.Seen++
		}
		c.seen[x.key] = true
	}
	return res, nil
}

// copyOne — прочитать файл кольца одним открытием и сохранить копию, если её ещё нет.
// false без ошибки — такая копия уже есть (тот же цикл под другим именем или после перезапуска).
func (c *Capture) copyOne(src string, loc *time.Location) (bool, error) {
	d, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	start, err := shp.HeaderTime(d)
	if err != nil {
		return false, err
	}
	dst := Path(c.Dst, c.Op, start, loc)
	if _, err := os.Stat(dst); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, d, 0o644); err != nil {
		os.Remove(tmp)
		return false, err
	}
	// время изменения копии — когда скопировали (не как в кольце): по нему чистка считает срок
	// хранения, иначе давно записанное кольцо (регистратор стоял) удалилось бы сразу после копии,
	// раньше, чем по нему посчитают признаки и отберут в архив
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// Prune — удалить копии, сделанные раньше, чем keep назад (по времени изменения копии — это время
// копирования). Удаляются только свои файлы (ring_*.shp) в папке dst; пустые папки дней — тоже.
// Возвращает, сколько файлов удалено.
func Prune(dst string, keep time.Duration, now time.Time) (int, error) {
	n := 0
	err := filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, Prefix) || !strings.EqualFold(filepath.Ext(name), ".shp") {
			return nil
		}
		info, err := d.Info()
		if err != nil || now.Sub(info.ModTime()) <= keep {
			return nil
		}
		if os.Remove(p) == nil {
			n++
		}
		return nil
	})
	removeEmptyDirs(dst)
	return n, err
}

func removeEmptyDirs(root string) {
	var dirs []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- { // сначала самые глубокие
		os.Remove(dirs[i]) // непустую папку Remove не удалит
	}
}
