package analyze

import (
	"math"
	"testing"
	"time"
)

// TestPressRecipes — режим пресса: рецепт по скорости опускания и нижним цилиндрам. Мусор
// повреждённых записей и единичные циклы режимом не считаются; SCu в ключ не входит.
func TestPressRecipes(t *testing.T) {
	n := 40
	sp, lc, scu := make([]float64, n), make([]float64, n), make([]float64, n)
	for i := range sp {
		sp[i], lc[i], scu[i] = 60, 650, float64(i%3*20) // SCu скачет внутри рецепта — это не рецепт
		if i >= 25 {
			sp[i], lc[i] = 45, 655
		}
	}
	sp[10], lc[10] = 233254, -1576   // повреждённая запись
	sp[11], lc[11] = 70, 650         // единичный цикл
	sp[12], lc[12] = math.NaN(), 650 // канала нет в цикле
	d := testSet(everyMin(n, time.Hour), col{RecipeChannels[0], sp}, col{RecipeChannels[1], lc}, col{RecipeChannels[2], scu})
	r := Regimes(d)
	if len(r) != 2 || r[0].Key != "rcp60-650" || r[0].Cycles != 22 || r[1].Key != "rcp45-655" || r[1].Cycles != 15 {
		t.Fatalf("рецепты: %+v", r)
	}
	if RegimeKey(d, 10) != "" || RegimeKey(d, 12) != "" {
		t.Error("мусор и пустое значение — не рецепт")
	}
	if l := RegimeLabel("rcp45-655"); l != "рецепт 45 / 655" || !IsRecipe("rcp45-655") || IsRecipe("dv860-dw895") {
		t.Errorf("подпись: %q", l)
	}
	if got := d.OnlyRegime("rcp45-655").Len(); got != 15 {
		t.Errorf("циклов рецепта 45/655: %d", got)
	}
	// журнал: смена рецепта пресса — не «смена колеса» (рецепт уже есть в событиях штампа)
	res := &Result{All: d, Prod: d}
	for _, e := range res.Journal() {
		if e.Kind == "wheel" {
			t.Errorf("у пресса событие «колесо»: %+v", e)
		}
	}
}
