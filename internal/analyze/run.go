package analyze

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Options — настройки полного анализа (флаги cmd/analyze и сервера).
type Options struct {
	Op           string
	BaselineFrac float64   // доля первых рабочих циклов как норма (0,2)
	BaseFrom     time.Time // норма по датам (нулевое — не задано)
	BaseTo       time.Time
	Norm         *NormSet // хранимая норма; если задана — BaseFrom/BaseTo/BaselineFrac не используются
	KeepDamaged  bool     // true — как Python: повреждённые циклы не выбрасывать
	Z            float64  // порог аномалии (6)
	GapMin       float64  // пауза-останов, мин (30)
	Source       string   // откуда таблица — пишется в сохраняемую норму
	Note         string
}

// DefaultOptions — значения по умолчанию, как в analyze.py.
func DefaultOptions() Options {
	return Options{BaselineFrac: 0.2, Z: 6, GapMin: StopGapMin}
}

// Result — всё, что даёт анализ одной операции. Считается один раз; cmd/analyze пишет
// из него файлы, сервер отдаёт JSON. Так расчёт отделён от того, как его показывают.
type Result struct {
	Opts     Options
	All      *Dataset // все циклы операции после очистки
	Prod     *Dataset // рабочие циклы
	Base     *Dataset // базовый период (норма); при хранимой норме — те её циклы, что есть в таблице
	Cols     Columns
	Norm     *NormSet
	Stored   bool   // норма из файла, а не посчитана
	Norms    []Norm // eng, затем raw
	SigEng   []float64
	Damaged  int       // выброшено по shp_damaged
	DamagedF []string  // их файлы — для архива: повреждённый файл стоит сохранить, пока буфер его не удалил
	Garbage  []Garbage // выброшено «мусорных» (индексы — в рабочих циклах ДО очистки)
	GarbageT []time.Time
	GarbageF []string
	Dropped  int // признаков нет в хранимой норме

	Anom     *Anomalies
	Order    []int      // аномальные циклы, сильнейшие первыми
	Reasons  [][]Reason // объяснение для Order[k]
	Bins     *Bins
	Trends   []Trend
	Top      []Trend
	Stops    *StopsResult
	Sensors  []SensorIssue
	Lost     []SignalLost
	Diag     []Diag
	Notes    []string
	StopRows []string // остановы с привязкой к замене штампа
	SpanH    float64
	OpName   string
	Period   string
	Text     string // diagnosis.txt
}

