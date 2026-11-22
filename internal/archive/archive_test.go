package archive

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/features"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

func TestSaveReadVerify(t *testing.T) {
	buf := t.TempDir()
	src := filepath.Join(buf, "x_OP1.shp")
	data := bytes.Repeat([]byte("press-go shp "), 5000)
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(filepath.Join(t.TempDir(), "archive"), 0)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := a.Save(src, "2026/11/19/x_OP1.shp", "OP1", []string{WhyAnomaly})
	if !ok || err != nil {
		t.Fatalf("сохранение: %v %v", ok, err)
	}
	got, err := a.Read(`2026\11\19\x_OP1.shp`) // обратные слеши Windows — тот же файл
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("чтение из архива: %v, совпало %v", err, bytes.Equal(got, data))
	}
	// источник не тронут
	if st, err := os.Stat(src); err != nil || st.Size() != int64(len(data)) {
		t.Errorf("источник изменился: %v", err)
	}
	// второй раз — не перезаписывается
	if ok, err := a.Save(src, "2026/11/19/x_OP1.shp", "OP1", nil); ok || err != nil {
		t.Errorf("повторное сохранение: %v %v", ok, err)
	}
	// журнал и «уже есть» переживают перезапуск
	b, _ := Open(a.Root, 0)
	if !b.Has("2026/11/19/x_OP1.shp") || b.Used() != a.Used() {
		t.Errorf("после перезапуска: has %v, занято %d / %d", b.Has("2026/11/19/x_OP1.shp"), b.Used(), a.Used())
	}
	es := b.Entries()
	if len(es) != 1 || es[0].Op != "OP1" || !slices.Equal(es[0].Reasons, []string{WhyAnomaly}) || es[0].Size != int64(len(data)) || len(es[0].SHA256) != 64 {
		t.Errorf("журнал: %+v", es)
	}
	// .tmp не осталось
	if m, _ := filepath.Glob(filepath.Join(a.Root, "2026", "11", "19", "*.tmp")); len(m) != 0 {
		t.Errorf("остался временный файл: %v", m)
	}
}

func TestSaveFullAndBadPath(t *testing.T) {
	src := filepath.Join(t.TempDir(), "y.shp")
	os.WriteFile(src, bytes.Repeat([]byte{1, 2, 3, 4}, 10000), 0o644)
	a, _ := Open(t.TempDir(), 10) // 10 байт — ничего не влезет
	if _, err := a.Save(src, "2026/y.shp", "OP1", nil); !errors.Is(err, ErrFull) {
		t.Errorf("переполнение: %v", err)
	}
	for _, bad := range []string{"../y.shp", "/etc/y.shp", "..", ""} {
		if _, err := a.Save(src, bad, "OP1", nil); err == nil {
			t.Errorf("путь %q должен быть отклонён", bad)
		}
	}
}

func TestSelect(t *testing.T) {
	at := func(m int) time.Time {
		return time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC).Add(time.Duration(m) * time.Minute)
	}
	var times []time.Time
	var ds []float64
	for i := 0; i < 30; i++ {
		times, ds = append(times, at(i)), append(ds, float64(i+1))
	}
	p := testDataset(times, ds)
	flag := make([]bool, 30)
	score := make([]float64, 30)
	flag[3], score[3] = true, 9
	r := &analyze.Result{All: p, Prod: p, Anom: &analyze.Anomalies{Flag: flag, Score: score},
		DamagedF: []string{"bad.shp"},
		Stops:    &analyze.StopsResult{GapThrMin: 20, Stops: []analyze.Stop{{From: at(15), Resume: at(20)}}}}
	ps := Select(r, "OP1", 10)
	why := map[string][]string{}
	for _, x := range ps {
		why[x.File] = x.Reasons
	}
	if !slices.Contains(why["f03"], WhyAnomaly) || !slices.Contains(why["bad.shp"], WhyDamaged) {
		t.Errorf("аномальный/повреждённый: %v", why)
	}
	if !slices.Contains(why["f15"], WhyBefore) || slices.Contains(why["f16"], WhyBefore) || !slices.Contains(why["f06"], WhyBefore) || slices.Contains(why["f05"], WhyBefore) {
		t.Errorf("перед остановом — 10 последних до 08:15 включительно: %v", why)
	}
	if !slices.Contains(why["f20"], WhyAfter) || !slices.Contains(why["f24"], WhyAfter) || slices.Contains(why["f25"], WhyAfter) {
		t.Errorf("после пуска — 5 первых с 08:20: %v", why)
	}
	// фон: ход штампа кратен 10 (f09 → 10, f19 → 20, f29 → 30); f09 уже сохранён как «перед остановом» —
	// фоном его второй раз не помечаем
	if !slices.Equal(why["f19"], []string{WhyFrequent}) || !slices.Equal(why["f29"], []string{WhyFrequent}) || slices.Contains(why["f09"], WhyFrequent) {
		t.Errorf("фон: f09 %v, f19 %v, f29 %v", why["f09"], why["f19"], why["f29"])
	}
	// важное — раньше фона
	if ps[len(ps)-1].Reasons[0] != WhyFrequent || ps[0].Reasons[0] == WhyFrequent {
		t.Errorf("порядок: сначала важное")
	}
}

// Предел 0 из общего потолка — «места нет», а не «без предела».
func TestSetMaxZeroMeansFull(t *testing.T) {
	src := filepath.Join(t.TempDir(), "x.shp")
	os.WriteFile(src, bytes.Repeat([]byte("shp"), 100), 0o644)
	a, err := Open(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	a.SetMax(0)
	if _, err := a.Save(src, "x.shp", "OP1", nil); !errors.Is(err, ErrFull) {
		t.Fatalf("при пределе 0 записалось: %v", err)
	}
}

// КПС: счётчика ходов штампа нет — фон по номеру заготовки из имени, и он не меняется,
// когда в таблицу досчитываются пропуски (номер строки при этом сдвигается).
func TestSelectBackgroundByPiece(t *testing.T) {
	mk := func(pieces []int) []int {
		tb := batch.NewTable()
		t0 := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
		for _, n := range pieces {
			f := fmt.Sprintf(`\\kps\shp\26.%06d.067665_OP3_x.shp`, n)
			tb.Add(f, "OP3", t0.Add(time.Duration(n)*time.Minute).Format("2006-01-02 15:04:05"), features.Features{"MRF_Force_F__max": 800})
		}
		d, err := analyze.FromTable(tb)
		if err != nil {
			t.Fatal(err)
		}
		r := &analyze.Result{All: d, Prod: d, Anom: &analyze.Anomalies{Flag: make([]bool, d.Len()), Score: make([]float64, d.Len())},
			Stops: &analyze.StopsResult{GapThrMin: 1e9}}
		var bg []int
		for _, p := range Select(r, "OP3", 5) {
			bg = append(bg, shp.ParseName(p.File, nil).Piece)
		}
		return bg
	}
	sparse := mk([]int{100, 102, 104, 105, 110, 113, 115})
	full := mk([]int{100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 114, 115})
	if want := []int{100, 105, 110, 115}; !slices.Equal(sparse, want) || !slices.Equal(full, want) {
		t.Fatalf("фон по заготовке: выборка %v, полная %v", sparse, full)
	}
}
