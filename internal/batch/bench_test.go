package batch

import (
	"os"
	"testing"

	"github.com/KudinovArtemilus/press-go/internal/features"
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// Полный путь одного файла: чтение с диска + разбор + все признаки.
//
//	$env:SHP_FILE = "полный\путь\к\файлу.shp"; go test -run x -bench . -benchmem ./internal/batch/
func BenchmarkReadAndCompute(b *testing.B) {
	path := os.Getenv("SHP_FILE")
	if path == "" {
		b.Skip("укажите SHP_FILE")
	}
	for b.Loop() {
		f, err := shp.Read(path)
		if err != nil {
			b.Fatal(err)
		}
		features.Compute(f)
	}
}
