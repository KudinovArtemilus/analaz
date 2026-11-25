package web

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
)

// Отчёт для руководства (И6): одна страница выводов за период — смена 08:00–20:00, сутки,
// неделя, месяц или свои даты; печатается в PDF кнопкой браузера «Печать → Сохранить как PDF».
// Без графиков и σ: светофор по машинам, что найдено и что сделать, работа и простои, что
// умеет система. Всё — из того же анализа, что страница «Анализ».

const (
	reportPause    = 30 * time.Minute // пауза между циклами дольше — простой (как порог остановов анализа)
	reportPauses   = 5                // самых долгих простоев в отчёте
	reportEvents   = 12               // изменений из журнала
	reportSilentAt = 2 * time.Hour    // последний цикл старше — «регистратор молчит»
)

type reportPeriod struct {
	Kind, Label string
	From, To    time.Time // в «настенном» времени, как время циклов в таблице
	Day         string    // дата для смены/суток (поле формы)
}

// reportPeriodOf — период из запроса: p=shift|day|week|month|custom, d=ГГГГ-ММ-ДД, from/to.
func reportPeriodOf(q map[string][]string, now time.Time) (reportPeriod, error) {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
		return ""
	}
	day := func(s string, def time.Time) (time.Time, error) {
		if s == "" {
			return def, nil
		}
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return t, fmt.Errorf("дата %q: ждали ГГГГ-ММ-ДД", s)
		}
		return t, nil
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	p := reportPeriod{Kind: get("p")}
	switch p.Kind {
	case "", "shift":
		p.Kind = "shift"
		def := today
		if now.Hour() < 8 { // до начала смены — вчерашняя
			def = today.AddDate(0, 0, -1)
		}
		d, err := day(get("d"), def)
		if err != nil {
			return p, err
		}
		p.From, p.To, p.Day = d.Add(8*time.Hour), d.Add(20*time.Hour), d.Format("2006-01-02")
		p.Label = "смена " + d.Format("02.01.2006") + " 08:00–20:00"
	case "day":
		d, err := day(get("d"), today)
		if err != nil {
			return p, err
		}
		p.From, p.To, p.Day = d, d.AddDate(0, 0, 1), d.Format("2006-01-02")
		p.Label = "сутки " + d.Format("02.01.2006")
	case "week", "month":
		n := 7
		if p.Kind == "month" {
			n = 30
		}
		p.From, p.To = now.AddDate(0, 0, -n), now
		p.Label = fmt.Sprintf("%d суток: %s — %s", n, p.From.Format("02.01.2006 15:04"), p.To.Format("02.01.2006 15:04"))
	case "custom":
		f, err := day(get("from"), today.AddDate(0, 0, -7))
		if err != nil {
			return p, err
		}
		t, err := day(get("to"), today)
		if err != nil {
			return p, err
		}
		if t.Before(f) {
			return p, fmt.Errorf("«по» раньше, чем «с»")
		}
		p.From, p.To = f, t.AddDate(0, 0, 1)
		p.Label = f.Format("02.01.2006") + " — " + t.Format("02.01.2006")
	default:
		return p, fmt.Errorf("период %q: ждали shift, day, week, month или custom", p.Kind)
	}
	return p, nil
}

type reportUnit struct {
	Name, Status, Label, Fact, Check string
}

type reportGap struct {
	From, To, Dur string
}

type reportMachine struct {
	Name, Op, View          string // машина, операция, что оценивалось (рецепт / колесо)
	Err                     string
	OK, Warn, Alarm         int
	Units                   []reportUnit // проблема и «наблюдать»: то, о чём надо сказать
	Lost                    []string     // пропавшие сигналы
	Cycles, Prod            int          // за период
	WorkH, IdleH            float64      // в работе / простой за период, ч
	Pauses                  []reportGap
	Events                  []journalView
	Last                    string // последний цикл
	Silent                  string // «регистратор молчит с …»
	Damaged                 int    // повреждённых файлов за всё время таблицы
	StatusWord, StatusClass string
}

