package features

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// КПС (колёсопрокатный стан, OP3): признаки с физическим смыслом.
//
// Все признаки с префиксом kps_ и считаются, только если в файле есть каналы КПС, — у пресса
// их нет, и его таблица не меняется. Всё, что связано со временем, — в секундах и миллисекундах,
// а не в отсчётах: у регистратора КПС менялся шаг записи (20 мс ↔ 5 мс), и признак «в отсчётах»
// у циклов с разным шагом был бы несравним.
//
// Названия осей — как в программе ПЛК (Schuler): MRF — подача главного валка, WRF — валков
// диска (левый/правый), SRS/SRF — наклонных валков (поворот/подача), LCRF/UCRF — нижнего/верхнего
// центрирующих, GRF — направляющих, Exc — эксцентриков. Расшифровка сокращений — моя по
// названиям каналов, УТОЧНИТЬ по документации стана.

// kpsLagWindow — окно поиска запаздывания золотника за командой, с: больше — уже не запаздывание.
const kpsLagWindow = 0.5

// kpsValve — пропорциональный клапан: команда (U) и обратная связь золотника (FU).
type kpsValve struct{ u, fu, key string }

// kpsAxis — ось подачи (гидроцилиндр с клапанами): усилие, давления поршень/шток, скорость.
type kpsAxis struct {
	key                  string
	force, head, ring, v string // "" — канала нет
	valves               []kpsValve
}

func axis(key, force, head, ring, v string, valves ...kpsValve) kpsAxis {
	return kpsAxis{key, force, head, ring, v, valves}
}

func valve(base, key string) kpsValve { return kpsValve{base + "_U", base + "_FU", key} }

// KpsAxes — 13 осей подачи. Экспортировано для узлов в анализе.
var KpsAxes = []kpsAxis{
	axis("mrf", "MRF_Force_F", "MRFPr_Head_p", "MRFPr_Ring_p", "MRF_Speed_v",
		valve("MRF_Prop_Y1.1", "y1_1"), valve("MRF_Prop_Y1.2", "y1_2")),
	axis("wrf_l", "WRFLeftForce_F", "WRFLeftPr_Head_p", "WRFLeftPr_Ring_p", "WRFLeftSpeed_v",
		valve("WRFLeft_Prop_Y11.1", "y11_1"), valve("WRFLeft_Prop_Y11.2", "y11_2")),
	axis("wrf_r", "WRFRightForce_F", "WRFRightPr_Head_p", "WRFRightPr_Ring_p", "WRFRightSpeed_v",
		valve("WRFRight_Prop_Y21.1", "y21_1"), valve("WRFRight_Prop_Y21.2", "y21_2")),
	axis("srs_l", "", "SRSLeftPr_Head_p", "SRSLeftPr_Ring_p", "SRSLeftSpeed_v", valve("SRSLeft_Prop_Y31", "y31")),
	axis("srs_r", "", "SRSRightPr_Head_p", "SRSRightPr_Ring_p", "SRSRightSpeed_v", valve("SRSRight_Prop_Y41", "y41")),
	axis("srf_l", "SRFLeftForce_F", "SRFLeftPr_Head_p", "SRFLeftPr_Ring_p", "SRFLeftSpeed_v",
		valve("SRFLeft_Prop_Y11.1", "y11_1"), valve("SRFLeft_Prop_Y11.2", "y11_2")),
	axis("srf_r", "SRFRightForce_F", "SRFRightPr_Head_p", "SRFRightPr_Ring_p", "SRFRightSpeed_v",
		valve("SRFRight_Prop_Y21.1", "y21_1"), valve("SRFRight_Prop_Y21.2", "y21_2")),
	axis("lcrf", "LCRF_Force_F", "LCRFPr_Head_p", "LCRFPr_Ring_p", "LCRFSpeed_v",
		valve("LCRF_Prop_Y11.1", "y11_1"), valve("LCRF_Prop_Y11.2", "y11_2")),
	axis("ucrf", "UCRF_Force_F", "UCRFPr_Head_p", "UCRFPr_Ring_p", "UCRFSpeed_v",
		valve("UCRF_Prop_Y21.1", "y21_1"), valve("UCRF_Prop_Y21.2", "y21_2")),
	axis("grf_l", "GRFLeftForce_F", "GRFLeftPr_Head_p", "GRFLeftPr_Ring_p", "GRFLeftSpeed_v", valve("GRFLeft_Prop_Y11.1", "y11_1")),
	axis("grf_r", "GRFRightForce_F", "GRFRightPr_Head_p", "GRFRightPr_Ring_p", "GRFRightSpeed_v", valve("GRFRight_Prop_Y21.1", "y21_1")),
	axis("exc_l", "ExcLeftForce_F", "ExcLeftPr_Head_p", "ExcLeftPr_Ring_p", "ExcLeftSpeed_v", valve("ExcLeft_Prop_Y31", "y31")),
	axis("exc_r", "ExcRightForce_F", "ExcRightPr_Head_p", "ExcRightPr_Ring_p", "ExcRightSpeed_v", valve("ExcRight_Prop_Y41", "y41")),
}

