package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
	"github.com/KudinovArtemilus/press-go/internal/config"
	"github.com/KudinovArtemilus/press-go/internal/netio"
	"github.com/KudinovArtemilus/press-go/internal/ring"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// Админка: профили машин, папки-источники, псевдонимы каналов. Пароля нет — поэтому она
// открывается ТОЛЬКО с самого компьютера, где работает press-go (localOnly), а изменения
// принимаются только со своих же страниц (sameOrigin).

// UseConfig подключает настройки: профили, псевдонимы, админку — и анализ по папке из настроек.
// Папку таблиц признаков можно поменять в админке, сервер подхватит её без перезапуска.
// (Если после этого вызвать EnableAnalysis с явной папкой — она главнее.)
func (s *Server) UseConfig(st *config.Store) {
	s.cfg = st
	netio.SetRate(func(host string) float64 { return st.Get().NetRate(host, netio.Host) }) // меняется в «Настройках» — действует сразу
	s.arch = newArchiveRunner(s)
	s.feat = newFeatRunner(s)
	s.ring = newRingRunner(s)
	s.an = &analysisStore{cache: map[string]*analysisEntry{}, dirs: func() (string, string) {
		c := st.Get()
		return c.FeaturesDir, c.NormsDir
	}}
}

// findLabel — своё название из включённых профилей по порядку ("" — нигде не задано).
// Операция в местах вызова неизвестна, поэтому смотрим все профили: каналы разных машин
// называются по-разному (slide:total force [kN] у пресса, Vorformdurchmesser DV у КПС).
// Если у двух машин совпадёт имя канала с разными своими названиями — берётся первый профиль.
func (s *Server) findLabel(name string) string {
	if s.cfg == nil {
		return ""
	}
	for _, p := range s.cfg.Get().EnabledProfiles() {
		if a := p.Label(name); a != "" {
			return a
		}
	}
	return ""
}

// label — своё название канала, если задано, иначе исходное.
func (s *Server) label(name string) string {
	if a := s.findLabel(name); a != "" {
		return a
	}
	return name
}

// alias — только псевдоним ("" — не задан); для мест, где исходное имя показывается рядом.
func (s *Server) alias(name string) string { return s.findLabel(name) }

// human — название признака для страниц: псевдоним или описание из анализа.
func (s *Server) human(f string) string {
	if a := s.findLabel(f); a != "" {
		return a
	}
	return analyze.Human(f)
}

// localOnly пропускает только запросы с этого же компьютера (127.0.0.1, ::1).
// Если сервер слушает :8080 для всей сети, анализ открыт всем, а админка — нет.
func localOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			http.Error(w, "админка открывается только на компьютере, где запущен press-go", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// sameOrigin — защита от CSRF. Без пароля любая открытая в браузере вредная страница могла бы
// отправить форму на http://localhost:8080/admin. Браузер сам пишет, откуда запрос (Origin /
// Sec-Fetch-Site), подделать это из чужой страницы нельзя.
func sameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "запрос с чужой страницы отклонён", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			if u, err := url.Parse(o); err != nil || u.Host != r.Host {
				http.Error(w, "запрос с чужой страницы отклонён", http.StatusForbidden)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) adminRoutes(mux *http.ServeMux) {
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return localOnly(func(w http.ResponseWriter, r *http.Request) {
			if s.cfg == nil {
				s.render(w, "admin.html", map[string]any{"NoConfig": true})
				return
			}
			h(w, r)
		})
	}
	mux.HandleFunc("GET /admin", guard(s.adminPage))
	mux.HandleFunc("POST /admin/profile", guard(sameOrigin(s.adminSaveProfile)))
	mux.HandleFunc("POST /admin/archive", guard(sameOrigin(s.adminSaveArchive)))
	mux.HandleFunc("POST /admin/archive/run", guard(sameOrigin(s.adminArchiveRun)))
	mux.HandleFunc("POST /admin/features/run", guard(sameOrigin(s.adminFeatRun)))
	mux.HandleFunc("GET /admin/check", guard(s.adminCheck))
	mux.HandleFunc("GET /admin/aliases", guard(s.adminAliases))
	mux.HandleFunc("POST /admin/alias", guard(sameOrigin(s.adminSaveAlias)))
}

