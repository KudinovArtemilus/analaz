package shp

// Пресс 5 (OP4, папка Datalogger) — пресс того же семейства, что пресс 10, но регистратор
// называет многие каналы иначе: «slide total force [kN]» вместо «slide:total force [kN]»,
// «hyd:03-B135.1 hp [bar]» вместо «hyd:high pressure [bar]»… Признаки пресса 10 ищут каналы по
// именам пресса 10, поэтому у файла пресса 5 под этими именами доступны его каналы (Analog,
// AnalogIndex). Так усилие, работа, ход, гидростанция, привод считаются тем же кодом и под теми же
// именами признаков, с теми же описаниями и узлами. Исходные имена тоже остаются — сырые признаки
// a000_… и графики идут по ним.
//
// Соответствие — по смыслу канала (название, единицы, диапазон на 10 циклах 20.11.2026).
// Не сопоставлено то, в чём нет уверенности: 12Y008.1 (у пресса 5 задание есть, а обратная связь
// «12Y108_1 PinF» всё время 0 — это надо проверить на прессе, а не прятать).

// press5Marker — канал, который есть только у пресса 5: по нему файл узнаётся.
const press5Marker = "slide: RV2 accu pressure[bar]"

// press5Aliases — имя канала у пресса 5 → имя того же по смыслу канала у пресса 10.
var press5Aliases = map[string]string{
	"slide total force [kN]":          "slide:total force [kN]",
	"hyd:03-B135.1 hp [bar]":          "hyd:high pressure [bar]",
	"hyd:03-B235 lp [bar]":            "hyd:low pressure [bar]",
	"slide:12Y102 SetVal_adjust pres": "slide:12Y102 set value [V]",
	"slide:12Y102 PinF[V]":            "slide:12Y102 pistion pos.[V]",
	"SCu:31Y102 SetVal_adjust pressu": "SCu:31Y102: set value [V]",
	"SCu:31Y102 PinF[V]":              "SCu:31Y102:piston pos.[V]",
	"drives:set value [1/min]":        "Oildr:FrequenceConverter NSoll",
	"drives:act value [1/min]":        "Oildr:FrequenceConverter NIst",
	`SCu":actual force[kN]`:           "SCu:netto force [kN]",
	"BEj:head side actual force[kN]":  "BEj:netto force [kN]",
	"BEj:actual speed[mm/s]":          "bed ejector speed [mm/s]",
}

// press5Absent — каналы, которых у пресса 5 нет: во всех записанных циклах ровно 0 (насосов 4–6
// нет, тестовые датчики, подогрев масла не используется, два из четырёх энкодеров ползуна,
// счётчики оборотов моторов). Признаков по ним не считаем — иначе анализ видел бы в них
// «пропавший сигнал», хотя ломаться там нечему. Сами каналы в файле и на графике остаются.
var press5Absent = map[string]bool{
	"slide:encoder front right [mm]": true, "slide:encoder rear left [mm]": true,
	"slide:set position down[mm]": true, "BEj:set position down[mm]": true,
	"10-B070.1 press.sensor test 1[b": true, "10-B070.2 press.sensor test 2[b": true,
	"10-B070.3 press.sensor test 3[b": true, "10-B070.4 press.sensor test 4[b": true,
	"Oildr:Motor1Turns": true, "Oildr:Motor2Turns": true, "Oildr:Motor3Turns": true,
	"PFG_OIL_HEATING.SetFlow": true,
}

func init() {
	for _, a := range []string{"4", "5", "6"} {
		for _, b := range []string{"1", "2"} {
			for _, s := range []string{"svValve", "avValve", "actPressure", "setPivoting", "actPivoting"} {
				press5Absent["Pump "+a+"."+b+" "+s] = true
			}
		}
	}
}

// IsPress5 — файл пресса 5.
func (f *File) IsPress5() bool { return f.press5 }

// Absent — канала у этой машины нет (у пресса 5 — см. press5Absent); у других машин — false.
func (f *File) Absent(name string) bool { return f.press5 && press5Absent[name] }

// addAliases — у файла пресса 5 сделать его каналы доступными под именами пресса 10.
// Имя, которое в файле уже есть, не перекрывается.
func (f *File) addAliases() {
	if _, ok := f.anaIdx[press5Marker]; !ok {
		return
	}
	f.press5 = true
	for own, std := range press5Aliases {
		j, ok := f.anaIdx[own]
		if !ok {
			continue
		}
		if _, taken := f.anaIdx[std]; !taken {
			f.anaIdx[std] = j
		}
	}
}
