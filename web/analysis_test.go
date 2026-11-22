package web

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/features"
)

// writeTable — небольшая таблица признаков: 80 рабочих циклов OP1 за 4 суток, MD5 мёртв.
func writeTable(t *testing.T, dir string) string {
	t.Helper()
	tb := batch.NewTable()
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := range 80 {
		f := features.Features{"dt_ms": 5, "force_max_kN": 25000 + float64(i%7)*100, "hp_min": 274 + float64(i%3)*0.2,
			"cycle_s": 40 + float64(i%4)*0.3}
		for k := 1; k <= 6; k++ {
			f[fmt.Sprintf("casedrain_md%d_mean", k)] = 0.5 + float64((i+k)%5)*0.01
		}
		f["casedrain_md5_mean"] = 0
		f["fn_piece"], f["fn_heat"] = float64(100000+i), float64(500+i/10) // номер из имени файла: 10 заготовок на плавку
		tm := t0.Add(time.Duration(i) * 72 * time.Minute).Format("2006-01-02 15:04:05")
		tb.Add(fmt.Sprintf(`D:\shp\c%03d_OP1.shp`, i), "OP1", tm, f)
	}
	p := filepath.Join(dir, "features_OP1.csv.gz")
	if err := tb.WriteCSVGz(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func get(t *testing.T, h http.Handler, url string, wantCode int, into any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
	if rec.Code != wantCode {
		t.Fatalf("%s: код %d, ждали %d: %s", url, rec.Code, wantCode, rec.Body.String())
	}
	if into != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			t.Fatalf("%s: не JSON: %v\n%s", url, err, rec.Body.String())
		}
	}
}

