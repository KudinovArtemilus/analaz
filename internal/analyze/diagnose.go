package analyze

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Status — светофор узла.
type Status int

const (
	OK Status = iota
	Warn
	Alarm
)

// StatusText — как STATUS_TXT в diagnose.py.
var StatusText = map[Status]string{OK: "НОРМА", Warn: "НАБЛЮДАТЬ", Alarm: "ПРОБЛЕМА"}

// Diag — вывод по одному узлу.
type Diag struct {
	Name, Group string
	Status      Status
	Facts       []string // что видно в цифрах, самые тревожные первыми
	Forecast    []string // через сколько дойдёт до порога, если тренд сохранится
	Cause       string
	Check       string
}

type fact struct {
	st  Status
	txt string
}

var (
	rePeerCasedrain = regexp.MustCompile(`^casedrain_md\d_mean$`)
	rePeerPivot     = regexp.MustCompile(`^pump\d\d_pivot_err_active$`)
	rePumpPair      = regexp.MustCompile(`^pump(\d)(\d)_`)
)

// Evaluate повторяет diagnose.evaluate: светофор по каждому узлу.
// P — рабочие циклы по времени, normMed — медиана признака в норме (B[f].median() в Python
// или из хранимой нормы), sig — робастная σ признаков, spanH — длина данных в часах,
// T — оцениваемое окно (nil — последние 15% циклов).
func Evaluate(P *Dataset, normMed func(string) float64, sig map[string]float64, spanH float64, T *Dataset) ([]Diag, []string) {
	window := T != nil
	if T == nil {
		T = P.Rows(lastN(P.Len(), max(5, int(float64(P.Len())*0.15))))
	}
	th := hoursFrom(P.Times, 0)
	peerCache := map[string]*peerSet{}
	peers := func(kind string) *peerSet {
		if p, ok := peerCache[kind]; ok {
			return p
		}
		re := rePeerCasedrain
		if kind == "pivot" {
			re = rePeerPivot
		}
		p := &peerSet{cur: map[string]float64{}}
		for _, c := range P.Cols {
			if re.MatchString(c) && notna(P.Col(c)) > 3 {
				p.cols = append(p.cols, c)
				p.cur[c] = NanMedian(T.Col(c))
			}
		}
		peerCache[kind] = p
		return p
	}

	var out []Diag
	var notes []string
	mixed := mixedRegimes(P) // несколько колёс — показатели «внутри колеса» не оцениваем
	for _, u := range Units() {
		status, have := OK, false
		var facts []fact
		var forecast []string
		for _, in := range u.Ind {
			if in.PerRegime && mixed {
				continue
			}
			x := P.Col(in.Feat)
			if x == nil || notna(x) < 5 {
				continue
			}
			have = true
			base, cur := normMed(in.Feat), NanMedian(T.Col(in.Feat))
			if !finite(base) || !finite(cur) {
				continue
			}
			s, ok := sig[in.Feat]
			if !ok {
				s = math.NaN()
			}
			rel := (cur - base) / (math.Abs(base) + 1e-9)
			z := 0.0
			if finite(s) && s > 0 {
				z = (cur - base) / s
			}
			sign := map[string]float64{"up": 1, "down": -1, "both": 0}[in.Bad]
			badz, badrel := z*sign, rel*sign
			if sign == 0 {
				badz, badrel = math.Abs(z), math.Abs(rel)
			}

			st := OK
			// абсолютные пороги: для «down» — по минимуму за последний период, иначе по максимуму
			worst := nanMax(T.Col(in.Feat))
			if in.Bad == "down" {
				worst = nanMin(T.Col(in.Feat))
			}
			beyond := func(lim float64) bool {
				return !math.IsNaN(lim) && (sign < 0 && worst < lim || sign > 0 && worst > lim)
			}
			if beyond(in.AbsAlarm) {
				st = Alarm
			} else if beyond(in.AbsWarn) {
				st = max(st, Warn)
			}
			if badz > 8 && badrel > in.AlarmRel {
				st = Alarm
			} else if badz > 4 && badrel > in.WarnRel {
				st = max(st, Warn)
			}

			// тренд и прогноз
			slope := RobustSlope(th, x)
			rho := SpearmanXY(th, x)
			eta := math.NaN()
			target := in.AbsAlarm
			if math.IsNaN(target) && sign != 0 {
				target = base * (1 + sign*in.AlarmRel)
			}
			if !u.Clamp && !math.IsNaN(target) && finite(slope) && slope != 0 && math.Abs(rho) > 0.4 && sign*slope > 0 {
				if etaH := (target - cur) / slope; etaH > 0 {
					eta = etaH / 24
					if spanH < 72 && math.Abs(badz) < 3 {
						eta = math.NaN() // короткие данные и нет заметного отклонения — прогноз не показываем
					} else if eta < 14 && st < Warn && spanH >= 72 {
						st = Warn
					}
				}
			}
			line := fmt.Sprintf("%s: сейчас %s, норма %s (%+.0f%%)", in.Label, fmtv(cur, in.Unit), fmtv(base, in.Unit), rel*100)
			if math.Abs(base) < 1e-9 { // норма 0: процент бессмыслен (у КПС было «+500000000000%»)
				line = fmt.Sprintf("%s: сейчас %s, в норме было 0", in.Label, fmtv(cur, in.Unit))
			}
			if in.Bad == "down" && finite(worst) {
				line += ", минимум за последний период " + fmtv(worst, in.Unit)
			}
			if st > OK || math.Abs(badz) > 3 {
				facts = append(facts, fact{st, line})
			}
			if !math.IsNaN(eta) {
				when := fmt.Sprintf("%.0f ч", eta*24)
				if eta >= 1 {
					when = fmt.Sprintf("%.0f дн.", eta)
				}
				forecast = append(forecast, fmt.Sprintf("«%s» дойдёт до %s примерно через %s (если тренд сохранится)", in.Label, fmtv(target, in.Unit), when))
			}
			status = max(status, st)
		}

		// сравнение с соседями: 6 одинаковых насосов, 12 регуляторов
		if u.PeerKind != "" && P.Col(u.PeerFeat) != nil {
			ps := peers(u.PeerKind)
			f := u.PeerFeat
			if v, ok := ps.cur[f]; ok && len(ps.cols) >= 3 && finite(v) {
				var others []float64
				for _, c := range ps.cols {
					if c != f && !math.IsNaN(ps.cur[c]) {
						others = append(others, ps.cur[c])
					}
				}
				om := NanMedian(others)
				if om > 0 {
					ratio := v / om
					switch {
					case u.PeerKind == "casedrain" && ratio < 0.25:
						status = max(status, Warn)
						facts = append(facts, fact{Warn, fmt.Sprintf("дренаж %s, у остальных насосов ~%s — в %.0f раз меньше. Насос не может "+
							"не иметь дренажа: вероятнее, неисправен датчик/канал или насос не работает",
							fmtv(v, "бар"), fmtv(om, "бар"), 1/math.Max(ratio, 1e-3))})
						u.Cause = "Подозрение на ДАТЧИК, а не на насос: значение почти нулевое на фоне остальных."
						u.Check = "Проверить датчик давления дренажа MD (0 бар при работающем насосе?), кабель, масштабирование канала в ПЛК; убедиться, что насос работает."
					case ratio > 1.5:
						pr, havePr := 0.0, false
						m := rePumpPair.FindStringSubmatch(f)
						if m != nil {
							other := "1"
							if m[2] == "1" {
								other = "2"
							}
							partner := "pump" + m[1] + other + "_" + strings.SplitN(f, "_", 2)[1]
							if pv, ok := ps.cur[partner]; ok && pv > 0 {
								pr, havePr = v/pv, true
								if pr >= 0.8 && pr <= 1.25 {
									pair := []string{fmtv(v, ""), fmtv(pv, "")}
									sort.Strings(pair)
									notes = append(notes, fmt.Sprintf("Пара насосов %s.1/%s.2: оба в %.1f раза «хуже» остальных и одинаковы между собой (%s и %s). "+
										"Так ведёт себя вся пара — скорее это особенность её контура/режима, а не износ одного насоса. "+
										"Тревога, если один из пары начнёт отличаться от второго.", m[1], m[1], ratio, pair[0], pair[1]))
									break
								}
							}
						}
						st := Warn
						if ratio > 2.5 {
							st = Alarm
						}
						status = max(status, st)
						txt := fmt.Sprintf("хуже соседних в %.1f раза (%s против ~%s у остальных)", ratio, fmtv(v, ""), fmtv(om, ""))
						if havePr {
							txt += fmt.Sprintf(", и хуже напарника по паре в %.1f раза", pr)
						}
						facts = append(facts, fact{st, txt})
					}
				}
			}
		}

		// напарник (КПС): левая ось против правой, P1.1 против P1.2. Одинаковых узлов по двое,
		// поэтому не «против группы», как у пресса, а против второго из пары. Отличие было и в
		// норме — это особенность стороны или датчика (проверить один раз), а не износ.
		if u.PairFeat != "" && P.Col(u.PairFeat) != nil && P.Col(u.PairOther) != nil {
			v, o := NanMedian(T.Col(u.PairFeat)), NanMedian(T.Col(u.PairOther))
			bv, bo := normMed(u.PairFeat), normMed(u.PairOther)
			if finite(v) && finite(o) && o > 0 && v/o > PairRatioWarn {
				ratio := v / o
				always := finite(bv) && finite(bo) && bo > 0 && bv/bo > PairRatioWarn
				st := Warn
				if ratio > PairRatioAlarm && !always {
					st = Alarm
				}
				txt := fmt.Sprintf("хуже напарника (%s) в %.1f раза: %s против %s", u.PairName, ratio, fmtv(v, ""), fmtv(o, ""))
				if always {
					txt += fmt.Sprintf("; и в норме было так же (в %.1f раза) — скорее особенность этой стороны или датчика: проверить один раз", bv/bo)
				} else if finite(bv) && finite(bo) && bo > 0 {
					txt += fmt.Sprintf("; в норме было в %.1f раза — отличие появилось недавно", bv/bo)
				}
				status = max(status, st)
				facts = append(facts, fact{st, txt})
			}
		}

		// выбросы (сервоклапаны)
		if x := P.Col(u.SpikeFeat); u.SpikeFeat != "" && x != nil && notna(x) > 5 {
			med := NanMedian(x)
			sg := sig[u.SpikeFeat] // нет в норме → 0, как sig.get(f, 0)
			thr := 3 * med
			if v := med + 6*sg; v > thr { // Python max(a, b): при NaN во втором остаётся первое
				thr = v
			}
			W := P
			if window {
				W = T // в суточном отчёте считаем срывы только за эти сутки
			}
			w := W.Col(u.SpikeFeat)
			var when []string
			hits := 0
			for i, v := range w {
				if v > thr {
					hits++
					if len(when) < 6 {
						when = append(when, W.Times[i].Format("02.01 15:04"))
					}
				}
			}
			if hits > 0 {
				rate := float64(hits) / float64(W.Len())
				st := OK
				if rate >= 0.02 {
					st = Warn
				}
				status = max(status, st)
				facts = append(facts, fact{st, fmt.Sprintf("%s: %d раз(а) из %d циклов (%.0f%%), порог %s, обычно %s. Когда: %s",
					u.SpikeLabel, hits, W.Len(), rate*100, fmtv(thr, ""), fmtv(med, ""), strings.Join(when, ", "))})
			}
		}

		// зажим штампа: падение давления с момента перезажима
		if u.Clamp && P.Col("die_strokes") != nil {
			cf, fc := clampFacts(P, T)
			facts = append(facts, cf...)
			forecast = append(forecast, fc...)
		}

		if !have && len(facts) == 0 {
			continue
		}
		sort.SliceStable(facts, func(a, b int) bool { return facts[a].st > facts[b].st })
		d := Diag{Name: u.Name, Group: u.Group, Status: status, Forecast: forecast, Cause: u.Cause, Check: u.Check}
		for _, f := range facts {
			d.Facts = append(d.Facts, f.txt)
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Status != out[b].Status {
			return out[a].Status > out[b].Status
		}
		if out[a].Group != out[b].Group {
			return out[a].Group < out[b].Group
		}
		return out[a].Name < out[b].Name
	})
	return out, uniq(notes)
}

