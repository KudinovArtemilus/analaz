package netio

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHost(t *testing.T) {
	for in, want := range map[string]string{
		`\\10.130.1.2\BufferShp\2026\a.shp`: "10.130.1.2",
		`//Srv/share/a.shp`:                 "srv",
		`D:\press_go\a.shp`:                 "",
		`/tmp/a.shp`:                        "",
	} {
		if got := Host(in); got != want {
			t.Errorf("Host(%q) = %q, ждали %q", in, got, want)
		}
	}
}

// TestRate — 1 МБ при 4 МБ/с: читается целиком и с паузами на ~0,25 с суммарно; второй
// файл с того же сервера ждёт, пока «пройдёт» первый (полоса общая).
func TestRate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.shp")
	data := bytes.Repeat([]byte{7}, 1_000_000)
	os.WriteFile(p, data, 0o644)
	var slept time.Duration
	clock := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	sleep = func(d time.Duration) { slept += d; clock = clock.Add(d) }
	now = func() time.Time { return clock }
	defer func() { sleep, now = time.Sleep, time.Now }()
	SetRate(func(h string) float64 {
		if h == "press10" {
			return 40 // своя скорость у сервера пресса
		}
		return 4
	})
	defer SetRate(func(string) float64 { return DefaultMBps })

	got, err := readLimited(p, "kps")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("прочитано %d байт, %v", len(got), err)
	}
	if slept < 200*time.Millisecond || slept > 260*time.Millisecond {
		t.Errorf("пауз %v, ждали ~0,25 с (1 МБ / 4 МБ/с)", slept)
	}
	before := slept
	readLimited(p, "kps")
	if d := slept - before; d < 200*time.Millisecond {
		t.Errorf("второй файл: пауз %v — полоса сервера должна быть общей", d)
	}
	before = slept
	readLimited(p, "press10") // другой сервер — своя полоса и своя скорость (40 МБ/с): ~0,025 с
	if d := slept - before; d > 40*time.Millisecond {
		t.Errorf("другой сервер: пауз %v, ждали ~0,025 с", d)
	}
}
