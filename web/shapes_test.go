package web

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/config"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// pressCycle — цикл пресса с формой: ползун опускается, растёт усилие, падает давление.
func pressCycle(t *testing.T, dir string, at time.Time, force float64) string {
	t.Helper()
	name := fmt.Sprintf("26.%06d.067000_OP1_%s.shp", at.Unix()%1000000, at.Format("20060102_150405"))
	p := filepath.Join(dir, at.Format("2006"), at.Format("01"), at.Format("02"), name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	fn := func(ch int, x float64) float64 {
		b := math.Exp(-(x - 5) * (x - 5) / 2) // «удар» в середине записи
		switch ch {
		case 0:
			return 2100 - 900*b
		case 1:
			return force * b
		case 2:
			return 280 - 10*b
		case 3:
			return 0.5 * b
		}
		return 0
	}
	data := shp.SyntheticSignals([]string{"Y1", "Y2", "Y3", "Y4", "Y5", "Y6", "Y7", "Y8"},
		[]string{"slide:average val.encoder [mm]", "slide:total force [kN]", "hyd:high pressure [bar]", "slide:deviation left-right [mm]"}, fn, 10, 20)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(p, old, old)
	return p
}

// TestShapesAPI — 6.2е: по типичному циклу на сутки с кривыми четырёх каналов; ссылка на цикл.
func TestShapesAPI(t *testing.T) {
	press, feat := t.TempDir(), t.TempDir()
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	for day := range 6 {
		for k := range 8 {
			pressCycle(t, press, t0.Add(time.Duration(day)*24*time.Hour+time.Duration(k)*time.Hour), 20000+float64(k)*500)
		}
	}
	st, _ := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	st.Update(func(c *config.Config) error {
		c.FeaturesDir = feat
		p := c.Active()
		p.Sources, p.Ops = []string{press}, []string{"OP1"}
		return nil
	})
	s, _ := New(press, time.UTC)
	s.UseConfig(st)
	s.feat.Run()

	rec := do(s.Handler(), "GET", "/api/analysis/OP1/shapes", nil, false, nil)
	var v shapesJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != 200 {
		t.Fatalf("%d %v %.300s", rec.Code, err, rec.Body.String())
	}
	if v.Step != "day" || len(v.Days) != 6 || v.Missing != 0 || v.Skipped != 0 {
		t.Fatalf("суток %d, step %s, нет файлов %d, не успели %d", len(v.Days), v.Step, v.Missing, v.Skipped)
	}
	d := v.Days[0]
	if d.Label != "10.11" || len(d.Curves) != 4 || d.Rel == "" {
		t.Fatalf("первые сутки: %q, кривых %d, ссылка %q", d.Label, len(d.Curves), d.Rel)
	}
	f := d.Curves["slide:total force [kN]"]
	peak := 0.0
	for _, y := range f.Y {
		peak = math.Max(peak, float64(y))
	}
	// типичный цикл суток — с усилием, ближайшим к медиане (20000…23500 → ~21750)
	if peak < 21000 || peak > 22500 || len(f.T) > shapePoints {
		t.Errorf("пик усилия типичного цикла %.0f, точек %d", peak, len(f.T))
	}
}
