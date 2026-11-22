package archive

import (
	"math"
	"sort"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// Pick — файл, который стоит сохранить, и почему.
type Pick struct {
	File    string // полный путь, как в таблице признаков
	Op      string
	Reasons []string
}

// Сколько циклов брать вокруг событий.
const (
	BeforeStop = 10 // последних рабочих циклов перед остановом
	AfterStart = 5  // первых циклов после пуска
	RunStart   = 10 // первых циклов «отрезка вне нормы» и нового рецепта
	// AnomPerDay — сколько аномальных циклов в сутки сохранять (самые сильные). «Аномальных»
	// по порогу 30–47% циклов (шум клапанов CCyl) — все сохранять нельзя: это десятки ГБ.
	AnomPerDay = 20
)

// Причины — короткие, по ним в журнале и на странице видно, зачем файл сохранён.
const (
	WhyAnomaly  = "аномальный"
	WhyBefore   = "перед остановом"
	WhyAfter    = "после пуска"
	WhyDamaged  = "повреждённый"
	WhyOffRun   = "отрезок вне нормы"
	WhyRecipe   = "другой рецепт"
	WhyFrequent = "фон"
)

// Select — что сохранить по результату анализа операции. every — каждый N-й рабочий цикл
// как фон (0 — без фона). Порядок: сначала важное (без фона), внутри — по времени, чтобы
// при нехватке места первым сохранилось нужное.
func Select(r *analyze.Result, op string, every int) []Pick {
	why := map[string][]string{}
	add := func(file, reason string) {
		if file == "" {
			return
		}
		for _, x := range why[file] {
			if x == reason {
				return
			}
		}
		why[file] = append(why[file], reason)
	}
	p, all := r.Prod, r.All

	// аномальные — самые сильные в каждых сутках (по силе, как в списке аномальных)
	byDay := map[int64][]int{}
	for i, flagged := range r.Anom.Flag {
		if flagged {
			d := p.Times[i].Unix() / 86400
			byDay[d] = append(byDay[d], i)
		}
	}
	for _, rows := range byDay {
		sort.SliceStable(rows, func(a, b int) bool { return r.Anom.Score[rows[a]] > r.Anom.Score[rows[b]] })
		for _, i := range rows[:min(AnomPerDay, len(rows))] {
			add(p.File[i], WhyAnomaly)
		}
	}
	for _, f := range r.DamagedF {
		add(f, WhyDamaged)
	}
	if r.Stops != nil {
		for _, s := range r.Stops.Stops {
			// последние рабочие циклы до останова (включительно) и первые циклы после пуска
			var before []int
			for i, t := range p.Times {
				if !t.After(s.From) {
					before = append(before, i)
				}
			}
			for _, i := range before[max(0, len(before)-BeforeStop):] {
				add(p.File[i], WhyBefore)
			}
			n := 0
			for i, t := range all.Times {
				if !t.Before(s.Resume) && n < AfterStart {
					add(all.File[i], WhyAfter)
					n++
				}
			}
		}
	}
	for _, run := range r.OffRuns() {
		if len(run.Off) == 0 {
			continue
		}
		n := 0
		for i, t := range p.Times {
			if !t.Before(run.From) && !t.After(run.To) && n < RunStart {
				add(p.File[i], WhyOffRun)
				n++
			}
		}
	}
	inst := r.DieInstalls()
	main := analyze.MainRecipe(inst)
	for _, d := range inst {
		if d.Recipe == main {
			continue
		}
		n := 0
		for i, t := range p.Times {
			if !t.Before(d.From) && !t.After(d.To) && n < RunStart {
				add(p.File[i], WhyRecipe)
				n++
			}
		}
	}

	important := make([]Pick, 0, len(why))
	for f, rs := range why {
		important = append(important, Pick{f, op, rs})
	}
	order := fileTimes(r)
	sort.Slice(important, func(a, b int) bool { return order[important[a].File] < order[important[b].File] })

	// фон — каждый N-й рабочий цикл. N-й по счётчику ходов штампа, а не по номеру строки:
	// номер строки сдвигается, когда таблица растёт или в неё досчитываются пропуски, а ход
	// штампа у цикла постоянный. Где штампа нет (КПС) — по номеру заготовки из имени файла,
	// он у цикла тоже постоянный. Номер строки — только если нет ни того, ни другого.
	var bg []Pick
	if every > 0 {
		ds := p.Col("die_strokes")
		for i := range p.Len() {
			on := i%every == 0
			if ds != nil && !math.IsNaN(ds[i]) && ds[i] == math.Trunc(ds[i]) {
				on = int(ds[i])%every == 0
			} else if n := shp.ParseName(p.File[i], nil); n.HasIDs {
				on = n.Piece%every == 0
			}
			if on && why[p.File[i]] == nil {
				bg = append(bg, Pick{p.File[i], op, []string{WhyFrequent}})
			}
		}
	}
	return append(important, bg...)
}

// fileTimes — порядковый номер файла по времени (для сортировки отобранного).
func fileTimes(r *analyze.Result) map[string]int {
	m := make(map[string]int, r.All.Len()+len(r.DamagedF))
	for i, f := range r.All.File {
		m[f] = i
	}
	for k, f := range r.DamagedF { // повреждённых в All нет — в конец, по порядку
		if _, ok := m[f]; !ok {
			m[f] = r.All.Len() + k
		}
	}
	return m
}
