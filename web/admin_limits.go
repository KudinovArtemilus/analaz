package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
	"github.com/KudinovArtemilus/press-go/internal/config"
)

// Паспортные пределы показателей узлов («Настройки» → машина → «Паспортные пределы»).
// Норма относительна: «как машина работала в выбранный период». Если в тот период давление уже
// было занижено, сравнение с нормой этого не увидит. Предел из паспорта машины или от механиков
// от нормы не зависит: нарушение — «проблема» в диагнозе и в отчёте, и проверка периода нормы
// покажет, что период уже за пределом.

type limitRow struct {
	analyze.IndicatorInfo
	Min, Max, Note string
}

// adminLimits — таблица показателей машины с полями «мин.», «макс.», «откуда цифра».
func (s *Server) adminLimits(w http.ResponseWriter, r *http.Request) {
	c := s.cfg.Get()
	p := c.Profile(r.URL.Query().Get("id"))
	if p == nil {
		s.fail(w, errors.New("нет такого профиля"))
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	kps := p.Kind == "kps"
	var rows []limitRow
	set := 0
	for _, in := range analyze.Indicators() {
		if in.Kps != kps {
			continue
		}
		row := limitRow{IndicatorInfo: in}
		if l, ok := p.Limits[in.Feat]; ok {
			row.Min, row.Max, row.Note = fmtLimit(l.Min), fmtLimit(l.Max), l.Note
			set++
		}
		if q != "" && !strings.Contains(strings.ToLower(in.Feat+" "+in.Label+" "+in.Group), q) {
			continue
		}
		rows = append(rows, row)
	}
	s.render(w, "admin_limits.html", map[string]any{"P": p, "Rows": rows, "Q": q, "Set": set})
}

func fmtLimit(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// parseLimit — «110», «110,5» или пусто (границы нет).
func parseLimit(s, what string) (*float64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, fmt.Errorf("%s: ждали число, получили %q", what, s)
	}
	return &v, nil
}

// adminSaveLimit — сохранить предел одного показателя (форма строки отправляется при изменении поля).
func (s *Server) adminSaveLimit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f := r.PostForm
	id, feat := f.Get("id"), f.Get("feat")
	known := false
	for _, in := range analyze.Indicators() {
		known = known || in.Feat == feat
	}
	if !known {
		inlineError(w, fmt.Errorf("неизвестный показатель %q", feat))
		return
	}
	lo, err1 := parseLimit(f.Get("min"), "минимум")
	hi, err2 := parseLimit(f.Get("max"), "максимум")
	if err := errors.Join(err1, err2); err != nil {
		inlineError(w, err)
		return
	}
	if len([]rune(f.Get("note"))) > 200 {
		inlineError(w, errors.New("пояснение длиннее 200 символов"))
		return
	}
	err := s.cfg.Update(func(c *config.Config) error {
		p := c.Profile(id)
		if p == nil {
			return fmt.Errorf("нет профиля %q", id)
		}
		return p.SetLimit(feat, config.Limit{Min: lo, Max: hi, Note: f.Get("note")})
	})
	if err != nil {
		inlineError(w, err)
		return
	}
	if lo == nil && hi == nil {
		log.Printf("админка: %s — снят паспортный предел %s", id, feat)
		fmt.Fprint(w, `<span class="muted small">нет предела</span>`)
		return
	}
	log.Printf("админка: %s — паспортный предел %s: мин %s, макс %s", id, feat, fmtLimit(lo), fmtLimit(hi))
	fmt.Fprint(w, `<span class="muted small">сохранено</span>`)
}