type peerSet struct {
	cols []string
	cur  map[string]float64
}

// clampFacts — разбор зажима штампа по периодам между заменами штампа.
func clampFacts(P, T *Dataset) ([]fact, []string) {
	var facts []fact
	var forecast []string
	resets := DieChanges(P.Col("die_strokes"))
	start := 0
	if len(resets) > 0 {
		start = resets[len(resets)-1]
	}
	seg := P.Rows(rangeIdx(start, P.Len()))
	for _, c := range [][2]string{{"dieclamp_c1_min", "контур 1"}, {"dieclamp_c2_min", "контур 2"}} {
		if P.Col(c[0]) != nil && seg.Len() >= 4 {
			sl := RobustSlope(hoursFrom(seg.Times, 0), seg.Col(c[0]))
			if finite(sl) && sl < 0 {
				facts = append(facts, fact{OK, fmt.Sprintf("%s: после последнего перезажима падает ~%.1f бар/ч", c[1], -sl)})
			}
		}
	}
	// по каждому периоду между заменами штампа: с какого давления начали, минимум, через сколько часов ниже порогов
	bounds := slices.Concat([]int{0}, resets, []int{P.Len()})
	var per []string
	var hrsWarn, hrsAlarm []float64
	c2 := P.Col("dieclamp_c2_min")
	for k := 0; k+1 < len(bounds); k++ {
		a, b := bounds[k], bounds[k+1]
		if b-a < 3 || c2 == nil {
			continue
		}
		g := c2[a:b]
		th0 := hoursFrom(P.Times[a:b], 0)
		txt := fmt.Sprintf("%s–%s: старт %s, минимум %s", P.Times[a].Format("02.01 15:04"), P.Times[b-1].Format("02.01 15:04"),
			fmtv(NanMedian(g[:2]), "бар"), fmtv(nanMin(g), "бар"))
		for _, lim := range []struct {
			thr float64
			acc *[]float64
		}{{DieclampWarn, &hrsWarn}, {DieclampAlarm, &hrsAlarm}} {
			for i, v := range g {
				if v < lim.thr {
					*lim.acc = append(*lim.acc, th0[i])
					txt += fmt.Sprintf(", ниже %g бар через %.0f ч после перезажима", lim.thr, th0[i])
					break
				}
			}
		}
		per = append(per, txt)
	}
	if len(per) > 0 {
		facts = append(facts, fact{OK, "контур 2 по периодам между заменами штампа: " + strings.Join(per, "; ")})
	}
	if len(hrsAlarm) > 0 {
		forecast = append(forecast, fmt.Sprintf("после перезажима штампа контур 2 опускается ниже %g бар примерно через %.0f ч работы (по %d периодам). "+
			"Если штамп стоит дольше — давление уходит ниже порога", DieclampAlarm, NanMedian(hrsAlarm), len(hrsAlarm)))
	} else if len(hrsWarn) > 0 {
		forecast = append(forecast, fmt.Sprintf("после перезажима штампа контур 2 опускается ниже %g бар примерно через %.0f ч работы", DieclampWarn, NanMedian(hrsWarn)))
	}
	facts = append(facts, fact{OK, fmt.Sprintf("пороги %g/%g бар приняты условно — уточните допустимый минимум давления зажима по паспорту пресса", DieclampWarn, DieclampAlarm)})
	if c1t, c2t := T.Col("dieclamp_c1_min"), T.Col("dieclamp_c2_min"); c1t != nil && c2t != nil {
		diff := make([]float64, len(c1t))
		for i := range diff {
			diff[i] = c1t[i] - c2t[i]
		}
		if d := NanMedian(diff); d > 5 {
			facts = append(facts, fact{OK, fmt.Sprintf("контур 2 ниже контура 1 на ~%.0f бар — утечка, вероятнее, в контуре 2", d)})
		}
	}
	return facts, forecast
}

