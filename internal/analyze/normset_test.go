package analyze

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"time"
)

// NormSet — норма, по которой оценивается пресс: медиана, робастная σ и квантили каждого признака.
//
// Её можно посчитать «на лету» из базового периода (как analyze.py), а можно СОХРАНИТЬ
// в файл, снятый с заведомо хорошего периода, и дальше оценивать по нему. Зачем:
// норма из начала скользящего буфера уже содержит поломки (MD1 0,03 бар, MD5 = 0,
// зажим 90 бар), и анализ принимает их за норму. Хранимая норма — осознанное решение
// инженера: какой период считать хорошим. Файл JSON — его можно открыть и прочитать.
type NormSet struct {
	Op         string                  `json:"op"`
	Created    LocalTime               `json:"created"`
	Source     string                  `json:"source"`      // таблица признаков, из которой снята
	BaseFrom   LocalTime               `json:"base_from"`   // первый цикл базового периода
	BaseTo     LocalTime               `json:"base_to"`     // последний цикл
	BaseCycles int                     `json:"base_cycles"` // рабочих циклов в базе
	Note       string                  `json:"note,omitempty"`
	Features   map[string]*FeatureNorm `json:"features"`
}

// FeatureNorm — норма одного признака. Sigma и Q есть только у анализируемых (инженерных и сырых);
// у остальных — только медиана (её использует диагноз по узлам).
type FeatureNorm struct {
	Kind   string `json:"kind,omitempty"` // eng, raw или пусто
	Median JF     `json:"median"`
	Sigma  JF     `json:"sigma"`
	Q      [5]JF  `json:"q"` // квантили 1, 5, 50, 95, 99%
}

// BuildNormSet считает норму из базового периода base так же, как analyze.py:
// σ и квантили — для cols.Eng и cols.Raw (пол σ берётся по всем рабочим циклам prod),
// медиана — для всех числовых колонок.
func BuildNormSet(d, prod, base *Dataset, cols Columns) *NormSet {
	n := &NormSet{Features: map[string]*FeatureNorm{}, BaseCycles: base.Len()}
	if base.Len() > 0 {
		n.BaseFrom, n.BaseTo = LocalTime{base.Times[0]}, LocalTime{base.Times[base.Len()-1]}
	}
	for _, c := range d.Cols {
		if !IsMeta(c) {
			n.Features[c] = &FeatureNorm{Median: JF(NanMedian(base.Col(c))), Sigma: JF(math.NaN()), Q: nanQ()}
		}
	}
	for _, nm := range ComputeNorms(base, prod, cols) {
		f := n.Features[nm.Feature]
		f.Kind, f.Sigma = nm.Kind, JF(nm.Sigma)
		for k, v := range nm.Q {
			f.Q[k] = JF(v)
		}
	}
	return n
}

// Median — медиана признака в норме (NaN, если признака нет).
func (n *NormSet) Median(f string) float64 {
	if x, ok := n.Features[f]; ok {
		return float64(x.Median)
	}
	return math.NaN()
}

// SigmaMap — σ анализируемых признаков (как Series sig в Python: только eng + raw).
func (n *NormSet) SigmaMap() map[string]float64 {
	m := map[string]float64{}
	for k, f := range n.Features {
		if f.Kind != "" {
			m[k] = float64(f.Sigma)
		}
	}
	return m
}

// Restrict оставляет в cols только признаки, у которых в норме есть σ.
// Возвращает, сколько признаков выброшено (в данных есть, в норме нет).
func (n *NormSet) Restrict(cols Columns) (Columns, int) {
	drop := 0
	keep := func(names []string) []string {
		var out []string
		for _, c := range names {
			if f, ok := n.Features[c]; ok && f.Kind != "" {
				out = append(out, c)
			} else {
				drop++
			}
		}
		return out
	}
	return Columns{Eng: keep(cols.Eng), Raw: keep(cols.Raw), Dig: cols.Dig}, drop
}

// Norms — список Norm (eng, затем raw) в порядке cols — для norm.csv и аномалий.
func (n *NormSet) Norms(cols Columns) []Norm {
	var out []Norm
	for _, group := range [][]string{cols.Eng, cols.Raw} {
		for _, c := range group {
			f := n.Features[c]
			nm := Norm{Feature: c, Kind: f.Kind, Median: float64(f.Median), Sigma: float64(f.Sigma)}
			for k, v := range f.Q {
				nm.Q[k] = float64(v)
			}
			out = append(out, nm)
		}
	}
	return out
}

// Save записывает норму в JSON (атомарно: сначала во временный файл, потом переименование).
func (n *NormSet) Save(path string) error {
	b, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadNorm читает норму из JSON.
func LoadNorm(path string) (*NormSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var n NormSet
	if err := json.Unmarshal(b, &n); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(n.Features) == 0 {
		return nil, fmt.Errorf("%s: в норме нет признаков", path)
	}
	return &n, nil
}

// LocalTime — время цикла как на часах пресса, без часового пояса: «2026-11-06 00:00:44».
// Время в таблице признаков — местное, и «2026-11-06T00:00:44Z» в файле нормы вводило бы в заблуждение.
type LocalTime struct{ time.Time }

const localLayout = "2006-01-02 15:04:05"

func (t LocalTime) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(t.Format(localLayout))), nil
}

