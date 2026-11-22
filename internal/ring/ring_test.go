package ring

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fake — «файл кольца»: заголовок с временем начала (FILETIME) и немного данных.
func fake(t *testing.T, dir, name string, start time.Time, mod time.Time, size int) {
	t.Helper()
	d := make([]byte, size)
	const ticks1601to1970 = 116444736000000000
	binary.LittleEndian.PutUint64(d[8:], uint64(start.UnixNano()/100+ticks1601to1970))
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, d, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(p, mod, mod)
}

func TestCaptureShift(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	plant := time.FixedZone("UTC+5", 5*3600)
	t0 := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) // 17:00 на заводе
	cyc := func(i int) (time.Time, time.Time) {
		s := t0.Add(time.Duration(i) * 90 * time.Second)
		return s, s.Add(80 * time.Second)
	}
	// кольцо из трёх: old01 — цикл 2 (свежий), old03 — цикл 0; actual — пишется
	for k := 0; k < 3; k++ {
		s, m := cyc(2 - k)
		fake(t, src, []string{"old01.shp", "old02.shp", "old03.shp"}[k], s, m, 100+2-k)
	}
	s3, m3 := cyc(3)
	fake(t, src, "actual.shp", s3, m3, 999)
	c := &Capture{Src: src, Dst: dst, Op: "OP4", Loc: plant}
	r, err := c.Run()
	if err != nil || r.New != 3 || len(r.Errors) != 0 {
		t.Fatalf("первый проход: %+v %v", r, err)
	}
	want := filepath.Join(dst, "2026", "11", "20", "ring_OP4_20261120_170000.shp")
	if st, err := os.Stat(want); err != nil {
		t.Fatalf("нет копии %s", want)
	} else if time.Since(st.ModTime()) > time.Minute {
		t.Errorf("время изменения копии %v — ждали время копирования", st.ModTime())
	}
	// сдвиг: actual дописан и стал old01, остальные сдвинулись, old03 (цикл 0) затёрт
	os.Remove(filepath.Join(src, "old03.shp"))
	os.Rename(filepath.Join(src, "old02.shp"), filepath.Join(src, "old03.shp"))
	os.Rename(filepath.Join(src, "old01.shp"), filepath.Join(src, "old02.shp"))
	os.Rename(filepath.Join(src, "actual.shp"), filepath.Join(src, "old01.shp"))
	s4, m4 := cyc(4)
	fake(t, src, "actual.shp", s4, m4, 999)
	r, _ = c.Run()
	if r.New != 1 || r.Seen != 2 {
		t.Fatalf("после сдвига ждали 1 новый и 2 виденных (их даже не читаем): %+v", r)
	}
	// перезапуск программы (память о виденном пропала): копии не дублируются
	c2 := &Capture{Src: src, Dst: dst, Op: "OP4", Loc: plant}
	if r, _ := c2.Run(); r.New != 0 || r.Seen != 3 {
		t.Fatalf("после перезапуска: %+v", r)
	}
	n := 0
	filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if n != 4 {
		t.Errorf("копий %d, ждали 4 (циклы 0–3)", n)
	}
	// источник не тронут
	if es, _ := os.ReadDir(src); len(es) != 4 {
		t.Errorf("в источнике %d файлов, ждали 4", len(es))
	}
}

func TestCaptureBadFile(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(src, "old01.shp"), []byte{1, 2, 3}, 0o644)
	// файлы с временем в имени — не кольцо, их читает обычный путь
	os.WriteFile(filepath.Join(src, "26.1.2_OP4_20261120_170000.shp"), make([]byte, 100), 0o644)
	c := &Capture{Src: src, Dst: dst, Op: "OP4"}
	r, err := c.Run()
	if err != nil || r.New != 0 || len(r.Errors) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if r, _ := c.Run(); len(r.Errors) != 1 {
		t.Errorf("непрочитанный файл должен пробоваться снова: %+v", r)
	}
}

func TestPrune(t *testing.T) {
	dst := t.TempDir()
	plant := time.FixedZone("UTC+5", 5*3600)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, plant)
	// цикл записан давно (регистратор стоял), но скопирован только что — не удаляется
	old := Path(dst, "OP4", now.Add(-72*time.Hour), plant)
	fresh := Path(dst, "OP4", now.Add(-96*time.Hour), plant)
	alien := filepath.Join(filepath.Dir(old), "мой_файл.shp")
	for _, p := range []string{old, fresh, alien} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
		os.Chtimes(p, now.Add(-time.Hour), now.Add(-time.Hour))
	}
	os.Chtimes(old, now.Add(-72*time.Hour), now.Add(-72*time.Hour)) // скопирован 3 суток назад
	alienOld := filepath.Join(filepath.Dir(fresh), "ring_чужое.txt")
	os.WriteFile(alienOld, []byte("x"), 0o644)
	os.Chtimes(alienOld, now.Add(-72*time.Hour), now.Add(-72*time.Hour))
	alien2 := filepath.Join(filepath.Dir(fresh), "mine.shp")
	os.WriteFile(alien2, []byte("x"), 0o644)
	os.Chtimes(alien2, now.Add(-72*time.Hour), now.Add(-72*time.Hour))
	n, err := Prune(dst, 48*time.Hour, now)
	if err != nil || n != 1 {
		t.Fatalf("удалено %d, %v", n, err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("старая копия не удалена")
	}
	for _, p := range []string{fresh, alien, alienOld, alien2} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("удалён %s — а не должен", p)
		}
	}
}
