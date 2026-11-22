package analyze

import (
	"fmt"
	"strings"

	"github.com/KudinovArtemilus/press-go/internal/features"
)

// КПС (колёсопрокатный стан, OP3): узлы для светофора, названия признаков, группы однотипных узлов.
//
// Узлы КПС лежат в том же списке Units(), что и узлы пресса: у таблицы пресса нет признаков kps_*,
// у таблицы КПС — признаков пресса, а узел без данных диагноз пропускает. Так каждая машина
// видит только свои узлы, и диагноз пресса не меняется.
//
// Усилия, давления и момент зависят от того, какое колесо катают (DV/DW): при общей норме смена
// колеса выглядела бы как поломка. Поэтому они — показатели «внутри колеса» (PerRegime): диагноз
// оценивает их, только когда анализ считается по одному колесу (OP3@dv860-dw895). Ошибка и
// запаздывание клапанов и регуляторов, аккумулятор, ПЛК от колеса почти не зависят — они всегда.

// kpsValveLabel — «клапан Y11.1» по ключу y11_1.
func kpsValveLabel(key string) string {
	return "клапан " + strings.ToUpper(strings.Replace(key, "_", ".", 1))
}

// kpsTwin — ключ напарника: wrf_l ↔ wrf_r, p1_1 ↔ p1_2 ("" — напарника нет).
func kpsTwin(key string) string {
	switch {
	case strings.HasSuffix(key, "_l"):
		return strings.TrimSuffix(key, "_l") + "_r"
	case strings.HasSuffix(key, "_r"):
		return strings.TrimSuffix(key, "_r") + "_l"
	case key == "p1_1":
		return "p1_2"
	case key == "p1_2":
		return "p1_1"
	}
	return ""
}

// kpsValvesOf — клапаны оси или привода по ключу (для напарника: у левого Y11.x, у правого Y21.x).
func kpsValvesOf(key string) []string {
	for _, a := range features.KpsAxes {
		if a.Key() == key {
			return a.ValveKeys()
		}
	}
	for _, d := range features.KpsDrives {
		if d.Key() == key {
			return d.ValveKeys()
		}
	}
	return nil
}

