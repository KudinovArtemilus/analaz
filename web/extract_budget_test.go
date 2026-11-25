package web

import (
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/config"
)

// TestFeaturesBudget — проход ограничен по времени: внутри машины у каждой операции своя доля,
// featFair новых — в любом случае; остаток — в следующем проходе, который начнётся скоро (more),
// а не через 5 минут. Операции одной машины (OP1, OP2 пресса 10) идут по очереди.
func TestFeaturesBudget(t *testing.T) {
	press, feat := t.TempDir(), t.TempDir()
	for i := range 30 {
		shpOf(t, press, "OP1", i, []string{"slide:total force [kN]"})
	}
	for i := range 15 {
		shpOf(t, press, "OP2", i, []string{"slide:total force [kN]"})
	}
	st, _ := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	st.Update(func(c *config.Config) error {
		c.FeaturesDir = feat
		p := c.Active()
		p.Sources, p.Ops = []string{press}, []string{"OP1", "OP2"}
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
	// доля OP1 — половина из 3 мин: 0–1 мин 10 новых в любом случае, 1–2 — ещё порция (1 < 1,5);
	// доля OP2 — остаток: 2–3 мин свои 10 новых в любом случае; 3 мин — время вышло
	if want := []string{"OP1 новые 10", "OP1 новые 10", "OP2 новые 10"}; !slices.Equal(order, want) {
		t.Fatalf("порядок: %v", order)
	}
	fs := s.feat.Status()
	if fs.Ops[0].Todo != 10 || fs.Ops[1].Todo != 5 || fs.Ops[0].Added != 20 || fs.Ops[1].Added != 10 || !s.feat.more {
		t.Fatalf("после прохода: OP1 осталось %d, OP2 осталось %d, добавлено %d, more=%v",
			fs.Ops[0].Todo, fs.Ops[1].Todo, fs.Ops[1].Added, s.feat.more)
	}
	order = nil
	s.feat.Run()
	if want := []string{"OP1 новые 10", "OP2 новые 5"}; !slices.Equal(order, want) || s.feat.more {
		t.Fatalf("второй проход: %v, more=%v", order, s.feat.more)
	}
}

// TestFeaturesMachinesInParallel — машины (профили) считаются одновременно: пока пресс читает
// свой файл, КПС не ждёт. Подсчёт пресса стоит, пока не начнёт считаться КПС, — в один поток
// проход бы здесь завис (защита — таймаут).
func TestFeaturesMachinesInParallel(t *testing.T) {
	press, kps, feat := t.TempDir(), t.TempDir(), t.TempDir()
	for i := range 3 {
		shpOf(t, press, "OP1", i, []string{"slide:total force [kN]"})
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
	kpsStarted := make(chan struct{})
	var once sync.Once
	parallel := true
	s.feat.onApply = func(op, phase string, n int) {
		switch op {
		case "OP3":
			once.Do(func() { close(kpsStarted) })
		case "OP1":
			select {
			case <-kpsStarted:
			case <-time.After(5 * time.Second):
				parallel = false
			}
		}
	}
	s.feat.Run()
	if !parallel {
		t.Fatal("пресс ждал, пока КПС закончит: машины считаются не одновременно")
	}
	if fs := s.feat.Status(); fs.Ops[0].Added != 3 || fs.Ops[1].Added != 3 || fs.Running {
		t.Fatalf("после прохода: %+v", fs.Ops)
	}
}

// TestFeaturesTableIO — таблица операции читается и записывается раз за долю машины, а не
// после каждой порции: у пресса она — десятки МБ.
func TestFeaturesTableIO(t *testing.T) {
	press, feat := t.TempDir(), t.TempDir()
	for i := range 35 {
		shpOf(t, press, "OP1", i, []string{"slide:total force [kN]"})
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
	target := filepath.Join(feat, "features_OP1.csv.gz")
	saves := 0
	var last time.Time
	s.feat.onApply = func(op, phase string, n int) {
		if m := modTime(target); !m.Equal(last) {
			saves++
			last = m
		}
	}
	s.feat.Run() // 10 в любом случае + 25 порциями по 10 — одна запись таблицы в конце
	if fs := s.feat.Status(); fs.Ops[0].Added != 35 || fs.Ops[0].Rows != 35 {
		t.Fatalf("%+v", fs.Ops[0])
	}
	// между порциями таблица на диск не писалась
	if saves != 0 {
		t.Errorf("записей таблицы между порциями: %d, ждали 0", saves)
	}
	tb, err := batch.ReadCSVGz(target)
	if err != nil || tb.Len() != 35 {
		t.Fatalf("на диске %v строк, %v", tbLen(tb), err)
	}
}