func TestAnalysisAPI(t *testing.T) {
	dir := t.TempDir()
	path := writeTable(t, dir)
	s, _ := New(t.TempDir(), time.UTC)
	h := s.Handler()

	get(t, h, "/api/analysis", http.StatusServiceUnavailable, nil) // без -features — понятная ошибка, а не 404
	if err := s.EnableAnalysis(dir, dir); err != nil {
		t.Fatal(err)
	}

	var ops struct{ Ops []opJSON }
	get(t, h, "/api/analysis", 200, &ops)
	if len(ops.Ops) != 1 || ops.Ops[0].Op != "OP1" {
		t.Fatalf("операции: %+v", ops)
	}

	// пять одновременных первых запросов — один расчёт
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() { defer wg.Done(); get(t, h, "/api/analysis/op1", 200, nil) }()
	}
	wg.Wait()
	if n := s.an.computed.Load(); n != 1 {
		t.Errorf("посчитано %d раз, ждали 1", n)
	}

	var sum summaryJSON
	get(t, h, "/api/analysis/OP1", 200, &sum)
	if sum.Cycles != 80 || len(sum.Units) == 0 || len(sum.Lost) != 1 || sum.Lost[0].Signal != "casedrain_md5" {
		t.Errorf("сводка: циклов %d, узлов %d, пропал %+v", sum.Cycles, len(sum.Units), sum.Lost)
	}
	if sum.Units[0].Status == "ok" { // сначала проблемные узлы
		t.Errorf("первым идёт узел в норме: %+v", sum.Units[0])
	}

	var feat featureJSON
	get(t, h, "/api/analysis/OP1/feature?f=hp_min&n=200", 200, &feat)
	if feat.Points != 80 || len(feat.T) != len(feat.V) || len(feat.Rm) == 0 {
		t.Errorf("ряд: %d точек, t %d, v %d, rm %d", feat.Points, len(feat.T), len(feat.V), len(feat.Rm))
	}
	get(t, h, "/api/analysis/OP1/anomalies?limit=5", 200, nil)
	get(t, h, "/api/analysis/OP1/trends", 200, nil)
	var hm healthJSON
	get(t, h, "/api/analysis/OP1/health", 200, &hm)
	if len(hm.Subsystems) == 0 || len(hm.Z) != len(hm.Subsystems) || len(hm.Z[0]) != len(hm.Bins) || len(hm.Index) != len(hm.Bins) {
		t.Errorf("карта здоровья: подсистем %d, интервалов %d", len(hm.Subsystems), len(hm.Bins))
	}
	var gr groupsJSON
	get(t, h, "/api/analysis/OP1/groups", 200, &gr)
	// MD5 в синтетике — мёртвый датчик (всегда 0): постоянный признак отбрасывается ещё
	// при выборе колонок (как в Python), поэтому линий 5, а MD5 — в карточке «Сигнал пропал»
	if len(gr.Groups) == 0 || gr.Groups[0].Title != "Дренаж корпусов насосов MD1–MD6" || len(gr.Groups[0].Feats) != 5 ||
		len(gr.Groups[0].Feats[0].Val) != len(gr.Bins) || len(gr.Groups[0].Feats[0].Z) != len(gr.Bins) {
		t.Errorf("группы: %.300v", gr)
	} else if n := gr.Groups[0].Feats[4].Name; !strings.Contains(n, "MD6") {
		t.Errorf("подпись дренажа MD6: %q", n)
	}
	var pre preStopsJSON
	get(t, h, "/api/analysis/OP1/prestops", 200, &pre)
	if pre.Features == nil || pre.Lines == nil { // пустые списки, а не null: браузеру проще
		t.Errorf("перед остановами: %+v", pre)
	}
	for _, l := range pre.Lines {
		if len(l.Z) != len(pre.Features) || len(l.Z[0]) != len(l.X) {
			t.Errorf("линия %s: признаков %d, точек %d/%d", l.Label, len(l.Z), len(l.X), len(l.Z[0]))
		}
	}
	var hz groupHealthJSON
	get(t, h, "/api/analysis/OP1/heats", 200, &hz)
	// в синтетике 80 циклов, по 10 на плавку (fn_heat = 500 + i/10) → 8 плавок
	if len(hz.Keys) != 8 || hz.Keys[0] != 500 || len(hz.Z) != len(hz.Subsystems) || len(hz.Z[0]) != 8 || hz.ZMax < 10 {
		t.Errorf("по плавкам: %+v", hz)
	}
	get(t, h, "/api/analysis/OP1/heats?h=503", 200, &hz)
	if hz.Heat != 503 || len(hz.Keys) != 10 || hz.Keys[0] != 100030 {
		t.Errorf("заготовки плавки 503: %+v", hz.Keys)
	}
	get(t, h, "/api/analysis/OP1/heats?h=x", http.StatusBadRequest, nil)
	var cmp compareJSON
	get(t, h, "/api/analysis/OP1/compare", 200, &cmp)
	// в синтетике нет признаков пар (работа, ходы штампа) — пар может не быть, а длительность есть
	if len(cmp.Cycle) == 0 || cmp.Cycle[0].N == 0 || cmp.Pairs == nil || cmp.BeforeAfter == nil {
		t.Errorf("сравнение: интервалов %d, пар %v", len(cmp.Cycle), cmp.Pairs)
	}
	get(t, h, "/api/analysis/OP1/day", 200, nil)
	get(t, h, "/api/analysis/OP1/day?d=2026-11-02", 200, nil)

	// ошибки — JSON с текстом, правильный код
	// «..» в пути ServeMux чистит сам редиректом — до наших обработчиков запрос не доходит.
	// Код редиректа зависит от версии Go (1.24 — 301, новее — 307), поэтому проверяем «любой 3xx».
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/analysis/../etc", nil))
	if rec.Code < 300 || rec.Code > 399 {
		t.Errorf("/api/analysis/../etc: код %d, ждали редирект 3xx", rec.Code)
	}
	get(t, h, "/api/analysis/x1", http.StatusBadRequest, nil)
	get(t, h, "/api/analysis/OP7", http.StatusNotFound, nil)
	get(t, h, "/api/analysis/OP1/feature?f=nope", http.StatusNotFound, nil)
	get(t, h, "/api/analysis/OP1/day?d=вчера", http.StatusBadRequest, nil)

	// таблица обновилась — пересчёт; не обновилась — из памяти
	get(t, h, "/api/analysis/OP1", 200, nil)
	if n := s.an.computed.Load(); n != 1 {
		t.Errorf("без изменений пересчитали: %d", n)
	}
	later := time.Now().Add(time.Minute)
	os.Chtimes(path, later, later)
	get(t, h, "/api/analysis/OP1", 200, nil)
	if n := s.an.computed.Load(); n != 2 {
		t.Errorf("после изменения таблицы посчитано %d раз, ждали 2", n)
	}
}