// kpsDrive — гидро/электропривод вращения валка.
type kpsDrive struct {
	key, rev, in, out, torque string
	valves                    []kpsValve
}

// KpsDrives — приводы: главного валка, валков диска, наклонных валков.
var KpsDrives = []kpsDrive{
	{"mrd", "MRD_Rev_n", "MRDPr_In_p", "MRDPr_Out_p", "", []kpsValve{valve("MRD_Prop_Y7", "y7")}},
	{"wrd_l", "WRDLeftRev_n", "", "", "WRDLeftTorque_%", nil},
	{"wrd_r", "WRD_RightRev_n", "", "", "WRDRightTorque_%", nil},
	{"srd_l", "SRDLeftRev_n", "SRDLeftPr_In_p", "SRDLeftPr_Out_p", "", []kpsValve{valve("SRDLeft_Prop_Y17", "y17")}},
	{"srd_r", "SRDRightRev_n", "SRDRightPr_In_p", "SRDRightPr_Out_p", "", []kpsValve{valve("SRDRight_Prop_Y27", "y27")}},
}

// kpsPump — регулируемый насос: задание и факт угла, давление, клапан регулятора.
type kpsPump struct{ key, set, act, press, vu, vfu string }

// KpsPumps — насосы гидростанции КПС.
var KpsPumps = []kpsPump{
	{"p1_1", "PumpSwivelP1_1_SetVal", "PumpSwivelP1_1_ActVal", "PumpPrP1_1_p", "PumpPropValveY1_1_U", "PumpPropValveY1_1_F_U"},
	{"p1_2", "PumpSwivelP1_2_SetVal", "PumpSwivelP1_2_ActVal", "PumpPrP1_2_p", "PumpPropValveY1_2_U", "PumpPropValveY1_2_F_U"},
	{"p2_1", "PumpSwivelP2_1_SetVal", "PumpSwivelP2_1_ActVal", "PumpPrP2_1_p", "", ""},
}

// KpsWheel — заданные размеры колеса (рецепт; в цикле постоянны): тип колеса.
var KpsWheel = []struct{ ch, key string }{
	{"Vorformdurchmesser DV", "dv"}, {"Fertigformdurchmesser DW", "dw"},
	{"Innendurchmesser Radkranz D1i", "d1i"}, {"Aussendurchmesser Nabe Dai", "dai"},
	{"Radkranzbreite Vorform BRV", "brv"}, {"Radkranzbreite Fertigform BRW", "brw"},
	{"Radstirnflaeche-Messkreis MKV", "mkv"}, {"Stegdicke an Nabe Vorform BSNV", "bsnv"},
	{"Abstand Radstirnfl. zu Steg SSV", "ssv"}, {"Stegdicke am Radkranz BSR", "bsr"},
	{"Laufflaechenwinkel Alpha", "alpha"},
}

