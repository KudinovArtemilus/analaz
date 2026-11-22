package analyze

import (
	"math"
	"slices"
	"sort"
	"time"
)

// Карта здоровья по плавкам и по заготовкам плавки (идея Артёма к И3: смотреть, не зная номеров).
//
// Та же формула, что у дневной карты здоровья (HealthMap, charts.py раздел 1): для каждой
// подсистемы — среднее трёх самых больших |z| медиан её признаков, только столбцы — не дни,
// а плавки (медиана по рабочим циклам плавки) или заготовки одной плавки (один цикл —
// медиана равна самому значению). Номер плавки и заготовки — из имени файла (см. piece.go).

// GroupHealth — карта «подсистема × плавка» или «подсистема × заготовка».
type GroupHealth struct {
	Subsystems []string
	Keys       []int       // номер плавки или заготовки — столбец карты
	From, To   []time.Time // первый и последний рабочий цикл столбца
	Cycles     []int       // рабочих циклов в столбце
	Anom       []int       // из них аномальных
	Z          [][]float64 // [подсистема][столбец], σ; NaN — нет данных
	Index      []float64   // худшая подсистема столбца
}

// HeatHealth — карта по плавкам, по времени первой заготовки. nil — номеров плавок в таблице нет.
func (r *Result) HeatHealth() *GroupHealth {
	return r.groupHealth(r.Prod.Col("fn_heat"), nil)
}

// PieceHealth — карта по заготовкам плавки h, по времени. nil — такой плавки нет.
func (r *Result) PieceHealth(h int) *GroupHealth {
	heat := r.Prod.Col("fn_heat")
	if heat == nil {
		return nil
	}
	return r.groupHealth(r.Prod.Col("fn_piece"), func(i int) bool { return toInt(heat[i]) == h })
}

// groupHealth — столбцы по значению key (номер плавки или заготовки) среди рабочих циклов,
// отобранных keep (nil — все).
func (r *Result) groupHealth(key []float64, keep func(int) bool) *GroupHealth {
	p := r.Prod
	if key == nil || p.Len() == 0 {
		return nil
	}
	idx := map[int]int{} // номер → столбец
	var rows [][]int
	g := &GroupHealth{}
	for i := range p.Len() {
		k := toInt(key[i])
		if k < 0 || (keep != nil && !keep(i)) {
			continue
		}
		c, ok := idx[k]
		if !ok {
			c = len(g.Keys)
			idx[k] = c
			g.Keys = append(g.Keys, k)
			g.From, g.To = append(g.From, p.Times[i]), append(g.To, p.Times[i])
			g.Cycles, g.Anom = append(g.Cycles, 0), append(g.Anom, 0)
			rows = append(rows, nil)
		}
		rows[c] = append(rows[c], i)
		g.Cycles[c]++
		if r.Anom != nil && r.Anom.Flag[i] {
			g.Anom[c]++
		}
		if p.Times[i].Before(g.From[c]) {
			g.From[c] = p.Times[i]
		}
		if p.Times[i].After(g.To[c]) {
			g.To[c] = p.Times[i]
		}
	}
	if len(g.Keys) == 0 {
		return nil
	}
	// по времени первого цикла: плавки идут одна за другой
	order := make([]int, len(g.Keys))
	for k := range order {
		order[k] = k
	}
	sort.SliceStable(order, func(a, b int) bool { return g.From[order[a]].Before(g.From[order[b]]) })
	reorder(g, rows, order)

	subOf := r.subsystemsOf(&g.Subsystems)
	g.Z = make([][]float64, len(g.Subsystems))
	for k := range g.Z {
		g.Z[k] = make([]float64, len(g.Keys))
	}
	g.Index = make([]float64, len(g.Keys))
	vals := make([][]float64, len(g.Subsystems))
	for c, rs := range rows {
		for k := range vals {
			vals[k] = vals[k][:0]
		}
		for j, f := range r.Cols.Eng {
			z := math.Abs((NanMedian(pick(p.Col(f), rs)) - r.Norms[j].Median) / r.Norms[j].Sigma)
			if !math.IsNaN(z) {
				vals[subOf[j]] = append(vals[subOf[j]], z)
			}
		}
		g.Index[c] = math.NaN()
		for k, v := range vals {
			g.Z[k][c] = topMeanNoNaN(v, 3)
			if z := g.Z[k][c]; !math.IsNaN(z) && (math.IsNaN(g.Index[c]) || z > g.Index[c]) {
				g.Index[c] = z
			}
		}
	}
	return g
}

// subsystemsOf — подсистема каждого инженерного признака; список подсистем — в порядке
// первого появления (как в HealthMap: одна и та же подсистема на всех картах в одной строке).
func (r *Result) subsystemsOf(subs *[]string) []int {
	subOf := make([]int, len(r.Cols.Eng))
	for j, c := range r.Cols.Eng {
		s := Subsystem(c)
		k := slices.Index(*subs, s)
		if k < 0 {
			k = len(*subs)
			*subs = append(*subs, s)
		}
		subOf[j] = k
	}
	return subOf
}

func reorder(g *GroupHealth, rows [][]int, order []int) {
	keys, from, to, cyc, an, rs := slices.Clone(g.Keys), slices.Clone(g.From), slices.Clone(g.To), slices.Clone(g.Cycles), slices.Clone(g.Anom), slices.Clone(rows)
	for k, o := range order {
		g.Keys[k], g.From[k], g.To[k], g.Cycles[k], g.Anom[k], rows[k] = keys[o], from[o], to[o], cyc[o], an[o], rs[o]
	}
}