// Битая норма не роняет сервер: ошибка приходит в ответе.
func TestAnalysisBadNorm(t *testing.T) {
	dir := t.TempDir()
	writeTable(t, dir)
	os.WriteFile(filepath.Join(dir, "norm_OP1.json"), []byte("{не json"), 0o644)
	s, _ := New(t.TempDir(), time.UTC)
	s.EnableAnalysis(dir, dir)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/analysis/OP1", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "norm_OP1.json") {
		t.Errorf("код %d: %s", rec.Code, rec.Body.String())
	}
}

// Страницы анализа рендерятся без ошибок шаблона (ошибка поля в шаблоне видна только при выполнении).
func TestAnalysisPages(t *testing.T) {
	dir := t.TempDir()
	writeTable(t, dir)
	s, _ := New(t.TempDir(), time.UTC)
	h := s.Handler()

	body := func(url string) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: код %d", url, rec.Code)
		}
		return rec.Body.String()
	}
	if b := body("/analysis"); !strings.Contains(b, "Анализ не включён") {
		t.Errorf("без -features: %.200s", b)
	}
	s.EnableAnalysis(dir, dir)
	b := body("/analysis?op=OP1")
	for _, want := range []string{"Пресс OP1", "Сигнал пропал", "Давление дренажа корпуса MD5", "Отчёт за сутки", "</html>"} {
		if !strings.Contains(b, want) {
			t.Errorf("страница анализа: нет %q", want)
		}
	}
	// К4б: регистратор молчит с 04.11 22:48 — журнал говорит об этом первой строкой
	if !strings.Contains(b, "Журнал изменений") || !strings.Contains(b, "Нет новых циклов с 04.11 22:48") {
		t.Errorf("журнал изменений: нет карточки или «хвоста»")
	}
	if b := body("/analysis/day?op=OP1&d=2026-11-02"); !strings.Contains(b, "циклов") || strings.Contains(b, "шаблон") {
		t.Errorf("сутки: %.300s", b)
	}
	// 6.2з: данные «Сутки по часам» лежат во фрагменте как JSON (html/template кодирует сам)
	if b := body("/analysis/day?op=OP1&d=2026-11-02"); !strings.Contains(b, `<script type="application/json">{"plots":[`) || !strings.Contains(b, `"per_hour":[[`) {
		t.Errorf("сутки по часам: нет данных во фрагменте")
	}
	for _, want := range []string{"Здоровье датчиков", "пауза между циклами", "/analysis/norm?op=OP1"} { // виновников в синтетике нет — аномалий нет
		if !strings.Contains(b, want) {
			t.Errorf("страница анализа (6.2а): нет %q", want)
		}
	}
	if b := body("/analysis/norm?op=OP1"); !strings.Contains(b, "Профиль нормы") || !strings.Contains(b, "Высокое давление, минимум") {
		t.Errorf("профиль нормы: %.300s", b)
	}
	if b := body("/analysis/norm?op=OP1&q=md5"); !strings.Contains(b, "MD5") || strings.Contains(b, "Высокое давление") {
		t.Errorf("поиск в норме: %.400s", b)
	}
	if !strings.Contains(b, `data-health data-op="OP1"`) {
		t.Error("нет островка карты здоровья")
	}
	if b := body("/analysis/health/cell?op=OP1&sub=Насосы%20(утечки)&b=1"); !strings.Contains(b, "Давление дренажа корпуса MD") {
		t.Errorf("клетка карты: %.300s", b)
	}
	if b := body("/analysis/chart?op=OP1&f=hp_min"); !strings.Contains(b, `data-analysis-chart`) {
		t.Errorf("график: %.300s", b)
	}
	if b := body("/?open=2026/11/01/x.shp"); !strings.Contains(b, `hx-get="/cycle?f=2026/11/01/x.shp"`) {
		t.Errorf("ссылка «открыть цикл» не передаётся в страницу циклов")
	}
	if b := body("/?open=2026/11/01/x.shp&feat=hp_min&feat=pump62_press_max"); !strings.Contains(b, `hx-get="/cycle?f=2026/11/01/x.shp&amp;feat=hp_min&amp;feat=pump62_press_max"`) {
		t.Errorf("признаки-виновники не передаются в карточку цикла: %.200s", b[strings.Index(b, "<main"):])
	}
}

