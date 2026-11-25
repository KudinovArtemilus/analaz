package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/config"
	"github.com/KudinovArtemilus/press-go/internal/netio"
)

func adminServer(t *testing.T) (*Server, *config.Store) {
	t.Helper()
	st, err := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	if err != nil {
		t.Fatal(err)
	}
	s, _ := New(t.TempDir(), time.UTC)
	s.UseConfig(st)
	return s, st
}

// do — запрос «с этого компьютера» (127.0.0.1), если local, иначе — из сети.
func do(h http.Handler, method, target string, form url.Values, local bool, hdr map[string]string) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, target, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if local {
		r.RemoteAddr = "127.0.0.1:50000"
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestAdminOnlyLocal(t *testing.T) {
	s, _ := adminServer(t)
	h := s.Handler()
	if rec := do(h, "GET", "/admin", nil, false, nil); rec.Code != http.StatusForbidden {
		t.Errorf("из сети: код %d, ждали 403", rec.Code)
	}
	rec := do(h, "GET", "/admin", nil, true, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Пресс 10") || !strings.Contains(rec.Body.String(), "</html>") {
		t.Errorf("локально: код %d\n%.300s", rec.Code, rec.Body.String())
	}
}

func TestAdminSaveProfile(t *testing.T) {
	s, st := adminServer(t)
	h := s.Handler()
	form := url.Values{"id": {"press10"}, "name": {"Пресс 10"}, "kind": {"press10"}, "ops": {"op1, OP2"},
		"tz": {"5"}, "sources": {"\\\\recorder\\shp\r\n\r\nD:\\shp_copy\r\n"}, "enabled": {"on"}}

	// чужая страница не может менять настройки (CSRF)
	if rec := do(h, "POST", "/admin/profile", form, true, map[string]string{"Origin": "http://evil.example"}); rec.Code != http.StatusForbidden {
		t.Errorf("чужой Origin: код %d", rec.Code)
	}
	if rec := do(h, "POST", "/admin/profile", form, true, map[string]string{"Sec-Fetch-Site": "cross-site"}); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site: код %d", rec.Code)
	}
	if st.Get().Profile("press10").Sources != nil {
		t.Fatal("отклонённый запрос всё-таки изменил настройки")
	}

	rec := do(h, "POST", "/admin/profile", form, true, map[string]string{"Origin": "http://example.com"}) // httptest: Host = example.com
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("сохранение: код %d\n%s", rec.Code, rec.Body.String())
	}
	p := st.Get().Profile("press10")
	if len(p.Sources) != 2 || p.Sources[1] != `D:\shp_copy` || len(p.Ops) != 2 || p.Ops[0] != "OP1" {
		t.Errorf("профиль после сохранения: %+v", p)
	}

	// ошибка в форме — страница с ошибкой, настройки не меняются
	bad := url.Values{"id": {"press10"}, "name": {"Пресс 10"}, "ops": {"PRESS1"}, "tz": {"5"}}
	rec = do(h, "POST", "/admin/profile", bad, true, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Не сохранено") {
		t.Errorf("плохая форма: код %d", rec.Code)
	}
	if len(st.Get().Profile("press10").Sources) != 2 {
		t.Error("плохая форма изменила настройки")
	}
	// операция в русской раскладке («ОР4» — кириллические О и Р) сохраняется латиницей
	rus := url.Values{"id": {"press5"}, "name": {"Пресс 5"}, "kind": {"press5"}, "ops": {"ОР4"}, "tz": {"5"}, "ring": {"on"}}
	if rec := do(h, "POST", "/admin/profile", rus, true, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("ОР4: код %d\n%s", rec.Code, rec.Body.String())
	}
	if p := st.Get().Profile("press5"); p == nil || len(p.Ops) != 1 || p.Ops[0] != "OP4" || !p.Ring {
		t.Errorf("ОР4 → %+v", p)
	}
	// своя скорость чтения у профиля: сохраняется; пусто — общая; сервер профиля берёт свою
	rus.Set("net_mbps", "8")
	if rec := do(h, "POST", "/admin/profile", rus, true, nil); rec.Code != http.StatusSeeOther || st.Get().Profile("press5").NetMBps != 8 {
		t.Fatalf("скорость профиля: %d %v", rec.Code, st.Get().Profile("press5").NetMBps)
	}
	c := st.Get()
	c.Profile("press5").Sources = []string{`\\192.0.2.32\Datalogger`}
	c.Profile("press5").Enabled = true
	if r := c.NetRate("192.0.2.32", netio.Host); r != 8 {
		t.Errorf("скорость сервера пресса 5: %v, ждали 8", r)
	}
	if r := c.NetRate("192.0.2.42", netio.Host); r != c.NetMBps {
		t.Errorf("чужой сервер: %v, ждали общую %v", r, c.NetMBps)
	}
	rus.Set("net_mbps", "")
	do(h, "POST", "/admin/profile", rus, true, nil)
	if st.Get().Profile("press5").NetMBps != 0 {
		t.Error("пустое поле — должна стать общая (0)")
	}
}

