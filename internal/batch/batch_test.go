package batch

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/KudinovArtemilus/press-go/internal/features"
)

func TestListFiles(t *testing.T) {
	root := t.TempDir() // временная папка, Go сам удалит её после теста
	for _, p := range []string{
		"2026/11/17/26.1.1_OP1_20261117_230000.shp",
		"2026/11/18/26.1.2_OP1_20261118_010000.shp",
		"2026/11/18/26.1.2_OP2_20261118_010100.shp",
		"2026/11/18/26.1.3_OP1_20261118_005900.shp", // раньше по времени, хотя имя «больше»
		"2026/11/18/notes.txt",
		"2026/11/19/26.1.4_OP1_20261119_000100.shp",
		"old10.shp",
	} {
		full := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, nil, 0o644)
	}
	names := func(paths []string) []string {
		var n []string
		for _, p := range paths {
			n = append(n, filepath.Base(p))
		}
		return n
	}

	got, err := ListFiles(root, Filter{Op: "op1", From: "2026-11-18", To: "2026-11-18"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"26.1.3_OP1_20261118_005900.shp", "26.1.2_OP1_20261118_010000.shp"}
	if !slices.Equal(names(got), want) {
		t.Errorf("фильтр OP1 за 18.11:\n got  %v\n want %v", names(got), want)
	}

	all, _ := ListFiles(root, Filter{})
	if len(all) != 6 { // 5 файлов с датой + old10.shp; txt не берётся
		t.Errorf("без фильтра ждали 6 файлов, получили %v", names(all))
	}
}

func TestTableRoundTrip(t *testing.T) {
	tb := NewTable()
	tb.Add(`D:\press\a.shp`, "OP1", "2026-11-18 04:56:19", features.Features{"dt_ms": 5, "force_max_kN": 23571.28125})
	// второй цикл с новым признаком и NaN — у первого цикла этого признака нет
	tb.Add(`D:\press\b.shp`, "OP1", "2026-11-18 04:57:00", features.Features{"dt_ms": 5, "hp_recover_s": math.NaN(), "x": 1e-300})

	path := filepath.Join(t.TempDir(), "f.csv.gz")
	if err := tb.WriteCSVGz(path); err != nil {
		t.Fatal(err)
	}
	back, err := ReadCSVGz(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Len() != 2 || !back.Files()[`D:\press\b.shp`] {
		t.Fatalf("прочитано %d строк, файлы %v", back.Len(), back.Files())
	}
	get := func(r int, name string) float64 {
		j := back.idx[name]
		if j >= len(back.rows[r].v) {
			return math.NaN()
		}
		return back.rows[r].v[j]
	}
	if get(0, "force_max_kN") != 23571.28125 || get(1, "x") != 1e-300 {
		t.Error("числа не пережили запись и чтение без потерь")
	}
	if !math.IsNaN(get(0, "x")) || !math.IsNaN(get(1, "hp_recover_s")) {
		t.Error("отсутствующее значение и NaN должны читаться как NaN")
	}

	// файл без dt_ms — признаки старой версии
	old := NewTable()
	old.Add("a.shp", "OP1", "2026-09-28 00:00:00", features.Features{"cycle_s": 81})
	oldPath := filepath.Join(t.TempDir(), "old.csv.gz")
	old.WriteCSVGz(oldPath)
	if _, err := ReadCSVGz(oldPath); err != ErrOldStep {
		t.Errorf("ждали ErrOldStep, получили %v", err)
	}
}
