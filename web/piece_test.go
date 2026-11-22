package web

import (
	"strings"
	"testing"
	"time"
)

func TestPiecePage(t *testing.T) {
	dir := t.TempDir()
	writeTable(t, dir)
	s, err := New(t.TempDir(), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnableAnalysis(dir, ""); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	body := func(url string) string {
		t.Helper()
		rec := do(h, "GET", url, nil, false, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: код %d", url, rec.Code)
		}
		return rec.Body.String()
	}
	// заготовка 100042 — 43-й цикл OP1, плавка 504
	b := body("/piece?n=100042")
	for _, want := range []string{"Заготовка 100042", `href="/piece?h=504"`, "этот цикл", "норма"} {
		if !strings.Contains(b, want) {
			t.Errorf("паспорт: нет %q", want)
		}
	}
	// плавка 504 — 10 заготовок (100040…100049), у каждой есть цикл на OP1
	b = body("/piece?h=504")
	if !strings.Contains(b, "Плавка 504: заготовок 10") || strings.Count(b, `href="/piece?n=1000`) != 10 {
		t.Errorf("плавка: %.400s", b[strings.Index(b, "Плавка"):])
	}
	if b := body("/piece?n=999"); !strings.Contains(b, "цикла этой заготовки в таблице признаков нет") {
		t.Errorf("нет такой заготовки — должно быть сказано прямо")
	}
	if b := body("/piece?n=abc"); !strings.Contains(b, "целое число") {
		t.Errorf("мусор в номере")
	}
	if b := body("/piece"); !strings.Contains(b, `name="n"`) {
		t.Errorf("пустая страница без формы поиска")
	}
}
