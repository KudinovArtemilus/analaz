package features

import (
	"os"
	"testing"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// go test -bench . -benchmem ./internal/features/   (нужен файл в переменной SHP_FILE)
func BenchmarkCompute(b *testing.B) {
	path := os.Getenv("SHP_FILE")
	if path == "" {
		b.Skip("укажите SHP_FILE=путь\\к\\файлу.shp")
	}
	f, err := shp.Read(path)
	if err != nil {
		b.Fatal(err)
	}
	// b.Loop (Go 1.24+) сам сбрасывает таймер после подготовки выше
	// и не даёт компилятору выкинуть вызов как «ненужный».
	for b.Loop() {
		Compute(f)
	}
}
