// feat — признаки одного цикла и сверка с Python.
//
//	go run ./cmd/feat файл.shp                        # напечатать признаки Go
//	go run ./cmd/feat -compare py_feat.txt файл.shp   # сверить с выводом dump_features.py
//
// Сверка показывает три вещи: что совпало, что расходится и сколько признаков Python
// ещё не перенесено. Последнее число — это «сколько работы осталось».
package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/KudinovArtemilus/press-go/internal/features"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

func main() {
	compare := flag.String("compare", "", "файл от dump_features.py (строки «имя значение»)")
	tol := flag.Float64("tol", 1e-9, "допустимое относительное расхождение")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "использование: feat [-compare py_feat.txt] [-tol 1e-9] файл.shp")
		os.Exit(2)
	}
	f, err := shp.Read(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
	got := features.Compute(f)

	if *compare == "" {
		for _, k := range sortedKeys(got) {
			fmt.Printf("%-24s %.10g\n", k, got[k])
		}
		return
	}

	py, err := readPy(*compare)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
	var ok, bad, missing int
	for _, k := range sortedKeys(got) {
		want, has := py[k]
		if !has {
			fmt.Printf("  ?  %-24s есть в Go (%.6g), нет в Python\n", k, got[k])
			continue
		}
		if same(got[k], want, *tol) {
			ok++
			fmt.Printf("  ✓  %-24s %.10g\n", k, got[k])
		} else {
			bad++
			fmt.Printf("  ✗  %-24s Go %.10g  Python %.10g\n", k, got[k], want)
		}
	}
	for k := range py {
		if _, has := got[k]; !has {
			missing++
		}
	}
	fmt.Printf("\nСовпало %d, расходится %d. Ещё не перенесено из Python: %d из %d признаков.\n",
		ok, bad, missing, len(py))
	if bad > 0 {
		os.Exit(1)
	}
}

// same — равны с относительным допуском; NaN равен NaN (оба «признак не посчитан»).
// Точного равенства не ждём: numpy складывает попарно (pairwise), Go — по порядку,
// и последние знаки могут отличаться.
func same(a, b, tol float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Abs(a-b) <= tol*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// readPy читает строки «имя значение»; нечисловые значения (file, t_start, op) пропускает.
func readPy(path string) (map[string]float64, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	m := map[string]float64{}
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) != 2 {
			continue
		}
		if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
			m[parts[0]] = v
		}
	}
	return m, sc.Err()
}

func sortedKeys(m features.Features) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
