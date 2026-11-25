package web

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/config"
)

// TestFeaturesBudget — проход ограничен по времени: сначала каждой машине по featFair новых
// (медленная КПС не задерживает пресс и наоборот), потом порциями, пока не вышло время; остаток —
// в следующем проходе, который начнётся скоро (more), а не через 5 минут.
func TestFeaturesBudget(t *testing.T) {
	press, kps, feat := t.TempDir(), t.TempDir(), t.TempDir()
	for i := range 30 {
		shpOf(t, press, "OP1", i, []string{"slide:total force [kN]"})
	}
	for i := range 15 {
		shpOf(t, kps, "OP3", i, []string{"slide:total force [kN]"})
	}
	st, _ := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	st.Update(func(c *config.Config) error {
		c.FeaturesDir = feat
		p := c.Active()
		p.Sources, p.Ops = []string{press}, []string{"OP1"}
		c.Profiles = append(c.Profiles, config.Profile{ID: "kps", Name: "КПС", Kind: "press10", Sources: []string{kps}, Ops: []string{"OP3"}, Enabled: true})
		return nil
	})
	s, _ := New(press, time.UTC)
	s.UseConfig(st)
	clock := time.Now().Add(time.Hour) // файлы уже «старше 2 минут»
	s.feat.now = func() time.Time { return clock }
	s.feat.budget = 3 * time.Minute
	var order []string
	s.feat.onApply = func(op, phase string, n int) {
		order = append(order, fmt.Sprintf("%s %s %d", op, phase, n))
		clock = clock.Add(time.Minute) // каждая порция «читается» минуту
	}

	s.feat.Run()
	// 0–1 мин: пресс 10 новых; 1–2: КПС 10; 2–3: пресс ещё 20 (порция ≤ 25); 3 мин — время вышло
	if want := []string{"OP1 новые 10", "OP3 новые 10", "OP1 новые 20"}; !slices.Equal(order, want) {
		t.Fatalf("порядок: %v", order)
	}
	fs := s.feat.Status()
	if fs.Ops[0].Todo != 0 || fs.Ops[1].Todo != 5 || fs.Ops[1].Added != 10 || !s.feat.more {
		t.Fatalf("после прохода: пресс осталось %d, КПС осталось %d, добавлено %d, more=%v",
			fs.Ops[0].Todo, fs.Ops[1].Todo, fs.Ops[1].Added, s.feat.more)
	}
	order = nil
	s.feat.Run()
	if want := []string{"OP3 новые 5"}; !slices.Equal(order, want) || s.feat.more {
		t.Fatalf("второй проход: %v, more=%v", order, s.feat.more)
	}
}