// KpsRolls — валки со счётчиком ходов с замены и отметкой замены (дата, время — формат не расшифрован).
var KpsRolls = []struct{ prefix, date, key string }{
	{"MR", "MR_Date", "mr"}, {"WRL", "WRL_Date", "wrl"}, {"WRR", "WRR_Date", "wrr"},
	{"SRL", "SRL_Date", "srl"}, {"SRR", "SRR_Date", "srr"}, {"UCR", "UCR_Date", "ucr"},
	{"LCR", "LRC_Date", "lcr"}, // LRC — так в файле
	{"GRL", "GRL_Date", "grl"}, {"GRR", "GRR_Date", "grr"},
}

// IsKps — файл КПС (есть его характерные каналы).
func IsKps(f *shp.File) bool {
	_, a := f.Analog("Vorformdurchmesser DV")
	_, b := f.Analog("MRF_Force_F")
	return a && b
}

// Kps — признаки КПС. У файлов других машин ничего не делает.
func Kps(f *shp.File, dt float64, out Features) {
	if !IsKps(f) {
		return
	}
	maxLag := int(math.Round(kpsLagWindow / dt))
	ana := func(name string) []float64 {
		if name == "" {
			return nil
		}
		x, _ := f.Analog(name)
		return x
	}
	valves := func(prefix string, vs []kpsValve) {
		for _, v := range vs {
			u, fu := ana(v.u), ana(v.fu)
			if u == nil || fu == nil {
				continue
			}
			k := prefix + "_" + v.key
			d := sub(u, fu)
			out[k+"_err_rms"] = RMS(d)
			out[k+"_err_absmax"] = NanMax(absAll(d))
			out[k+"_lag_ms"] = LagSamples(u, fu, maxLag) * dt * 1000
		}
	}

	// --- оси подачи ---
	for _, a := range KpsAxes {
		k := "kps_" + a.key
		if x := ana(a.force); x != nil {
			out[k+"_force_max"] = NanMax(x)
		}
		if x := ana(a.head); x != nil {
			out[k+"_head_max"] = NanMax(x)
		}
		if x := ana(a.ring); x != nil {
			out[k+"_ring_max"] = NanMax(x)
		}
		if x := ana(a.v); x != nil {
			out[k+"_speed_absmax"] = NanMax(absAll(x))
		}
		valves(k, a.valves)
	}

	// --- приводы ---
	for _, d := range KpsDrives {
		k := "kps_" + d.key
		if x := ana(d.rev); x != nil {
			out[k+"_rev_max"] = NanMax(x)
		}
		if x := ana(d.in); x != nil {
			out[k+"_in_max"] = NanMax(x)
		}
		if x := ana(d.out); x != nil {
			out[k+"_out_max"] = NanMax(x)
		}
		if x := ana(d.torque); x != nil {
			out[k+"_torque_max"] = NanMax(x)
		}
		valves(k, d.valves)
	}

	// --- насосы: как у пресса — ошибка угла под нагрузкой (задание > 20%), запаздывание ---
	for _, p := range KpsPumps {
		k := "kps_" + p.key
		sp, ap := ana(p.set), ana(p.act)
		if sp != nil && ap != nil {
			d := sub(sp, ap)
			out[k+"_swivel_err_rms"] = RMS(d)
			var act []float64
			for i, s := range sp {
				if s > 20 {
					act = append(act, d[i])
				}
			}
			// «под нагрузкой» — не меньше 0,4 с такого задания (у пресса — 20 отсчётов по 5 мс = 0,1 с;
			// здесь в секундах, чтобы не зависеть от шага)
			if float64(len(act))*dt >= 0.4 {
				out[k+"_swivel_err_active"] = RMS(act)
			} else {
				out[k+"_swivel_err_active"] = math.NaN()
			}
			out[k+"_swivel_lag_ms"] = LagSamples(sp, ap, maxLag) * dt * 1000
			out[k+"_swivel_set_max"] = NanMax(sp)
			out[k+"_swivel_set_min"] = NanMin(sp) // «нижнее» задание: у P2.1 менялось 50 → 59, а максимум почти нет
		}
		if x := ana(p.press); x != nil {
			out[k+"_press_max"] = NanMax(x)
			out[k+"_press_min"] = NanMin(x)
		}
		if u, fu := ana(p.vu), ana(p.vfu); u != nil && fu != nil {
			out[k+"_valve_err_rms"] = RMS(sub(u, fu))
		}
	}

	// --- аккумулятор: просадка за цикл ---
	if x := ana("HydAccuPressure_p"); x != nil {
		lo, hi := NanMin(x), NanMax(x)
		out["kps_accu_min"], out["kps_accu_max"], out["kps_accu_drop"] = lo, hi, hi-lo
	}

	// --- колесо: заданные размеры (тип колеса) и диаметр в конце цикла ---
	for _, w := range KpsWheel {
		if x := ana(w.ch); x != nil {
			out["kps_wheel_"+w.key] = NanMedian(x)
		}
	}
	for _, c := range []struct{ ch, key string }{
		{"Diameter_Laser", "dia_laser_end"}, {"Diameter_CR", "dia_cr_end"}, {"DiffDiameter_Laser_CR", "dia_diff_end"},
	} {
		if x := ana(c.ch); x != nil {
			out["kps_"+c.key] = lastFinite(x)
		}
	}

	// --- валки: ходы с замены и отметка замены (по ней и сбросу счётчика ищутся замены) ---
	for _, r := range KpsRolls {
		if x := ana(r.prefix + "_StokesSinceChange"); x != nil {
			out["kps_roll_"+r.key+"_strokes"] = NanMax(x)
		}
		if x := ana(r.date); x != nil {
			t := ana(r.prefix + "_Time")
			if t != nil {
				out["kps_roll_"+r.key+"_change"] = NanMedian(x)*1e6 + NanMedian(t) // ДДДДД·10⁶ + ЧЧММСС — одна метка замены
			}
		}
	}

	// --- ПЛК: время цикла программы (рост = контроллер не успевает) ---
	if x := ana("S7 Actual Cycle Time (ms)"); x != nil {
		out["kps_plc_cycle_max"] = NanMax(x)
	}

	// --- фазы цикла: сколько секунд в каждом шаге программы (ActualStep) ---
	if x := ana("ActualStep"); x != nil {
		n := map[int]int{}
		for _, v := range x {
			if finite(v) && v == math.Trunc(v) && v >= 0 && v < 1000 {
				n[int(v)]++
			}
		}
		for step, c := range n {
			out[fmt.Sprintf("kps_step%02d_s", step)] = float64(c) * dt
		}
	}
}

