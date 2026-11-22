package analyze

import (
	"strings"
	"testing"
	"time"
)

// Журнал КПС: колесо, валок, рецепт, задание, шаг записи, пауза. Одиночный выброс — не событие.
func TestJournalKps(t *testing.T) {
	n := 60
	times := everyMin(n, 10*time.Minute)
	times[40] = times[39].Add(5 * time.Hour) // пауза 5 ч перед 40-м
	for i := 41; i < n; i++ {
		times[i] = times[i-1].Add(10 * time.Minute)
	}
	dv, dw, brw, dt, sw, mark, strokes := make([]float64, n), make([]float64, n), make([]float64, n),
		make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n)
	for i := range n {
		dv[i], dw[i], brw[i], dt[i], sw[i] = 860, 895, 138, 20, 50
		mark[i], strokes[i] = 27105134609, float64(500+i)
		if i >= 50 { // колесо сменилось на 50-м
			dv[i], dw[i] = 1164, 1253
		}
		if i >= 20 { // рецепт: обод 138 → 141 с 20-го
			brw[i] = 141
		}
		if i >= 30 { // шаг записи 20 → 5 мс с 30-го
			dt[i] = 5
		}
		if i >= 25 { // задание угла P2.1: 50 → 59 с 25-го
			sw[i] = 59 + float64(i%2)*0.1
		}
		if i >= 45 { // замена главного валка на 45-м: отметка новая, счётчик заново
			mark[i], strokes[i] = 27110080000, float64(i-45)
		}
	}
	dv[10] = 1164 // один цикл другого колеса — выброс, не событие
	brw[12] = 150 // и в рецепте — тоже
	d := testSet(times, col{"kps_wheel_dv", dv}, col{"kps_wheel_dw", dw}, col{"kps_wheel_brw", brw}, col{"dt_ms", dt},
		col{"kps_p2_1_swivel_set_max", sw}, col{"kps_roll_mr_change", mark}, col{"kps_roll_mr_strokes", strokes})
	r := &Result{All: d, Prod: d}
	ev := r.Journal()
	byKind := map[string][]Event{}
	for _, e := range ev {
		byKind[e.Kind] = append(byKind[e.Kind], e)
	}
	check := func(kind string, at int, want string) {
		t.Helper()
		es := byKind[kind]
		if len(es) != 1 || !es[0].At.Equal(times[at]) || !strings.Contains(es[0].Text, want) {
			t.Errorf("%s: %+v (ждали одно событие в цикле %d, «%s»)", kind, es, at, want)
		}
	}
	check("wheel", 50, "Колесо: DV 860 / DW 895 → DV 1164 / DW 1253")
	check("recipe", 20, "ширина обода готового колеса BRW 138 → 141")
	check("step", 30, "шаг записи 20 → 5 мс")
	check("setpoint", 25, "Насос КПС P2.1: задание угла, макс. 50 → 59")
	check("roll", 45, "Замена валка: главный (MR) — ходов до замены 544, после 0")
	check("gap", 40, "Пауза: нет циклов 5 ч 0 мин")
	if len(ev) != 6 {
		t.Errorf("событий %d, ждали 6: %+v", len(ev), ev)
	}
	for k := 1; k < len(ev); k++ {
		if ev[k].At.After(ev[k-1].At) {
			t.Fatal("журнал не по убыванию времени")
		}
	}
}

// Счётчик валка без отметки замены: замена — по сбросу счётчика.
func TestJournalRollByStrokes(t *testing.T) {
	st := []float64{100, 101, 102, 3, 4, 5}
	d := testSet(everyMin(len(st), time.Minute), col{"kps_roll_wrl_strokes", st})
	ev := (&Result{All: d, Prod: d}).Journal()
	if len(ev) != 1 || ev[0].Kind != "roll" || !strings.Contains(ev[0].Text, "диска левый (WRL) — ходов до замены 102, после 3") {
		t.Fatalf("%+v", ev)
	}
}
