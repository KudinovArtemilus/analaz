package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/config"
	"github.com/KudinovArtemilus/press-go/internal/features"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// Сквозной: буфер с настоящими (синтетическими) .shp → проход архива → файл «удалили из буфера» →
// карточка цикла открывает его из архива. Источник при этом не меняется.
func TestArchiveEndToEnd(t *testing.T) {
	buf := t.TempDir() // «BufferShp»
	feat := t.TempDir()
	tb := batch.NewTable()
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var files []string
	for i := range 40 {
		rel := fmt.Sprintf("2026/11/01/26.%06d.067000_OP1_x%02d.shp", 100000+i, i)
		p := filepath.Join(buf, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, shp.Synthetic([]string{"Y1", "Y2", "Y3", "Y4", "Y5", "Y6", "Y7", "Y8"}, []string{"slide:total force [kN]", "hp"}, 50, 0), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, rel)
		f := features.Features{"dt_ms": 5, "force_max_kN": 25000 + float64(i%5)*100, "hp_min": 274 + float64(i%3)*0.2, "die_strokes": float64(i + 1)}
		tb.Add(p, "OP1", t0.Add(time.Duration(i)*time.Minute).Format("2006-01-02 15:04:05"), f)
	}
	if err := tb.WriteCSVGz(filepath.Join(feat, "features_OP1.csv.gz")); err != nil {
		t.Fatal(err)
	}
	st, err := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	st.Update(func(c *config.Config) error {
		c.FeaturesDir, c.DataDir = feat, dataDir
		c.Archive.KeepEvery = 10 // фон: ходы 10, 20, 30, 40
		return nil
	})
	s, _ := New(buf, time.UTC)
	s.UseConfig(st)

	s.arch.Run()
	as := s.arch.Status()
	if as.Note != "" || as.Saved == 0 || as.Files != as.Saved || len(as.Errors) != 0 {
		t.Fatalf("проход: %+v", as)
	}
	// второй проход — ничего нового
	s.arch.Run()
	if as2 := s.arch.Status(); as2.Saved != 0 || as2.Known != as.Saved {
		t.Errorf("повтор: сохранено %d, уже было %d (ждали 0 и %d)", as2.Saved, as2.Known, as.Saved)
	}
	// фоновый цикл с ходом 10 (i = 9) сохранён; «удаляем из буфера» — карточка берёт из архива
	rel := files[9]
	if !s.arch.forProfile("press10").Has(rel) {
		t.Fatalf("фон (ход 10) не сохранён")
	}
	os.Remove(filepath.Join(buf, filepath.FromSlash(rel)))
	f, err := s.open(rel)
	if err != nil || f.NumSamples() != 50 || !strings.Contains(f.Path, "26.100009.067000_OP1") {
		t.Fatalf("из архива: %v", err)
	}
	// не сохранённый и удалённый — понятная ошибка
	other := files[0]
	if !s.arch.forProfile("press10").Has(other) {
		os.Remove(filepath.Join(buf, filepath.FromSlash(other)))
		if _, err := s.open(other); err == nil || !strings.Contains(err.Error(), "не сохранён в архиве") {
			t.Errorf("ждали понятную ошибку: %v", err)
		}
	}

	// «Настройки» показывают статус; кнопка «Сохранить сейчас» — только с этого компьютера
	h := s.Handler()
	rec := do(h, "GET", "/admin", nil, true, nil)
	if !strings.Contains(rec.Body.String(), "Архив:") || !strings.Contains(rec.Body.String(), "Сохранить сейчас") {
		t.Errorf("в настройках нет статуса архива")
	}
	if rec := do(h, "POST", "/admin/archive/run", nil, false, nil); rec.Code != http.StatusForbidden {
		t.Errorf("из сети кнопка должна быть закрыта: %d", rec.Code)
	}
}
