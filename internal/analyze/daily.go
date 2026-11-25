package analyze

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/features"
)

// DailyKey — ключевые показатели для таблицы «день против нормы» (KEY в daily.py).
var DailyKey = []string{"force_max_kN", "cycle_s", "press_time_s", "force_rise_s",
	"hp_min", "hp_drop", "temp_bottom_mean", "temp_upper_mean", "level_bottom_mean", "level_upper_mean",
	"dieclamp_c1_min", "dieclamp_c2_min", "accu_rv1_min", "accu_rv3_min",
	"casedrain_md2_mean", "casedrain_md3_mean", "casedrain_md4_mean", "casedrain_md5_mean", "casedrain_md6_mean",
	"lr_dev_absmax_mm", "v12Y102_err_absmax", "fc_speed_err_rms"}

// DayAnomaly — аномальный цикл суток: три признака, сильнее всего отличающихся от соседних циклов.
type DayAnomaly struct {
	Time    time.Time
	File    string
	Z       float64 // наибольший |z| (обрезан до 100)
	What    string
	Reasons []Reason // до трёх признаков, из которых собран What, — чтобы страница подставила свои названия
}

// DayReasonText — «признак: значение (обычно норма); …» для аномалии суток; name — как называть
// признак (Human или название из «Настроек»).
func DayReasonText(rs []Reason, name func(string) string) string {
	parts := make([]string, len(rs))
	for k, x := range rs {
		parts[k] = fmt.Sprintf("%s: %s (обычно %s)", name(x.Feature), fmtv(x.Value, ""), fmtv(x.Rm, ""))
	}
	return strings.Join(parts, "; ")
}

// DayRow — строка таблицы «день против нормы».
type DayRow struct {
	Mark, Name, Value, Median, Range, Z string
	Feature                             string // ключ признака — для названий из «Настроек»
}

// DayReport — отчёт за сутки (daily.py one_day).
type DayReport struct {
	Day        time.Time
	Cycles     int // все циклы суток
	Production int // рабочие (ковка)
	DownH      float64
	DieChanges int
	BaseNote   string
	Diag       []Diag
	Notes      []string
	Stops      []string
	Short      bool
	Anomalies  []DayAnomaly
	Rows       []DayRow
	Text       string      // diagnosis_ГГГГ-ММ-ДД_OP.txt
	Hourly     []DayPlot   // «Сутки по часам» (6.2з)
	PerHour    []HourCount // «Циклов в час» — все циклы суток, и нерабочие тоже
}

// DailyPlot — признаки графиков «Сутки по часам» (PLOT в daily.py).
var DailyPlot = []string{"force_max_kN", "cycle_s", "hp_min", "temp_bottom_mean", "dieclamp_c2_min",
	"casedrain_md3_mean", "lr_dev_absmax_mm", "v12Y102_err_absmax"}

// DayPlot — один график суток: точки — рабочие циклы, линия — медиана за час,
// полоса — обычный диапазон нормы (5–95%).
type DayPlot struct {
	Feature string
	T       []time.Time
	V       []float64
	HourT   []time.Time // начало часа; часы без циклов пропущены (dropna)
	HourMed []float64
	Lo, Hi  float64 // полоса нормы; NaN — нет
}

// HourCount — сколько циклов началось в этот час.
type HourCount struct {
	Hour time.Time
	N    int
}

// Days — дни, за которые есть циклы, по порядку.
func Days(d *Dataset) []time.Time {
	var days []time.Time
	for _, t := range d.Times {
		day := t.Truncate(24 * time.Hour)
		if len(days) == 0 || !days[len(days)-1].Equal(day) {
			days = append(days, day)
		}
	}
	return days
}

