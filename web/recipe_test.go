package web

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/features"
)

// writeRecipes — таблица пресса: первые n1 циклов рецепт 60/650, дальше n2 — 45/655 (если n2 > 0).
func writeRecipes(t *testing.T, dir, op string, n1, n2 int) {
	t.Helper()
	tb := batch.NewTable()
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := range n1 + n2 {
		f := features.Features{"dt_ms": 5, "force_max_kN": 26000 + float64(i%7)*100, "hp_min": 274 + float64(i%3)*0.2,
			analyze.RecipeChannels[0]: 60, analyze.RecipeChannels[1]: 650, analyze.RecipeChannels[2]: float64(i % 2 * 50)}
		if i >= n1 {
			f["force_max_kN"], f[analyze.RecipeChannels[0]], f[analyze.RecipeChannels[1]] = 21000+float64(i%7)*100, 45, 655
		}
		tm := t0.Add(time.Duration(i) * 30 * time.Minute).Format("2006-01-02 15:04:05")
		tb.Add(fmt.Sprintf(`D:\shp\r%03d_%s.shp`, i, op), op, tm, f)
	}
	if err := tb.WriteCSVGz(filepath.Join(dir, "features_"+op+".csv.gz")); err != nil {
		t.Fatal(err)
	}
}

// TestPressRecipePages — у пресса с двумя рецептами анализ по умолчанию — по текущему рецепту,
// со своей нормой; «все рецепты вместе» — как раньше, с пометкой; у пресса с одним рецептом
// выбора нет и страница прежняя.
func TestPressRecipePages(t *testing.T) {
	dir := t.TempDir()
	writeRecipes(t, dir, "OP1", 40, 20)
	writeRecipes(t, dir, "OP2", 40, 0)
	s, _ := New(t.TempDir(), time.UTC)
	s.EnableAnalysis(dir, dir)
	h := s.Handler()

	b := page(h, "/analysis?op=OP1")
	for _, want := range []string{"рецепт:", `href="/analysis?op=OP1%40rcp45-655" class="on"`, "рецепт 60 / 650", "все рецепты вместе",
		"Пресс OP1 · рецепт 45 / 655", "первые 20% циклов этого рецепта"} {
		if !strings.Contains(b, want) {
			i := strings.Index(b, "<main")
			t.Errorf("OP1: нет %q\n%.1200s", want, b[max(0, i):])
		}
	}
	if b := page(h, "/analysis?op=OP1@all"); !strings.Contains(b, "Все рецепты вместе — норма общая") || !strings.Contains(b, "Пресс OP1 · все рецепты") {
		t.Error("OP1 все рецепты: нет пометки про общую норму")
	}
	if b := page(h, "/analysis?op=OP1@rcp60-650"); !strings.Contains(b, "Пресс OP1 · рецепт 60 / 650") || !strings.Contains(b, "циклов 40") {
		i := strings.Index(b, "an-title")
		t.Errorf("OP1 рецепт 60/650\n%.600s", b[max(0, i):])
	}
	if b := page(h, "/analysis?op=OP2"); strings.Contains(b, "рецепт:") || strings.Contains(b, "Все рецепты вместе") {
		t.Error("OP2 (один рецепт): выбора рецепта быть не должно")
	}
}