// DieChanges — номера циклов, первых после замены штампа: счётчик ходов упал минимум на 50 и больше чем вдвое.
func DieChanges(ds []float64) []int {
	var out []int
	for i := 1; i < len(ds); i++ {
		if isDieChange(ds[i-1], ds[i]) {
			out = append(out, i)
		}
	}
	return out
}

func isDieChange(prev, cur float64) bool { return prev-cur > 50 && cur < 0.5*prev }

// StopsSummary — остановы с привязкой к замене штампа (stops_summary в diagnose.py).
func StopsSummary(all *Dataset, gapThrMin float64) []string {
	var rows []string
	ds := all.Col("die_strokes")
	for i := 1; i < all.Len(); i++ {
		g := all.Times[i].Sub(all.Times[i-1]).Seconds() / 60
		if !(g > gapThrMin) {
			continue
		}
		why := "причина не видна в данных"
		if ds != nil && isDieChange(ds[i-1], ds[i]) {
			why = fmt.Sprintf("ЗАМЕНА ШТАМПА (счётчик ходов %d → %d)", int(ds[i-1]), int(ds[i]))
		}
		rows = append(rows, fmt.Sprintf("%s → %s (%.1f ч): %s", all.Times[i-1].Format("02.01 15:04"), all.Times[i].Format("02.01 15:04"), g/60, why))
	}
	return rows
}

