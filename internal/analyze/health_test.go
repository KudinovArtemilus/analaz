package analyze

import (
	"math"
	"testing"
)

func TestTopMeanNoNaN(t *testing.T) {
	if v := topMeanNoNaN([]float64{1, 9, 4, 7}, 3); v != (4+7+9)/3.0 {
		t.Errorf("три наибольших = %v", v)
	}
	if v := topMeanNoNaN([]float64{2, 5}, 3); v != 3.5 {
		t.Errorf("меньше трёх = %v", v)
	}
	if v := topMeanNoNaN(nil, 3); !math.IsNaN(v) {
		t.Errorf("пусто = %v", v)
	}
}
