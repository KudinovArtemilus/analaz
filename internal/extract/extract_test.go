package extract

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/features"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// buffer — папка ГГГГ/ММ/ДД с n синтетическими файлами OP1, по минуте друг за другом.
func buffer(t *testing.T, n int) (string, []string) {
	t.Helper()
	root := t.TempDir()
	var paths []string
	for i := range n {
		name := fmt.Sprintf("26.%06d.067000_OP1_20261118_%02d%02d00.shp", 100000+i, 4+i/60, i%60)
		p := filepath.Join(root, "2026", "11", "18", name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		data := shp.Synthetic([]string{"Y1", "Y2", "Y3", "Y4", "Y5", "Y6", "Y7", "Y8"}, []string{"slide:total force [kN]", "hp"}, 50, 0)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Hour) // «давно записан»
		os.Chtimes(p, old, old)
		abs, _ := filepath.Abs(p)
		paths = append(paths, abs)
	}
	return root, paths
}

func opts(root string) Options {
	return Options{Roots: []string{root}, Filter: batch.Filter{Op: "OP1"}, Jobs: 2, Loc: time.UTC}
}

func files(tb *batch.Table) []string { return tb.Text(0) }

func TestUpdateAppendsOnlyNew(t *testing.T) {
	root, paths := buffer(t, 6)
	out := filepath.Join(t.TempDir(), "features_OP1.csv.gz")

	tb := batch.NewTable()
	st, err := Update(tb, out, opts(root))
	if err != nil || st.Added != 6 || st.Saves != 1 || len(st.Errors) != 0 {
		t.Fatalf("первый проход: %+v, %v", st, err)
	}
	// порядок строк — в каком воркеры закончили (анализ всё равно сортирует по времени)
	if got := slices.Sorted(slices.Values(files(tb))); !slices.Equal(got, paths) {
		t.Fatalf("файлы: %v", got)
	}

	// второй проход по той же папке — ничего не делает и файл не трогает
	info, _ := os.Stat(out)
	tb2, err := batch.ReadCSVGz(out)
	if err != nil {
		t.Fatal(err)
	}
	st, err = Update(tb2, out, opts(root))
	if err != nil || st.Added != 0 || st.Done != 6 || st.Saves != 0 {
		t.Fatalf("повтор: %+v, %v", st, err)
	}
	if info2, _ := os.Stat(out); !info2.ModTime().Equal(info.ModTime()) {
		t.Fatal("таблица переписана, хотя добавлять нечего")
	}

	// буфер удалил два старых файла, пришли два новых: старые строки остаются
	os.Remove(paths[0])
	os.Remove(paths[1])
	for i, p := range paths[:2] {
		np := strings.Replace(p, "_OP1_20261118_04", fmt.Sprintf("_OP1_20261118_%02d", 6+i), 1)
		data := shp.Synthetic([]string{"Y1", "Y2", "Y3", "Y4", "Y5", "Y6", "Y7", "Y8"}, []string{"slide:total force [kN]"}, 20, 0)
		os.WriteFile(np, data, 0o644)
		old := time.Now().Add(-time.Hour)
		os.Chtimes(np, old, old)
	}
	st, err = Update(tb2, out, opts(root))
	if err != nil || st.Added != 2 || tb2.Len() != 8 {
		t.Fatalf("после удаления: %+v, %v, строк %d", st, err, tb2.Len())
	}
	if !slices.Contains(files(tb2), paths[0]) {
		t.Fatal("строка удалённого из буфера файла пропала")
	}
}