// KpsUnits — узлы КПС.
func KpsUnits() []Unit {
	var u []Unit
	// показатели «внутри колеса» по ключу оси/привода
	extra := map[string][]Indicator{}
	for _, a := range features.KpsAxes {
		k := "kps_" + a.Key()
		if a.HasForce() {
			extra[a.Key()] = append(extra[a.Key()], indRegime(k+"_force_max", "усилие, макс. (внутри колеса)", "both", 0.15, 0.30, ""))
		}
		extra[a.Key()] = append(extra[a.Key()], indRegime(k+"_head_max", "давление в поршневой полости, макс. (внутри колеса)", "both", 0.15, 0.30, "бар"))
	}
	for _, d := range features.KpsDrives {
		if len(d.ValveKeys()) == 0 { // WRD: клапанов нет — узел по моменту
			extra[d.Key()] = append(extra[d.Key()], indRegime("kps_"+d.Key()+"_torque_max", "момент, макс. (внутри колеса), %", "up", 0.20, 0.50, ""))
		}
	}
	valveUnit := func(key, group string, valves []string) {
		if len(valves) == 0 && len(extra[key]) == 0 {
			return
		}
		k := "kps_" + key
		un := Unit{Name: features.KpsAxisName(key), Group: group}
		un.Ind = append(un.Ind, extra[key]...) // усилие, давление, момент — внутри колеса
		for _, v := range valves {
			un.Ind = append(un.Ind,
				ind(k+"_"+v+"_err_rms", kpsValveLabel(v)+": ошибка команда–золотник (RMS)", "up", 0.30, 0.80, ""),
				ind(k+"_"+v+"_lag_ms", kpsValveLabel(v)+": запаздывание золотника, мс", "up", 0.50, 1.00, ""))
		}
		if len(valves) == 0 { // привод без клапанов (WRD): только момент
			un.Cause = "Привод тяжелее крутит валок при том же колесе: подшипники, редуктор, муфта или сам валок (износ, налипание)."
			un.Check = "Ток и температура двигателя, вибрация и шум редуктора, состояние валка. Сравнить с приводом другой стороны."
			if tw := kpsTwin(key); tw != "" {
				un.PairFeat, un.PairOther, un.PairName = "kps_"+key+"_torque_max", "kps_"+tw+"_torque_max", features.KpsAxisName(tw)
			}
			u = append(u, un)
			return
		}
		un.SpikeFeat = k + "_" + valves[0] + "_err_absmax"
		un.SpikeLabel = "выбросы ошибки " + kpsValveLabel(valves[0]) + " (золотник не дошёл / рывок)"
		// напарник: тот же по счёту клапан у оси другой стороны
		if tw := kpsTwin(key); tw != "" {
			if tv := kpsValvesOf(tw); len(tv) > 0 {
				un.PairFeat = k + "_" + valves[0] + "_err_rms"
				un.PairOther = "kps_" + tw + "_" + tv[0] + "_err_rms"
				un.PairName = features.KpsAxisName(tw)
			}
		}
		un.Cause = "Золотник пропорционального клапана хуже следует за командой: загрязнение масла, износ золотника/втулки, " +
			"датчик положения золотника или его питание."
		un.Check = "Фильтр пилотной линии и чистота масла (ISO 4406); разъём и кабель датчика положения золотника, питание клапана. " +
			"Сравнить с осью другой стороны. Если выбросы учащаются — клапан на стенд/замену в ближайший ППР."
		u = append(u, un)
	}
	for _, a := range features.KpsAxes {
		valveUnit(a.Key(), "КПС: оси подачи", a.ValveKeys())
	}
	for _, d := range features.KpsDrives {
		valveUnit(d.Key(), "КПС: приводы валков", d.ValveKeys())
	}

	for _, p := range features.KpsPumps {
		k := "kps_" + p.Key()
		un := Unit{
			Name: "Насос КПС " + strings.ToUpper(strings.Replace(p.Key(), "_", ".", 1)), Group: "КПС: насосы",
			Ind: []Indicator{
				ind(k+"_swivel_err_active", "ошибка угла наклона под нагрузкой", "up", 0.25, 0.60, ""),
				ind(k+"_swivel_lag_ms", "запаздывание угла, мс", "up", 0.30, 0.80, ""),
				ind(k+"_valve_err_rms", "ошибка клапана регулятора", "up", 0.30, 0.80, ""),
				indRegime(k+"_press_max", "давление насоса, макс. (внутри колеса)", "both", 0.10, 0.20, "бар"),
			},
			Cause: "Насос не успевает за заданием угла: загрязнение/износ пропорционального клапана регулятора, люфт люльки, давление управления.",
			Check: "Записать задание/факт угла на осциллограф. Фильтр линии управления, чистота масла (ISO 4406). Сравнить с соседним насосом.",
		}
		if tw := kpsTwin(p.Key()); tw != "" {
			un.PairFeat, un.PairOther = k+"_swivel_err_active", "kps_"+tw+"_swivel_err_active"
			un.PairName = "насос " + strings.ToUpper(strings.Replace(tw, "_", ".", 1))
		}
		u = append(u, un)
	}

	u = append(u, Unit{
		Name: "Аккумулятор КПС", Group: "КПС: гидростанция",
		Ind: []Indicator{
			ind("kps_accu_min", "давление аккумулятора, минимум за цикл", "down", 0.05, 0.10, "бар"),
			ind("kps_accu_drop", "просадка аккумулятора за цикл", "up", 0.25, 0.60, "бар"),
		},
		Cause: "Падает минимум или растёт просадка — потеря азота (предзаправки), утечка или насосы не успевают подкачивать.",
		Check: "Давление предзаправки азотом, герметичность; сопоставить с насосами КПС (выше).",
	}, Unit{
		Name: "ПЛК КПС: время цикла программы", Group: "КПС: управление",
		Ind:   []Indicator{ind("kps_plc_cycle_max", "время цикла ПЛК, максимум, мс", "up", 0.30, 1.00, "мс")},
		Cause: "Контроллер не успевает: перегрузка программы, связь (опрос периферии), диагностика.",
		Check: "Диагностический буфер ПЛК, загрузка цикла, состояние сети/периферии.",
	})
	return u
}