// DiagnosisText — diagnosis.txt (to_text в diagnose.py).
func DiagnosisText(diag []Diag, op, period string, n int, stops []string, short bool, notes []string) string {
	machine := "ПРЕССА" // как в diagnose.py — у пресса текст совпадает с Python побайтно
	if len(diag) > 0 && strings.HasPrefix(diag[0].Group, "КПС") {
		machine = "КПС"
	}
	L := []string{fmt.Sprintf("ДИАГНОЗ %s %s — %s, циклов: %d", machine, op, period, n), ""}
	if short {
		L = append(L, "ВНИМАНИЕ: данных меньше недели — прогнозы и тренды предварительные; сравнение «насос с насосом» надёжно.", "")
	}
	for _, s := range []Status{Alarm, Warn} {
		var names []string
		for _, d := range diag {
			if d.Status == s {
				names = append(names, d.Name)
			}
		}
		list := "нет"
		if len(names) > 0 {
			list = strings.Join(names, ", ")
		}
		L = append(L, StatusText[s]+": "+list)
	}
	nOK := 0
	for _, d := range diag {
		if d.Status == OK {
			nOK++
		}
	}
	L = append(L, fmt.Sprintf("НОРМА: %d узлов", nOK), "")
	for _, d := range diag {
		if d.Status == OK {
			continue
		}
		L = append(L, fmt.Sprintf("[%s] %s", StatusText[d.Status], d.Name))
		for _, f := range d.Facts {
			L = append(L, "   • "+f)
		}
		for _, f := range d.Forecast {
			L = append(L, "   ⏱ "+f)
		}
		L = append(L, "   Причина: "+d.Cause, "   Проверить: "+d.Check, "")
	}
	if len(notes) > 0 {
		L = append(L, "НАБЛЮДЕНИЯ БЕЗ ТРЕВОГИ:")
		for _, x := range notes {
			L = append(L, "   • "+x)
		}
		L = append(L, "")
	}
	if len(stops) > 0 {
		L = append(L, "ОСТАНОВЫ:")
		for _, s := range stops {
			L = append(L, "   • "+s)
		}
	}
	return strings.Join(L, "\n")
}