// report — страница отчёта.
func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	now := wallNow()
	p, err := reportPeriodOf(r.URL.Query(), now)
	data := map[string]any{"Data": s.rootsLabel(), "P": p, "Made": now.Format("02.01.2006 15:04"),
		"From": r.URL.Query().Get("from"), "To": r.URL.Query().Get("to")}
	if s.cfg != nil {
		c := s.cfg.Get()
		data["By"], data["Role"] = c.ReportBy, c.ReportRole
	}
	if err != nil {
		data["Error"] = err.Error()
		s.render(w, "report.html", data)
		return
	}
	if s.an == nil {
		data["Error"] = "анализ не включён: укажите папку таблиц признаков в «Настройках»"
		s.render(w, "report.html", data)
		return
	}
	var ms []reportMachine
	for _, m := range s.reportOps() {
		ms = append(ms, s.reportMachine(m[0], m[1], p, now))
	}
	alarms, warns := 0, 0
	var attention []string
	for _, m := range ms {
		alarms += m.Alarm
		warns += m.Warn
		for _, u := range m.Units {
			if u.Status == "alarm" {
				attention = append(attention, m.Name+" ("+m.Op+") — "+u.Name)
			}
		}
		if m.Silent != "" {
			attention = append(attention, m.Name+" ("+m.Op+") — "+m.Silent)
		}
	}
	data["Machines"], data["Alarms"], data["Warns"], data["Attention"] = ms, alarms, warns, attention
	data["Profiles"] = s.reportProfiles()
	s.render(w, "report.html", data)
}

// reportOps — пары (машина, операция) включённых профилей; без настроек — операции таблиц.
func (s *Server) reportOps() [][2]string {
	var out [][2]string
	if s.cfg != nil {
		for _, p := range s.cfg.Get().EnabledProfiles() {
			for _, op := range p.Ops {
				out = append(out, [2]string{p.Name, op})
			}
		}
	}
	if len(out) == 0 {
		tabs, _ := s.an.tables()
		var ops []string
		for op := range tabs {
			ops = append(ops, op)
		}
		sort.Strings(ops)
		for _, op := range ops {
			out = append(out, [2]string{"", op})
		}
	}
	return out
}

func (s *Server) reportProfiles() []string {
	if s.cfg == nil {
		return nil
	}
	var out []string
	for _, p := range s.cfg.Get().EnabledProfiles() {
		kind := map[string]string{"press10": "пресс 10", "press5": "пресс 5", "kps": "КПС"}[p.Kind]
		if kind == "" {
			kind = "машина"
		}
		extra := ""
		if p.Ring {
			extra = ", кольцевой буфер регистратора"
		}
		out = append(out, fmt.Sprintf("%s — %s, операции %s%s", p.Name, kind, strings.Join(p.Ops, ", "), extra))
	}
	return out
}

