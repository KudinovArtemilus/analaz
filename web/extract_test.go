package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/config"
	"github.com/KudinovArtemilus/press-go/internal/extract"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

func writeShp(t *testing.T, buf string, i int, age time.Duration) string {
	t.Helper()
	name := fmt.Sprintf("26.%06d.067000_OP1_20261118_%02d%02d00.shp", 100000+i, 4+i/60, i%60)
	p := filepath.Join(buf, "2026", "11", "18", name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	data := shp.Synthetic([]string{"Y1", "Y2", "Y3", "Y4", "Y5", "Y6", "Y7", "Y8"}, []string{"slide:total force [kN]", "hp"}, 50, 0)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-age)
	os.Chtimes(p, mt, mt)
	return p
}

// Сквозной: буфер + quick-таблица от утилиты → проход сервера → полная таблица без дублей,
// quick не тронута → повтор ничего не пишет → новые файлы, «молодой» файл, удалённый из буфера,
// битый файл.
func TestFeaturesEndToEnd(t *testing.T) {
	buf, feat := t.TempDir(), t.TempDir()
	for i := range 10 {
		writeShp(t, buf, i, time.Hour)
	}
	// «features_quick_OP1» — как у Артёма: утилита extract с --every 2
	quick := filepath.Join(feat, "features_quick_OP1.csv.gz")
	if _, err := extract.Update(batch.NewTable(), quick, extract.Options{
		Roots: []string{buf}, Filter: batch.Filter{Op: "OP1"}, Every: 2, Jobs: 2, Loc: time.UTC}); err != nil {
		t.Fatal(err)
	}
	quickBytes, _ := os.ReadFile(quick)

	st, err := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	if err != nil {
		t.Fatal(err)
	}
	st.Update(func(c *config.Config) error {
		c.FeaturesDir = feat
		p := c.Active()
		p.Sources, p.TZ = []string{buf}, 0
		return nil
	})
	s, _ := New(buf, time.UTC)
	s.UseConfig(st)

	// --- первый проход: 5 строк перенесено из quick, 5 досчитано ---
	s.feat.Run()
	fs := s.feat.Status()
	if fs.Note != "" || len(fs.Ops) != 2 { // профиль по умолчанию — OP1 и OP2
		t.Fatalf("статус: %+v", fs)
	}
	op1 := fs.Ops[0]
	if op1.Err != "" || op1.Merged != 5 || op1.Added != 5 || op1.Rows != 10 || op1.Todo != 0 {
		t.Fatalf("OP1: %+v", op1)
	}
	if fs.Ops[1].Err != "" || fs.Ops[1].Rows != 0 || fs.Ops[1].Added != 0 {
		t.Fatalf("OP2 (файлов нет): %+v", fs.Ops[1])
	}
	full := filepath.Join(feat, "features_OP1.csv.gz")
	tb, err := batch.ReadCSVGz(full)
	if err != nil {
		t.Fatal(err)
	}
	files := tb.Text(0)
	if uniq := slices.Compact(slices.Sorted(slices.Values(files))); len(uniq) != 10 || len(files) != 10 {
		t.Fatalf("дубли или нехватка: %d строк, %d разных", len(files), len(uniq))
	}
	if b, _ := os.ReadFile(quick); !bytes.Equal(b, quickBytes) {
		t.Fatal("quick-таблица изменилась — чужие файлы трогать нельзя")
	}
	if _, err := os.Stat(filepath.Join(feat, "features_OP2.csv.gz")); err == nil {
		t.Fatal("для OP2 без файлов создана пустая таблица")
	}
	// анализ теперь берёт полную таблицу — она самая свежая
	if tabs, _ := s.an.tables(); !samePath(tabs["OP1"], full) {
		t.Fatalf("анализ смотрит в %s", tabs["OP1"])
	}

	// --- повтор: ничего нового, таблица не переписана ---
	m0 := modTime(full)
	s.feat.Run()
	if op := s.feat.Status().Ops[0]; op.Added != 0 || op.Merged != 0 || op.Rows != 10 || !modTime(full).Equal(m0) {
		t.Fatalf("повтор: %+v", op)
	}

	// --- новый файл, «молодой» файл, удалённый из буфера, битый ---
	writeShp(t, buf, 10, time.Hour)
	writeShp(t, buf, 11, 0) // только что записан
	os.Remove(filepath.Join(buf, "2026", "11", "18", "26.100000.067000_OP1_20261118_040000.shp"))
	bad := writeShp(t, buf, 12, time.Hour)
	os.WriteFile(bad, []byte("не shp"), 0o644)
	os.Chtimes(bad, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))

	s.feat.Run()
	op := s.feat.Status().Ops[0]
	if op.Added != 1 || op.Young != 1 || len(op.Errors) != 1 || op.Rows != 11 {
		t.Fatalf("новые: %+v", op)
	}
	// битый файл в следующем проходе не повторяется, «молодой» дозрел
	f11 := filepath.Join(buf, "2026", "11", "18", "26.100011.067000_OP1_20261118_041100.shp")
	os.Chtimes(f11, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	s.feat.Run()
	op = s.feat.Status().Ops[0]
	if op.Added != 1 || op.Young != 0 || len(op.Errors) != 0 || op.Rows != 12 {
		t.Fatalf("после дозаписи: %+v", op)
	}
	tb, _ = batch.ReadCSVGz(full)
	if !slices.ContainsFunc(tb.Text(0), func(f string) bool { return batch.FileName(f) == "26.100000.067000_op1_20261118_040000.shp" }) {
		t.Fatal("строка удалённого из буфера файла пропала")
	}

	// --- утилиту extract запустили руками в quick ещё раз: переносится только новое, без дублей ---
	writeShp(t, buf, 13, time.Hour)
	time.Sleep(20 * time.Millisecond) // чтобы время quick стало позже полной таблицы
	qt, _ := batch.ReadCSVGz(quick)
	if _, err := extract.Update(qt, quick, extract.Options{Roots: []string{buf}, Filter: batch.Filter{Op: "OP1"}, Jobs: 2, Loc: time.UTC}); err != nil {
		t.Fatal(err)
	}
	s.feat.Run()
	op = s.feat.Status().Ops[0]
	if op.Merged != 1 || op.Rows != 13 {
		t.Fatalf("повторный перенос: %+v", op)
	}
	s.feat.Run() // та же quick второй раз не читается и ничего не даёт
	if op = s.feat.Status().Ops[0]; op.Merged != 0 || op.Rows != 13 {
		t.Fatalf("третий проход: %+v", op)
	}
}

// Без папки таблиц — понятная причина, а не тишина.
func TestFeaturesNoDir(t *testing.T) {
	st, _ := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	s, _ := New(t.TempDir(), time.UTC)
	s.UseConfig(st)
	s.feat.Run()
	if n := s.feat.Status().Note; n == "" {
		t.Fatal("нет причины")
	}
}
