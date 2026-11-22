package analyze

import (
	"fmt"
	"math"
)

// Новое правило (в Python-отчёте его нет): «узел не похож на однотипных соседей».
//
// Обычный анализ сравнивает узел с его же нормой — с тем, каким он был в базовом периоде.
// Если датчик или узел «не такой» с первого дня (дренаж MD1 ≈ 0,03 бар при 0,5 у пяти
// соседей), норма это впитывает, и отклонения не видно. Однотипные узлы — шесть одинаковых
// насосов, двенадцать одинаковых регуляторов — должны вести себя одинаково, поэтому их
// можно сравнить друг с другом.
//
// Правило:
//   - группы — дренажи MD1–MD6 и 4 группы по 12 насосов;
//   - пропавшие сигналы (SignalLost) из сравнения исключаются: мёртвый ноль раздувает разброс,
//     и настоящий «чужак» перестаёт выделяться; о них и так говорит «Сигнал пропал»;
//   - «сейчас» — медиана признака за последние max(10, 20%) рабочих циклов;
//   - центр группы — медиана этих медиан, разброс — MAD·1,4826 (не боится самих «чужаков»);
//   - «чужак»: |z| ≥ 5 и отличие от центра ≥ 30% центра (одно z без процентов ловило бы
//     мелочь в очень ровной группе), в группе ≥ 4 узлов;
//   - то же самое считается по базовому периоду: если узел отличался в ту же сторону и тогда —
//     «так всегда» (проверить один раз: датчик или так задумано), иначе — «стал отличаться».
//
// Эталон на pandas — ref/dump_peers.py; совпадение с Go проверено бит в бит.

// PeerGroups — группы однотипных узлов для правила.
var PeerGroups = [][]string{
	feats("casedrain_md%d_mean", 1, 6),
	pumps("pump%d%d_pivot_err_active"),
	pumps("pump%d%d_pivot_lag"),
	pumps("pump%d%d_valve_err_rms"),
	pumps("pump%d%d_press_max"),
}

const (
	PeerZ     = 5.0 // |z| от центра группы
	PeerRel   = 0.3 // и отличие ≥ 30% центра
	PeerMinN  = 4   // узлов в группе после исключений
	madToSigm = 1.4826
)

// PeerOutlier — узел, который не похож на однотипных соседей.
type PeerOutlier struct {
	Feature            string
	Value, Center, Z   float64 // сейчас: медиана узла, центр группы, z
	BaseVal, BaseCentr float64 // то же в базовом периоде (NaN — данных не было)
	Always             bool    // отличался в ту же сторону и в базовом периоде
	Worse              bool    // отличался и раньше, но отрыв от соседей вырос больше чем в 1,5 раза
	N                  int     // узлов в группе
}

// Kind — для страницы: new — стал отличаться, worse — давно, но хуже, always — давно, nobase — нормы нет.
func (o PeerOutlier) Kind() string {
	switch {
	case math.IsNaN(o.BaseVal):
		return "nobase"
	case o.Worse:
		return "worse"
	case o.Always:
		return "always"
	}
	return "new"
}

// PeerOutliers — «чужаки» во всех группах, в порядке групп и узлов.
func (r *Result) PeerOutliers() []PeerOutlier {
	p := r.Prod
	if p == nil || p.Len() == 0 {
		return nil
	}
	n := p.Len()
	k := min(n, max(10, int(float64(n)*0.2)))
	tail := p.Rows(rangeIdx(n-k, n))
	base := r.Base
	if base == nil || base.Len() == 0 { // хранимая норма без своих циклов — как в Python, первые 20%
		base = p.Rows(rangeIdx(0, k))
	}
	lost := map[string]bool{}
	for _, l := range r.Lost {
		lost[SignalKey(l.Feature)] = true
	}

	var out []PeerOutlier
	for _, g := range PeerGroups {
		var fs []string
		for _, f := range g {
			if c := p.Col(f); c != nil && !lost[SignalKey(f)] && anyFinite(c) {
				fs = append(fs, f)
			}
		}
		if len(fs) < PeerMinN {
			continue
		}
		m, med, mad := peerStats(tail, fs)
		if mad == 0 || med == 0 || math.IsNaN(mad) {
			continue
		}
		mb, medb, madb := peerStats(base, fs)
		for i, f := range fs {
			z := (m[i] - med) / mad
			if !(math.Abs(z) >= PeerZ && math.Abs(m[i]-med)/math.Abs(med) >= PeerRel) {
				continue
			}
			zb, relb := math.NaN(), math.NaN()
			if madb != 0 {
				zb = (mb[i] - medb) / madb
			}
			if medb != 0 {
				relb = math.Abs(mb[i]-medb) / math.Abs(medb)
			}
			always := math.Abs(zb) >= PeerZ && relb >= PeerRel && math.Signbit(zb) == math.Signbit(z)
			rel := math.Abs(m[i]-med) / math.Abs(med)
			out = append(out, PeerOutlier{f, m[i], med, z, mb[i], medb, always, always && rel > 1.5*relb, len(fs)})
		}
	}
	return out
}

// peerStats — медианы признаков, их медиана и MAD·1,4826 (pandas: m.median(), (m−med).abs().median()).
// NaN-медиану (у узла нет данных в периоде) pandas пропускает в обеих медианах — так же и здесь.
func peerStats(d *Dataset, fs []string) (m []float64, med, mad float64) {
	m = make([]float64, len(fs))
	for i, f := range fs {
		m[i] = NanMedian(d.Col(f))
	}
	med = NanMedian(m)
	dev := make([]float64, len(m))
	for i, v := range m {
		dev[i] = math.Abs(v - med)
	}
	return m, med, NanMedian(dev) * madToSigm
}

// Text — одна строка для отчёта.
func (o PeerOutlier) Text(human func(string) string) string {
	dir := "выше"
	if o.Z < 0 {
		dir = "ниже"
	}
	s := fmt.Sprintf("%s: %s, у соседей %s — %s на %.0f%% (z %+.1f)", human(o.Feature), fmtv(o.Value, ""), fmtv(o.Center, ""),
		dir, 100*math.Abs(o.Value-o.Center)/math.Abs(o.Center), o.Z)
	switch {
	case math.IsNaN(o.BaseVal):
		s += "; в базовом периоде данных нет"
	case o.Worse:
		s += fmt.Sprintf("; отличался и в норме (%s при %s), но отрыв вырос: было %.0f%%, стало %.0f%%", fmtv(o.BaseVal, ""), fmtv(o.BaseCentr, ""),
			100*math.Abs(o.BaseVal-o.BaseCentr)/math.Abs(o.BaseCentr), 100*math.Abs(o.Value-o.Center)/math.Abs(o.Center))
	case o.Always:
		s += fmt.Sprintf("; так и в норме (%s при %s) — проверить один раз: датчик или так задумано", fmtv(o.BaseVal, ""), fmtv(o.BaseCentr, ""))
	default:
		s += fmt.Sprintf("; в норме был как все (%s при %s) — стал отличаться", fmtv(o.BaseVal, ""), fmtv(o.BaseCentr, ""))
	}
	return s
}
