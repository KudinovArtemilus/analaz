package web

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
)

// TestNormSave — «Пересчитать норму»: только с этого компьютера, проверка периода и пояснения,
// прежняя норма не удаляется, а переименовывается в .bak.
func TestNormSave(t *testing.T) {
	defer func(n int) { analyze.AutoNormCycles = n }(analyze.AutoNormCycles)
	analyze.AutoNormCycles = 30 // в тестовой таблице 80 циклов, по 20 в сутки

	dir := t.TempDir()
	writeTable(t, dir)
	s, _ := New(t.TempDir(), time.UTC)
	s.EnableAnalysis(dir, dir)
	h := s.Handler()
	sq := func(b string) string { return strings.Join(strings.Fields(b), " ") }

	if b := page(h, "/analysis/norm?op=OP1"); strings.Contains(b, "Пересчитать норму") {
		t.Error("с другого компьютера форма видна")
	}
	b := sq(do(h, "GET", "/analysis/norm?op=OP1", nil, true, nil).Body.String())
	if !strings.Contains(b, "Пересчитать норму") || !strings.Contains(b, "выбрать") {
		t.Fatalf("на этом компьютере нет формы или подсказок: %.1500s", b)
	}

	form := func(from, to, note string) url.Values {
		return url.Values{"op": {"OP1"}, "from": {from}, "to": {to}, "note": {note}}
	}
	post := func(f url.Values, local bool) (int, string) {
		rec := do(h, "POST", "/analysis/norm/save", f, local, nil)
		return rec.Code, sq(rec.Body.String())
	}
	normFile := filepath.Join(dir, "norm_OP1.json")

	if code, _ := post(form("2026-11-01T00:00:00", "2026-11-03T00:00:00", "x"), false); code != 403 {
		t.Errorf("из сети: код %d, ждали 403", code)
	}
	if _, b := post(form("2026-11-01T00:00", "2026-11-01T12:00", "мало"), true); !strings.Contains(b, "нужно не меньше 30") {
		t.Errorf("короткий период: %.400s", b)
	}
	if _, b := post(form("2026-11-01T00:00", "2026-11-03T00:00", ""), true); !strings.Contains(b, "почему период хороший") {
		t.Errorf("без пояснения: %.400s", b)
	}
	if _, err := os.Stat(normFile); err == nil {
		t.Fatal("после ошибок норма всё-таки записана")
	}

	_, b = post(form("2026-11-01T00:00:00", "2026-11-03T00:00:00", "сутки без остановов"), true)
	if !strings.Contains(b, "Норма пересчитана: 01.11.2026 00:00 — 03.11.2026 00:00, 41 циклов") ||
		!strings.Contains(b, "вручную: сутки без остановов") {
		t.Fatalf("первое сохранение: %.800s", b)
	}
	time.Sleep(1100 * time.Millisecond) // имя .bak — с точностью до секунды
	_, b = post(form("2026-11-02T00:00:00", "2026-11-04T00:00:00", "другие сутки"), true)
	if !strings.Contains(b, "Прежняя сохранена как") {
		t.Fatalf("второе сохранение: %.800s", b)
	}
	baks, _ := filepath.Glob(normFile + ".*.bak")
	if _, err := os.Stat(normFile); err != nil || len(baks) != 1 {
		t.Fatalf("норма %v, копий прежней %d", err, len(baks))
	}
	// анализ идёт уже по новой норме
	var sum struct {
		Norm struct {
			Stored bool   `json:"stored"`
			Note   string `json:"note"`
		} `json:"norm"`
	}
	get(t, h, "/api/analysis/OP1", 200, &sum)
	if !sum.Norm.Stored || sum.Norm.Note != "вручную: другие сутки" {
		t.Errorf("анализ после пересчёта: %+v", sum.Norm)
	}
}
