package web

import (
	"sync"

	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// shpCache держит несколько последних прочитанных циклов в памяти.
// Открыл цикл, переключаешь каналы — файл (10 МБ с сетевой папки) не читается заново.
// Каждый цикл в памяти ~12 МБ, так что 6 штук ≈ 70 МБ.
type shpCache struct {
	mu    sync.Mutex // веб-сервер обрабатывает запросы параллельно — без мьютекса будет гонка
	max   int
	order []string // от старых к новым
	m     map[string]*shp.File
}

func newShpCache(max int) *shpCache {
	return &shpCache{max: max, m: map[string]*shp.File{}}
}

func (c *shpCache) get(path string) (*shp.File, error) {
	return c.getWith(path, func() (*shp.File, error) { return shp.Read(path) })
}

// getWith — то же, но файл загружает load (например, из архива, если в буфере его уже нет).
func (c *shpCache) getWith(path string, load func() (*shp.File, error)) (*shp.File, error) {
	c.mu.Lock()
	if f, ok := c.m[path]; ok {
		c.touch(path)
		c.mu.Unlock()
		return f, nil
	}
	c.mu.Unlock()

	// Читаем файл БЕЗ блокировки: иначе пока один пользователь ждёт сетевую папку,
	// все остальные запросы стояли бы в очереди.
	f, err := load()
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[path]; !ok {
		c.m[path] = f
		c.order = append(c.order, path)
		if len(c.order) > c.max { // вытесняем самый давний
			delete(c.m, c.order[0])
			c.order = c.order[1:]
		}
	}
	return c.m[path], nil
}

// touch переносит путь в конец очереди — «недавно использован».
func (c *shpCache) touch(path string) {
	for i, p := range c.order {
		if p == path {
			c.order = append(append(c.order[:i:i], c.order[i+1:]...), path)
			return
		}
	}
}