// reportMachine — выводы по одной операции за период.
func (s *Server) reportMachine(name, base string, p reportPeriod, now time.Time) reportMachine {
	m := reportMachine{Name: name, Op: base}
	if m.Name == "" {
		m.Name = "Операция"
	}
	mixed, err := s.an.get(base)
	if err != nil {
		m.Err = "нет данных: " + err.Error()
		return m
	}
	// светофор — как на странице «Анализ» по умолчанию: текущий рецепт / колесо со своей нормой
	res, view := mixed, "все циклы"
	if rg := currentRegime(mixed); rg != "" {
		if r2, err := s.an.get(analyze.JoinOp(base, rg)); err == nil {
			res, view = r2, analyze.RegimeLabel(rg)
		}
	}
	m.View = view
	for _, d := range res.Diag {
		switch d.Status {
		case analyze.Alarm:
			m.Alarm++
		case analyze.Warn:
			m.Warn++
		default:
			m.OK++
			continue
		}
		u := reportUnit{Name: d.Name, Status: statusKey[d.Status], Label: analyze.StatusText[d.Status], Check: d.Check}
		if len(d.Facts) > 0 {
			u.Fact = d.Facts[0]
		}
		m.Units = append(m.Units, u)
	}
	sort.SliceStable(m.Units, func(i, j int) bool { return m.Units[i].Status == "alarm" && m.Units[j].Status != "alarm" })
	seen := map[string]bool{}
	for _, l := range res.Lost {
		k := analyze.SignalKey(l.Feature)
		if !seen[k] {
			seen[k] = true
			m.Lost = append(m.Lost, s.human(l.Feature)+" — с "+l.Since.Format("02.01 15:04"))
		}
	}
	switch {
	case m.Alarm > 0:
		m.StatusWord, m.StatusClass = "ПРОБЛЕМА", "alarm"
	case m.Warn > 0:
		m.StatusWord, m.StatusClass = "НАБЛЮДАТЬ", "warn"
	default:
		m.StatusWord, m.StatusClass = "НОРМА", "ok"
	}

	// работа и простои за период — по всем циклам операции
	d := mixed.All
	end := p.To
	if end.After(now) {
		end = now
	}
	var times []time.Time
	var last time.Time
	for _, t := range d.Times {
		if t.After(last) {
			last = t
		}
		if !t.Before(p.From) && t.Before(p.To) {
			times = append(times, t)
			m.Cycles++
		}
	}
	sort.Slice(times, func(a, b int) bool { return times[a].Before(times[b]) })
	if prod := mixed.Prod; prod != nil {
		for _, t := range prod.Times {
			if !t.Before(p.From) && t.Before(p.To) {
				m.Prod++
			}
		}
	}
	var pauses []reportGap
	type gap struct {
		a, b time.Time
	}
	var gaps []gap
	prev := p.From
	for _, t := range times {
		if t.Sub(prev) > reportPause {
			gaps = append(gaps, gap{prev, t})
		} else {
			m.WorkH += t.Sub(prev).Hours()
		}
		prev = t
	}
	if end.Sub(prev) > reportPause {
		gaps = append(gaps, gap{prev, end})
	} else if end.After(prev) {
		m.WorkH += end.Sub(prev).Hours()
	}
	for _, g := range gaps {
		m.IdleH += g.b.Sub(g.a).Hours()
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].b.Sub(gaps[i].a) > gaps[j].b.Sub(gaps[j].a) })
	for _, g := range gaps[:min(len(gaps), reportPauses)] {
		pauses = append(pauses, reportGap{g.a.Format("02.01 15:04"), g.b.Format("02.01 15:04"), analyze.DurText(g.b.Sub(g.a))})
	}
	m.Pauses = pauses
	if !last.IsZero() {
		m.Last = last.Format("02.01.2006 15:04")
		if ago := now.Sub(last); ago > reportSilentAt {
			m.Silent = "регистратор молчит с " + last.Format("02.01 15:04") + " (" + analyze.DurText(ago) + ")"
		}
	}
	m.Damaged = mixed.Damaged
	// что меняли за период — из журнала изменений (кроме пауз: они уже выше)
	for _, e := range mixed.Journal() {
		if e.Kind == "gap" || e.At.Before(p.From) || !e.At.Before(p.To) {
			continue
		}
		if len(m.Events) == reportEvents {
			break
		}
		m.Events = append(m.Events, journalView{At: e.At.Format("02.01 15:04"), Kind: e.Kind, KindName: journalKinds[e.Kind], Text: e.Text})
	}
	return m
}

// currentRegime — рецепт / колесо, по которому машина работает сейчас ("" — выбирать нечего),
// как выбор по умолчанию на странице «Анализ».
func currentRegime(mixed *analyze.Result) string {
	if len(mixed.Regimes) == 0 || (analyze.IsRecipe(mixed.Regimes[0].Key) && len(mixed.Regimes) < 2) {
		return ""
	}
	cur := mixed.Regimes[0]
	for _, rg := range mixed.Regimes {
		if rg.To.After(cur.To) {
			cur = rg
		}
	}
	return cur.Key
}
