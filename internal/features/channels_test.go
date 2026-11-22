package features

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

func TestSources(t *testing.T) {
	for ft, want := range map[string][]string{
		"vCCylR_Y22_1_err_absmax": {"CCyl Right Prop Y22.1 U", "CCyl Right Prop Y22.1 PinF"},
		"LCylRight_force_absmax":  {"LCyl Right Force"},
		"CCylLeft_posdev_rms":     {"CCyl Left lvPosCtrlDev"},
		"pump62_press_max":        {"Pump 6.2 actPressure"},
		"pump41_pivot_lag":        {"Pump 4.1 setPivoting", "Pump 4.1 actPivoting"},
		"casedrain_md5_p95":       {"Oildr: MD5 Casedrain Pressure"},
		"dieclamp_c2_min":         {"DieCl: reload press. circuit 2"},
		"hp_recover_s":            {chHighPressure, chSlideForce},
		"stroke_mm":               {chSlidePos},
		"dieclamp_c2_p95":         nil, // такого окончания у зажима нет
		"pump71_press_max":        nil, // насоса 7.1 нет
		"cycle_s":                 nil, // служебный, не из канала
	} {
		if got := Sources(ft); !slices.Equal(got, want) {
			t.Errorf("Sources(%q) = %q, ждали %q", ft, got, want)
		}
	}
}

// TestSourcesCoverAll — на настоящем файле: у каждого признака, который считается
// из каналов, источник найден и такой канал в файле есть.
//
//	SHP_FILE=путь\к\файлу.shp go test -run SourcesCoverAll ./internal/features/
func TestSourcesCoverAll(t *testing.T) {
	path := os.Getenv("SHP_FILE")
	if path == "" {
		t.Skip("укажите SHP_FILE=путь\\к\\файлу.shp")
	}
	f, err := shp.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	// служебные признаки: из имени файла, структуры файла и сырые дискретные
	service := regexp.MustCompile(`^(n_samples|cycle_s|dt_ms|fn_.*|shp_.*|d\d{3}_.*)$`)
	n := 0
	for ft := range Compute(f) {
		if service.MatchString(ft) {
			continue
		}
		n++
		if len(ChannelIndexes(f, []string{ft})) == 0 {
			t.Errorf("признак %s: не нашёл канал (Sources = %q)", ft, Sources(ft))
		}
	}
	t.Logf("проверено признаков: %d", n)
}

func TestChannelIndexesRawNameCheck(t *testing.T) {
	f := &shp.File{AnaNames: []string{"slide:total force [kN]", "hyd:low pressure [bar]"}}
	if got := ChannelIndexes(f, []string{"a001_hyd_low_pressure_bar__max", "a001_slide_total_force_kN__max", "a009_x__max"}); !slices.Equal(got, []int{1}) {
		t.Errorf("сырые признаки: %v, ждали [1] (номер и имя должны совпасть)", got)
	}
}

// TestLocateMatchesFeature — на настоящем файле: значение в найденной точке совпадает
// со значением признака бит в бит (для максимумов, минимумов и «макс. по модулю»).
// Так проверено, что отметка стоит именно там, откуда взялось число в таблице.
func TestLocateMatchesFeature(t *testing.T) {
	path := os.Getenv("SHP_FILE")
	if path == "" {
		t.Skip("укажите SHP_FILE=путь\\к\\файлу.shp")
	}
	f, err := shp.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	exact, marked := 0, 0
	for ft, v := range Compute(f) {
		m, ok := Locate(f, ft)
		if !ok {
			continue
		}
		marked++
		if ft == "hp_drop" || m.Kind != "min" && m.Kind != "max" && !strings.Contains(ft, "absmax") {
			continue // RMS и просадка: точка — пик, а не само значение признака
		}
		exact++
		if m.Value != v {
			t.Errorf("%s: в точке %d значение %v, признак %v", ft, m.At, m.Value, v)
		}
	}
	t.Logf("отметок: %d, из них сверено бит в бит: %d", marked, exact)
}

func TestPair(t *testing.T) {
	k1, r1, ok1 := Pair("CCyl Right Prop Y22.1 U")
	k2, r2, ok2 := Pair("CCyl Right Prop Y22.1 PinF")
	if !ok1 || !ok2 || k1 != k2 || r1 != "set" || r2 != "act" {
		t.Errorf("клапан CCylR: %q %q %v / %q %q %v", k1, r1, ok1, k2, r2, ok2)
	}
	if k, r, ok := Pair("Pump 6.2 actPivoting"); !ok || k != "Pump 6.2 pivot" || r != "act" {
		t.Errorf("насос 6.2: %q %q %v", k, r, ok)
	}
	if _, _, ok := Pair("slide:total force [kN]"); ok {
		t.Error("у усилия нет пары")
	}
}

// Каждый признак ошибки «задание − факт» должен соответствовать паре: иначе отметка
// встанет на два отдельных графика вместо одного общего.
func TestPairsMatchSources(t *testing.T) {
	for _, s := range servos {
		src := Sources(s.key + "_err_rms")
		k1, _, _ := Pair(src[0])
		k2, _, _ := Pair(src[1])
		if k1 == "" || k1 != k2 {
			t.Errorf("%s: каналы %q не в одной паре", s.key, src)
		}
	}
}
