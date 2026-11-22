// Package analyze — анализ таблицы признаков: норма, аномалии, тренды (перенос analyze.py).
package analyze

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/batch"
)

// Dataset — таблица признаков в виде столбцов: Data[колонка][цикл].
// Анализ почти всегда идёт по одному признаку через все циклы (медиана, тренд),
// поэтому столбцы удобнее и быстрее строк.
type Dataset struct {
	Cols  []string
	Data  [][]float64
	Times []time.Time
	Op    []string
	File  []string
	index map[string]int
}

// FromTable повторяет load() из analyze.py: Inf и |x| > 1e15 → NaN, сортировка по времени цикла.
func FromTable(t *batch.Table) (*Dataset, error) {
	cols := t.Columns()
	d := &Dataset{Cols: cols, Data: make([][]float64, len(cols)), index: map[string]int{}}
	for j := range cols {
		col := t.Column(j)
		for i, v := range col {
			if math.IsInf(v, 0) || math.Abs(v) > 1e15 { // мусор из старых прогонов, как в Python
				col[i] = math.NaN()
			}
		}
		d.Data[j] = col
		d.index[cols[j]] = j
	}
	d.File, d.Op = t.Text(0), t.Text(1)
	ts := t.Text(2)
	d.Times = make([]time.Time, len(ts))
	for i, s := range ts {
		tm, err := parseTime(s)
		if err != nil {
			return nil, fmt.Errorf("строка %d (%s): %w", i+2, d.File[i], err)
		}
		d.Times[i] = tm
	}
	return d.sortByTime(), nil
}

// parseTime понимает и формат Go ("2026-11-18 04:56:19"), и pandas (с долями секунды).
func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("не разобрать время %q", s)
}

// Col — колонка по имени (nil, если нет).
func (d *Dataset) Col(name string) []float64 {
	if j, ok := d.index[name]; ok {
		return d.Data[j]
	}
	return nil
}

// Len — число циклов.
func (d *Dataset) Len() int { return len(d.Times) }

// Rows — новый Dataset только из указанных строк (в указанном порядке).
func (d *Dataset) Rows(idx []int) *Dataset {
	out := &Dataset{
		Cols: d.Cols, Data: make([][]float64, len(d.Data)), index: d.index,
		Times: make([]time.Time, len(idx)), Op: make([]string, len(idx)), File: make([]string, len(idx)),
	}
	for j, col := range d.Data {
		c := make([]float64, len(idx))
		for k, i := range idx {
			c[k] = col[i]
		}
		out.Data[j] = c
	}
	for k, i := range idx {
		out.Times[k], out.Op[k], out.File[k] = d.Times[i], d.Op[i], d.File[i]
	}
	return out
}

// Where — строки, для которых keep(i) == true.
func (d *Dataset) Where(keep func(i int) bool) *Dataset {
	var idx []int
	for i := range d.Times {
		if keep(i) {
			idx = append(idx, i)
		}
	}
	return d.Rows(idx)
}

// OnlyOp — только циклы операции op (без учёта регистра), как df[df.op.str.upper() == ...].
func (d *Dataset) OnlyOp(op string) *Dataset {
	op = strings.ToUpper(op)
	return d.Where(func(i int) bool { return strings.ToUpper(d.Op[i]) == op })
}

// sortByTime — устойчивая сортировка: циклы с одинаковым временем остаются в исходном порядке.
func (d *Dataset) sortByTime() *Dataset {
	idx := make([]int, d.Len())
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return d.Times[idx[a]].Before(d.Times[idx[b]]) })
	return d.Rows(idx)
}
