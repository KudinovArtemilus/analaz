// Package archive — свой архив сырых .shp (этап 8, первая часть).
//
// Буфер регистратора (BufferShp) хранит около двух недель и сам удаляет старое. Всё подряд
// копировать нельзя: ~14 ГБ в сутки. Поэтому сохраняем только то, что пригодится потом:
// аномальные циклы, циклы перед остановами и после пуска, повреждённые файлы, начало
// «отрезков вне нормы» и нового рецепта, и каждый N-й цикл как фон для сравнения формы.
//
// Правила, которые здесь соблюдаются:
//   - источник только читается: ничего не переносится, не переименовывается, не удаляется;
//   - запись атомарная: сначала «имя.tmp», проверка, потом переименование — оборванная
//     запись (выключили свет) не оставит битый архивный файл;
//   - после записи файл распаковывается и сверяется SHA-256 с исходником: сжатая копия,
//     которая не совпадает с оригиналом, хуже, чем никакой;
//   - архив сам ничего не удаляет; когда он дорос до предела — новые файлы не пишутся,
//     а в журнал и в «Настройки» выводится предупреждение.
package archive

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/netio"
)

// Archive — папка архива: <Root>/<путь как в буфере>.gz и журнал index.jsonl.
type Archive struct {
	Root     string
	MaxBytes int64 // 0 — без предела

	mu    sync.Mutex
	used  int64 // занято, байт (считается при Open и растёт при записи)
	known map[string]bool
}

// Entry — строка журнала index.jsonl: что сохранено и почему.
type Entry struct {
	Rel     string    `json:"rel"`     // путь относительно буфера: 2026/11/19/x.shp
	Source  string    `json:"source"`  // откуда скопирован
	Op      string    `json:"op"`      // операция
	Reasons []string  `json:"reasons"` // почему сохранён
	Size    int64     `json:"size"`    // исходный размер
	Gz      int64     `json:"gz"`      // размер в архиве
	SHA256  string    `json:"sha256"`  // исходника
	Saved   time.Time `json:"saved"`
}

// ErrFull — архив дорос до предела.
var ErrFull = errors.New("архив заполнен до предела")

const indexName = "index.jsonl"

// Open — архив в папке root (создаётся). Считает занятое место и читает журнал.
func Open(root string, maxBytes int64) (*Archive, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	a := &Archive{Root: root, MaxBytes: maxBytes, known: map[string]bool{}}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if info, err := d.Info(); err == nil {
			a.used += info.Size()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, e := range a.Entries() {
		a.known[e.Rel] = true
	}
	return a, nil
}

// SetMax — поменять предел на ходу (служба делит общий потолок между архивами профилей).
// n ≤ 0 здесь значит «места нет», а не «без предела»: иначе архив, которому из общего
// потолка ничего не осталось, стал бы безлимитным.
func (a *Archive) SetMax(n int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.MaxBytes = max(n, 1)
}

// Used — занято, байт.
func (a *Archive) Used() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.used
}

// Has — файл уже в архиве.
func (a *Archive) Has(rel string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.known[cleanRel(rel)]
}

// Path — где лежит архивная копия (даже если её ещё нет).
func (a *Archive) Path(rel string) string {
	return filepath.Join(a.Root, filepath.FromSlash(cleanRel(rel))) + ".gz"
}

// Read — содержимое архивной копии (распакованное).
func (a *Archive) Read(rel string) ([]byte, error) {
	f, err := os.Open(a.Path(rel))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

// Save копирует src в архив под именем rel со сжатием. Уже сохранённый файл не трогается
// (возвращает false, nil). Источник только читается.
func (a *Archive) Save(src, rel, op string, reasons []string) (bool, error) {
	rel = cleanRel(rel)
	if rel == "" {
		return false, fmt.Errorf("недопустимое имя в архиве: %q", src)
	}
	if a.Has(rel) {
		return false, nil
	}
	data, err := netio.ReadFile(src) // источник часто сетевой: с ограничением скорости
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(data)

	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.DefaultCompression) // .shp жмётся в 2,7–3 раза; уровень 6 — ещё 5% за 0,2 с
	zw.Name = filepath.Base(src)
	if _, err := zw.Write(data); err != nil {
		return false, err
	}
	if err := zw.Close(); err != nil {
		return false, err
	}

	a.mu.Lock()
	if a.MaxBytes > 0 && a.used+int64(buf.Len()) > a.MaxBytes {
		a.mu.Unlock()
		return false, ErrFull
	}
	a.mu.Unlock()

	dst := a.Path(rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	tmp := dst + ".tmp"
	if err := writeSync(tmp, buf.Bytes()); err != nil {
		os.Remove(tmp) // свой недописанный файл — не чужой
		return false, err
	}
	// проверка: то, что легло на диск, распаковывается в точно такой же файл
	if err := verify(tmp, sum); err != nil {
		os.Remove(tmp)
		return false, fmt.Errorf("проверка архивной копии %s: %w", rel, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return false, err
	}

	e := Entry{Rel: rel, Source: src, Op: op, Reasons: reasons, Size: int64(len(data)), Gz: int64(buf.Len()),
		SHA256: hex.EncodeToString(sum[:]), Saved: time.Now().Truncate(time.Second)}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.used += e.Gz
	a.known[rel] = true
	return true, a.appendIndex(e)
}

// Entries — журнал архива (битые строки пропускаются: журнал дописывается, а не переписывается).
func (a *Archive) Entries() []Entry {
	f, err := os.Open(filepath.Join(a.Root, indexName))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Rel != "" {
			out = append(out, e)
		}
	}
	return out
}

func (a *Archive) appendIndex(e Entry) error {
	f, err := os.OpenFile(filepath.Join(a.Root, indexName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	line, _ := json.Marshal(e)
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	a.used += int64(len(line) + 1)
	return err
}

func writeSync(path string, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil { // на диск, а не в кэш: иначе при отключении питания «переименованный» файл бывает пустым
		f.Close()
		return err
	}
	return f.Close()
}

func verify(path string, want [32]byte) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, zr); err != nil {
		return err
	}
	if !bytes.Equal(h.Sum(nil), want[:]) {
		return errors.New("SHA-256 не совпадает с исходником")
	}
	return nil
}

// cleanRel — относительный путь с прямыми слешами; «..» и абсолютные пути не допускаются.
func cleanRel(rel string) string {
	r := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.ReplaceAll(rel, `\`, "/"))))
	if r == "." || filepath.IsAbs(rel) || strings.HasPrefix(r, "../") || r == ".." || strings.HasPrefix(r, "/") {
		return ""
	}
	return r
}
