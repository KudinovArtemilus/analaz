package web

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
	"github.com/KudinovArtemilus/press-go/internal/batch"
)

// Пересчёт хранимой нормы человеком: выбрал спокойный период — норма считается по нему.
// Сама норма у колеса (КПС) и рецепта (пресс 10) создаётся автоматически по первым 200 циклам
// и дальше не меняется; если те циклы были неудачными (5 часов одного утра), всё, что после,
// выглядит отклонением. Старый файл не удаляется, а переименовывается в …json.ГГГГММДД-ЧЧММСС.bak.

// normInputTF — формат поля <input type="datetime-local" step="1">.
const normInputTF = "2006-01-02T15:04:05"

// isLocal — запрос с этого же компьютера (как у админки).
func isLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}

type normSuggestView struct {
	From, To string // для полей формы
	Label    string // 17.11 00:03 — 17.11 23:58
	Days     int
	Cycles   int
	Stops    int
	AnomPct  float64
}

func normSuggestViews(ps []analyze.NormPeriod) []normSuggestView {
	var out []normSuggestView
	for _, p := range ps {
		out = append(out, normSuggestView{p.From.Format(normInputTF), p.To.Format(normInputTF),
			p.From.Format("02.01 15:04") + " — " + p.To.Format("02.01 15:04"), p.Days, p.Cycles, p.Stops, p.AnomPct})
	}
	return out
}

// baseRun — анализ операции op (с колесом/рецептом) с нормой по циклам from…to: из него берётся
// новая норма и по нему проверяется период. path — куда записывать норму.
func (a *analysisStore) baseRun(op string, from, to time.Time, note string) (*analyze.Result, string, error) {
	op = opKey(op)
	base, regime := analyze.SplitOp(op)
	tabs, err := a.tables()
	if err != nil {
		return nil, "", err
	}
	src, ok := tabs[base]
	if !ok {
		return nil, "", fmt.Errorf("нет таблицы признаков для %s", base)
	}
	path := a.normPath(op)
	if path == "" {
		return nil, "", errors.New("папка норм не указана")
	}
	t, err := batch.ReadCSVGz(src)
	if err != nil {
		return nil, "", err
	}
	d, err := analyze.FromTable(t)
	if err != nil {
		return nil, "", err
	}
	o := analyze.DefaultOptions()
	o.Op, o.Regime, o.Source = base, regime, src
	o.BaseFrom, o.BaseTo = from, to
	o.Note = "вручную: " + note
	o.Limits = a.limitsOf(base)
	r, err := analyze.Run(d, o)
	if err != nil {
		return nil, "", err
	}
	if n := r.Norm.BaseCycles; n < analyze.AutoNormCycles {
		return nil, "", fmt.Errorf("в периоде %s — %s только %d рабочих циклов, нужно не меньше %d: расширьте период",
			from.Format("02.01 15:04"), to.Format("02.01 15:04"), n, analyze.AutoNormCycles)
	}
	return r, path, nil
}

// checkNorm — проверка периода перед сохранением: что в нём хуже прежней нормы, начала данных,
// паспортных пределов и что плывёт внутри него (analyze.CheckBase).
func (a *analysisStore) checkNorm(op string, from, to time.Time, note string) (*analyze.Result, []analyze.NormCheck, error) {
	r, path, err := a.baseRun(op, from, to, note)
	if err != nil {
		return nil, nil, err
	}
	var prev *analyze.NormSet
	if _, err := os.Stat(path); err == nil {
		if prev, err = analyze.LoadNorm(path); err != nil {
			return nil, nil, fmt.Errorf("прежняя норма не читается: %w", err)
		}
	}
	return r, r.CheckBase(prev, r.Opts.Limits), nil
}