// adminError — страница админки с ошибкой сверху; введённое не сохранено.
func (s *Server) adminError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusBadRequest)
	s.render(w, "admin.html", map[string]any{"C": s.cfg.Get(), "Path": s.cfg.Path(), "Error": err.Error()})
}

// inlineError — ошибка рядом с полем. Код 200: HTMX по умолчанию не показывает ответы 4xx.
func inlineError(w http.ResponseWriter, err error) {
	fmt.Fprintf(w, `<span class="error small">%s</span>`, template.HTMLEscapeString(err.Error()))
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	c := s.cfg.Get()
	rings := map[string]*ringStatus{}
	for _, p := range c.Profiles {
		if st := s.ring.Status(p.ID); st != nil {
			rings[p.ID] = st
		}
	}
	s.render(w, "admin.html", map[string]any{"C": c, "Path": s.cfg.Path(), "Saved": r.URL.Query().Get("saved"), "Arch": s.arch.Status(), "Feat": s.feat.Status(),
		"Ring": rings})
}

// latinOp — операции в верхнем регистре и латиницей: «ор4» и «ОП4», набранные в русской
// раскладке, становятся OP4 (в именах файлов регистратора операция всегда латиницей).
func latinOp(s string) string {
	return strings.NewReplacer("О", "O", "Р", "P", "П", "P").Replace(strings.ToUpper(s))
}

// adminSaveProfile — сохранить профиль из формы (или добавить новый, если id ещё нет).
func (s *Server) adminSaveProfile(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f := r.PostForm
	id := strings.TrimSpace(f.Get("id"))
	tz, err := strconv.Atoi(strings.TrimSpace(f.Get("tz")))
	if err != nil {
		s.adminError(w, fmt.Errorf("часовой пояс: ждали число, получили %q", f.Get("tz")))
		return
	}
	err = s.cfg.Update(func(c *config.Config) error {
		p := c.Profile(id)
		if p == nil {
			c.Profiles = append(c.Profiles, config.Profile{ID: id, Aliases: map[string]string{}})
			p = &c.Profiles[len(c.Profiles)-1]
		}
		p.Name = strings.TrimSpace(f.Get("name"))
		p.Kind = strings.TrimSpace(f.Get("kind"))
		p.Sources = lines(f.Get("sources"))
		p.Ops = nil
		for _, op := range strings.FieldsFunc(latinOp(f.Get("ops")), func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
			p.Ops = append(p.Ops, op)
		}
		p.TZ = tz
		p.Enabled = f.Get("enabled") == "on"
		p.Ring = f.Get("ring") == "on"
		p.NetMBps = 0
		if v := strings.TrimSpace(strings.ReplaceAll(f.Get("net_mbps"), ",", ".")); v != "" {
			x, err := strconv.ParseFloat(v, 64)
			if err != nil || x < 0.1 || x > 1000 {
				return fmt.Errorf("скорость чтения профиля: МБ/с от 0,1 до 1000 (пусто — общая), получили %q", v)
			}
			p.NetMBps = x
		}
		return nil
	})
	if err != nil {
		s.adminError(w, err)
		return
	}
	log.Printf("админка: сохранён профиль %s", id)
	// после POST — переход GET-ом: обновление страницы (F5) не отправит форму повторно
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape(id), http.StatusSeeOther)
}

