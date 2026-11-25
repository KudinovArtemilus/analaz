// Package netio — чтение файлов с сетевых папок с ограничением скорости.
//
// Регистраторы машин (КПС, пресс 5…) отдают .shp по сети, часто по той же линии, по которой
// работает сама машина. Если служба читает их без оглядки (после смены версии признаков —
// тысячи файлов по 3–5 МБ подряд, по два сразу), линия забивается и «виснет сеть» КПС.
// Поэтому всё, что служба читает с сетевого пути (\\сервер\папка\…), идёт не быстрее
// заданного числа МБ/с — отдельно на каждый сервер: медленная КПС не тормозит пресс 10.
// Локальные пути читаются как обычно.
package netio

import (
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// DefaultMBps — скорость чтения с одного сетевого сервера, если не задана в настройках:
// 2 МБ/с ≈ 16 Мбит/с — заметно меньше даже слабой 100-мегабитной линии.
const DefaultMBps = 2.0

const chunk = 64 << 10 // читаем кусками по 64 КБ: трафик идёт ровно, а не пачками по файлу

var (
	mu     sync.Mutex
	rateFn = func() float64 { return DefaultMBps }
	next   = map[string]time.Time{} // по серверу: когда можно отправлять следующий кусок
	sleep  = time.Sleep
	now    = time.Now
)

// SetRate — откуда брать скорость (МБ/с на сервер; ≤ 0 — DefaultMBps). Сервер передаёт
// функцию от своих настроек: изменение в «Настройках» действует сразу, без перезапуска.
func SetRate(f func() float64) {
	mu.Lock()
	defer mu.Unlock()
	rateFn = f
}

// Host — сервер сетевого пути (\\10.1.2.3\share\a.shp → «10.1.2.3»); "" — путь локальный.
// Подключённый сетевой диск (Z:\) отсюда не виден — для него указывайте путь \\сервер\папка.
func Host(path string) string {
	p := strings.ReplaceAll(path, "/", `\`)
	if !strings.HasPrefix(p, `\\`) {
		return ""
	}
	p = p[2:]
	if i := strings.IndexByte(p, '\\'); i >= 0 {
		p = p[:i]
	}
	return strings.ToLower(p)
}

// ReadFile — как os.ReadFile, но с сетевого пути — не быстрее заданной скорости.
func ReadFile(path string) ([]byte, error) {
	h := Host(path)
	if h == "" {
		return os.ReadFile(path)
	}
	return readLimited(path, h)
}

func readLimited(path, host string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var buf []byte
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		buf = make([]byte, 0, st.Size())
	}
	tmp := make([]byte, chunk)
	for {
		n, err := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			wait(host, n)
		}
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// wait — «потратить» n байт из полосы сервера: пауза, если читаем быстрее разрешённого.
// Полоса общая для всех, кто читает с этого сервера (признаки, архив, страницы).
func wait(host string, n int) {
	mu.Lock()
	rate := rateFn()
	if rate <= 0 {
		rate = DefaultMBps
	}
	t := now()
	start := next[host]
	if start.Before(t) {
		start = t
	}
	next[host] = start.Add(time.Duration(float64(n) / (rate * 1e6) * float64(time.Second)))
	mu.Unlock()
	if d := start.Sub(t); d > 0 {
		sleep(d)
	}
}