// saveNorm считает норму по циклам from…to и записывает её вместо прежней; прежняя
// переименовывается в .bak. Возвращает новую норму и имя копии старой.
func (a *analysisStore) saveNorm(op string, from, to time.Time, note string) (*analyze.NormSet, string, error) {
	a.saveMu.Lock() // две формы одновременно не должны писать один файл
	defer a.saveMu.Unlock()
	r, path, err := a.baseRun(op, from, to, note)
	if err != nil {
		return nil, "", err
	}
	bak := ""
	if _, err := os.Stat(path); err == nil {
		bak = path + "." + time.Now().Format("20060102-150405") + ".bak"
		if err := os.Rename(path, bak); err != nil {
			return nil, "", fmt.Errorf("не переименовать прежнюю норму: %w", err)
		}
	}
	if err := r.Norm.Save(path); err != nil {
		if bak != "" {
			os.Rename(bak, path) // вернуть прежнюю, раз новая не записалась
		}
		return nil, "", err
	}
	return r.Norm, bak, nil
}

// normCheckView — итог проверки периода для формы.
type normCheckView struct {
	From, To, Note string // что ввёл человек — для кнопки «Сохранить»
	Label          string // 13.11 00:29 — 13.11 23:52
	Cycles         int
	Items          []normCheckItem
}

type normCheckItem struct{ Kind, Label, Text string }

var normCheckKind = map[string]string{"passport": "за паспортным пределом", "prev": "хуже прежней нормы",
	"start": "хуже начала данных", "trend": "плывёт внутри периода"}

// analysisNormSave — POST /analysis/norm/save (только с этого компьютера). Два шага:
// step=check (по умолчанию) — проверить период и показать находки; step=save — сохранить.
func (s *Server) analysisNormSave(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	op := strings.ToUpper(r.PostForm.Get("op"))
	if !reOp.MatchString(op) {
		s.fail(w, errBadOp(op))
		return
	}
	fail := func(err error) { s.normFragment(w, r, op, "", map[string]any{"Err": err.Error()}) }
	fromS, toS := strings.TrimSpace(r.PostForm.Get("from")), strings.TrimSpace(r.PostForm.Get("to"))
	from, err1 := analyze.ParseWhen(fromS)
	to, err2 := analyze.ParseWhen(toS)
	if err := errors.Join(err1, err2); err != nil {
		fail(err)
		return
	}
	if from.IsZero() || to.IsZero() || !to.After(from) {
		fail(errors.New("укажите период: «с» раньше «по»"))
		return
	}
	note := strings.TrimSpace(r.PostForm.Get("note"))
	if note == "" {
		fail(errors.New("напишите, почему период хороший (например: «сутки без остановов, после ППР»)"))
		return
	}
	if r.PostForm.Get("step") != "save" {
		res, cs, err := s.an.checkNorm(op, from, to, note)
		if err != nil {
			fail(err)
			return
		}
		v := normCheckView{From: fromS, To: toS, Note: note, Cycles: res.Norm.BaseCycles,
			Label: res.Norm.BaseFrom.Format("02.01.2006 15:04") + " — " + res.Norm.BaseTo.Format("02.01.2006 15:04")}
		for _, c := range cs {
			v.Items = append(v.Items, normCheckItem{normCheckKind[c.Kind], c.Label, c.Text})
		}
		s.normFragment(w, r, op, "", map[string]any{"Check": v})
		return
	}
	ns, bak, err := s.an.saveNorm(op, from, to, note)
	if err != nil {
		fail(err)
		return
	}
	msg := fmt.Sprintf("Норма пересчитана: %s — %s, %d циклов.", ns.BaseFrom.Format("02.01.2006 15:04"),
		ns.BaseTo.Format("02.01.2006 15:04"), ns.BaseCycles)
	if bak != "" {
		msg += " Прежняя сохранена как " + bak
	}
	log.Printf("норма %s пересчитана вручную: %s (%s)", op, msg, note)
	s.normFragment(w, r, op, "", map[string]any{"Saved": msg})
}
