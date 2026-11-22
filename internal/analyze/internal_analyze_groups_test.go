package analyze

import (
	"math"
	"slices"
	"testing"
)

// Сверка с Python (charts.py, раздел 3) на настоящих таблицах OP1/OP2 сделана отдельно:
// 1339 и 1209 значений G и Gz совпали бит в бит (скрипт ref/dump_groups.py).
// Здесь — правила, которые легко сломать при правке.

func TestGroupsList(t *testing.T) {
	if len(pressGroups) != 18 {
		t.Errorf("групп пресса %d, в charts.py 18", len(pressGroups))
	}
	if !slices.Equal(titles(Groups[:18]), titles(pressGroups)) || len(Groups) != 18+len(kpsGroups()) {
		t.Errorf("сначала группы пресса как в charts.py, потом КПС: всего %d", len(Groups))
	}
	if g := Groups[0].Feats; len(g) != 6 || g[0] != "casedrain_md1_mean" || g[5] != "casedrain_md6_mean" {
		t.Errorf("дренажи: %v", g)
	}
	if g := Groups[1].Feats; len(g) != 12 || g[0] != "pump11_pivot_err_active" || g[11] != "pump62_pivot_err_active" {
		t.Errorf("насосы: %v", g)
	}
	if g := Groups[5].Feats; len(g) != 11 || g[6] != "vCCylR_Y22_1_err_rms" {
		t.Errorf("сервоклапаны: %v", g)
	}
	// каждый признак групп должен иметь описание — иначе в легенде будет сырое имя
	for _, g := range Groups {
		for _, f := range g.Feats {
			if _, ok := Descs[f]; !ok {
				t.Errorf("%s: нет описания %s", g.Title, f)
			}
		}
	}
}

func titles(gs []Group) []string {
	var out []string
	for _, g := range gs {
		out = append(out, g.Title)
	}
	return out
}

func TestGroupDataSkipsEmpty(t *testing.T) {
	nan := math.NaN()
	r := &Result{
		Cols:  Columns{Eng: []string{"casedrain_md1_mean", "casedrain_md2_mean", "hp_min"}},
		Bins:  &Bins{Med: [][]float64{{2, 4}, {nan, nan}, {nan, nan}}},
		Norms: []Norm{{Median: 3, Sigma: 0.5}, {Median: 1, Sigma: 1}, {Median: 1, Sigma: 1}},
	}
	gs := r.GroupData()
	if len(gs) != 1 || gs[0].Title != Groups[0].Title {
		t.Fatalf("ждали одну группу дренажей, получили %d", len(gs))
	}
	g := gs[0]
	if !slices.Equal(g.Feats, []string{"casedrain_md1_mean"}) { // MD2 без данных — убран
		t.Errorf("признаки: %v", g.Feats)
	}
	if !slices.Equal(g.Z[0], []float64{-2, 2}) { // (2−3)/0.5, (4−3)/0.5
		t.Errorf("z: %v", g.Z[0])
	}
}

func TestShort(t *testing.T) {
	long := "Насос 6.2: ошибка угла наклона под нагрузкой (задание − факт), %"
	if s := Short(long); s != "Насос 6.2" {
		t.Errorf("длинное: %q", s)
	}
	if s := Short("Насос 6.2: макс. давление"); s != "Насос 6.2: макс. давление" {
		t.Errorf("короткое не трогаем: %q", s)
	}
}
