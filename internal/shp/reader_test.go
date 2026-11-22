package shp

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// buildSHP — синтетический .shp для тестов (сам сборщик — в synthetic.go, им пользуются и тесты сервера).
func buildSHP(digNames, anaNames []string, samples int, ft uint64) []byte {
	return Synthetic(digNames, anaNames, samples, ft)
}

func TestParse(t *testing.T) {
	dig := make([]string, 16)
	for i := range dig {
		dig[i] = "Y" + string(rune('A'+i))
	}
	ana := []string{"slide:total force [kN]", "  pressure  ", "slide:total force [kN]"} // дубль имени
	// 2026-09-28 00:08:25 UTC в формате FILETIME
	want := time.Date(2026, 9, 28, 0, 8, 25, 0, time.UTC)
	ft := uint64(want.UnixNano()/100 + 116444736000000000)

	f, err := Parse(buildSHP(dig, ana, 5, ft))
	if err != nil {
		t.Fatal(err)
	}

	if f.NumSamples() != 5 {
		t.Fatalf("отсчётов %d, ждали 5", f.NumSamples())
	}
	if !f.FileTime.Equal(want) {
		t.Errorf("время %v, ждали %v", f.FileTime, want)
	}
	if f.AnaNames[1] != "pressure" {
		t.Errorf("пробелы не обрезаны: %q", f.AnaNames[1])
	}
	if f.Index[3] != 1015 {
		t.Errorf("Index[3] = %d", f.Index[3])
	}
	if f.Step() != 5*time.Millisecond {
		t.Errorf("шаг %v, ждали 5ms", f.Step())
	}
	if f.Duration() != 25*time.Millisecond { // 5 отсчётов × 5 мс
		t.Errorf("длительность %v, ждали 25ms", f.Duration())
	}
	if tt := f.Times(); tt[0] != 0 || tt[4] != 0.020 {
		t.Errorf("Times() = %v", tt)
	}
	// биты: канал 0 включён на чётных отсчётах, канал 9 — на нечётных
	if !f.Dig[0][0] || f.Dig[0][1] || !f.Dig[9][1] {
		t.Errorf("биты разобраны неверно: %v / %v", f.Dig[0], f.Dig[9])
	}
	if f.Ana[2][4] != 42 {
		t.Errorf("Ana[2][4] = %v, ждали 42", f.Ana[2][4])
	}

	// дубль имени: берётся первый канал (индекс 0), как setdefault в Python
	x, ok := f.Analog("slide:total force [kN]")
	if !ok || x[4] != 40 {
		t.Errorf("Analog по дублю имени: %v", x)
	}
	p, _ := f.Analog("pressure")
	if !math.IsNaN(p[2]) || p[3] != 31 {
		t.Errorf("мусор не заменён на NaN: %v", p)
	}
	if _, ok := f.Analog("нет такого"); ok {
		t.Error("несуществующий канал найден")
	}
}

func TestParseBroken(t *testing.T) {
	if _, err := Parse(make([]byte, 100)); err == nil {
		t.Error("ждали ошибку на коротком файле")
	}
	d := buildSHP([]string{"a", "b", "c", "d", "e", "f", "g", "h"}, []string{"x"}, 1, 0)
	binary.LittleEndian.PutUint32(d[offNumAna:], 5000) // заголовок врёт
	if _, err := Parse(d); err == nil {
		t.Error("ждали ошибку, когда заголовок обещает больше каналов, чем есть")
	}
}
