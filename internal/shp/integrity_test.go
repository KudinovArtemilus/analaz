package shp

import (
	"testing"
)

// makeFile — синтетический файл на 2000 отсчётов (счётчик 1000+5s, значение канала ch = s*10+ch).
func makeFile() []byte {
	dig := make([]string, 16)
	for i := range dig {
		dig[i] = "d"
	}
	return buildSHP(dig, []string{"a", "b", "c"}, 2000, 0)
}

const testRec = 4 + 2 + 3*4 // длина записи в makeFile: счётчик + 16 бит + 3 float32

func recStart(n int) int { return nameStart + (16+3)*nameStep + n*testRec }

// checkAligned — у каждого прочитанного отсчёта значения принадлежат его же моменту:
// номер отсчёта по счётчику s = (Index-1000)/5, канал 2 должен быть равен s*10+2.
func checkAligned(t *testing.T, f *File) {
	t.Helper()
	for k, c := range f.Index {
		s := float32((c - 1000) / 5)
		if f.Ana[2][k] != s*10+2 {
			t.Fatalf("отсчёт %d (счётчик %d): канал = %v, ждали %v — данные съехали", k, c, f.Ana[2][k], s*10+2)
		}
	}
}

func TestIntactFile(t *testing.T) {
	f, err := Parse(makeFile())
	if err != nil {
		t.Fatal(err)
	}
	if f.Damage != nil || f.NumSamples() != 2000 {
		t.Fatalf("исправный файл: повреждения %v, отсчётов %d", f.Damage, f.NumSamples())
	}
	checkAligned(t, f)
}

func TestMissingBytes(t *testing.T) {
	d := makeFile()
	p := recStart(1200) + 7
	d = append(d[:p:p], d[p+3:]...) // внутри записи 1200 пропали 3 байта — всё дальше съехало

	f, err := Parse(d)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Damage.Serious() || len(f.Damage.Breaks) != 1 || f.Damage.Truncated {
		t.Fatalf("ждали один сбой с восстановлением, получили: %v", f.Damage)
	}
	checkAligned(t, f)
	if f.NumSamples() < 1995 { // теряются только записи вокруг сбоя
		t.Errorf("потеряно слишком много: осталось %d из 2000", f.NumSamples())
	}
	t.Log(f.Damage)
}

func TestExtraBytes(t *testing.T) {
	d := makeFile()
	p := recStart(700) + 5
	junk := []byte{1, 2, 3, 4, 5, 6, 7}
	d = append(d[:p:p], append(junk, d[p:]...)...) // вставлены 7 лишних байт

	f, err := Parse(d)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Damage.Serious() || len(f.Damage.Breaks) != 1 {
		t.Fatalf("ждали один сбой, получили: %v", f.Damage)
	}
	checkAligned(t, f)
	t.Log(f.Damage)
}

func TestGarbageBlock(t *testing.T) {
	d := makeFile()
	for i := recStart(500); i < recStart(600); i++ {
		d[i] = 0xAB // 100 записей затёрты мусором, длина файла та же
	}
	f, err := Parse(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Damage.Breaks) != 1 {
		t.Fatalf("ждали один сбой, получили: %v", f.Damage)
	}
	checkAligned(t, f)
	if lost := f.Damage.LostSamples(); lost < 100 || lost > 102 {
		t.Errorf("потеряно %d отсчётов, ждали ~100", lost)
	}
}

func TestTruncatedTail(t *testing.T) {
	d := makeFile()
	d = d[:len(d)-5] // файл оборван посреди последней записи
	f, err := Parse(d)
	if err != nil {
		t.Fatal(err)
	}
	if f.Damage == nil || f.Damage.TailBytes != testRec-5 || f.NumSamples() != 1999 {
		t.Fatalf("ждали неполную запись в конце: %v, отсчётов %d", f.Damage, f.NumSamples())
	}
	checkAligned(t, f)
}

func TestGarbageToEnd(t *testing.T) {
	d := makeFile()
	for i := recStart(1500); i < len(d); i++ {
		d[i] = 0xCD // всё после 1500 — мусор, восстановиться не на чем
	}
	f, err := Parse(d)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Damage.Truncated || f.NumSamples() >= 1500 {
		t.Fatalf("ждали обрезку: %v, отсчётов %d", f.Damage, f.NumSamples())
	}
	checkAligned(t, f)
}