// RobustSlope — наклон по Тейлу–Сену: медиана наклонов по всем парам точек (_robust_slope).
// Один выброс почти не влияет, в отличие от МНК. Больше 150 точек — равномерная выборка 150.
func RobustSlope(t, y []float64) float64 {
	var tt, yy []float64
	for i := range t {
		if finite(t[i]) && finite(y[i]) {
			tt, yy = append(tt, t[i]), append(yy, y[i])
		}
	}
	n := len(tt)
	if n < 6 {
		return math.NaN()
	}
	if n > 150 { // np.linspace(0, n-1, 150).astype(int)
		step := float64(n-1) / 149
		ts, ys := make([]float64, 150), make([]float64, 150)
		for i := range 150 {
			k := int(float64(i) * step)
			if i == 149 {
				k = n - 1
			}
			ts[i], ys[i] = tt[k], yy[k]
		}
		tt, yy, n = ts, ys, 150
	}
	var s []float64
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if dt := tt[j] - tt[i]; dt > 0 {
				s = append(s, (yy[j]-yy[i])/dt)
			}
		}
	}
	if len(s) == 0 {
		return math.NaN()
	}
	return NanMedian(s)
}

// SpearmanXY — ранговая корреляция двух рядов по парам, где оба значения есть.
func SpearmanXY(x, y []float64) float64 {
	var a, b []float64
	for i := range x {
		if !math.IsNaN(x[i]) && !math.IsNaN(y[i]) {
			a, b = append(a, x[i]), append(b, y[i])
		}
	}
	if len(a) < 2 {
		return math.NaN()
	}
	return pearson(AverageRanks(a), AverageRanks(b))
}

