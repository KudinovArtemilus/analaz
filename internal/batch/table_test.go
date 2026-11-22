package batch

import (
	"slices"
	"testing"

	"github.com/KudinovArtemilus/press-go/internal/features"
)

// Set заменяет цикл с тем же файлом (по имени — и при другом пути), а новый дописывает.
func TestSetAndStale(t *testing.T) {
	tb := NewTable()
	tb.Add(`\\srv\shp\a_OP3_1.shp`, "OP3", "2026-11-11 00:00:00", features.Features{"x": 1})
	tb.Add(`\\srv\shp\b_OP3_2.shp`, "OP3", "2026-11-11 00:01:00", features.Features{"x": 2, "feat_ver": 1})
	if st := tb.Stale(1); len(st) != 1 || !st["a_op3_1.shp"] {
		t.Fatalf("старая версия: %v", st)
	}
	if len(tb.Stale(0)) != 0 {
		t.Fatal("версия 0: пересчитывать нечего")
	}
	tb.Set(`Z:\shp\A_OP3_1.shp`, "OP3", "2026-11-11 00:00:00", features.Features{"x": 10, "y": 5, "feat_ver": 1})
	tb.Set(`\\srv\shp\c_OP3_3.shp`, "OP3", "2026-11-11 00:02:00", features.Features{"x": 3, "feat_ver": 1})
	if tb.Len() != 3 || len(tb.Stale(1)) != 0 {
		t.Fatalf("строк %d, старых %v", tb.Len(), tb.Stale(1))
	}
	files := tb.Text(0)
	x := tb.Column(slices.Index(tb.Columns(), "x"))
	if files[0] != `Z:\shp\A_OP3_1.shp` || x[0] != 10 || x[1] != 2 || x[2] != 3 {
		t.Fatalf("после Set: %v %v", files, x)
	}
}