// NanMedian — медиана без NaN (np.nanmedian); NaN, если значений нет.
func NanMedian(x []float64) float64 { return NanPercentile(x, 50) }

// lastFinite — последнее не-NaN значение.
func lastFinite(x []float64) float64 {
	for i := len(x) - 1; i >= 0; i-- {
		if finite(x[i]) {
			return x[i]
		}
	}
	return math.NaN()
}

// KpsAxisName — человеческое название оси по ключу (mrf, wrf_l…), для узлов и подписей.
func KpsAxisName(key string) string {
	side := ""
	switch {
	case strings.HasSuffix(key, "_l"):
		side, key = " левый", strings.TrimSuffix(key, "_l")
	case strings.HasSuffix(key, "_r"):
		side, key = " правый", strings.TrimSuffix(key, "_r")
	}
	names := map[string]string{
		"mrf": "Подача главного валка (MRF)", "wrf": "Подача валка диска (WRF)",
		"srs": "Поворот наклонного валка (SRS)", "srf": "Подача наклонного валка (SRF)",
		"lcrf": "Нижний центрирующий валок (LCRF)", "ucrf": "Верхний центрирующий валок (UCRF)",
		"grf": "Направляющий валок (GRF)", "exc": "Эксцентрик (Exc)",
		"mrd": "Привод главного валка (MRD)", "wrd": "Привод валка диска (WRD)", "srd": "Привод наклонного валка (SRD)",
	}
	if n, ok := names[key]; ok {
		return n + side
	}
	return key + side
}