// Run — полный анализ одной операции: норма, аномалии, тренды, остановы, датчики, диагноз.
// d — таблица признаков (все операции или одна).
func Run(d *Dataset, o Options) (*Result, error) {
	r := &Result{Opts: o}
	if o.Op != "" {
		d = d.OnlyOp(o.Op)
	}
	if dm := d.Col("shp_damaged"); dm != nil {
		for i, v := range dm {
			if v > 0 {
				r.DamagedF = append(r.DamagedF, d.File[i])
			}
		}
	}
	if !o.KeepDamaged {
		d, r.Damaged = DropDamaged(d)
		p := Production(d)
		if g := FindGarbage(p, SelectColumns(d).Eng); len(g) > 0 {
			bad := map[string]bool{}
			for _, x := range g {
				bad[p.File[x.Index]] = true
				r.GarbageT = append(r.GarbageT, p.Times[x.Index])
				r.GarbageF = append(r.GarbageF, p.File[x.Index])
			}
			r.Garbage = g
			d = d.Where(func(i int) bool { return !bad[d.File[i]] })
		}
	}
	if d.Len() == 0 {
		return nil, fmt.Errorf("в таблице нет циклов %s", o.Op)
	}
	r.All, r.Prod, r.Cols = d, Production(d), SelectColumns(d)

	// ---------- норма ----------
	if o.Norm != nil {
		if o.Norm.Op != "" && o.Op != "" && !strings.EqualFold(o.Norm.Op, o.Op) {
			return nil, fmt.Errorf("норма снята для %s, а анализируется %s", o.Norm.Op, o.Op)
		}
		r.Norm, r.Stored = o.Norm, true
		r.Cols, r.Dropped = o.Norm.Restrict(r.Cols)
		r.Sensors = SensorHealthBy(d, r.Prod, o.Norm.Median, o.Norm.Median)
		// для «было / стало»: циклы базового периода нормы, если они ещё есть в таблице
		// (у старой нормы без дат базы — пусто, а не «вся таблица»)
		r.Base = r.Prod.Rows(nil)
		if !o.Norm.BaseFrom.IsZero() && !o.Norm.BaseTo.IsZero() {
			r.Base = BaselineRange(r.Prod, o.Norm.BaseFrom.Time, o.Norm.BaseTo.Time)
		}
	} else {
		var base *Dataset
		if !o.BaseFrom.IsZero() || !o.BaseTo.IsZero() {
			base = BaselineRange(r.Prod, o.BaseFrom, o.BaseTo)
		} else {
			base = Baseline(r.Prod, o.BaselineFrac)
		}
		if base.Len() == 0 {
			return nil, fmt.Errorf("в базовом периоде нет рабочих циклов")
		}
		r.Base = base
		r.Norm = BuildNormSet(d, r.Prod, base, r.Cols)
		r.Norm.Op, r.Norm.Source, r.Norm.Note = strings.ToUpper(o.Op), o.Source, o.Note
		now := time.Now()
		r.Norm.Created = LocalTime{time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), 0, time.UTC)}
		r.Sensors = SensorHealth(d, r.Prod, base)
	}
	r.Norms = r.Norm.Norms(r.Cols)

	// ---------- аномалии ----------
	r.SigEng = make([]float64, len(r.Cols.Eng)) // Norms кладёт инженерные первыми, в том же порядке
	for j := range r.Cols.Eng {
		r.SigEng[j] = r.Norms[j].Sigma
	}
	r.Anom = DetectAnomalies(r.Prod, r.Cols.Eng, r.SigEng, o.Z)
	r.Order = r.Anom.Order()
	r.Reasons = make([][]Reason, len(r.Order))
	for k, i := range r.Order {
		r.Reasons[k] = r.Anom.Explain(r.Prod, r.SigEng, i, AnomTopK, o.Z)
	}

	// ---------- тренды ----------
	feats := slices.Concat(r.Cols.Eng, r.Cols.Raw)
	med, sig := make([]float64, len(r.Norms)), make([]float64, len(r.Norms))
	for j, n := range r.Norms {
		med[j], sig[j] = n.Median, n.Sigma
	}
	r.Bins = Resample(r.Prod, feats)
	r.Trends = Trends(r.Bins, feats, med, sig)
	r.Top = SignificantTrends(r.Trends, TrendTop)

	// ---------- остановы, сигнал пропал ----------
	r.Stops = FindStops(d, r.Prod, r.Cols.Eng, med[:len(r.Cols.Eng)], r.SigEng, o.GapMin)
	r.Lost = FindSignalLost(r.Prod)

	// ---------- диагноз по узлам ----------
	r.SpanH = r.Prod.Times[r.Prod.Len()-1].Sub(r.Prod.Times[0]).Hours()
	r.Diag, r.Notes = Evaluate(r.Prod, r.Norm.Median, r.Norm.SigmaMap(), r.SpanH, nil)
	r.OpName = o.Op
	if r.OpName == "" {
		r.OpName = strings.Join(uniqueOps(d), ",")
	}
	r.Period = d.Times[0].Format("02.01.2006 15:04") + " – " + d.Times[d.Len()-1].Format("02.01.2006 15:04")
	r.StopRows = StopsSummary(d, r.Stops.GapThrMin)
	r.Text = DiagnosisText(r.Diag, r.OpName, r.Period, r.Prod.Len(), r.StopRows, r.SpanH < 24*7, r.Notes)
	return r, nil
}

// Daily — отчёт за сутки с теми же настройками и той же нормой (если она хранимая).
func (r *Result) Daily(day time.Time) (*DayReport, error) {
	var stored *NormSet
	if r.Stored {
		stored = r.Norm
	}
	return Daily(r.All, day, r.Opts.Op, r.Opts.GapMin, r.Opts.Z, stored)
}

// uniqueOps — операции в данных по алфавиту (sorted(df["op"].unique()) в Python).
func uniqueOps(d *Dataset) []string {
	var ops []string
	for _, o := range d.Op {
		if o != "" && !slices.Contains(ops, o) {
			ops = append(ops, o)
		}
	}
	slices.Sort(ops)
	return ops
}
