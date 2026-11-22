package web

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Сервер не должен отдавать файлы за пределами папки данных и не-.shp файлы.
func TestOpenRejectsForeignPaths(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("x"), 0o644)
	s, err := New(dir, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "../x.shp", "../../etc/passwd.shp", "/etc/passwd.shp", "secret.txt", `..\x.shp`} {
		if _, err := s.open(bad); err == nil {
			t.Errorf("путь %q должен быть отклонён", bad)
		}
	}
}

func TestPagesRender(t *testing.T) {
	s, err := New(t.TempDir(), time.UTC)
	if err != nil {
		t.Fatal(err) // ошибка в шаблонах всплывёт здесь, а не у пользователя
	}
	h := s.Handler()
	for path, want := range map[string]string{
		"/":                    "press-go",
		"/files":               "ничего не найдено",
		"/static/app.js":       "",
		"/cycle?f=../a.shp":    "недопустимый путь",
		"/cycle/chart?f=x.shp": "",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if want != "" && !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: в ответе нет %q (код %d)", path, want, rec.Code)
		}
	}
}

func TestGroupName(t *testing.T) {
	for in, want := range map[string]string{
		"slide:total force [kN]": "slide", "CCyl.lvSetDynPos": "ccyl", "CCyl Left Encoder": "ccyl",
		"CLP_C1_bar": "clp", "Pump 1.1 svValve": "pump", "Which OP is Pressing": "which",
	} {
		if got := groupName(in); got != want {
			t.Errorf("groupName(%q) = %q, ждали %q", in, got, want)
		}
	}
}

func TestTimeWindow(t *testing.T) {
	times := []float64{0, 0.5, 1, 1.5, 2, 2.5}
	for _, c := range []struct {
		t0, t1 string
		want   []int
	}{
		{"1", "2", []int{1, 2, 3, 4, 5}}, // с запасом по отсчёту с краёв
		{"0", "0.6", []int{0, 1, 2}},
		{"", "", nil},    // без участка — весь цикл
		{"2", "1", nil},  // перепутаны края
		{"x", "1", nil},  // мусор
		{"9", "10", nil}, // за пределами цикла — пусто, а значит весь цикл
	} {
		if got := timeWindow(times, c.t0, c.t1); !slices.Equal(got, c.want) {
			t.Errorf("timeWindow(%s, %s) = %v, ждали %v", c.t0, c.t1, got, c.want)
		}
	}
}

// Сбой датчика (NaN) должен уйти в JSON как null: стандартный JSON не умеет NaN,
// и без этого весь ответ /api/series падал бы с ошибкой.
func TestF32sJSON(t *testing.T) {
	b, err := json.Marshal(f32s{1.5, float32(math.NaN()), -2, 0.005})
	if err != nil || string(b) != "[1.5,null,-2,0.005]" {
		t.Errorf("f32s: %s, %v", b, err)
	}
}

func TestOnIntervals(t *testing.T) {
	times := []float64{0, 1, 2, 3, 4}
	for _, c := range []struct {
		bits []bool
		want [][2]float64
	}{
		{[]bool{false, true, true, false, true}, [][2]float64{{1, 3}, {4, 5}}}, // последний — до конца + шаг
		{[]bool{true, false, false, false, false}, [][2]float64{{0, 1}}},
		{[]bool{true, true, true, true, true}, nil},      // не менялся — не показываем
		{[]bool{false, false, false, false, false}, nil}, // тоже
	} {
		if got := onIntervals(c.bits, times, 1); !slices.EqualFunc(got, c.want, func(a, b [2]float64) bool { return a == b }) {
			t.Errorf("%v: %v, ждали %v", c.bits, got, c.want)
		}
	}
}

// Обзор цикла показывается, даже если ни один канал не отмечен.
func TestChartOverviewWithoutSelection(t *testing.T) {
	s, err := New(t.TempDir(), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := s.tmpl.ExecuteTemplate(&b, "chart.html", chartData("a.shp", nil, nil, []int{3, 7})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `data-ov="3,7"`) || !strings.Contains(b.String(), "появятся под обзором") {
		t.Errorf("нет обзора без выбранных каналов: %s", b.String())
	}
	b.Reset()
	s.tmpl.ExecuteTemplate(&b, "chart.html", chartData("a.shp", nil, nil, nil)) // другой пресс, без каналов обзора
	if !strings.Contains(b.String(), "Отметьте каналы слева") {
		t.Errorf("без обзора и без каналов должна быть подсказка: %s", b.String())
	}
}

// zcls: NaN (нормы нет) — без подсветки. Через gt/lt шаблона NaN давал «тревогу».
func TestZClass(t *testing.T) {
	for _, c := range []struct {
		z    any
		want string
	}{
		{4.5, "st-alarm"}, {-4.5, "st-alarm"}, {2.5, "st-warn"}, {-2.5, "st-warn"}, {1.0, ""},
		{math.NaN(), ""}, {num(math.NaN()), ""}, {num(-5), "st-alarm"}, {"x", ""},
	} {
		if got := zClass(c.z, 2, 4); got != c.want {
			t.Errorf("zClass(%v) = %q, ждали %q", c.z, got, c.want)
		}
	}
}
