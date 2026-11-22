package analyze

import (
	"math"
	"sort"
	"time"
)

// И3 — паспорт заготовки: что было с конкретной деталью на этой операции.
//
// Номер заготовки берётся из ИМЕНИ ФАЙЛА (fn_piece, fn_heat: 26.169197.067563_OP1_…),
// а не из канала слежения PartTracking_PieceId_Consecutiv (piece_id). Проверено на таблицах
// OP1/OP2: в паре «ход штампа N на OP1 → ход N+1 на OP2 через ~40 с» номер в имени совпал
// 21 из 21, а канал слежения в записи OP1 к концу цикла уже переключается на СЛЕДУЮЩУЮ
// заготовку (piece_id = максимум за запись — в 64% случаев номер следующей).

// PieceCycle — цикл одной заготовки на операции.
type PieceCycle struct {
	Piece, Heat int
	Time        time.Time
	File        string
	Production  bool     // рабочий цикл (усилие > порога); нерабочий — без сравнения с нормой
	Anomalous   bool     // попал в аномальные (выброс относительно соседних циклов)
	Score       float64  // сила аномалии (как в списке аномальных); NaN — не считалась
	Reasons     []Reason // до 5 признаков, сильнее всего отличающихся от соседних циклов
	Key         []PieceValue
	Run         *OffRun // отрезок работы вне нормы, в который попал цикл (nil — в норме или отрезка нет)
}

// PieceValue — ключевой показатель цикла против нормы.
type PieceValue struct {
	Feature string
	Value   float64
	Norm    float64
	Z       float64 // σ от нормы (NaN — нормы нет)
}

// PieceKey — ключевые показатели паспорта: те же, что в таблице суток (KEY в daily.py).
var PieceKey = DailyKey

// Piece — циклы заготовки n на этой операции (обычно один). nil — такой нет.
func (r *Result) Piece(n int) []PieceCycle {
	return r.piecesWhere(func(piece, _ int) bool { return piece == n })
}

// Heat — все циклы заготовок плавки h на этой операции, по времени.
func (r *Result) Heat(h int) []PieceCycle {
	return r.piecesWhere(func(_, heat int) bool { return heat == h })
}

func (r *Result) piecesWhere(match func(piece, heat int) bool) []PieceCycle {
	all := r.All
	fp, fh := all.Col("fn_piece"), all.Col("fn_heat")
	if fp == nil {
		return nil
	}
	// номер рабочего цикла: аномалии и признаки посчитаны по рабочим циклам.
	// Ключ — время и файл вместе: одного имени файла мало (в тестах и при сбоях имена бывают пустыми)
	type key struct {
		t time.Time
		f string
	}
	prodIdx := make(map[key]int, r.Prod.Len())
	for i, f := range r.Prod.File {
		prodIdx[key{r.Prod.Times[i], f}] = i
	}
	var runs []OffRun
	var out []PieceCycle
	for i := range all.Times {
		piece, heat := toInt(fp[i]), -1
		if fh != nil {
			heat = toInt(fh[i])
		}
		if piece < 0 || !match(piece, heat) {
			continue
		}
		c := PieceCycle{Piece: piece, Heat: heat, Time: all.Times[i], File: all.File[i], Score: math.NaN()}
		if j, ok := prodIdx[key{all.Times[i], all.File[i]}]; ok {
			c.Production = true
			c.Score = r.Anom.Score[j]
			c.Anomalous = r.Anom.Flag[j]
			c.Reasons = r.Anom.Explain(r.Prod, r.SigEng, j, AnomTopK, r.Opts.Z)
			for _, f := range PieceKey {
				col := r.Prod.Col(f)
				if col == nil {
					continue
				}
				pv := PieceValue{Feature: f, Value: col[j], Norm: math.NaN(), Z: math.NaN()}
				if k := indexOf(r.Cols.Eng, f); k >= 0 {
					pv.Norm = r.Norms[k].Median
					pv.Z = (col[j] - pv.Norm) / r.Norms[k].Sigma
				}
				c.Key = append(c.Key, pv)
			}
			if runs == nil {
				runs = r.OffRuns()
			}
			for k := range runs {
				if len(runs[k].Off) > 0 && !c.Time.Before(runs[k].From) && !c.Time.After(runs[k].To) {
					c.Run = &runs[k]
				}
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Time.Before(out[b].Time) })
	return out
}

// toInt — номер из таблицы (float64) в int; NaN и дробные — −1.
func toInt(v float64) int {
	if math.IsNaN(v) || v != math.Trunc(v) || v < 0 || v > math.MaxInt32 {
		return -1
	}
	return int(v)
}
