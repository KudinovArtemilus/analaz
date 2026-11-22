package shp

import (
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
)

// Целостность записей.
//
// В исправном файле счётчик в начале каждой записи идёт ровным шагом (0, 5, 10… мс),
// а размер данных делится на длину записи без остатка — это проверено на файлах
// пресса 10, пресса 5 и КПС. Если регистратор записал лишние байты или потерял часть,
// все записи после этого места «съезжают»: каждый канал читает байты соседнего
// (так в цикле OP1 13.11 17:52:58 «вольты» стали 233 254).
//
// Сбой виден по счётчику: вместо следующего значения читается мусор. Тогда ищем
// дальше по файлу место, где счётчик снова идёт ровно, и продолжаем оттуда.
// Теряется только испорченный кусок, а не весь остаток цикла.

// maxGap — сколько отсчётов подряд может пропустить регистратор, чтобы это ещё
// считалось пропуском, а не мусором вместо счётчика.
const maxGap = 1000

// Break — место, где записи съехали и чтение продолжено с найденной ровной записи.
type Break struct {
	Sample       int // номер отсчёта (в прочитанных данных), перед которым был сбой
	SkippedBytes int // сколько байт пропущено
	LostSamples  int // сколько отсчётов потеряно по счётчику
}

// Damage — повреждения файла. Nil у исправного файла.
type Damage struct {
	Breaks      []Break
	Gaps        int  // пропуски отсчётов: счётчик перескочил на целое число шагов, данные целы
	Truncated   bool // после сбоя ровных записей больше не нашлось — остаток файла отброшен
	TailBytes   int  // неполная запись в конце файла
	TotalChunks int  // сколько записей влезло бы в файл по размеру
}

// Serious — повреждение искажает данные (а не просто пропуск отсчётов).
func (d *Damage) Serious() bool {
	return d != nil && (len(d.Breaks) > 0 || d.Truncated || d.TailBytes > 0)
}

// LostSamples — сколько отсчётов потеряно во всех сбоях.
func (d *Damage) LostSamples() int {
	if d == nil {
		return 0
	}
	n := 0
	for _, b := range d.Breaks {
		n += b.LostSamples
	}
	return n
}

func (d *Damage) String() string {
	if d == nil {
		return "исправен"
	}
	var parts []string
	for _, b := range d.Breaks {
		parts = append(parts, fmt.Sprintf("сбой перед отсчётом %d: пропущено %d байт, потеряно ~%d отсчётов",
			b.Sample, b.SkippedBytes, b.LostSamples))
	}
	if d.Truncated {
		parts = append(parts, "после сбоя ровных записей не найдено — остаток отброшен")
	}
	if d.Gaps > 0 {
		parts = append(parts, fmt.Sprintf("пропусков отсчётов: %d", d.Gaps))
	}
	if d.TailBytes > 0 {
		parts = append(parts, fmt.Sprintf("неполная запись в конце: %d байт", d.TailBytes))
	}
	return strings.Join(parts, "; ")
}

// recordOffsets находит начало каждой исправной записи.
// d — весь файл, start — начало данных, recLen — длина записи.
func recordOffsets(d []byte, start, recLen int) ([]int, *Damage) {
	le := binary.LittleEndian
	total := (len(d) - start) / recLen
	dmg := &Damage{TailBytes: (len(d) - start) % recLen, TotalChunks: total}
	if total == 0 {
		return nil, finish(dmg)
	}
	u32 := func(p int) uint32 { return le.Uint32(d[p:]) }

	step := expectedStep(d, start, recLen, total)
	offs := make([]int, 0, total)
	if step == 0 { // счётчик стоит на месте — проверять нечем, читаем как есть
		for s := 0; s < total; s++ {
			offs = append(offs, start+s*recLen)
		}
		return offs, finish(dmg)
	}

	// regular — запись по смещению p продолжает счётчик prev (ровно или с пропуском).
	regular := func(p int, prev uint32) (ok, gap bool) {
		c := u32(p)
		if c <= prev || (c-prev)%step != 0 || (c-prev)/step > maxGap {
			return false, false
		}
		return true, c-prev != step
	}
	// trusted — с p начинаются три записи подряд с ровным счётчиком: это не случайное совпадение.
	trusted := func(p int, prev uint32) bool {
		if p+3*recLen > len(d) {
			return false
		}
		if ok, _ := regular(p, prev); !ok {
			return false
		}
		c := u32(p)
		return u32(p+recLen) == c+step && u32(p+2*recLen) == c+2*step
	}

	offs = append(offs, start)
	for p := start + recLen; p+recLen <= len(d); p += recLen {
		prev := u32(offs[len(offs)-1])
		if ok, gap := regular(p, prev); ok {
			if gap {
				dmg.Gaps++
			}
			offs = append(offs, p)
			continue
		}
		// Сбой. Последняя принятая запись тоже под подозрением: байты могли пропасть
		// уже внутри неё, тогда её хвост — начало следующей записи. Отбрасываем её.
		bad := offs[len(offs)-1]
		if len(offs) > 1 {
			offs = offs[:len(offs)-1]
		}
		prev = u32(offs[len(offs)-1])
		found := -1
		for q := bad + 1; q+3*recLen <= len(d); q++ {
			if trusted(q, prev) {
				found = q
				break
			}
		}
		if found < 0 {
			dmg.Truncated = true
			break
		}
		dmg.Breaks = append(dmg.Breaks, Break{
			Sample:       len(offs),
			SkippedBytes: found - (offs[len(offs)-1] + recLen),
			LostSamples:  int((u32(found)-prev)/step) - 1,
		})
		offs = append(offs, found)
		p = found // цикл прибавит recLen и проверит следующую запись
	}
	return offs, finish(dmg)
}

// expectedStep — шаг счётчика по первым записям (медиана, чтобы один сбой её не сдвинул).
func expectedStep(d []byte, start, recLen, total int) uint32 {
	n := min(total, 101)
	var diffs []uint32
	for s := 1; s < n; s++ {
		a := binary.LittleEndian.Uint32(d[start+(s-1)*recLen:])
		b := binary.LittleEndian.Uint32(d[start+s*recLen:])
		if b > a {
			diffs = append(diffs, b-a)
		}
	}
	if len(diffs) == 0 {
		return 0
	}
	slices.Sort(diffs)
	return diffs[len(diffs)/2]
}

// finish возвращает nil, если повреждений нет: у исправного файла Damage == nil.
func finish(d *Damage) *Damage {
	if len(d.Breaks) == 0 && !d.Truncated && d.Gaps == 0 && d.TailBytes == 0 {
		return nil
	}
	return d
}