func (s *Server) adminSaveArchive(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f := r.PostForm
	days, e1 := strconv.Atoi(f.Get("keep_days"))
	every, e2 := strconv.Atoi(f.Get("keep_every"))
	gb, e3 := strconv.ParseFloat(strings.ReplaceAll(f.Get("max_gb"), ",", "."), 64)
	if err := errors.Join(e1, e2, e3); err != nil {
		s.adminError(w, fmt.Errorf("архив: ждали числа: %w", err))
		return
	}
	err := s.cfg.Update(func(c *config.Config) error {
		ringDays := c.Archive.RingDays
		if v := strings.TrimSpace(f.Get("ring_days")); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return fmt.Errorf("копии кольцевого буфера: дней — целое ≥ 1, получили %q", v)
			}
			ringDays = n
		}
		if v := strings.TrimSpace(strings.ReplaceAll(f.Get("net_mbps"), ",", ".")); v != "" {
			x, err := strconv.ParseFloat(v, 64)
			if err != nil || x < 0.1 || x > 1000 {
				return fmt.Errorf("скорость чтения с сетевой папки: МБ/с от 0,1 до 1000, получили %q", v)
			}
			c.NetMBps = x
		}
		c.Archive = config.Archive{KeepDays: days, KeepEvery: every, MaxGB: gb, RingDays: ringDays}
		c.FeaturesDir = strings.TrimSpace(f.Get("features_dir"))
		c.ReportBy = strings.TrimSpace(f.Get("report_by"))
		c.ReportRole = strings.TrimSpace(f.Get("report_role"))
		c.DataDir = strings.TrimSpace(f.Get("data_dir"))
		return nil
	})
	if err != nil {
		s.adminError(w, err)
		return
	}
	log.Printf("админка: сохранены архив и папки")
	http.Redirect(w, r, "/admin?saved=archive", http.StatusSeeOther)
}

type sourceCheck struct {
	Path   string
	OK     bool
	Error  string
	Files  int  // сколько .shp нашлось (не больше лимита)
	More   bool // нашлось больше лимита — дальше не считали
	Ring   int  // из них без времени в имени (old01.shp…) — похоже на кольцевой буфер
	Newest string
	Took   string
}

// adminCheck — доступны ли папки профиля и есть ли в них .shp. Считает не дольше 5 секунд
// и не больше 20 000 файлов: на сетевой папке полный обход может идти минутами.
func (s *Server) adminCheck(w http.ResponseWriter, r *http.Request) {
	p := s.cfg.Get().Profile(r.URL.Query().Get("id"))
	if p == nil {
		s.fail(w, fmt.Errorf("нет профиля %q", r.URL.Query().Get("id")))
		return
	}
	var out []sourceCheck
	for _, src := range p.Sources {
		out = append(out, checkSource(r.Context(), src))
	}
	s.render(w, "admin_check.html", map[string]any{"Checks": out, "Ring": p.Ring})
}

const checkLimit = 20000

var errEnough = errors.New("хватит")

func checkSource(ctx context.Context, dir string) sourceCheck {
	c := sourceCheck{Path: dir}
	t0 := time.Now()
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		c.Error = "папка недоступна"
		if err != nil {
			c.Error += ": " + err.Error()
		}
		return c
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var newest time.Time
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // нет доступа к подпапке — идём дальше
		}
		if ctx.Err() != nil || c.Files >= checkLimit {
			c.More = true
			return errEnough
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".shp") {
			c.Files++
			if ring.IsRingName(d.Name()) {
				c.Ring++
			}
			if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
				newest = info.ModTime()
				c.Newest = filepath.Base(p) + " · " + info.ModTime().Format("02.01.2006 15:04")
			}
		}
		return nil
	})
	c.OK = c.Files > 0
	if !c.OK && c.Error == "" {
		c.Error = "папка есть, но .shp в ней не найдено"
	}
	c.Took = time.Since(t0).Round(10 * time.Millisecond).String()
	return c
}

type aliasRow struct {
	Name, Alias, Default, Kind, Group string
}