// Daily повторяет one_day из daily.py: как отработал пресс за сутки по сравнению с нормой
// (норма — вся история до этих суток, если её хотя бы 30 рабочих циклов).
// d — все циклы одной операции по времени. stored — хранимая норма (nil — считать, как в Python).
func Daily(d *Dataset, day time.Time, op string, gapMin, zThr float64, stored *NormSet, lim map[string]Limit) (*DayReport, error) {
	d0 := day.Truncate(24 * time.Hour)
	d1 := d0.Add(24 * time.Hour)
	upto := d.Where(func(i int) bool { return d.Times[i].Before(d1) })
	first := sort.Search(upto.Len(), func(i int) bool { return !upto.Times[i].Before(d0) })
	if first == upto.Len() {
		return nil, fmt.Errorf("за %s циклов нет", d0.Format("2006-01-02"))
	}
	r := &DayReport{Day: d0, Cycles: upto.Len() - first}

	f := upto.Col("force_max_kN")
	P := upto.Where(func(i int) bool { return f != nil && f[i] > ProdForceKN })
	tFrom := sort.Search(P.Len(), func(i int) bool { return !P.Times[i].Before(d0) })
	T := P.Rows(rangeIdx(tFrom, P.Len()))
	r.Production = T.Len()
	if P.Len() == 0 {
		return nil, fmt.Errorf("до конца %s нет ни одного рабочего цикла (усилие > %g кН)", d0.Format("2006-01-02"), ProdForceKN)
	}
	cols := SelectColumns(upto)
	var (
		sig     map[string]float64 // σ анализируемых признаков
		med     map[string]float64 // медиана нормы анализируемых признаков
		normMed func(string) float64
		quant   func(c string) (lo, mid, hi float64, ok bool) // норма 5 / 50 / 95%
	)
	if stored != nil {
		cols, _ = stored.Restrict(cols)
		sig, normMed = stored.SigmaMap(), stored.Median
		med = make(map[string]float64, len(sig))
		for c := range sig {
			med[c] = stored.Median(c)
		}
		quant = func(c string) (float64, float64, float64, bool) {
			f, ok := stored.Features[c]
			if !ok || math.IsNaN(float64(f.Q[2])) {
				return 0, 0, 0, false
			}
			return float64(f.Q[1]), float64(f.Q[2]), float64(f.Q[3]), true
		}
		r.BaseNote = fmt.Sprintf("хранимая норма %s — %s (%d циклов)",
			stored.BaseFrom.Format("02.01.2006 15:04"), stored.BaseTo.Format("02.01.2006 15:04"), stored.BaseCycles)
	} else {
		var B *Dataset
		switch {
		case tFrom >= 30: // история до этих суток
			B = P.Rows(rangeIdx(0, tFrom))
			r.BaseNote = fmt.Sprintf("вся история до этого дня (%d циклов)", tFrom)
		default:
			n := max(10, int(float64(P.Len())*0.2))
			B = P.Rows(rangeIdx(0, min(n, P.Len())))
			r.BaseNote = fmt.Sprintf("мало истории до этого дня — норма по первым 20%% данных (%d циклов)", n)
		}
		feats := slices.Concat(cols.Eng, cols.Raw)
		sig = make(map[string]float64, len(feats))
		med = make(map[string]float64, len(feats))
		for _, c := range feats {
			med[c], sig[c] = RobustStats(B.Col(c), P.Col(c))
		}
		normMed = func(c string) float64 { return NanMedian(B.Col(c)) }
		quant = func(c string) (float64, float64, float64, bool) {
			bc := B.Col(c)
			if bc == nil {
				return 0, 0, 0, false
			}
			return features.NanPercentile(bc, 5), features.NanPercentile(bc, 50), features.NanPercentile(bc, 95), true
		}
	}
	spanH := 0.0
	if P.Len() > 0 {
		spanH = P.Times[P.Len()-1].Sub(P.Times[0]).Hours()
	}
	var win *Dataset
	if T.Len() > 0 {
		win = T
	}
	sig = floorSigmaMap(sig, StepFloors(P, slices.Concat(cols.Eng, cols.Raw))) // σ не меньше шага записи
	r.Diag, r.Notes = Evaluate(P, normMed, sig, spanH, win, lim)

	// простои внутри суток, включая паузу от последнего цикла прошлых суток
	gaps := make([]float64, 0, upto.Len())
	for i := 1; i < upto.Len(); i++ {
		gaps = append(gaps, upto.Times[i].Sub(upto.Times[i-1]).Seconds()/60)
	}
	gapThr := gapMin // Python max(30, nan) = 30: один цикл за всю историю — порог по умолчанию
	if m := 4 * NanMedian(gaps); m > gapThr {
		gapThr = m
	}
	i0 := max(0, first-1)
	tail := upto.Rows(rangeIdx(i0, upto.Len()))
	r.Stops = StopsSummary(tail, gapThr)
	for i := 1; i < tail.Len(); i++ {
		if g := tail.Times[i].Sub(tail.Times[i-1]).Seconds() / 60; g > gapThr {
			r.DownH += g
		}
	}
	r.DownH /= 60
	if ds := upto.Col("die_strokes"); ds != nil {
		for i := max(1, first); i < len(ds); i++ {
			if isDieChange(ds[i-1], ds[i]) {
				r.DieChanges++
			}
		}
	}

	// аномалии суток: выбросы относительно соседних циклов (окно видит только данные до конца суток)
	sigEng := make([]float64, len(cols.Eng))
	for j, c := range cols.Eng {
		sigEng[j] = sig[c]
	}
	an := DetectAnomalies(P, cols.Eng, sigEng, zThr)
	for i := tFrom; i < P.Len(); i++ {
		var zs []Reason
		for j, c := range cols.Eng {
			v, rm := P.Col(c)[i], an.Rm[j][i]
			if z := math.Min(math.Abs((v-rm)/sigEng[j]), AnomClip); !math.IsNaN(z) {
				zs = append(zs, Reason{c, v, rm, z})
			}
		}
		if len(zs) == 0 {
			continue
		}
		sort.SliceStable(zs, func(a, b int) bool { return zs[a].Z > zs[b].Z })
		if !(zs[0].Z > zThr) {
			continue
		}
		top := zs[:min(3, len(zs))]
		r.Anomalies = append(r.Anomalies, DayAnomaly{P.Times[i], baseName(P.File[i]), zs[0].Z, DayReasonText(top, Human), top})
	}
	sort.SliceStable(r.Anomalies, func(a, b int) bool { return r.Anomalies[a].Z > r.Anomalies[b].Z })

	// таблица «день против нормы»
	for _, c := range DailyKey {
		tc := T.Col(c)
		lo, mid, hi, ok := quant(c)
		if tc == nil || notna(tc) == 0 || !ok {
			continue
		}
		v := NanMedian(tc)
		z := 0.0
		if s, ok := sig[c]; ok && s != 0 {
			m, ok := med[c]
			if !ok {
				m = mid
			}
			z = (v - m) / s
		}
		mark := "🟢"
		if !(lo <= v && v <= hi) {
			mark = "🔴"
			if math.Abs(z) < 4 {
				mark = "🟡"
			}
		}
		zs := "—"
		if finite(z) {
			zs = fmt.Sprintf("%+.1f", z)
		}
		r.Rows = append(r.Rows, DayRow{mark, Human(c), fmtv(v, ""), fmtv(mid, ""), fmtv(lo, "") + " … " + fmtv(hi, ""), zs, c})
	}

	r.Short = spanH < 24*7
	label := d0.Format("02.01.2006")
	r.Text = fmt.Sprintf("СУТКИ %s, %s: циклов %d (рабочих %d), простоев %.1f ч, замен штампа %d, аномальных циклов %d. Норма: %s.\n",
		label, op, r.Cycles, r.Production, r.DownH, r.DieChanges, len(r.Anomalies), r.BaseNote) +
		DiagnosisText(r.Diag, op, "сутки "+label, T.Len(), r.Stops, r.Short, r.Notes)
	r.Hourly = dayPlots(T, quant)
	r.PerHour = perHour(upto.Rows(rangeIdx(first, upto.Len())))
	return r, nil
}

