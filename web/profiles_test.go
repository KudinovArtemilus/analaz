package web

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/config"
	"github.com/KudinovArtemilus/press-go/internal/extract"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// shpOf — синтетический .shp операции op в buf (ГГГГ/ММ/ДД), цикл i — по 10 минут друг за другом.
// Разные циклы — разное число отсчётов, чтобы признаки отличались и анализ было из чего считать.
func shpOf(t *testing.T, buf, op string, i int, ana []string) string {
	t.Helper()
	tm := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * 10 * time.Minute)
	name := fmt.Sprintf("26.%06d.067000_%s_%s.shp", 100000+i, op, tm.Format("20060102_150405"))
	p := filepath.Join(buf, "2026", "11", "18", name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	data := shp.Synthetic([]string{"Y1", "Y2", "Y3", "Y4", "Y5", "Y6", "Y7", "Y8"}, ana, 40+i%7, 0)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(p, old, old)
	return p
}

// Сквозной К1: пресс и КПС в разных папках — у каждой машины свои признаки, вкладка анализа,
// циклы, архив и названия; одна операция в двух включённых профилях запрещена.
func TestTwoProfiles(t *testing.T) {
	press, kps, feat, data := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for i := range 30 {
		shpOf(t, press, "OP1", i, []string{"slide:total force [kN]", "hyd:high pressure [bar]"})
		shpOf(t, kps, "OP3", i, []string{"Vorformdurchmesser DV", "MRF_Force_F"})
	}
	st, err := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(c *config.Config) error {
		c.FeaturesDir, c.DataDir = feat, data
		c.Archive.KeepEvery = 5
		p := c.Active()
		p.Sources, p.Ops, p.TZ = []string{press}, []string{"OP1"}, 0
		c.Profiles = append(c.Profiles, config.Profile{ID: "kps", Name: "КПС", Kind: "kps", Sources: []string{kps},
			Ops: []string{"OP3"}, Enabled: true, Aliases: map[string]string{"MRF_Force_F": "Усилие главного валка"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s, _ := New(press, time.UTC)
	s.UseConfig(st)
	h := s.Handler()

	// --- признаки: обе машины, каждая в свою таблицу ---
	s.feat.Run()
	fs := s.feat.Status()
	if fs.Note != "" || len(fs.Ops) != 2 {
		t.Fatalf("признаки: %+v", fs)
	}
	for _, o := range fs.Ops {
		if o.Err != "" || o.Added != 30 {
			t.Fatalf("%s %s: %+v", o.Profile, o.Op, o)
		}
	}
	if fs.Ops[1].Profile != "КПС" || fs.Ops[1].Op != "OP3" {
		t.Fatalf("второй профиль: %+v", fs.Ops[1])
	}
	for _, name := range []string{"features_OP1.csv.gz", "features_OP3.csv.gz"} {
		if _, err := os.Stat(filepath.Join(feat, name)); err != nil {
			t.Fatal(err)
		}
	}

	// --- анализ: вкладка OP3, название машины, пометка про узлы ---
	body := do(h, "GET", "/analysis?op=OP3", nil, false, nil).Body.String()
	for _, want := range []string{`href="/analysis?op=OP1"`, `href="/analysis?op=OP3"`, "КПС OP3", "Узлы этой машины ещё не описаны"} {
		if !strings.Contains(body, want) {
			t.Errorf("анализ OP3: нет %q", want)
		}
	}
	if b := do(h, "GET", "/analysis?op=OP1", nil, false, nil).Body.String(); strings.Contains(b, "Узлы этой машины ещё не описаны") {
		t.Error("у пресса пометка про неописанные узлы")
	}

	// --- циклы: обе папки в списке, цикл КПС открывается по адресу @kps/… ---
	list := do(h, "GET", "/files?q=OP3", nil, false, nil).Body.String()
	if !strings.Contains(list, "/cycle?f=%40kps%2F2026%2F11%2F18%2F") || !strings.Contains(list, "показано 30 из 30") {
		t.Fatalf("список: %.400s", list)
	}
	rel := "@kps/2026/11/18/26.100007.067000_OP3_20261118_011000.shp"
	if c := do(h, "GET", "/cycle?f="+url.QueryEscape(rel), nil, false, nil); c.Code != http.StatusOK ||
		!strings.Contains(c.Body.String(), "Усилие главного валка") {
		t.Fatalf("цикл КПС: %d %.300s", c.Code, c.Body.String())
	}
	if c := do(h, "GET", "/cycle?f="+url.QueryEscape("@kps/../../etc/passwd.shp"), nil, false, nil); c.Code == http.StatusOK {
		t.Fatal("выход из папки через .. пропущен")
	}
	if c := do(h, "GET", "/cycle?f="+url.QueryEscape("@nope/2026/x.shp"), nil, false, nil); c.Code == http.StatusOK {
		t.Fatal("неизвестная папка пропущена")
	}
	// ссылка из анализа на цикл КПС ведёт в его папку
	if got := s.relData(filepath.Join(kps, "2026", "11", "18", "x_OP3_.shp")); got != "@kps/2026/11/18/x_OP3_.shp" {
		t.Fatalf("relData КПС: %q", got)
	}
	if got := s.relData(filepath.Join(press, "2026", "x.shp")); got != "2026/x.shp" {
		t.Fatalf("relData пресса (старые адреса не меняются): %q", got)
	}

	// --- архив: у каждого профиля свой; цикл КПС открывается из архива после удаления из буфера ---
	s.arch.Run()
	as := s.arch.Status()
	if as.Note != "" || len(as.Per) != 2 || as.NoRel != 0 || len(as.Errors) != 0 {
		t.Fatalf("архив: %+v", as)
	}
	ka := s.arch.forProfile("kps")
	if ka == nil || len(ka.Entries()) == 0 || !strings.HasSuffix(filepath.ToSlash(ka.Root), "archive/kps") {
		t.Fatalf("архив КПС: %+v", as.Per)
	}
	var saved string
	for _, e := range ka.Entries() {
		if !strings.Contains(e.Rel, "_OP3_") || e.Op != "OP3" {
			t.Fatalf("в архиве КПС чужой файл: %+v", e)
		}
		saved = e.Rel
	}
	if pa := s.arch.forProfile("press10"); pa == nil || pa.Has(saved) {
		t.Fatal("файл КПС попал в архив пресса")
	}
	os.Remove(filepath.Join(kps, filepath.FromSlash(saved)))
	if f, err := s.open("@kps/" + saved); err != nil || !strings.Contains(f.Path, "_OP3_") {
		t.Fatalf("цикл КПС из архива: %v", err)
	}

	// --- названия каналов: у профиля КПС — файл своей операции ---
	al := do(h, "GET", "/admin/aliases?id=kps", nil, true, nil).Body.String()
	if !strings.Contains(al, "Vorformdurchmesser DV") || strings.Contains(al, "slide:total force") {
		t.Fatalf("названия КПС: %.300s", al)
	}

	// --- одна операция в двух включённых профилях — отказ ---
	err = st.Update(func(c *config.Config) error {
		c.Profile("kps").Ops = []string{"OP3", "OP1"}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "OP1") {
		t.Fatalf("повтор OP1 пропущен: %v", err)
	}
}

// В общей папке разных машин «Названия каналов» берут свежий файл своей операции.
func TestNewestShpOfOp(t *testing.T) {
	buf := t.TempDir()
	p1 := shpOf(t, buf, "OP1", 1, []string{"slide:total force [kN]"})
	p3 := shpOf(t, buf, "OP3", 0, []string{"MRF_Force_F"})
	now := time.Now()
	os.Chtimes(p1, now, now) // OP1 свежее
	if _, path, err := newestShp([]string{buf}, []string{"OP3"}); err != nil || path != p3 {
		t.Fatalf("OP3: %s %v", path, err)
	}
	if _, path, _ := newestShp([]string{buf}, nil); path != p1 {
		t.Fatalf("без операций — самый свежий: %s", path)
	}
}

// Сначала новые файлы всех машин, потом история; во время прохода видно, что он идёт,
// а кнопка «Посчитать сейчас» не молчит.
func TestFeaturesFreshFirstAcrossMachines(t *testing.T) {
	press, kps, feat := t.TempDir(), t.TempDir(), t.TempDir()
	var pressFiles []string
	for i := range 10 {
		pressFiles = append(pressFiles, shpOf(t, press, "OP1", i, []string{"slide:total force [kN]"}))
	}
	for i := range 4 {
		shpOf(t, kps, "OP3", i, []string{"MRF_Force_F"})
	}
	// таблица пресса: посчитан только последний файл (9-й) — перед ним 9 пропусков истории
	one := t.TempDir()
	os.MkdirAll(filepath.Join(one, "d"), 0o755)
	b, _ := os.ReadFile(pressFiles[9])
	os.WriteFile(filepath.Join(one, "d", filepath.Base(pressFiles[9])), b, 0o644)
	if _, err := extract.Update(batch.NewTable(), filepath.Join(feat, "features_OP1.csv.gz"),
		extract.Options{Roots: []string{one}, Filter: batch.Filter{Op: "OP1"}, Jobs: 1, Loc: time.UTC}); err != nil {
		t.Fatal(err)
	}
	shpOf(t, press, "OP1", 10, []string{"slide:total force [kN]"}) // пришёл новый

	st, _ := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	st.Update(func(c *config.Config) error {
		c.FeaturesDir = feat
		p := c.Active()
		p.Sources, p.Ops = []string{press}, []string{"OP1"}
		c.Profiles = append(c.Profiles, config.Profile{ID: "kps", Name: "КПС", Kind: "kps", Sources: []string{kps}, Ops: []string{"OP3"}, Enabled: true})
		return nil
	})
	s, _ := New(press, time.UTC)
	s.UseConfig(st)
	s.feat.perPass = 3
	h := s.Handler()

	var order []string
	s.feat.onApply = func(op, phase string, n int) {
		order = append(order, fmt.Sprintf("%s %s %d", op, phase, n))
		fs := s.feat.Status()
		if !fs.Running || !strings.Contains(fs.Doing, op) || !strings.Contains(fs.Doing, phase) {
			t.Errorf("во время прохода статус: running=%v doing=%q", fs.Running, fs.Doing)
		}
		if rec := do(h, "POST", "/admin/features/run", nil, true, nil); !strings.Contains(rec.Header().Get("Location"), "feat-busy") {
			t.Errorf("кнопка во время прохода: %s", rec.Header().Get("Location"))
		}
	}
	if !s.feat.Run() {
		t.Fatal("проход не запустился")
	}
	// пресс: новый — 1 (10-й), история — в остаток лимита 3 − 1 = 2; КПС (новая машина): 3 из 4.
	// Новые КПС — раньше истории пресса.
	if want := []string{"OP1 новые 1", "OP3 новые 3", "OP1 история 2"}; !slices.Equal(order, want) {
		t.Fatalf("порядок фаз: %v", order)
	}
	fs := s.feat.Status()
	if fs.Running || fs.Doing != "" || fs.Ops[0].Added != 3 || fs.Ops[0].Todo != 7 || fs.Ops[1].Added != 3 || fs.Ops[1].Todo != 1 {
		t.Fatalf("после прохода: %+v", fs)
	}
	body := do(h, "GET", "/admin", nil, true, nil).Body.String()
	if !strings.Contains(body, "КПС · OP3:") || !strings.Contains(body, "осталось посчитать 7") {
		t.Errorf("Настройки: нет строк профилей")
	}
	if b := do(h, "GET", "/admin?saved=feat-busy", nil, true, nil).Body.String(); !strings.Contains(b, "уже идёт") {
		t.Error("нет сообщения «проход уже идёт»")
	}
}

// Список циклов: выбор машины. У машины, которая давно не писала файлов, циклы иначе
// тонут под тысячами более новых циклов другой машины (показываются 300 самых новых).
func TestFilesByMachine(t *testing.T) {
	press, kps := t.TempDir(), t.TempDir()
	for i := range 5 {
		shpOf(t, kps, "OP3", i, []string{"MRF_Force_F"}) // КПС — раньше
	}
	for i := 10; i < 320; i++ {
		shpOf(t, press, "OP1", i, []string{"slide:total force [kN]"}) // 310 более новых циклов пресса
	}
	st, _ := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	st.Update(func(c *config.Config) error {
		c.Active().Sources = []string{press}
		c.Profiles = append(c.Profiles, config.Profile{ID: "kps", Name: "КПС", Kind: "kps", Sources: []string{kps}, Ops: []string{"OP3"}, Enabled: true})
		return nil
	})
	s, _ := New(press, time.UTC)
	s.UseConfig(st)
	h := s.Handler()
	if b := do(h, "GET", "/files", nil, false, nil).Body.String(); strings.Contains(b, "_OP3_") || !strings.Contains(b, "показано 300 из 315") {
		t.Fatalf("все: OP3 должен утонуть под 300 новыми прессами (так и было) — %.200s", b)
	}
	b := do(h, "GET", "/files?m=kps", nil, false, nil).Body.String()
	if strings.Count(b, `class="op op-OP3"`) != 5 || strings.Contains(b, "_OP1_") || !strings.Contains(b, "показано 5 из 5") {
		t.Fatalf("КПС: %.300s", b)
	}
	// «показать ещё»: вторая страница — оставшиеся 15 (5 КПС + 10 самых старых прессов), без шапки «ничего не найдено»
	all := do(h, "GET", "/files", nil, false, nil).Body.String()
	if !strings.Contains(all, "показано 300 из 315") || !strings.Contains(all, `hx-get="/files?m=&amp;o=300&amp;q="`) {
		t.Fatalf("нет «показать ещё»: %.300s", all[len(all)-400:])
	}
	more := do(h, "GET", "/files?o=300", nil, false, nil).Body.String()
	if strings.Count(more, `class="file"`) != 15 || !strings.Contains(more, "показано 315 из 315") || strings.Contains(more, "показать ещё") {
		t.Fatalf("вторая страница: %.300s", more)
	}
	if l := do(h, "GET", "/", nil, false, nil).Body.String(); !strings.Contains(l, `<option value="kps">КПС</option>`) {
		t.Fatal("нет выбора машины")
	}
}

// К2б: таблица КПС посчитана старым сервером (без feat_ver и признаков kps_*). Новый сервер
// пересчитывает её циклы в «истории», без дублей; пресс (версия 0) не трогается.
func TestFeaturesRecomputeOldVersion(t *testing.T) {
	press, kps, feat := t.TempDir(), t.TempDir(), t.TempDir()
	var kpsFiles []string
	for i := range 6 {
		shpOf(t, press, "OP1", i, []string{"slide:total force [kN]"})
		kpsFiles = append(kpsFiles, shpOf(t, kps, "OP3", i, []string{"Vorformdurchmesser DV", "MRF_Force_F"}))
	}
	// «старый сервер»: признаки без feat_ver и без kps_* — как было до К2а
	old := batch.NewTable()
	for _, p := range kpsFiles {
		r := extract.Process(p, time.UTC)
		for k := range r.F {
			if strings.HasPrefix(k, "kps_") || k == "feat_ver" {
				delete(r.F, k)
			}
		}
		old.Add(r.File, r.Op, r.TStart, r.F)
	}
	target := filepath.Join(feat, "features_OP3.csv.gz")
	if err := old.WriteCSVGz(target); err != nil {
		t.Fatal(err)
	}

	st, _ := config.Open(filepath.Join(t.TempDir(), "pressgo.json"), config.Default())
	st.Update(func(c *config.Config) error {
		c.FeaturesDir = feat
		p := c.Active()
		p.Sources, p.Ops = []string{press}, []string{"OP1"}
		c.Profiles = append(c.Profiles, config.Profile{ID: "kps", Name: "КПС", Kind: "kps", Sources: []string{kps}, Ops: []string{"OP3"}, Enabled: true})
		return nil
	})
	s, _ := New(press, time.UTC)
	s.UseConfig(st)
	s.feat.perPass = 4
	var order []string
	s.feat.onApply = func(op, phase string, n int) { order = append(order, fmt.Sprintf("%s %s %d", op, phase, n)) }

	s.feat.Run() // пресс: 6 новых (лимит 4); КПС: новых нет, пересчёт 4 из 6 — в «истории», после новых
	if want := []string{"OP1 новые 4", "OP3 история 4"}; !slices.Equal(order, want) {
		t.Fatalf("порядок: %v", order)
	}
	op3 := s.feat.Status().Ops[1]
	if op3.Redone != 4 || op3.Added != 0 || op3.Rows != 6 || op3.Stale != 2 {
		t.Fatalf("первый проход: %+v", op3)
	}
	order = nil
	s.feat.Run() // пресс: оставшиеся 2 новых; КПС: оставшиеся 2 на пересчёт
	if want := []string{"OP1 новые 2", "OP3 история 2"}; !slices.Equal(order, want) {
		t.Fatalf("второй проход, порядок: %v", order)
	}
	op3 = s.feat.Status().Ops[1]
	if op3.Redone != 2 || op3.Stale != 0 || op3.Rows != 6 {
		t.Fatalf("второй проход: %+v", op3)
	}
	tb, _ := batch.ReadCSVGz(target)
	if tb.Len() != 6 || len(tb.Stale(1)) != 0 {
		t.Fatalf("после пересчёта: строк %d, старых %d", tb.Len(), len(tb.Stale(1)))
	}
	if j := slices.Index(tb.Columns(), "kps_mrf_force_max"); j < 0 || math.IsNaN(tb.Column(j)[0]) {
		t.Fatal("нет признаков КПС после пересчёта")
	}
	// пресс — версия 0: его циклы не пересчитываются; дальше — делать нечего
	order = nil
	s.feat.Run()
	s.feat.Run()
	if len(order) != 0 {
		t.Fatalf("лишняя работа: %v", order)
	}
	if b := do(s.Handler(), "GET", "/admin", nil, true, nil).Body.String(); strings.Contains(b, "пересчёт по новой версии") {
		t.Error("пересчёт закончен, а «Настройки» ещё его показывают")
	}
}