func TestAdminAliasesAndLabels(t *testing.T) {
	s, st := adminServer(t)
	h := s.Handler()
	rec := do(h, "GET", "/admin/aliases?id=press10&q=md5", nil, true, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "casedrain_md5_mean") || strings.Contains(rec.Body.String(), "casedrain_md1_mean") {
		t.Errorf("поиск псевдонимов: код %d\n%.400s", rec.Code, rec.Body.String())
	}
	save := func(name, alias string) string {
		rec := do(h, "POST", "/admin/alias", url.Values{"id": {"press10"}, "name": {name}, "alias": {alias}}, true, nil)
		return rec.Body.String()
	}
	if b := save("casedrain_md5_mean", "Дренаж насоса 5"); !strings.Contains(b, "сохранено") {
		t.Errorf("сохранение псевдонима: %s", b)
	}
	save("Oildr:MD5 Casedrain Pressure", "Датчик дренажа 5")
	if got := s.human("casedrain_md5_mean"); got != "Дренаж насоса 5" {
		t.Errorf("human = %q", got)
	}
	if got := s.human("a066_Oildr_MD5_Casedrain_Pressure__max"); got != "Датчик дренажа 5 [max]" {
		t.Errorf("сырой признак = %q", got)
	}
	if got := s.label("hyd:high pressure [bar]"); got != "hyd:high pressure [bar]" {
		t.Errorf("без псевдонима = %q", got)
	}
	if b := save("casedrain_md5_mean", ""); !strings.Contains(b, "снято") || st.Get().Profile("press10").Aliases["casedrain_md5_mean"] != "" {
		t.Errorf("снятие псевдонима: %s", b)
	}
}

func TestAdminWithoutConfig(t *testing.T) {
	s, _ := New(t.TempDir(), time.UTC)
	rec := do(s.Handler(), "GET", "/admin", nil, true, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Настройки не подключены") {
		t.Errorf("без -config: %d %.200s", rec.Code, rec.Body.String())
	}
}

// Папку таблиц признаков меняют в админке — анализ появляется без перезапуска сервера.
func TestAnalysisFolderFromConfig(t *testing.T) {
	s, st := adminServer(t)
	h := s.Handler()
	rec := do(h, "GET", "/analysis", nil, false, nil)
	if !strings.Contains(rec.Body.String(), "не указана") {
		t.Errorf("без папки: ждали подсказку про настройки\n%.300s", rec.Body.String())
	}
	dir := t.TempDir()
	writeTable(t, dir)
	rec = do(h, "POST", "/admin/archive", url.Values{"keep_days": {"30"}, "keep_every": {"20"}, "max_gb": {"400"},
		"features_dir": {dir}, "data_dir": {`C:\press-go-data`}}, true, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("сохранение: %d %s", rec.Code, rec.Body.String())
	}
	if st.Get().FeaturesDir != dir {
		t.Fatal("папка не сохранилась")
	}
	// скорость чтения с сетевых папок: запятая — как в русской записи; 0 — нельзя (встанет всё)
	form := url.Values{"keep_days": {"30"}, "keep_every": {"20"}, "max_gb": {"400"}, "features_dir": {dir}, "data_dir": {`C:\press-go-data`}, "net_mbps": {"1,5"}}
	if rec := do(h, "POST", "/admin/archive", form, true, nil); rec.Code != http.StatusSeeOther || st.Get().NetMBps != 1.5 {
		t.Fatalf("скорость 1,5: код %d, сохранено %v", rec.Code, st.Get().NetMBps)
	}
	form.Set("net_mbps", "0")
	if rec := do(h, "POST", "/admin/archive", form, true, nil); rec.Code == http.StatusSeeOther || st.Get().NetMBps != 1.5 {
		t.Errorf("скорость 0 сохранилась: %d, %v", rec.Code, st.Get().NetMBps)
	}
	rec = do(h, "GET", "/analysis?op=OP1", nil, false, nil)
	if !strings.Contains(rec.Body.String(), "Пресс 10 OP1") {
		t.Errorf("после смены папки анализа нет\n%.300s", rec.Body.String())
	}
}

// Названия из «Настроек» доходят до отчёта за сутки: таблица «день против нормы»
// и аномалии суток. Анализ кладёт описание по умолчанию, страница подставляет своё.
func TestAliasesInDayReport(t *testing.T) {
	s, st := adminServer(t)
	dir := t.TempDir()
	writeTable(t, dir)
	if err := st.Update(func(c *config.Config) error {
		c.FeaturesDir = dir
		c.Profiles[0].SetAlias("hp_min", "Мин. давление ГС")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	rec := do(h, "GET", "/analysis/day?op=OP1&d=2026-11-02", nil, false, nil)
	if !strings.Contains(rec.Body.String(), "Мин. давление ГС") {
		t.Errorf("в таблице суток нет своего названия\n%.400s", rec.Body.String())
	}
	rec = do(h, "GET", "/api/analysis/OP1/day?d=2026-11-02", nil, false, nil)
	if !strings.Contains(rec.Body.String(), "Мин. давление ГС") {
		t.Errorf("в API суток нет своего названия")
	}
}
