package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/config"
	"github.com/KudinovArtemilus/press-go/internal/features"
)

// TestStaleFromArchive — циклы старой версии признаков пересчитываются и после того, как буфер
// удалил их файлы: из архива профиля. Цикл, которого нет ни в буфере, ни в архиве, остаётся как
// был, и это видно (Lost в «Настройках», пометка в «Анализе»).
func TestStaleFromArchive(t *testing.T) {
	kps, feat, data := t.TempDir(), t.TempDir(), t.TempDir()
	for i := range 3 {
		writeKps(t, kps, i, 860, 895)
	}
	st, _ := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	st.Update(func(c *config.Config) error {
		c.FeaturesDir, c.DataDir = feat, data
		c.Active().Enabled = false
		c.Profiles = append(c.Profiles, config.Profile{ID: "kps", Name: "КПС", Kind: "kps", Sources: []string{kps}, Ops: []string{"OP3"}, Enabled: true})
		return nil
	})
	s, _ := New(kps, time.UTC)
	s.UseConfig(st)
	s.feat.Run()

	dir := filepath.Join(kps, "2026", "11", "13")
	ents, _ := os.ReadDir(dir)
	if len(ents) != 3 {
		t.Fatalf("файлов %d", len(ents))
	}
	var paths []string
	for _, e := range ents {
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	// цикл 0 — в архиве профиля; потом буфер удалил циклы 0 и 1
	a := s.arch.forProfile("kps")
	if a == nil {
		t.Fatal("архив профиля не открылся")
	}
	if _, err := a.Save(paths[0], "2026/11/13/"+ents[0].Name(), "OP3", []string{"тест"}); err != nil {
		t.Fatal(err)
	}
	os.Remove(paths[0])
	os.Remove(paths[1])

	// таблица — как будто посчитана прежней версией признаков
	target := filepath.Join(feat, "features_OP3.csv.gz")
	tb := batch.NewTable()
	for i, p := range paths {
		tm := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * 10 * time.Minute)
		abs, _ := filepath.Abs(p)
		tb.Add(abs, "OP3", tm.Format("2006-01-02 15:04:05"), features.Features{"feat_ver": features.KpsVersion - 1, "dt_ms": 20, "x": 1})
	}
	if err := tb.WriteCSVGz(target); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute) // таблица новее — сервер её перечитает
	os.Chtimes(target, later, later)

	if b := page(s.Handler(), "/analysis?op=OP3"); !strings.Contains(b, "3 циклов посчитаны прежней версией признаков") {
		t.Error("анализ не говорит о циклах старой версии")
	}
	s.feat.Run()
	op := s.feat.Status().Ops[0]
	if op.Redone != 2 || op.Lost != 1 || op.Rows != 3 {
		t.Fatalf("ждали: пересчитано 2 (из буфера и из архива), без файла 1, строк 3: %+v", op)
	}
	tb, _ = batch.ReadCSVGz(target)
	st2 := tb.Stale(features.KpsVersion)
	if len(st2) != 1 || !st2[batch.FileName(paths[1])] {
		t.Fatalf("старой версии остались %v, ждали только цикл 1 (его нет нигде)", st2)
	}
	// следующий проход: пересчитывать нечего, файла нет — в работу не берётся
	s.feat.Run()
	if op := s.feat.Status().Ops[0]; op.Redone != 0 || op.Lost != 1 || len(op.Errors) != 0 {
		t.Fatalf("второй проход: %+v", op)
	}
	if b := page(s.Handler(), "/analysis?op=OP3"); !strings.Contains(b, "1 циклов посчитаны прежней версией признаков") {
		i := strings.Index(b, "an-title")
		t.Errorf("после пересчёта пометка — про 1 цикл\n%s", b[max(0, i):min(len(b), i+1500)])
	}
	if b := adminPage(s.Handler()); !strings.Contains(b, "старой версии без файла (ни в буфере, ни в архиве) 1") {
		t.Error("в «Настройках» не видно циклов, которые пересчитать нельзя")
	}
}
