package archive

import (
	"fmt"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/analyze"
	"github.com/KudinovArtemilus/press-go/internal/batch"
	"github.com/KudinovArtemilus/press-go/internal/features"
)

// testDataset — рабочие циклы f00…fNN с die_strokes, через ту же таблицу, что пишет extract.
func testDataset(times []time.Time, ds []float64) *analyze.Dataset {
	tb := batch.NewTable()
	for i, t := range times {
		tb.Add(fmt.Sprintf("f%02d", i), "OP1", t.Format("2006-01-02 15:04:05"), features.Features{"die_strokes": ds[i], "force_max_kN": 25000})
	}
	d, err := analyze.FromTable(tb)
	if err != nil {
		panic(err)
	}
	return d
}