func TestPlanFreshFirstLimitYoungSkip(t *testing.T) {
	root, paths := buffer(t, 10)
	done := map[string]bool{paths[3]: true, paths[6]: true} // посчитаны 3 и 6: пропуски 0–2, 4–5; свежие 7–9

	o := opts(root)
	o.FreshFirst = true
	todo, st, err := Plan(done, o)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{paths[7], paths[8], paths[9], paths[0], paths[1], paths[2], paths[4], paths[5]}
	if !slices.Equal(todo, want) || st.Todo != 8 || st.Done != 2 {
		t.Fatalf("FreshFirst: %v", todo)
	}

	// без FreshFirst — как Python: просто по времени
	o.FreshFirst = false
	todo, _, _ = Plan(done, o)
	if !slices.Equal(todo, []string{paths[0], paths[1], paths[2], paths[4], paths[5], paths[7], paths[8], paths[9]}) {
		t.Fatalf("по времени: %v", todo)
	}

	// Limit, «молодой» свежий файл и Skip
	o.FreshFirst, o.Limit, o.MinAge = true, 3, 2*time.Minute
	now := time.Now()
	os.Chtimes(paths[9], now, now) // регистратор, может быть, ещё пишет
	o.Skip = func(p string) bool { return p == paths[8] }
	todo, st, _ = Plan(done, o)
	if !slices.Equal(todo, []string{paths[7], paths[0], paths[1]}) || st.Young != 1 || st.Todo != 6 || st.Taken != 3 {
		t.Fatalf("Limit/MinAge/Skip: %v %+v", todo, st)
	}

	// Every: каждый 3-й из 10 — файлы 0, 3, 6, 9
	o = opts(root)
	o.Every = 3
	todo, st, _ = Plan(done, o)
	if st.Found != 4 || !slices.Equal(todo, []string{paths[0], paths[9]}) {
		t.Fatalf("Every: %v %+v", todo, st)
	}
}

func TestErrorsDoNotStopOthers(t *testing.T) {
	root, paths := buffer(t, 3)
	os.WriteFile(paths[1], []byte("не shp"), 0o644) // битый файл посередине
	old := time.Now().Add(-time.Hour)
	os.Chtimes(paths[1], old, old)
	tb := batch.NewTable()
	st, err := Update(tb, filepath.Join(t.TempDir(), "f_OP1.csv.gz"), opts(root))
	if err != nil || st.Added != 2 || len(st.Errors) != 1 || st.Errors[0].Path != paths[1] {
		t.Fatalf("%+v, %v", st, err)
	}
}

func TestMerge(t *testing.T) {
	a := batch.NewTable()
	a.Add("f1", "OP1", "2026-11-01 00:00:00", features.Features{"x": 1, "y": 2})
	b := batch.NewTable()
	b.Add("f1", "OP1", "2026-11-01 00:00:00", features.Features{"x": 100}) // уже есть — не берём
	b.Add("f2", "OP1", "2026-11-01 00:01:00", features.Features{"z": 7, "y": 5})
	b.Add("f2", "OP1", "2026-11-01 00:01:00", features.Features{"z": 8}) // дубль внутри b
	if n := a.Merge(b); n != 1 || a.Len() != 2 {
		t.Fatalf("добавлено %d, строк %d", n, a.Len())
	}
	cols := a.Columns()
	get := func(row int, name string) float64 { return a.Column(slices.Index(cols, name))[row] }
	if get(0, "x") != 1 || get(1, "y") != 5 || get(1, "z") != 7 || !math.IsNaN(get(1, "x")) || !math.IsNaN(get(0, "z")) {
		t.Fatalf("значения перепутаны: cols %v", cols)
	}
	// после записи и чтения — то же самое (dt_ms нужен, иначе таблица считается старой)
	a.Merge(withDT())
	p := filepath.Join(t.TempDir(), "m.csv.gz")
	if err := a.WriteCSVGz(p); err != nil {
		t.Fatal(err)
	}
	r, err := batch.ReadCSVGz(p)
	if err != nil || r.Len() != 3 || !slices.Equal(r.Text(0), []string{"f1", "f2", "f3"}) {
		t.Fatalf("после чтения: %v", err)
	}
	rc := r.Columns()
	if v := r.Column(slices.Index(rc, "z"))[1]; v != 7 {
		t.Fatalf("z после чтения = %v", v)
	}
}

func withDT() *batch.Table {
	t := batch.NewTable()
	t.Add("f3", "OP1", "2026-11-01 00:02:00", features.Features{"dt_ms": 5})
	return t
}

// Та же папка под другим путём (другой регистр, подключённый диск): по имени файла
// цикл узнаётся, дубля в таблице нет.
func TestDoneByNameOtherPath(t *testing.T) {
	root, paths := buffer(t, 3)
	done := map[string]bool{}
	for _, p := range paths {
		done[`Z:\BufferShp\2026\11\18\`+strings.ToUpper(filepath.Base(p))] = true
	}
	todo, st, err := Plan(done, opts(root))
	if err != nil || len(todo) != 0 || st.Found != 3 {
		t.Fatalf("todo %v, %+v, %v", todo, st, err)
	}
}