// kpsDescs — названия и подсистемы признаков КПС (для карты здоровья, подписей, причин аномалий).
func kpsDescs() map[string]Desc {
	d := map[string]Desc{}
	add := func(f, sub, text, why string) { d[f] = Desc{sub, text, why} }
	valves := func(k, sub string, keys []string) {
		for _, v := range keys {
			l := kpsValveLabel(v)
			add(k+"_"+v+"_err_rms", sub, sub+": "+l+", ошибка команда–золотник (RMS)", "загрязнение/износ золотника, датчик положения")
			add(k+"_"+v+"_err_absmax", sub, sub+": "+l+", ошибка команда–золотник (макс.)", "рывок / золотник не дошёл")
			add(k+"_"+v+"_lag_ms", sub, sub+": "+l+", запаздывание золотника, мс", "медленный отклик клапана")
		}
	}
	for _, a := range features.KpsAxes {
		k, sub := "kps_"+a.Key(), features.KpsAxisName(a.Key())
		add(k+"_force_max", sub, sub+": усилие, макс.", "другое колесо / сопротивление прокатке")
		add(k+"_head_max", sub, sub+": давление в поршневой полости, макс., бар", "другое колесо / утечки")
		add(k+"_ring_max", sub, sub+": давление в штоковой полости, макс., бар", "")
		add(k+"_speed_absmax", sub, sub+": скорость, макс.", "")
		valves(k, sub, a.ValveKeys())
	}
	for _, dr := range features.KpsDrives {
		k, sub := "kps_"+dr.Key(), features.KpsAxisName(dr.Key())
		add(k+"_rev_max", sub, sub+": обороты, макс.", "")
		add(k+"_in_max", sub, sub+": давление на входе, макс., бар", "")
		add(k+"_out_max", sub, sub+": давление на выходе, макс., бар", "")
		add(k+"_torque_max", sub, sub+": момент, макс., %", "тяжелее крутится / другое колесо")
		valves(k, sub, dr.ValveKeys())
	}
	for _, p := range features.KpsPumps {
		k := "kps_" + p.Key()
		sub := "Насос КПС " + strings.ToUpper(strings.Replace(p.Key(), "_", ".", 1))
		add(k+"_swivel_err_rms", sub, sub+": ошибка угла (RMS)", "")
		add(k+"_swivel_err_active", sub, sub+": ошибка угла под нагрузкой", "износ/загрязнение регулятора")
		add(k+"_swivel_lag_ms", sub, sub+": запаздывание угла, мс", "регулятор медленнее")
		add(k+"_swivel_set_max", sub, sub+": задание угла, макс.", "смена настройки")
		add(k+"_press_max", sub, sub+": давление, макс., бар", "")
		add(k+"_press_min", sub, sub+": давление, мин., бар", "")
		add(k+"_valve_err_rms", sub, sub+": ошибка клапана регулятора", "загрязнение/износ клапана регулятора")
	}
	for _, s := range [][3]string{{"min", "минимум", "азот / утечка"}, {"max", "максимум", ""}, {"drop", "просадка за цикл", "насосы не успевают, азот"}} {
		add("kps_accu_"+s[0], "КПС: гидростанция", "Аккумулятор КПС: "+s[1]+", бар", s[2])
	}
	add("kps_dia_laser_end", "КПС: колесо (факт)", "Диаметр по лазеру в конце цикла", "")
	add("kps_dia_cr_end", "КПС: колесо (факт)", "Диаметр по CR в конце цикла", "")
	add("kps_dia_diff_end", "КПС: колесо (факт)", "Разница диаметров лазер − CR в конце цикла", "калибровка измерителей")
	add("kps_plc_cycle_max", "КПС: управление", "ПЛК КПС: время цикла программы, макс., мс", "контроллер не успевает")
	return d
}

func init() {
	for k, v := range kpsDescs() {
		if _, ok := Descs[k]; !ok { // словарь пресса не трогаем
			Descs[k] = v
		}
	}
}

// KpsStepDesc — подпись признака длительности шага программы (kps_step13_s → «КПС: шаг программы 13, с»).
func kpsStepDesc(col string) (Desc, bool) {
	var n int
	if _, err := fmt.Sscanf(col, "kps_step%d_s", &n); err != nil || !strings.HasSuffix(col, "_s") {
		return Desc{}, false
	}
	return Desc{"КПС: шаги программы", fmt.Sprintf("КПС: длительность шага программы %d, с", n), "дольше = ожидание/медленнее движение"}, true
}

// kpsGroups — группы однотипных узлов КПС для графиков «как идут рядом».
func kpsGroups() []Group {
	var errs, lags, forces, drv []string
	for _, a := range features.KpsAxes {
		for _, v := range a.ValveKeys() {
			errs = append(errs, "kps_"+a.Key()+"_"+v+"_err_rms")
			lags = append(lags, "kps_"+a.Key()+"_"+v+"_lag_ms")
		}
		if a.HasForce() {
			forces = append(forces, "kps_"+a.Key()+"_force_max")
		}
	}
	for _, d := range features.KpsDrives {
		for _, v := range d.ValveKeys() {
			drv = append(drv, "kps_"+d.Key()+"_"+v+"_err_rms")
		}
	}
	var sw, swl []string
	for _, p := range features.KpsPumps {
		sw = append(sw, "kps_"+p.Key()+"_swivel_err_active")
		swl = append(swl, "kps_"+p.Key()+"_swivel_lag_ms")
	}
	return []Group{
		{"КПС: клапаны осей — ошибка команда–золотник (RMS)", "Рост = загрязнение масла, износ золотника. Левая и правая оси должны идти рядом.", errs},
		{"КПС: клапаны осей — запаздывание, мс", "Рост = медленный отклик клапана.", lags},
		{"КПС: клапаны приводов — ошибка команда–золотник (RMS)", "", drv},
		{"КПС: насосы — ошибка угла под нагрузкой", "Рост = износ/загрязнение регулятора.", sw},
		{"КПС: насосы — запаздывание угла, мс", "", swl},
		{"КПС: усилия осей, макс.", "Зависят от колеса (DV/DW): сравнивать внутри одного колеса.", forces},
		{"КПС: аккумулятор, бар", "Падение минимума / рост просадки = азот, утечка.", []string{"kps_accu_min", "kps_accu_drop"}},
	}
}
