package analyze

import (
	"math"
	"sort"
	"strconv"
	"time"
)

// И1 — норма по штампу.
//
// Номера штампа в записи нет. Есть время его замены: каналы DieTimeOfChange_Day (ГГММДД)
// и DieTimeOfChange_Time (ЧЧММСС) — одинаковые у всех циклов одной установки. По ним
// выделяется каждая УСТАНОВКА штампа (поставили тот же штамп повторно или другой — не узнать).
// Счётчик ходов (die_strokes) для этого хуже: его «сбросы» дают и повреждённые файлы.
//
// Зачем: штампы разные. На OP1 медиана усилия по установкам — от 17 700 до 35 000 кН,
// нижняя точка ползуна — от 1324 до 1335 мм. Общая норма это смазывает: усилие 35 000 для
// одного штампа — норма, для другого — отклонение. Поэтому:
//   - DieHealth — карта «подсистема × установка» (та же формула, что у карты здоровья);
//   - Drift — «норма своего штампа»: конец установки против её же начала (износ штампа и
//     то, что поменялось за время его работы, — общей нормой это не видно).
//
// Эталон на pandas — ref/dump_dies.py; совпадение с Go проверено бит в бит.

const (
	DieDriftMinCycles = 30 // меньше — дрейф не считаем: база и «сейчас» по 10 циклов перекрывались бы
	DieDriftZ         = 3.0
	dieMaxAge         = 60 * 24 * time.Hour // замена раньше начала цикла больше чем на 60 дней — мусор
)

// DieInstall — одна установка штампа.
type DieInstall struct {
	Change   time.Time // когда поменяли штамп (из DieTimeOfChange)
	From, To time.Time // первый и последний рабочий цикл
	Cycles   int       // рабочих циклов
	Strokes  float64   // ходов штампа к последнему циклу (макс. die_strokes)
	Drift    []DieDrift
	HasDrift bool // дрейф считался (циклов ≥ 30)
	Recipe   Recipe
	rows     []int
}

// Recipe — уставки, которые постоянны внутри установки штампа и меняются между установками:
// похоже, это рецепт колеса (типа изделия в записи нет). Найдено перебором каналов:
// скорость опускания ползуна (45/50/60/70 мм/с), позиция нижних цилиндров (645/650/655),
// позиция SCu (0/49/51/62). Значение — самое частое в установке (как pandas mode().iat[0]).
type Recipe struct {
	SpeedDown, LCylPos, SCuPos float64
}

// RecipeChannels — каналы рецепта в таблице признаков.
var RecipeChannels = [3]string{"a040_slide_SpeedDownTargetSpeed_v__max", "a150_LCyl_Left_SetPos__min", "a099_SCu_SpeedDownTargetPos_s__max"}

