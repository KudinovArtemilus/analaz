package web

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
)

// И3 — паспорт заготовки: /piece?n=169197 — циклы заготовки на всех операциях;
// /piece?h=67563 — заготовки плавки. Номер — из имени файла (см. analyze/piece.go).

type pieceOpView struct {
	Op     string
	Cycles []pieceCycleView
	Err    string
}

type pieceCycleView struct {
	Time, File, Open string // Open — ссылка «открыть цикл» с признаками-виновниками
	Production       bool
	Anomalous        bool
	Score            num
	Reasons          []reasonJSON
	Key              []pieceKeyView
	Run              string // «весь отрезок 18.11 20:31 — 20.11 10:30 вне нормы: …»; пусто — в норме
}

type pieceKeyView struct {
	Feature, Name string
	Value, Norm   num
	Z             num
}

// heatRow — заготовка плавки: на каких операциях была и с отклонениями ли.
type heatRow struct {
	Piece  int
	at     time.Time
	Time   string
	Marks  []heatMark // по операциям, в порядке Ops
	AnyBad bool
}

type heatMark struct {
	Op        string
	Seen      bool
	Anomalous bool
	InRun     bool
}

func (s *Server) piecePage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Enabled": s.an != nil}
	q := r.URL.Query()
	nStr, hStr := strings.TrimSpace(q.Get("n")), strings.TrimSpace(q.Get("h"))
	data["N"], data["H"] = nStr, hStr
	if s.an == nil || (nStr == "" && hStr == "") {
		s.render(w, "piece.html", data)
		return
	}
	n, errN := strconv.Atoi(nStr)
	h, errH := strconv.Atoi(hStr)
	if (nStr != "" && (errN != nil || n < 0)) || (hStr != "" && (errH != nil || h < 0)) {
		data["Error"] = "номер — целое число, например 169197"
		s.render(w, "piece.html", data)
		return
	}
	tabs, err := s.an.tables()
	if err != nil {
		data["Error"] = err.Error()
		s.render(w, "piece.html", data)
		return
	}
	var ops []string
	for op := range tabs {
		ops = append(ops, op)
	}
	sort.Strings(ops)
	data["Ops"] = ops

	if nStr != "" {
		var views []pieceOpView
		heat := -1
		for _, op := range ops {
			v := pieceOpView{Op: op}
			res, err := s.an.get(op)
			if err != nil {
				v.Err = err.Error()
				views = append(views, v)
				continue
			}
			for _, c := range res.Piece(n) {
				v.Cycles = append(v.Cycles, s.pieceCycle(c))
				heat = c.Heat
			}
			views = append(views, v)
		}
		data["Pieces"], data["Heat"] = views, heat
	}
	if hStr != "" {
		data["HeatRows"] = s.heatRows(ops, h)
	}
	s.render(w, "piece.html", data)
}

func (s *Server) pieceCycle(c analyze.PieceCycle) pieceCycleView {
	v := pieceCycleView{Time: c.Time.Format("02.01.2006 15:04:05"), File: baseFile(c.File), Production: c.Production,
		Anomalous: c.Anomalous, Score: num(c.Score)}
	// ссылка «открыть цикл» — с признаками-виновниками, как со страницы «Анализ»
	if rel := s.relData(c.File); rel != "" {
		u := "/?open=" + url.QueryEscape(rel)
		for _, x := range c.Reasons {
			u += "&feat=" + url.QueryEscape(x.Feature)
		}
		v.Open = u
	}
	for _, x := range c.Reasons {
		v.Reasons = append(v.Reasons, reasonJSON{x.Feature, s.human(x.Feature), num(x.Value), num(x.Rm), num(x.Z)})
	}
	for _, k := range c.Key {
		v.Key = append(v.Key, pieceKeyView{k.Feature, s.human(k.Feature), num(k.Value), num(k.Norm), num(k.Z)})
	}
	// отклонившиеся сверху: по паспорту сразу видно, чем эта заготовка отличалась
	sort.SliceStable(v.Key, func(a, b int) bool { return absNum(v.Key[a].Z) > absNum(v.Key[b].Z) })
	if run := c.Run; run != nil {
		var parts []string
		for _, f := range run.Off[:min(4, len(run.Off))] {
			parts = append(parts, fmt.Sprintf("%s %+.1fσ", s.human(f.Feature), f.Z))
		}
		v.Run = fmt.Sprintf("весь отрезок работы %s — %s был вне нормы: %s", run.From.Format("02.01 15:04"), run.To.Format("02.01 15:04"), strings.Join(parts, "; "))
		if len(run.Off) > 4 {
			v.Run += fmt.Sprintf(" и ещё %d", len(run.Off)-4)
		}
	}
	return v
}

func (s *Server) heatRows(ops []string, h int) []heatRow {
	byPiece := map[int]*heatRow{}
	for k, op := range ops {
		res, err := s.an.get(op)
		if err != nil {
			continue
		}
		for _, c := range res.Heat(h) {
			row := byPiece[c.Piece]
			if row == nil {
				row = &heatRow{Piece: c.Piece, at: c.Time, Time: c.Time.Format("02.01 15:04"), Marks: make([]heatMark, len(ops))}
				for j, o := range ops {
					row.Marks[j].Op = o
				}
				byPiece[c.Piece] = row
			}
			m := &row.Marks[k]
			m.Seen, m.Anomalous, m.InRun = true, c.Anomalous, c.Run != nil
			row.AnyBad = row.AnyBad || c.Anomalous || c.Run != nil
		}
	}
	rows := make([]heatRow, 0, len(byPiece))
	for _, r := range byPiece {
		rows = append(rows, *r)
	}
	sort.Slice(rows, func(a, b int) bool {
		if !rows[a].at.Equal(rows[b].at) {
			return rows[a].at.Before(rows[b].at)
		}
		return rows[a].Piece < rows[b].Piece
	})
	return rows
}

func absNum(v num) float64 {
	if math.IsNaN(float64(v)) {
		return -1 // нет нормы — в конец
	}
	return math.Abs(float64(v))
}
