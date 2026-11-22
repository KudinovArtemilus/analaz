package analyze

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Норма сохраняется в JSON и читается обратно без потери точности; NaN переживает JSON как null.
func TestNormSetRoundTrip(t *testing.T) {
	n := 40
	x, y := make([]float64, n), make([]float64, n)
	for i := range x {
		x[i] = 100 + 0.1*float64(i%7) + 1e-13*float64(i) // «некруглые» числа
		y[i] = math.NaN()
	}
	d := testSet(everyMin(n, time.Minute), col{"hp_min", x}, col{"temp_upper_mean", y})
	ns := BuildNormSet(d, d, firstRows(d, 20), Columns{Eng: []string{"hp_min"}})
	ns.Op = "OP1"
	path := filepath.Join(t.TempDir(), "norm.json")
	if err := ns.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("временный файл остался")
	}
	got, err := LoadNorm(path)
	if err != nil {
		t.Fatal(err)
	}
	a, b := ns.Features["hp_min"], got.Features["hp_min"]
	if a.Median != b.Median || a.Sigma != b.Sigma || a.Q != b.Q || b.Kind != "eng" {
		t.Errorf("hp_min: было %+v, стало %+v", *a, *b)
	}
	if v := got.Median("temp_upper_mean"); !math.IsNaN(v) {
		t.Errorf("NaN-медиана после JSON = %v", v)
	}
	if !got.BaseFrom.Equal(d.Times[0]) || !got.BaseTo.Equal(d.Times[19]) || got.BaseCycles != 20 {
		t.Errorf("период %v — %v (%d)", got.BaseFrom, got.BaseTo, got.BaseCycles)
	}
	if _, drop := got.Restrict(Columns{Eng: []string{"hp_min", "new_feature"}}); drop != 1 {
		t.Errorf("Restrict: выброшено %d, ждали 1", drop)
	}
}

func TestBaselineRangeLikePython(t *testing.T) {
	d := testSet(everyMin(5, 12*time.Hour), col{"x", []float64{1, 2, 3, 4, 5}}) // 01.11 00:00, 12:00, 02.11 00:00…
	to, _ := ParseWhen("2026-11-02")                                            // дата без времени = полночь, включительно
	if b := BaselineRange(d, time.Time{}, to); b.Len() != 3 {
		t.Errorf("до 02.11 00:00 включительно: %d циклов, ждали 3", b.Len())
	}
	from, _ := ParseWhen("2026-11-01 12:00")
	if b := BaselineRange(d, from, time.Time{}); b.Len() != 4 {
		t.Errorf("с 01.11 12:00: %d циклов, ждали 4", b.Len())
	}
}

func TestDropDamaged(t *testing.T) {
	nan := math.NaN()
	d := testSet(everyMin(4, time.Minute), col{"shp_damaged", []float64{0, 1, nan, 0}})
	if out, n := DropDamaged(d); n != 1 || out.Len() != 3 {
		t.Errorf("выброшено %d, осталось %d", n, out.Len())
	}
	d2 := testSet(everyMin(2, time.Minute), col{"x", []float64{1, 2}})
	if _, n := DropDamaged(d2); n != 0 {
		t.Errorf("таблица без shp_damaged: выброшено %d", n)
	}
}

// Мусорный цикл (сдвиг записей .shp) — невозможные значения сразу во многих признаках,
// в том числе в инертных (температура, дренаж). Сбой пресса — большие отклонения движения
// и усилия, но температура и дренажи нормальные: это НЕ мусор, такой цикл остаётся.
func TestFindGarbage(t *testing.T) {
	n := 60
	names := []string{"pos_bottom_mm", "stroke_mm", "cycle_s", "force_max_kN", "work_kJ", "press_time_s",
		"temp_bottom_mean", "casedrain_md2_mean", "accu_rv1_min"}
	var cols []col
	for k, name := range names {
		x := make([]float64, n)
		for i := range x {
			x[i] = 100 + float64(k) + float64(i%5)*0.3
		}
		x[30] = 175489 // повреждённый .shp: номер заготовки во всех каналах
		if k < 6 {
			x[20] = 1e6 // незавершённый цикл / аварийный стоп: движение и усилие далеко, масло и дренажи как обычно
		}
		cols = append(cols, col{name, x})
	}
	d := testSet(everyMin(n, time.Minute), cols...)
	g := FindGarbage(d, names)
	if len(g) != 1 || g[0].Index != 30 || g[0].Feats != 9 || g[0].Inert != 3 {
		t.Errorf("ждали только цикл 30 (9 признаков, 3 инертных), получили %+v", g)
	}
}