// String — «60 / 650 / 0»; NaN — «—».
func (r Recipe) String() string {
	f := func(v float64) string {
		if math.IsNaN(v) {
			return "—"
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return f(r.SpeedDown) + " / " + f(r.LCylPos) + " / " + f(r.SCuPos)
}

// DieDrift — признак, который за время работы штампа ушёл от его же начала.
type DieDrift struct {
	Feature   string
	Z         float64 // медиана последних 20% циклов установки, σ от её первых 20%
	Base, Now float64 // медианы: начало установки и конец
}

// DieInstalls — установки штампа по времени замены. nil — каналов DieTimeOfChange нет.
func (r *Result) DieInstalls() []DieInstall {
	p := r.Prod
	dmin, dmax := p.Col("a124_DieTimeOfChange_Day__min"), p.Col("a124_DieTimeOfChange_Day__max")
	tmin, tmax := p.Col("a125_DieTimeOfChange_Time__min"), p.Col("a125_DieTimeOfChange_Time__max")
	if dmax == nil || tmax == nil || dmin == nil || tmin == nil {
		return nil
	}
	byChange := map[time.Time]*DieInstall{}
	for i := range p.Len() {
		if dmin[i] != dmax[i] || tmin[i] != tmax[i] { // поменялось внутри записи — не доверяем
			continue
		}
		ct, ok := decodeChange(dmax[i], tmax[i])
		if !ok || ct.After(p.Times[i]) || p.Times[i].Sub(ct) > dieMaxAge {
			continue
		}
		d := byChange[ct]
		if d == nil {
			d = &DieInstall{Change: ct, From: p.Times[i], To: p.Times[i], Strokes: math.NaN()}
			byChange[ct] = d
		}
		d.rows = append(d.rows, i)
	}
	out := make([]DieInstall, 0, len(byChange))
	ds := p.Col("die_strokes")
	for _, d := range byChange {
		sort.SliceStable(d.rows, func(a, b int) bool { return p.Times[d.rows[a]].Before(p.Times[d.rows[b]]) })
		d.Cycles = len(d.rows)
		d.From, d.To = p.Times[d.rows[0]], p.Times[d.rows[len(d.rows)-1]]
		if ds != nil {
			d.Strokes = nanMaxOf(pick(ds, d.rows))
		}
		r.dieDrift(d)
		d.Recipe = Recipe{modeOf(p.Col(RecipeChannels[0]), d.rows), modeOf(p.Col(RecipeChannels[1]), d.rows), modeOf(p.Col(RecipeChannels[2]), d.rows)}
		out = append(out, *d)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Change.Before(out[b].Change) })
	return out
}

// dieDrift — «норма своего штампа»: база — первые max(10, 20%) циклов установки, «сейчас» —
// последние столько же; med, σ — RobustStats(база, вся установка), как у общей нормы.
func (r *Result) dieDrift(d *DieInstall) {
	if d.Cycles < DieDriftMinCycles {
		return
	}
	d.HasDrift = true
	k := max(10, int(float64(d.Cycles)*0.2))
	base, now := d.rows[:k], d.rows[d.Cycles-k:]
	for _, f := range r.Cols.Eng {
		col := r.Prod.Col(f)
		med, sig := RobustStats(pick(col, base), pick(col, d.rows))
		mNow := NanMedian(pick(col, now))
		z := (mNow - med) / sig
		if math.Abs(z) >= DieDriftZ {
			d.Drift = append(d.Drift, DieDrift{f, z, NanMedian(pick(col, base)), mNow})
		}
	}
	sort.SliceStable(d.Drift, func(a, b int) bool { return math.Abs(d.Drift[a].Z) > math.Abs(d.Drift[b].Z) })
}

// DieHealth — карта «подсистема × установка штампа» (формула карты здоровья).
func (r *Result) DieHealth(inst []DieInstall) *GroupHealth {
	key := make([]float64, r.Prod.Len())
	for i := range key {
		key[i] = math.NaN()
	}
	for k, d := range inst {
		for _, i := range d.rows {
			key[i] = float64(k)
		}
	}
	return r.groupHealth(key, nil)
}

// decodeChange — ГГММДД и ЧЧММСС в дату-время. Части проверяются сами: time.Date молча
// переносит «26 часов» на следующий день, а это мусор повреждённого файла (pd.Timestamp
// в Python на таком падает — и цикл отбрасывается).
func decodeChange(day, tod float64) (time.Time, bool) {
	if math.IsNaN(day) || math.IsNaN(tod) || day != math.Trunc(day) || tod != math.Trunc(tod) || day < 0 || tod < 0 {
		return time.Time{}, false
	}
	d, t := int(day), int(tod)
	y, mo, dd := 2000+d/10000, d/100%100, d%100
	h, mi, s := t/10000, t/100%100, t%100
	if d >= 1000000 || t >= 1000000 || mo < 1 || mo > 12 || dd < 1 || h > 23 || mi > 59 || s > 59 {
		return time.Time{}, false
	}
	ct := time.Date(y, time.Month(mo), dd, h, mi, s, 0, time.UTC)
	if ct.Day() != dd { // 30 февраля и т. п.
		return time.Time{}, false
	}
	return ct, true
}

// MainRecipe — рецепт, по которому сделано больше всего рабочих циклов (при равенстве — более ранний).
func MainRecipe(inst []DieInstall) Recipe {
	n := map[Recipe]int{}
	best, bestN := Recipe{math.NaN(), math.NaN(), math.NaN()}, 0
	for _, d := range inst {
		n[d.Recipe] += d.Cycles
	}
	for _, d := range inst { // по времени: при равенстве побеждает более ранний
		if n[d.Recipe] > bestN {
			best, bestN = d.Recipe, n[d.Recipe]
		}
	}
	return best
}

// modeOf — самое частое значение без NaN; при равенстве — меньшее (pandas mode().iat[0]). Нет — NaN.
func modeOf(col []float64, rows []int) float64 {
	if col == nil {
		return math.NaN()
	}
	cnt := map[float64]int{}
	for _, i := range rows {
		if v := col[i]; !math.IsNaN(v) {
			cnt[v]++
		}
	}
	best, bn := math.NaN(), 0
	for v, c := range cnt {
		if c > bn || c == bn && v < best {
			best, bn = v, c
		}
	}
	return best
}

func nanMaxOf(x []float64) float64 {
	m := math.NaN()
	for _, v := range x {
		if !math.IsNaN(v) && (math.IsNaN(m) || v > m) {
			m = v
		}
	}
	return m
}

// Median — медиана признака f по рабочим циклам установки.
func (d DieInstall) Median(p *Dataset, f string) float64 {
	col := p.Col(f)
	if col == nil {
		return math.NaN()
	}
	return NanMedian(pick(col, d.rows))
}
