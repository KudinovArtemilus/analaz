package analyze

import (
	"slices"
	"time"
)

// NormPeriod — кандидат в базовый период нормы.
type NormPeriod struct {
	From, To time.Time // первый и последний рабочий цикл периода
	Days     int       // сколько календарных суток занял
	Cycles   int       // рабочих циклов
	Stops    int       // пауз дольше GapMin между рабочими циклами
	AnomPct  float64   // доля аномальных циклов, %
}

// NormSuggestMaxDays — дольше стольких суток период не растягиваем: норма должна быть
// «одним состоянием» машины, а не смесью недель.
const NormSuggestMaxDays = 7

// SuggestNormPeriods — до k спокойных периодов для новой нормы по результату анализа r
// (уже отобраны операция и колесо/рецепт). Период начинается с начала суток и растягивается
// на следующие сутки, пока не наберётся minCycles рабочих циклов. Спокойный — меньше остановов,
// затем меньше аномальных циклов. Периоды не пересекаются.
//
// Это подсказка, а не решение: что машина в эти сутки работала исправно, знает только человек.
func SuggestNormPeriods(r *Result, minCycles, k int) []NormPeriod {
	p := r.Prod
	n := p.Len()
	if n < minCycles || minCycles <= 0 {
		return nil
	}
	gap := time.Duration(r.Opts.GapMin * float64(time.Minute))
	if gap <= 0 {
		gap = time.Duration(StopGapMin * float64(time.Minute))
	}
	anom := func(i int) bool {
		return r.Anom != nil && i < len(r.Anom.Flag) && r.Anom.Flag[i]
	}
	var cand []NormPeriod
	for i := 0; i < n; {
		day := p.Times[i].Truncate(24 * time.Hour)
		// конец периода: первые сутки, в которых набралось minCycles
		j, days := i, 0
		for days < NormSuggestMaxDays && j < n {
			end := day.Add(time.Duration(days+1) * 24 * time.Hour)
			for j < n && p.Times[j].Before(end) {
				j++
			}
			days++
			if j-i >= minCycles {
				break
			}
		}
		if j-i >= minCycles {
			c := NormPeriod{From: p.Times[i], To: p.Times[j-1], Days: days, Cycles: j - i}
			a := 0
			for q := i; q < j; q++ {
				if anom(q) {
					a++
				}
				if q > i && p.Times[q].Sub(p.Times[q-1]) > gap {
					c.Stops++
				}
			}
			c.AnomPct = 100 * float64(a) / float64(c.Cycles)
			cand = append(cand, c)
		}
		// следующие сутки
		next := day.Add(24 * time.Hour)
		for i < n && p.Times[i].Before(next) {
			i++
		}
	}
	slices.SortStableFunc(cand, func(a, b NormPeriod) int {
		// остановов на 100 циклов, потом доля аномалий, потом раньше по времени
		sa, sb := float64(a.Stops)/float64(a.Cycles), float64(b.Stops)/float64(b.Cycles)
		switch {
		case sa < sb:
			return -1
		case sa > sb:
			return 1
		case a.AnomPct < b.AnomPct:
			return -1
		case a.AnomPct > b.AnomPct:
			return 1
		}
		return a.From.Compare(b.From)
	})
	var out []NormPeriod
	for _, c := range cand {
		if len(out) == k {
			break
		}
		if !slices.ContainsFunc(out, func(o NormPeriod) bool { return !c.From.After(o.To) && !o.From.After(c.To) }) {
			out = append(out, c)
		}
	}
	return out
}
