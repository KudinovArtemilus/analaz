package web

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/config"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// writeRing — цикл i в кольце под именем name: синтетический .shp с временем начала в заголовке.
func writeRing(t *testing.T, dir, name string, i int) {
	t.Helper()
	start := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC).Add(time.Duration(i) * 90 * time.Second)
	fn := func(ch int, x float64) float64 { return float64(i) + math.Sin(x*float64(ch+1)) }
	d := shp.SyntheticSignals([]string{"Y1", "Y2", "Y3", "Y4", "Y5", "Y6", "Y7", "Y8"},
		[]string{"slide total force [kN]", "hyd:03-B135.1 hp [bar]"}, fn, 10, 20)
	const ticks1601to1970 = 116444736000000000
	binary.LittleEndian.PutUint64(d[8:], uint64(start.UnixNano()/100+ticks1601to1970))
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, d, 0o644); err != nil {
		t.Fatal(err)
	}
	m := start.Add(80 * time.Second)
	os.Chtimes(p, m, m)
}

// TestRingProfile — пресс 5: кольцо actual/old01… копируется к себе, признаки считаются по копиям,
// после сдвига имён — только новый цикл, источник не меняется.
func TestRingProfile(t *testing.T) {
	src, data, feat, press := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for k := 1; k <= 3; k++ {
		writeRing(t, src, "old0"+string(rune('0'+k))+".shp", 3-k) // old01 — цикл 2, old03 — цикл 0
	}
	writeRing(t, src, "actual.shp", 3)
	st, _ := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	err := st.Update(func(c *config.Config) error {
		c.FeaturesDir, c.DataDir = feat, data
		c.Active().Enabled = false
		c.Profiles = append(c.Profiles, config.Profile{ID: "press5", Name: "Пресс 5", Kind: "press5", Sources: []string{src},
			Ops: []string{"OP4"}, TZ: 5, Enabled: true, Ring: true})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := New(press, time.UTC)
	s.UseConfig(st)
	now := time.Date(2026, 11, 20, 12, 10, 0, 0, time.UTC)
	s.ring.Run(now)
	if rs := s.ring.Status("press5"); rs == nil || rs.Copied != 3 || rs.Err != "" {
		t.Fatalf("захват: %+v", rs)
	}
	copyDir := filepath.Join(data, "ring", "press5", "2026", "11", "20")
	if _, err := os.Stat(filepath.Join(copyDir, "ring_OP4_20261120_170000.shp")); err != nil {
		t.Fatal("нет копии первого цикла под постоянным именем (время из заголовка, пояс профиля)")
	}
	ageDir(t, data) // копии свежие — признаки ждут 2 минуты (MinAge); в тесте «прошло время»
	s.feat.Run()
	tb, err := batch.ReadCSVGz(filepath.Join(feat, "features_OP4.csv.gz"))
	if err != nil || tb.Len() != 3 {
		t.Fatalf("признаки OP4: %v, строк %d", err, tbLen(tb))
	}
	// «Циклы» показывают копии; ссылка на цикл — через префикс профиля
	if b := do(s.Handler(), "GET", "/files?m=press5", nil, false, nil).Body.String(); !strings.Contains(b, "ring_OP4_20261120_170300.shp") ||
		!strings.Contains(b, "/cycle?f=%40press5%2F2026%2F11%2F20%2Fring_OP4_20261120_170300.shp") {
		t.Errorf("в «Циклах» нет копий кольца\n%.1500s", b)
	}

	// сдвиг кольца: actual → old01, old01 → old02 …, самый старый затёрт; новый actual
	os.Remove(filepath.Join(src, "old03.shp"))
	os.Rename(filepath.Join(src, "old02.shp"), filepath.Join(src, "old03.shp"))
	os.Rename(filepath.Join(src, "old01.shp"), filepath.Join(src, "old02.shp"))
	os.Rename(filepath.Join(src, "actual.shp"), filepath.Join(src, "old01.shp"))
	writeRing(t, src, "actual.shp", 4)
	s.ring.Run(now.Add(time.Minute))
	ageDir(t, data)
	s.feat.Run()
	tb, _ = batch.ReadCSVGz(filepath.Join(feat, "features_OP4.csv.gz"))
	if rs := s.ring.Status("press5"); rs.Copied != 4 || tbLen(tb) != 4 {
		t.Fatalf("после сдвига: скопировано %d, строк %d — ждали 4 и 4", rs.Copied, tbLen(tb))
	}
	if es, _ := os.ReadDir(src); len(es) != 4 {
		t.Errorf("в кольце %d файлов — источник менять нельзя", len(es))
	}
	// настройки показывают состояние кольца; «Проверить» подсказывает галочку, если она снята
	if b := do(s.Handler(), "GET", "/admin", nil, true, nil).Body.String(); !strings.Contains(b, "скопировано с запуска 4") {
		t.Error("в настройках нет состояния кольца")
	}
}

func tbLen(tb *batch.Table) int {
	if tb == nil {
		return -1
	}
	return tb.Len()
}

func TestCheckSourceRingHint(t *testing.T) {
	src := t.TempDir()
	writeRing(t, src, "old01.shp", 0)
	writeRing(t, src, "actual.shp", 1)
	c := checkSource(t.Context(), src)
	if !c.OK || c.Files != 2 || c.Ring != 2 {
		t.Fatalf("%+v", c)
	}
}

// ageDir — состарить все файлы папки на час (как будто прошло время после копирования).
func ageDir(t *testing.T, dir string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			os.Chtimes(p, old, old)
		}
		return nil
	})
}
