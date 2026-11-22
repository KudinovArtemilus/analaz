package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesDefaultAndUpdateIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pressgo.json")
	s, err := Open(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("файл настроек не создан: %v", err)
	}
	// плохая правка не должна ничего поменять ни в памяти, ни в файле
	before, _ := os.ReadFile(path)
	err = s.Update(func(c *Config) error { c.Profiles[0].ID = "Пресс 10"; return nil })
	if err == nil {
		t.Fatal("id с пробелом и кириллицей должен быть отклонён")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || s.Get().Profiles[0].ID != "press10" {
		t.Errorf("отклонённая правка изменила настройки")
	}
	// хорошая правка — сохраняется, прежняя версия в .bak
	if err := s.Update(func(c *Config) error {
		c.Profiles[0].Sources = []string{`\\recorder\shp`}
		c.Profiles[0].SetAlias("slide:total force [kN]", "Усилие ползуна")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("нет .bak: %v", err)
	}
	s2, err := Open(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	p := s2.Get().Profile("press10")
	if p.Sources[0] != `\\recorder\shp` || p.Aliases["slide:total force [kN]"] != "Усилие ползуна" {
		t.Errorf("после перечитывания: %+v", p)
	}
}

func TestLabel(t *testing.T) {
	p := &Profile{}
	p.SetAlias("Oildr:MD5 Casedrain Pressure", "Дренаж насоса 5")
	p.SetAlias("casedrain_md1_mean", "Дренаж MD1, среднее")
	p.SetAlias("hp_min", "   ") // пустое — значит «снять псевдоним»
	for in, want := range map[string]string{
		"Oildr:MD5 Casedrain Pressure":            "Дренаж насоса 5",
		"a066_Oildr_MD5_Casedrain_Pressure__mean": "Дренаж насоса 5 [mean]", // сырой признак → канал
		"casedrain_md1_mean":                      "Дренаж MD1, среднее",
		"hp_min":                                  "",
		"a067_Oildr_MD6_Casedrain_Pressure__max":  "",
	} {
		if got := p.Label(in); got != want {
			t.Errorf("Label(%q) = %q, ждали %q", in, got, want)
		}
	}
	if len(p.Aliases) != 2 {
		t.Errorf("псевдонимов %d, ждали 2", len(p.Aliases))
	}
	var nilP *Profile
	if nilP.Label("x") != "" {
		t.Error("nil-профиль")
	}
}

func TestValidate(t *testing.T) {
	for name, bad := range map[string]func(c *Config){
		"повтор id":     func(c *Config) { c.Profiles = append(c.Profiles, c.Profiles[0]) },
		"пустое имя":    func(c *Config) { c.Profiles[0].Name = " " },
		"операция":      func(c *Config) { c.Profiles[0].Ops = []string{"ОП1"} },
		"часовой пояс":  func(c *Config) { c.Profiles[0].TZ = 20 },
		"архив −1 день": func(c *Config) { c.Archive.KeepDays = -1 }, // 0 можно: «только отобранное»
		"фон 0":         func(c *Config) { c.Archive.KeepEvery = 0 },
	} {
		c := Default()
		bad(c)
		if c.Validate() == nil {
			t.Errorf("%s: ошибка не найдена", name)
		}
	}
	if err := Default().Validate(); err != nil {
		t.Errorf("настройки по умолчанию: %v", err)
	}
	c := Default()
	c.Archive.KeepDays = 0
	if err := c.Validate(); err != nil {
		t.Errorf("0 дней — только отобранное, это допустимо: %v", err)
	}
}

func TestProfilesAndOpOwner(t *testing.T) {
	c := Default()
	c.Profiles = append(c.Profiles, Profile{ID: "kps", Name: "КПС", Kind: "kps", Ops: []string{"OP3"}, TZ: 5, Enabled: true})
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if ps := c.EnabledProfiles(); len(ps) != 2 || ps[1].ID != "kps" {
		t.Fatalf("включённые: %v", ps)
	}
	if p := c.ProfileFor("op3"); p == nil || p.ID != "kps" {
		t.Fatalf("OP3 → %v", p)
	}
	if p := c.ProfileFor("OP1"); p == nil || p.ID != "press10" {
		t.Fatalf("OP1 → %v", p)
	}
	if c.ProfileFor("OP9") != nil {
		t.Fatal("OP9 ничей")
	}
	// одна операция в двух включённых профилях — нельзя; в выключенном — можно
	c.Profiles[1].Ops = []string{"OP3", "OP2"}
	if c.Validate() == nil {
		t.Fatal("OP2 в двух включённых профилях пропущена")
	}
	c.Profiles[1].Enabled = false
	if err := c.Validate(); err != nil {
		t.Fatalf("в выключенном профиле повтор допустим: %v", err)
	}
	if len(c.EnabledProfiles()) != 1 {
		t.Fatal("выключенный профиль в списке")
	}
}