// dayPlots — «Сутки по часам»: признаки DailyPlot, у которых за сутки есть хоть одно число.
func dayPlots(T *Dataset, quant func(string) (float64, float64, float64, bool)) []DayPlot {
	var out []DayPlot
	if T.Len() == 0 {
		return nil
	}
	for _, c := range DailyPlot {
		v := T.Col(c)
		if v == nil || notna(v) == 0 {
			continue
		}
		p := DayPlot{Feature: c, T: T.Times, V: v, Lo: math.NaN(), Hi: math.NaN()}
		if lo, _, hi, ok := quant(c); ok {
			p.Lo, p.Hi = lo, hi
		}
		// resample("h").median().dropna(): часы подряд от первого до последнего цикла
		byHour := map[time.Time][]float64{}
		for i, t := range T.Times {
			byHour[t.Truncate(time.Hour)] = append(byHour[t.Truncate(time.Hour)], v[i])
		}
		for h := T.Times[0].Truncate(time.Hour); !h.After(T.Times[T.Len()-1]); h = h.Add(time.Hour) {
			if m := NanMedian(byHour[h]); !math.IsNaN(m) {
				p.HourT = append(p.HourT, h)
				p.HourMed = append(p.HourMed, m)
			}
		}
		out = append(out, p)
	}
	return out
}

// perHour — resample("h").count() по cycle_s: часы подряд от первого до последнего цикла
// суток, с нулями между ними (час простоя — столбик 0, а не пропуск).
func perHour(day *Dataset) []HourCount {
	cs := day.Col("cycle_s")
	if cs == nil || day.Len() == 0 {
		return nil
	}
	cnt := map[time.Time]int{}
	for i, t := range day.Times {
		if !math.IsNaN(cs[i]) {
			cnt[t.Truncate(time.Hour)]++
		}
	}
	var out []HourCount
	for h := day.Times[0].Truncate(time.Hour); !h.After(day.Times[day.Len()-1]); h = h.Add(time.Hour) {
		out = append(out, HourCount{h, cnt[h]})
	}
	return out
}

// DayFileBase — «ГГГГ-ММ-ДД_OP1», как в daily.py.
func DayFileBase(day time.Time, op string) string {
	if op == "" {
		op = "all"
	}
	return day.Format("2006-01-02") + "_" + op
}

func baseName(p string) string { return filepath.Base(strings.ReplaceAll(p, `\`, "/")) }
