package analyze

import (
	"fmt"
	"math"
	"slices"
)

// Проверка периода перед тем, как взять его за норму.
//
// Норма — «как машина работает, когда исправна». Если в выбранные сутки давление уже было
// занижено, после сохранения система примет занижение за норму и замолчит. Поэтому период
// сравнивается с тем, что от него не зависит:
//   - с прежней нормой;
//   - с началом данных (первые циклы этой операции и колеса/рецепта);
//   - с паспортными пределами из «Настроек»;
//   - и сам с собой: не плывёт ли показатель внутри периода (тренд).
// Смотрим только показатели узлов (их сотня с физическим смыслом), а не все 1400 признаков:
// человеку нужен короткий список «что не так», а не таблица.

// NormCheck — одна находка проверки.
type NormCheck struct {
	Kind  string // "prev" — хуже прежней нормы, "start" — хуже начала данных, "trend" — плывёт, "passport" — за пределом
	Label string // узел: показатель
	Text  string
}

// NormCheckZ — с какого отклонения (в σ эталона) разница считается настоящей, как «наблюдать» в диагнозе.
const NormCheckZ = 4.0

// NormCheckTrendMinH — короче стольких часов тренд внутри периода не оцениваем: мало точек.
const NormCheckTrendMinH = 6.0

// CheckBase — проверка базового периода r.Base (результат Run с BaseFrom/BaseTo).
// prev — прежняя норма (nil — нет), lim — паспортные пределы.
func (r *Result) CheckBase(prev *NormSet, lim map[string]Limit) []NormCheck {
	B, P := r.Base, r.Prod
	if B == nil || B.Len() == 0 || P.Len() == 0 {
		return nil
	}
	feats := []string{}
	type indRef struct {
		in   Indicator
		unit string
	}
	inds := map[string]indRef{}
	mixed := mixedRegimes(P)
	for _, u := range Units() {
		for _, in := range u.Ind {
			if _, dup := inds[in.Feat]; dup || (in.PerRegime && mixed) || notna(B.Col(in.Feat)) < 5 {
				continue
			}
			inds[in.Feat] = indRef{in, u.Name}
			feats = append(feats, in.Feat)
		}
	}
	floors := StepFloors(P, feats)
	floor := func(f string, s float64) float64 {
		if fl, ok := floors[f]; ok && !(s >= fl) {
			return fl
		}
		return s
	}

	// начало данных: первые циклы, если период нормы сам не начинается там
	nStart := min(AutoNormCycles, max(30, P.Len()/5))
	var S *Dataset
	if P.Len() >= 2*nStart && B.Times[0].After(P.Times[nStart-1]) {
		S = P.Rows(rangeIdx(0, nStart))
	}

	var out []NormCheck
	for _, f := range feats {
		ref := inds[f]
		in := ref.in
		label := ref.unit + ": " + in.Label
		b := B.Col(f)
		cur := NanMedian(b)
		if !finite(cur) {
			continue
		}
		sign := map[string]float64{"up": 1, "down": -1, "both": 0}[in.Bad]
		// хуже эталона: в «плохую» сторону, относительное отклонение как у «наблюдать» и > 4σ эталона
		worse := func(base, sig float64) (float64, bool) {
			if !finite(base) || !finite(sig) || sig <= 0 {
				return 0, false
			}
			rel := (cur - base) / (math.Abs(base) + 1e-9)
			z := (cur - base) / sig
			if sign == 0 {
				rel, z = math.Abs(rel), math.Abs(z)
			} else {
				rel, z = rel*sign, z*sign
			}
			return (cur - base) / (math.Abs(base) + 1e-9), rel > in.WarnRel && z > NormCheckZ
		}
		if prev != nil {
			if fn, ok := prev.Features[f]; ok {
				if rel, bad := worse(float64(fn.Median), floor(f, float64(fn.Sigma))); bad {
					out = append(out, NormCheck{"prev", label, fmt.Sprintf("в периоде %s, в прежней норме (%s) %s%s",
						fmtv(cur, in.Unit), prev.BaseFrom.Format("02.01"), fmtv(float64(fn.Median), in.Unit), relText(rel, float64(fn.Median)))})
				}
			}
		}
		if S != nil {
			s := S.Col(f)
			if notna(s) >= 5 {
				med, sig := RobustStats(s, s) // разброс — самого начала, без «пола» по всем данным (в них и плохой период)
				if rel, bad := worse(med, floor(f, sig)); bad {
					out = append(out, NormCheck{"start", label, fmt.Sprintf("в периоде %s, в начале данных (%s) %s%s",
						fmtv(cur, in.Unit), S.Times[0].Format("02.01"), fmtv(med, in.Unit), relText(rel, med))})
				}
			}
		}
		if l, ok := lim[f]; ok {
			if txt, broken := limitCheck(b, l, in.Unit); broken {
				out = append(out, NormCheck{"passport", label, "в периоде " + txt})
			}
		}
		// тренд внутри периода: последняя четверть периода заметно отличается от первой (больше
		// половины порога «наблюдать» и больше 2σ), и это ровный дрейф, а не разброс (|ρ| > 0,4)
		spanH := B.Times[B.Len()-1].Sub(B.Times[0]).Hours()
		if q := B.Len() / 4; spanH >= NormCheckTrendMinH && q >= 5 {
			th := hoursFrom(B.Times, 0)
			rho := SpearmanXY(th, b)
			a, z := NanMedian(b[:q]), NanMedian(b[len(b)-q:])
			_, sig := RobustStats(b, b)
			drift := z - a
			rel := drift / (math.Abs(a) + 1e-9)
			if finite(drift) && math.Abs(rho) > 0.4 && math.Abs(rel) > in.WarnRel/2 && math.Abs(drift) > 2*floor(f, sig) {
				out = append(out, NormCheck{"trend", label, fmt.Sprintf("внутри периода плывёт: в начале %s, в конце %s за %.0f ч%s",
					fmtv(a, in.Unit), fmtv(z, in.Unit), spanH, relText(rel, a))})
			}
		}
	}
	order := map[string]int{"passport": 0, "prev": 1, "start": 2, "trend": 3}
	slices.SortStableFunc(out, func(a, b NormCheck) int { return order[a.Kind] - order[b.Kind] })
	return out
}

func relText(rel, base float64) string {
	if math.Abs(base) < 1e-9 {
		return ""
	}
	return fmt.Sprintf(" (%+.0f%%)", rel*100)
}
