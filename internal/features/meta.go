package features

import (
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// Meta — служебные признаки: номера из имени файла и из каналов отслеживания детали.
// die_strokes (ходы штампа с замены) нужен потом для поиска замен штампа в diagnose.
func Meta(f *shp.File, out Features) {
	// время в имени здесь не нужно, поэтому часовой пояс не важен
	if n := shp.ParseName(f.Path, nil); n.HasIDs {
		out["fn_prefix"] = float64(n.Prefix)
		out["fn_piece"] = float64(n.Piece)
		out["fn_heat"] = float64(n.Heat)
	}
	// Повреждённый файл помечаем, чтобы на этапе нормы такие циклы можно было
	// исключить или проверить отдельно. У исправных файлов этих колонок нет.
	if d := f.Damage; d != nil {
		out["shp_damaged"] = b2f(d.Serious())
		out["shp_breaks"] = float64(len(d.Breaks))
		out["shp_lost_samples"] = float64(d.LostSamples())
		out["shp_truncated"] = b2f(d.Truncated)
		out["shp_gaps"] = float64(d.Gaps)
	}
	for _, c := range []struct{ name, key string }{
		{"PartTracking_PieceId_Consecutiv", "piece_id"},
		{"PartTracking_PieceId_HeatID", "heat_id"},
		{"Die.StokesSinceChange", "die_strokes"}, // "Stokes" — так в файле
		{"Which OP is Pressing", "which_op_max"},
	} {
		if x, ok := f.Analog(c.name); ok {
			out[c.key] = NanMax(x)
		}
	}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
