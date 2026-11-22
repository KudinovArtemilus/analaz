package analyze

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// Пороги детектора «сигнал пропал». В Python этого нет — новая проверка press-go.
const (
	LostZeroShare = 0.9 // канал считается пропавшим, если ровно 0 в ≥ 90% последних циклов…
	LostPeerShare = 0.1 // …а у соседей ровный ноль бывает не чаще чем в 10% циклов
	LostMinPeers  = 2
)

// SignalLost — канал, который показывает ровно 0, хотя однотипные соседи работают.
//
// Почему ровно 0, а не «мало»: настоящий датчик всегда шумит, и точный ноль
// почти всегда означает подставленное значение (обрыв, нет связи, канал отключён).
// Почему с соседями: сравнение с нормой не видит отказ, случившийся ДО базового
// периода (так с MD5 — он мёртв во всём буфере). Соседи (MD1…MD6, насосы 1.1…6.2)
// работают в тех же условиях, поэтому ноль у одного из них — подозрителен.
type SignalLost struct {
	Feature   string
	ZeroShare float64   // доля циклов с ровным нулём в последний период
	PeerShare float64   // то же у соседей (медиана)
	Peers     []string  // с кем сравнивали
	Since     time.Time // с какого рабочего цикла канал стоит в нуле без перерыва
	FromStart bool      // ноль с самого начала данных — отказ случился раньше
}

var (
	reDigits  = regexp.MustCompile(`\d+`)
	reNotLost = regexp.MustCompile(`^d\d{3}_|__nan$|^shp_`)
)

// peerKey — имя без цифр: casedrain_md5_mean → casedrain_md#_mean, у соседей ключ общий.
func peerKey(col string) string { return reDigits.ReplaceAllString(col, "#") }

// FindSignalLost ищет каналы с подставленным нулём среди групп однотипных каналов.
// Смотрит последние 20% рабочих циклов (не меньше 10) — то, что происходит сейчас.
func FindSignalLost(prod *Dataset) []SignalLost {
	tailIdx := lastN(prod.Len(), max(10, int(float64(prod.Len())*0.2)))
	if len(tailIdx) == 0 {
		return nil
	}
	share := map[string]float64{}
	groups := map[string][]string{}
	for j, c := range prod.Cols {
		if IsMeta(c) || reNotLost.MatchString(c) || c == "dt_ms" {
			continue
		}
		x := prod.Data[j]
		zeros, valid := 0, 0
		for _, i := range tailIdx {
			v := x[i]
			if v != v { // NaN — не ноль и не значение
				continue
			}
			valid++
			if v == 0 {
				zeros++
			}
		}
		if valid == 0 {
			continue
		}
		share[c] = float64(zeros) / float64(valid)
		k := peerKey(c)
		groups[k] = append(groups[k], c)
	}

	var out []SignalLost
	for _, cols := range groups {
		if len(cols) < LostMinPeers+1 {
			continue
		}
		for _, c := range cols {
			if share[c] < LostZeroShare {
				continue
			}
			// у сырого канала ноль в одной статистике ещё не отказ: уставка −10…0 В даёт max = 0
			// законно. Пропавший сигнал не «дышит» — его std тоже ровно 0.
			if std, ok := share[stdOf(c)]; ok && std < LostZeroShare {
				continue
			}
			var peers []string
			var ps []float64
			for _, p := range cols {
				if p != c {
					peers = append(peers, p)
					ps = append(ps, share[p])
				}
			}
			pm := NanMedian(ps)
			if pm > LostPeerShare {
				continue // нули у всех — значит, режим работы (насос выключен), а не отказ
			}
			s := SignalLost{Feature: c, ZeroShare: share[c], PeerShare: pm, Peers: peers}
			s.Since, s.FromStart = zeroSince(prod, c)
			out = append(out, s)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Feature < out[b].Feature })
	return out
}

// LostBreak — сколько ненулевых циклов подряд нужно, чтобы считать, что канал снова работал.
// Одиночные «живые» значения — почти всегда повреждённые .shp (сдвиг записей: в канал
// давления попадает номер заготовки 175489), их серия нулей не прерывает.
const LostBreak = 3

// zeroSince — начало последней серии нулей. Её не прерывают NaN, циклы из повреждённых
// .shp (shp_damaged > 0) и одиночные ненулевые значения (меньше LostBreak подряд).
func zeroSince(prod *Dataset, col string) (time.Time, bool) {
	x, dmg := prod.Col(col), prod.Col("shp_damaged")
	start, run := len(x), 0
	for i := len(x) - 1; i >= 0; i-- {
		if x[i] != x[i] || dmg != nil && dmg[i] > 0 {
			continue
		}
		if x[i] != 0 {
			if run++; run >= LostBreak {
				break
			}
			continue
		}
		run = 0
		start = i
	}
	if start == len(x) {
		return time.Time{}, false
	}
	return prod.Times[start], start == firstValid(x)
}

func firstValid(x []float64) int {
	for i, v := range x {
		if v == v {
			return i
		}
	}
	return -1
}

var reRawStat = regexp.MustCompile(`__(max|min|mean|std)$`)

// stdOf — колонка std того же сырого канала: a066_X__max → a066_X__std ("" для инженерных).
func stdOf(col string) string {
	if !reRawStat.MatchString(col) {
		return ""
	}
	return reRawStat.ReplaceAllString(col, "__std")
}

// SignalKey — один ключ на физический канал: a066_…_Pressure__max/mean/min/std → a066_…_Pressure.
func SignalKey(col string) string {
	return strings.TrimSuffix(reSigKey.ReplaceAllString(col, ""), "_")
}
