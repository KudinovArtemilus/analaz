package analyze

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func TestHumanMatchesPython(t *testing.T) {
	for _, c := range []struct{ col, sub, human string }{
		{"hp_drop", "Гидростанция", "Просадка высокого давления за цикл, бар"},
		{"pump41_pivot_lag", "Насосы (регулирование)", "Насос 4.1: запаздывание угла, мс"},
		{"vCCylL_Y22_2_err_absmax", "Сервоклапаны", "Клапан CCylL_Y22_2: макс. ошибка"},
		{"LCylRight_posdev_rms", "Цилиндры CCyl/LCyl", "Нижний цил. прав.: ошибка позиционирования (RMS)"},
		{"temp_bottom_max", "Гидростанция", "Температура масла, нижний бак, °C, максимум"},
		{"temp_bottom_mean", "Гидростанция", "Температура масла, нижний бак, °C"}, // явная запись не перетёрта setdefault
		{"a017_HP_Pressure__max", "Сырые аналоговые", "HP Pressure [max]"},
		{"d003_Pump_1_On__on", "Дискретные", "Pump 1 On [доля вкл.]"},
		{"d003_Pump_1_On__n", "Дискретные", "Pump 1 On [кол-во включений]"},
		{"shp_breaks", "Прочее", "shp_breaks"},
		{"dt_ms", "Дискретные", "dt_ms"}, // так в Python: «начинается на d» — грубое правило, но переносим как есть
	} {
		if s, h := Subsystem(c.col), Human(c.col); s != c.sub || h != c.human {
			t.Errorf("%s: получили (%q, %q), ждали (%q, %q)", c.col, s, h, c.sub, c.human)
		}
	}
}

// Полная сверка со словарём Python: HUMAN_TSV=файл, выгруженный из analyze.py (колонка, subsystem, human).
func TestHumanFullPython(t *testing.T) {
	path := os.Getenv("HUMAN_TSV")
	if path == "" {
		t.Skip("HUMAN_TSV не задан")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }() // файл только читаем — ошибка закрытия ничего не теряет

	sc, n := bufio.NewScanner(f), 0
	for sc.Scan() {
		p := strings.Split(sc.Text(), "\t")
		if len(p) < 3 {
			t.Errorf("строка %d: ждали 3 колонки через Tab, получили %q", n+1, sc.Text())
			continue
		}
		if s, h := Subsystem(p[0]), Human(p[0]); s != p[1] || h != p[2] {
			t.Errorf("%s: go (%q, %q), python (%q, %q)", p[0], s, h, p[1], p[2])
		}
		n++
	}
	if err := sc.Err(); err != nil { // без этого оборванное чтение выглядело бы как «всё сверено»
		t.Fatal(err)
	}
	t.Logf("сверено %d колонок", n)
}