func (t *LocalTime) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return err
	}
	if t.Time, err = time.Parse(localLayout, s); err != nil {
		// файлы первой версии писали время в RFC 3339 («2026-11-06T00:00:44Z») — читаем и их
		var e2 error
		if t.Time, e2 = time.Parse(time.RFC3339, s); e2 == nil {
			t.Time, err = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC), nil
		}
	}
	return err
}

// JF — float64, который в JSON пишется как null, если это NaN или ±Inf
// (стандартный JSON не умеет NaN, encoding/json на нём падает).
type JF float64

func (f JF) MarshalJSON() ([]byte, error) {
	v := float64(f)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return []byte("null"), nil
	}
	return []byte(strconv.FormatFloat(v, 'g', -1, 64)), nil // без потери точности
}

func (f *JF) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		*f = JF(math.NaN())
		return nil
	}
	v, err := strconv.ParseFloat(string(b), 64)
	*f = JF(v)
	return err
}

func nanQ() [5]JF {
	nan := JF(math.NaN())
	return [5]JF{nan, nan, nan, nan, nan}
}

// BaselineRange — базовый период по датам (--baseline-start / --baseline-end в analyze.py):
// циклы с from ≤ t ≤ to; нулевое время — граница не задана.
// Как в Python, дата без времени означает полночь: --baseline-end 2026-11-08 — до 08.11 00:00 включительно.
func BaselineRange(p *Dataset, from, to time.Time) *Dataset {
	return p.Where(func(i int) bool {
		t := p.Times[i]
		return (from.IsZero() || !t.Before(from)) && (to.IsZero() || !t.After(to))
	})
}

// ParseWhen — дата или дата со временем: 2026-11-08, 2026-11-08 12:00, 2026-11-08 12:00:00.
func ParseWhen(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	for _, l := range []string{"2006-01-02", "2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("не разобрать дату %q (ждали ГГГГ-ММ-ДД или ГГГГ-ММ-ДД ЧЧ:ММ)", s)
}

// DropDamaged убирает циклы из повреждённых .shp (shp_damaged > 0): сдвинутые записи дают
// мусор вроде «давление 175489 бар» или «счётчик ходов 261112», и он портит норму, медианы
// и диагноз. В Python такой проверки нет. Возвращает данные и сколько циклов убрано.
func DropDamaged(d *Dataset) (*Dataset, int) {
	dm := d.Col("shp_damaged")
	if dm == nil {
		return d, 0 // таблица сделана до проверки целостности
	}
	out := d.Where(func(i int) bool { return !(dm[i] > 0) })
	return out, d.Len() - out.Len()
}

// Пороги «мусорного» цикла. Мусор — это значения, которые ФИЗИЧЕСКИ невозможны:
//   - ≥ 5 инженерных признаков отличаются от соседних циклов на ≥ 1000σ, и
//   - среди них ≥ 2 «инертных» — температура и уровень масла, дренажи насосов, аккумуляторы.
//
// Второе условие отделяет повреждённый файл от настоящего сбоя пресса. Незавершённый цикл,
// аварийный стоп, ошибка привода меняют движение, усилие, время — позиция ползуна может уйти
// на тысячи σ. Но температура масла не прыгнет на 300 °C, а аккумулятор на 1000 бар за один
// цикл: такие числа бывают только от сдвига записей в .shp. Сбои пресса — это данные для
// диагностики, их выбрасывать нельзя.
const (
	GarbageZ     = 1000
	GarbageFeats = 5
	GarbageInert = 2
)

var reInert = regexp.MustCompile(`^(temp_|level_|casedrain_|accu_)`)

// Garbage — цикл, похожий на повреждённый .shp по самим значениям.
type Garbage struct {
	Index int
	Feats int     // сколько признаков ≥ 1000σ
	Inert int     // из них инертных (температура, уровень, дренаж, аккумулятор)
	Worst string  // признак с самым большим |z|
	Z     float64 // его |z|
}

// FindGarbage ищет циклы с физически невозможными значениями — для таблиц, сделанных до
// проверки целостности (там нет shp_damaged). σ — робастная по всем рабочим циклам,
// сравнение — со скользящей медианой соседей (как в аномалиях), норма для этого не нужна.
func FindGarbage(prod *Dataset, eng []string) []Garbage {
	n := prod.Len()
	cnt, inert := make([]int, n), make([]int, n)
	worst := make([]Garbage, n)
	for _, c := range eng {
		x := prod.Col(c)
		_, s := RobustStats(x, x)
		rm := RollingMedian(x, AnomWindow, AnomMinPeriods)
		isInert := reInert.MatchString(c)
		for i := range n {
			z := math.Abs((x[i] - rm[i]) / s)
			if z >= GarbageZ {
				cnt[i]++
				if isInert {
					inert[i]++
				}
				if z > worst[i].Z {
					worst[i].Worst, worst[i].Z = c, z
				}
			}
		}
	}
	var out []Garbage
	for i, k := range cnt {
		if k >= GarbageFeats && inert[i] >= GarbageInert {
			g := worst[i]
			g.Index, g.Feats, g.Inert = i, k, inert[i]
			out = append(out, g)
		}
	}
	return out
}
