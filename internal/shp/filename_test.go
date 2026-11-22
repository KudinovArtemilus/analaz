package shp

import (
	"testing"
	"time"
)

func TestParseName(t *testing.T) {
	plant := time.FixedZone("UTC+5", 5*3600)

	// Табличный тест — обычный стиль в Go: список случаев и один цикл проверки.
	cases := []struct {
		in   string
		want Name
	}{
		{
			in: `D:\press\2026\11\18\26.176613.067890_OP1_20261118_045619.shp`,
			want: Name{
				Op: "OP1", Prefix: 26, Piece: 176613, Heat: 67890, HasIDs: true,
				Time: time.Date(2026, 11, 18, 4, 56, 19, 0, plant),
			},
		},
		{
			in: "26.176613.067890_OP2_20261118_045659.shp",
			want: Name{
				Op: "OP2", Prefix: 26, Piece: 176613, Heat: 67890, HasIDs: true,
				Time: time.Date(2026, 11, 18, 4, 56, 59, 0, plant),
			},
		},
		{ // старый формат имени из README: без номеров
			in:   "OP1_20260928_000825.shp",
			want: Name{Op: "OP1", Time: time.Date(2026, 9, 28, 0, 8, 25, 0, plant)},
		},
		{ // ничего не распознано
			in:   "old10.shp",
			want: Name{},
		},
		{ // невозможная дата: время не ставим, но остальное разбираем
			in:   "26.1.2_OP1_20261340_250000.shp",
			want: Name{Op: "OP1", Prefix: 26, Piece: 1, Heat: 2, HasIDs: true},
		},
	}
	for _, c := range cases {
		got := ParseName(c.in, plant)
		if got.Op != c.want.Op || got.Prefix != c.want.Prefix || got.Piece != c.want.Piece ||
			got.Heat != c.want.Heat || got.HasIDs != c.want.HasIDs || !got.Time.Equal(c.want.Time) {
			t.Errorf("%s:\n got  %+v\n want %+v", c.in, got, c.want)
		}
	}
}

func TestStartTimeFallback(t *testing.T) {
	plant := time.FixedZone("UTC+5", 5*3600)
	hdr := time.Date(2026, 11, 17, 23, 56, 9, 0, time.UTC)

	f := &File{Path: "old10.shp", FileTime: hdr} // в имени времени нет → берём заголовок
	if got := StartTime(f, plant); !got.Equal(hdr) {
		t.Errorf("ждали время из заголовка %v, получили %v", hdr, got)
	}
	f.Path = "26.176613.067890_OP1_20261118_045619.shp" // есть в имени → имя главнее
	if got := StartTime(f, plant); got.Hour() != 4 || got.Minute() != 56 || got.Second() != 19 {
		t.Errorf("ждали 04:56:19 из имени, получили %v", got)
	}
}