// kpsSourceMap — признак КПС → каналы, из тех же списков, по которым признаки считаются
// (чтобы «открыть цикл» показал нужные сигналы и не разъехался с расчётом).
var kpsSourceMap = sync.OnceValue(func() map[string][]string {
	m := map[string][]string{}
	add := func(feat string, chs ...string) { m[feat] = chs }
	valves := func(k string, vs []kpsValve) {
		for _, v := range vs {
			for _, s := range []string{"_err_rms", "_err_absmax", "_lag_ms"} {
				add(k+"_"+v.key+s, v.u, v.fu)
			}
		}
	}
	for _, a := range KpsAxes {
		k := "kps_" + a.key
		add(k+"_force_max", a.force)
		add(k+"_head_max", a.head)
		add(k+"_ring_max", a.ring)
		add(k+"_speed_absmax", a.v)
		valves(k, a.valves)
	}
	for _, d := range KpsDrives {
		k := "kps_" + d.key
		add(k+"_rev_max", d.rev)
		add(k+"_in_max", d.in)
		add(k+"_out_max", d.out)
		add(k+"_torque_max", d.torque)
		valves(k, d.valves)
	}
	for _, p := range KpsPumps {
		k := "kps_" + p.key
		for _, s := range []string{"_swivel_err_rms", "_swivel_err_active", "_swivel_lag_ms", "_swivel_set_max", "_swivel_set_min"} {
			add(k+s, p.set, p.act)
		}
		add(k+"_press_max", p.press)
		add(k+"_press_min", p.press)
		add(k+"_valve_err_rms", p.vu, p.vfu)
	}
	for _, s := range []string{"min", "max", "drop"} {
		add("kps_accu_"+s, "HydAccuPressure_p")
	}
	for _, w := range KpsWheel {
		add("kps_wheel_"+w.key, w.ch)
	}
	add("kps_dia_laser_end", "Diameter_Laser")
	add("kps_dia_cr_end", "Diameter_CR")
	add("kps_dia_diff_end", "DiffDiameter_Laser_CR")
	for _, r := range KpsRolls {
		add("kps_roll_"+r.key+"_strokes", r.prefix+"_StokesSinceChange")
		add("kps_roll_"+r.key+"_change", r.date, r.prefix+"_Time")
	}
	add("kps_plc_cycle_max", "S7 Actual Cycle Time (ms)")
	for f, chs := range m { // пустые каналы («силы у оси нет») — не источник
		var keep []string
		for _, c := range chs {
			if c != "" {
				keep = append(keep, c)
			}
		}
		if len(keep) == 0 {
			delete(m, f)
		} else {
			m[f] = keep
		}
	}
	return m
})

// KpsSources — каналы признака КПС (nil — не признак КПС).
func KpsSources(feature string) []string {
	if chs, ok := kpsSourceMap()[feature]; ok {
		return chs
	}
	if strings.HasPrefix(feature, "kps_step") && strings.HasSuffix(feature, "_s") {
		return []string{"ActualStep"}
	}
	return nil
}

// Доступ к спискам КПС для анализа (узлы, группы, подписи) — из тех же списков, по которым
// считаются признаки, чтобы имена не разъехались.

// Key — ключ оси (mrf, wrf_l…).
func (a kpsAxis) Key() string { return a.key }

// ValveKeys — ключи клапанов оси (y1_1, y11_2…).
func (a kpsAxis) ValveKeys() []string { return valveKeys(a.valves) }

// HasForce — у оси есть датчик усилия.
func (a kpsAxis) HasForce() bool { return a.force != "" }

// Key — ключ привода (mrd, wrd_l…).
func (d kpsDrive) Key() string { return d.key }

// ValveKeys — ключи клапанов привода.
func (d kpsDrive) ValveKeys() []string { return valveKeys(d.valves) }

// Key — ключ насоса (p1_1…).
func (p kpsPump) Key() string { return p.key }

func valveKeys(vs []kpsValve) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.key
	}
	return out
}