func TestRelData(t *testing.T) {
	s := &Server{data: `\\recorder\shp`}
	for in, want := range map[string]string{
		`\\recorder\shp\2026\11\13\26.1_OP1.shp`: "2026/11/13/26.1_OP1.shp",
		`\\RECORDER\SHP\2026\x.shp`:              "2026/x.shp", // регистр в Windows не важен
		`D:\other\x.shp`:                         "",           // вне папки данных — без ссылки
		`\\recorder\shpOld\x.shp`:                "",           // похожее начало имени — не то же самое
	} {
		if got := s.relData(in); got != want {
			t.Errorf("relData(%q) = %q, ждали %q", in, got, want)
		}
	}
}

func TestHistogram(t *testing.T) {
	base := []float64{1, 1, 2, 2, 2, 3}
	tail := []float64{3, 3, 4, 4}
	h := histogram(base, tail, 3)
	if len(h.Edges) != 4 || len(h.Base) != 3 || len(h.Tail) != 3 {
		t.Fatalf("размеры: %+v", h)
	}
	// доли каждого набора в сумме — 1: сравниваем форму, а не число циклов
	var sb, st float64
	for k := range h.Base {
		sb += float64(h.Base[k])
		st += float64(h.Tail[k])
	}
	if math.Abs(sb-1) > 1e-12 || math.Abs(st-1) > 1e-12 {
		t.Errorf("суммы долей: %v %v", sb, st)
	}
	if float64(h.Base[0]) <= float64(h.Base[2]) || float64(h.Tail[2]) <= float64(h.Tail[0]) {
		t.Errorf("«было» слева, «стало» справа: base %v, tail %v", h.Base, h.Tail)
	}
	if one := histogram([]float64{5, 5}, []float64{5}, 30); len(one.Base) != 1 || one.Base[0] != 1 {
		t.Errorf("все одинаковые — один столбик: %+v", one)
	}
}

// TestJournalViews — «хвост» журнала: есть, только когда последний цикл старше паузы журнала.
func TestJournalViews(t *testing.T) {
	dir := t.TempDir()
	writeTable(t, dir)
	s, _ := New(t.TempDir(), time.UTC)
	s.EnableAnalysis(dir, dir)
	res, err := s.an.get("OP1")
	if err != nil {
		t.Fatal(err)
	}
	last := time.Date(2026, 11, 4, 22, 48, 0, 0, time.UTC)
	if _, _, tail := s.journalViews(res, last.Add(time.Hour)); tail != "" {
		t.Errorf("через час после цикла — не пауза, а «%s»", tail)
	}
	if _, _, tail := s.journalViews(res, last.Add(26*time.Hour+10*time.Minute)); tail != "Нет новых циклов с 04.11 22:48 (26 ч 10 мин назад)" {
		t.Errorf("хвост: «%s»", tail)
	}
}