func pearson(a, b []float64) float64 {
	n := float64(len(a))
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma, mb = ma/n, mb/n
	var sab, saa, sbb float64
	for i := range a {
		da, db := a[i]-ma, b[i]-mb
		sab += da * db
		saa += da * da
		sbb += db * db
	}
	if saa == 0 || sbb == 0 {
		return math.NaN()
	}
	return sab / math.Sqrt(saa*sbb)
}

// fmtv — число для текста (_fmt): чем больше число, тем меньше знаков.
func fmtv(v float64, unit string) string {
	if !finite(v) {
		return "—"
	}
	a := math.Abs(v)
	var s string
	switch {
	case a >= 100:
		s = fmt.Sprintf("%.0f", v)
	case a >= 10:
		s = fmt.Sprintf("%.1f", v)
	case a >= 0.1:
		s = fmt.Sprintf("%.2f", v)
	default:
		s = fmt.Sprintf("%.3f", v)
	}
	if unit != "" {
		s += " " + unit
	}
	return s
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func notna(x []float64) int {
	n := 0
	for _, v := range x {
		if !math.IsNaN(v) {
			n++
		}
	}
	return n
}

func nanMin(x []float64) float64 {
	m := math.NaN()
	for _, v := range x {
		if !math.IsNaN(v) && (math.IsNaN(m) || v < m) {
			m = v
		}
	}
	return m
}

func nanMax(x []float64) float64 {
	m := math.NaN()
	for _, v := range x {
		if !math.IsNaN(v) && (math.IsNaN(m) || v > m) {
			m = v
		}
	}
	return m
}

// hoursFrom — часы от times[from] до каждого момента.
func hoursFrom(times []time.Time, from int) []float64 {
	h := make([]float64, len(times))
	if len(times) == 0 {
		return h
	}
	for i, t := range times {
		h[i] = t.Sub(times[from]).Seconds() / 3600
	}
	return h
}

func rangeIdx(a, b int) []int {
	idx := make([]int, 0, max(0, b-a))
	for i := a; i < b; i++ {
		idx = append(idx, i)
	}
	return idx
}

func uniq(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
