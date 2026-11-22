package shp

import (
	"encoding/binary"
	"math"
)

// Synthetic собирает маленький синтетический .shp в памяти — для тестов: так они не зависят
// от настоящих файлов с завода. Счётчик — шаг 5 мс; дискретный канал ch включён, если (отсчёт+ch)
// чётное; аналоговый = отсчёт·10 + ch; у канала 1 в отсчёте 2 — мусор 1e13 (должен стать NaN).
func Synthetic(digNames, anaNames []string, samples int, ft uint64) []byte {
	nDig, nAna := len(digNames), len(anaNames)
	le := binary.LittleEndian
	start := nameStart + (nDig+nAna)*nameStep
	recLen := 4 + nDig/8 + 4*nAna
	d := make([]byte, start+samples*recLen)

	le.PutUint64(d[offFileTime:], ft)
	le.PutUint32(d[offNumDig:], uint32(nDig))
	le.PutUint32(d[offNumAna:], uint32(nAna))
	for i, nm := range append(append([]string{}, digNames...), anaNames...) {
		copy(d[nameStart+i*nameStep:], nm)
	}
	for s := 0; s < samples; s++ {
		r := d[start+s*recLen:]
		le.PutUint32(r, uint32(1000+5*s)) // счётчик в мс, шаг 5 мс
		// дискретный канал ch включён, если (s+ch) чётное
		for ch := 0; ch < nDig; ch++ {
			if (s+ch)%2 == 0 {
				r[4+ch/8] |= 1 << (ch % 8)
			}
		}
		for ch := 0; ch < nAna; ch++ {
			v := float32(s*10 + ch)
			if ch == 1 && s == 2 {
				v = 1e13 // мусор — должен стать NaN в Analog()
			}
			le.PutUint32(r[4+nDig/8+4*ch:], math.Float32bits(v))
		}
	}
	return d
}

// SyntheticSignals — синтетический .shp с заданным шагом и сигналами: значение аналогового канала
// ch в момент t (секунды от начала) даёт fn(ch, t). Для тестов, где важен шаг записи (КПС писал
// и 20, и 5 мс) — один и тот же «физический» цикл записывается с разным шагом.
func SyntheticSignals(digNames, anaNames []string, fn func(ch int, t float64) float64, dur float64, stepMs int) []byte {
	nDig, nAna := len(digNames), len(anaNames)
	samples := int(math.Round(dur * 1000 / float64(stepMs))) // отсчёты 0, шаг, 2·шаг… < dur: длительность одна при любом шаге
	le := binary.LittleEndian
	start := nameStart + (nDig+nAna)*nameStep
	recLen := 4 + nDig/8 + 4*nAna
	d := make([]byte, start+samples*recLen)
	le.PutUint32(d[offNumDig:], uint32(nDig))
	le.PutUint32(d[offNumAna:], uint32(nAna))
	for i, nm := range append(append([]string{}, digNames...), anaNames...) {
		copy(d[nameStart+i*nameStep:], nm)
	}
	for s := 0; s < samples; s++ {
		r := d[start+s*recLen:]
		le.PutUint32(r, uint32(1000+stepMs*s))
		t := float64(s*stepMs) / 1000
		for ch := 0; ch < nAna; ch++ {
			le.PutUint32(r[4+nDig/8+4*ch:], math.Float32bits(float32(fn(ch, t))))
		}
	}
	return d
}
