package web

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestReportPeriod(t *testing.T) {
	now := time.Date(2026, 11, 25, 13, 48, 0, 0, time.UTC)
	q := func(kv ...string) map[string][]string {
		m := map[string][]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = []string{kv[i+1]}
		}
		return m
	}
	for _, c := range []struct {
		q        map[string][]string
		now      time.Time
		from, to string
	}{
		{q(), now, "25.11 08:00", "25.11 20:00"},                                                        // по умолчанию — смена сегодня
		{q("p", "shift"), now.Add(-7 * time.Hour), "24.11 08:00", "24.11 20:00"},                        // в 06:48 — ещё вчерашняя
		{q("p", "shift", "d", "2026-11-20"), now, "20.11 08:00", "20.11 20:00"},                         // смена за дату
		{q("p", "day", "d", "2026-11-20"), now, "20.11 00:00", "21.11 00:00"},                           // сутки
		{q("p", "week"), now, "18.11 13:48", "25.11 13:48"},                                             // 7 суток до сейчас
		{q("p", "month"), now, "26.10 13:48", "25.11 13:48"},                                            // 30 суток
		{q("p", "custom", "from", "2026-11-01", "to", "2026-11-03"), now, "01.11 00:00", "04.11 00:00"}, // «по» — включительно
	} {
		p, err := reportPeriodOf(c.q, c.now)
		if err != nil || p.From.Format("02.01 15:04") != c.from || p.To.Format("02.01 15:04") != c.to {
			t.Errorf("%v: %s — %s, %v; ждали %s — %s", c.q, p.From.Format("02.01 15:04"), p.To.Format("02.01 15:04"), err, c.from, c.to)
		}
	}
	if _, err := reportPeriodOf(q("p", "custom", "from", "2026-11-05", "to", "2026-11-01"), now); err == nil {
		t.Error("«по» раньше «с» — должна быть ошибка")
	}
	if _, err := reportPeriodOf(q("p", "day", "d", "25.11.2026"), now); err == nil {
		t.Error("дата не ГГГГ-ММ-ДД — должна быть ошибка")
	}
}

// TestReportPage — отчёт: итог, светофор, неисправности, работа и простои, что делает система.
func TestReportPage(t *testing.T) {
	dir := t.TempDir()
	writeTable(t, dir) // OP1: 80 циклов с 01.11 через 72 мин
	s, _ := New(t.TempDir(), time.UTC)
	s.EnableAnalysis(dir, dir)
	// пробелы между тегами убираем: IDE переформатирует шаблон, а проверяем мы содержимое, не отступы
	b := regexp.MustCompile(`>\s+<`).ReplaceAllString(page(s.Handler(), "/report?p=custom&from=2026-11-01&to=2026-11-02"), "><")
	for _, want := range []string{"Отчёт о состоянии оборудования", "01.11.2026 — 02.11.2026", "Составил", "Итог", "Светофор по машинам",
		"Найденные неисправности", "Работа и простои за период", "Что делает система", "Печать / PDF", "OP1",
		"регистратор молчит с"} {
		if !strings.Contains(b, want) {
			i := strings.Index(b, "<main")
			t.Fatalf("нет %q\n%.2000s", want, b[max(0, i):])
		}
	}
	// 01.11–02.11: циклы через 72 мин — каждый промежуток длиннее 30 мин: всё «без циклов»
	if !strings.Contains(b, "<td>OP1</td><td>0.0</td><td>48.0</td>") {
		i := strings.Index(b, "Работа и простои")
		t.Errorf("работа/простои: %.600s", b[i:])
	}
	if b := page(s.Handler(), "/report?p=day&d=2026-13-01"); !strings.Contains(b, "ждали ГГГГ-ММ-ДД") {
		t.Error("неверная дата — нет ошибки на странице")
	}
}
