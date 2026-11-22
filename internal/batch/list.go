// Package batch — обработка папок с .shp: поиск файлов и таблица признаков.
package batch

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	// дата и время в конце имени: ..._20261118_045619.shp
	reNameDT = regexp.MustCompile(`(?i)_(\d{8})_(\d{6})\.shp$`)
	// путь кончается на ГГГГ\ММ\ДД (или ГГГГ\ММ)
	reDayDir = regexp.MustCompile(`(\d{4})[\\/](\d{2})(?:[\\/](\d{2}))?$`)
)

// Filter — какие файлы брать. Пустые поля — без ограничения.
type Filter struct {
	From, To string // ГГГГ-ММ-ДД, включительно
	Op       string // "OP1"
}

// ListFiles ищет .shp рекурсивно (в т.ч. D:\press\ГГГГ\ММ\ДД\*.shp) и сортирует по времени из имени —
// как list_files() в extract_features.py. Папки дней вне диапазона дат пропускаются целиком,
// не заходя внутрь: на 190 ГБ это экономит минуты обхода диска.
func ListFiles(root string, flt Filter) ([]string, error) {
	lo := strings.ReplaceAll(flt.From, "-", "")
	hi := strings.ReplaceAll(flt.To, "-", "")
	opTag := ""
	if flt.Op != "" {
		opTag = "_" + strings.ToUpper(flt.Op) + "_"
	}
	outOfRange := func(day string) bool {
		return (lo != "" && day < lo) || (hi != "" && day > hi)
	}

	type item struct{ key, path string }
	var items []item

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // папка недоступна — сообщаем, а не молчим
		}
		if d.IsDir() {
			if m := reDayDir.FindStringSubmatch(path); m != nil && m[3] != "" && (lo != "" || hi != "") {
				if outOfRange(m[1] + m[2] + m[3]) {
					return filepath.SkipDir // весь день вне диапазона — внутрь не идём
				}
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".shp") {
			return nil
		}
		if opTag != "" && !strings.Contains(strings.ToUpper(name), opTag) {
			return nil
		}
		key := name
		if m := reNameDT.FindStringSubmatch(name); m != nil {
			if outOfRange(m[1]) {
				return nil
			}
			key = m[1] + m[2] // ГГГГММДДЧЧММСС — сортируется как строка
		}
		items = append(items, item{key, path})
		return nil
	})
	if err != nil {
		return nil, err
	}
	// как sorted(out) в Python: по ключу, при равных ключах — по пути
	sort.Slice(items, func(i, j int) bool {
		if items[i].key != items[j].key {
			return items[i].key < items[j].key
		}
		return items[i].path < items[j].path
	})
	paths := make([]string, len(items))
	for i, it := range items {
		paths[i] = it.path
	}
	return paths, nil
}