// adminAliases — таблица «исходное имя → своё название»: каналы из самого свежего .shp профиля
// и инженерные признаки анализа. Поиск по любому из названий.
func (s *Server) adminAliases(w http.ResponseWriter, r *http.Request) {
	c := s.cfg.Get()
	p := c.Profile(r.URL.Query().Get("id"))
	if p == nil {
		p = c.Active()
	}
	if p == nil {
		s.fail(w, errors.New("нет ни одного профиля"))
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	var rows []aliasRow
	note := ""
	if f, path, err := newestShp(c.Dirs(p), p.Ops); err == nil { // у кольца — по копиям: в самом кольце операции в имени нет
		note = "каналы из файла " + filepath.Base(path)
		for _, name := range f.AnaNames {
			if spare.MatchString(name) {
				continue
			}
			rows = append(rows, aliasRow{name, p.Aliases[name], "", "канал", groupName(name)})
		}
		for _, name := range f.DigNames {
			if strings.TrimSpace(name) != "" && !spare.MatchString(name) {
				rows = append(rows, aliasRow{name, p.Aliases[name], "", "дискретный", groupName(name)})
			}
		}
	} else {
		note = "каналы не прочитаны: " + err.Error()
	}
	var feats []string
	for k := range analyze.Descs {
		feats = append(feats, k)
	}
	sort.Strings(feats)
	for _, k := range feats {
		rows = append(rows, aliasRow{k, p.Aliases[k], analyze.Human(k), "признак", analyze.Subsystem(k)})
	}
	if q != "" {
		rows = slices.DeleteFunc(rows, func(a aliasRow) bool {
			return !strings.Contains(strings.ToLower(a.Name+" "+a.Alias+" "+a.Default+" "+a.Group), q)
		})
	}
	total := len(rows)
	if len(rows) > 400 {
		rows = rows[:400]
	}
	s.render(w, "admin_aliases.html", map[string]any{"P": p, "Rows": rows, "Note": note, "Total": total, "Q": q})
}

// adminSaveAlias — одно поле таблицы псевдонимов (HTMX шлёт при уходе из поля).
func (s *Server) adminSaveAlias(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, name, alias := r.PostForm.Get("id"), r.PostForm.Get("name"), r.PostForm.Get("alias")
	if name == "" || len([]rune(alias)) > 120 {
		inlineError(w, errors.New("пустое имя или название длиннее 120 символов"))
		return
	}
	err := s.cfg.Update(func(c *config.Config) error {
		p := c.Profile(id)
		if p == nil {
			return fmt.Errorf("нет профиля %q", id)
		}
		p.SetAlias(name, alias)
		return nil
	})
	if err != nil {
		inlineError(w, err)
		return
	}
	if strings.TrimSpace(alias) == "" {
		fmt.Fprint(w, `<span class="muted small">снято</span>`)
		return
	}
	fmt.Fprint(w, `<span class="muted small">сохранено</span>`)
}

// newestShp — самый свежий .shp в папках профиля (по времени изменения), прочитанный.
// Ищем быстро: в каждой папке спускаемся по самым «поздним» подпапкам (ГГГГ\ММ\ДД).
func newestShp(sources, ops []string) (*shp.File, string, error) {
	var best string
	var bestT time.Time
	ofOp := func(name string) bool { // файл своей операции: в общей папке могут лежать и чужие
		if len(ops) == 0 {
			return true
		}
		up := strings.ToUpper(name)
		for _, op := range ops {
			if strings.Contains(up, "_"+strings.ToUpper(op)+"_") {
				return true
			}
		}
		return false
	}
	for _, src := range sources {
		dir := src
		for depth := 0; depth < 6; depth++ {
			ents, err := os.ReadDir(dir)
			if err != nil {
				break
			}
			var lastDir string
			for _, e := range ents {
				p := filepath.Join(dir, e.Name())
				if e.IsDir() {
					if e.Name() > filepath.Base(lastDir) || lastDir == "" {
						lastDir = p
					}
					continue
				}
				if strings.EqualFold(filepath.Ext(p), ".shp") && ofOp(e.Name()) {
					if info, err := e.Info(); err == nil && info.ModTime().After(bestT) {
						best, bestT = p, info.ModTime()
					}
				}
			}
			if best != "" || lastDir == "" {
				break
			}
			dir = lastDir
		}
	}
	if best == "" {
		return nil, "", errors.New("в папках профиля нет .shp")
	}
	f, err := shp.Read(best)
	return f, best, err
}

// lines — непустые строки текста (папки по одной на строку).
func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
