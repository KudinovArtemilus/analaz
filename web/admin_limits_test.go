package web

import (
	"net/url"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/press-go/internal/config"
)

// Паспортные пределы: задаются в «Настройках», доходят до диагноза, снимаются пустым полем.
func TestAdminLimits(t *testing.T) {
	s, st := adminServer(t)
	h := s.Handler()
	dir := t.TempDir()
	writeTable(t, dir) // высокое давление hp_min 274…274,4 бар
	st.Update(func(c *config.Config) error { c.FeaturesDir = dir; return nil })

	if rec := do(h, "GET", "/admin/limits?id=press10", nil, false, nil); rec.Code != 403 {
		t.Errorf("из сети: код %d", rec.Code)
	}
	b := strings.Join(strings.Fields(do(h, "GET", "/admin/limits?id=press10&q=высокое", nil, true, nil).Body.String()), " ")
	if !strings.Contains(b, "Гидростанция: давление: высокое давление, минимум, бар") || !strings.Contains(b, "встроенный: «проблема» ниже 255 бар") {
		t.Fatalf("таблица пределов: %.1200s", b)
	}
	if strings.Contains(b, "КПС") {
		t.Error("у пресса показаны показатели КПС")
	}

	hpUnit := func() (status string, facts []string) {
		var sum struct {
			Units []unitJSON `json:"units"`
		}
		get(t, h, "/api/analysis/OP1", 200, &sum)
		for _, u := range sum.Units {
			if u.Name == "Гидростанция: давление" {
				return u.Status, u.Facts
			}
		}
		return "", nil
	}
	if st, _ := hpUnit(); st != "ok" {
		t.Fatalf("без предела: %s", st)
	}

	save := func(min, max string) string {
		return do(h, "POST", "/admin/limit", url.Values{"id": {"press10"}, "feat": {"hp_min"}, "min": {min}, "max": {max},
			"note": {"паспорт, раздел 5"}}, true, nil).Body.String()
	}
	if b := save("280", "270"); !strings.Contains(b, "не меньше максимума") {
		t.Errorf("мин ≥ макс: %s", b)
	}
	if b := save("280,5", ""); !strings.Contains(b, "сохранено") {
		t.Fatalf("сохранение: %s", b)
	}
	if l := st.Get().Profile("press10").Limits["hp_min"]; l.Min == nil || *l.Min != 280.5 || l.Max != nil || l.Note != "паспорт, раздел 5" {
		t.Fatalf("в настройках: %+v", l)
	}
	status, facts := hpUnit()
	if status != "alarm" || len(facts) == 0 || !strings.Contains(strings.Join(facts, " "), "меньше паспортного минимума 280.5 бар (паспорт, раздел 5)") {
		t.Errorf("с пределом: %s %v", status, facts)
	}
	if b := save("", ""); !strings.Contains(b, "нет предела") {
		t.Errorf("снятие: %s", b)
	}
	if st, _ := hpUnit(); st != "ok" {
		t.Errorf("после снятия: %s", st)
	}
	if b := do(h, "POST", "/admin/limit", url.Values{"id": {"press10"}, "feat": {"rm -rf"}, "min": {"1"}}, true, nil).Body.String(); !strings.Contains(b, "неизвестный показатель") {
		t.Errorf("чужой признак: %s", b)
	}
}
