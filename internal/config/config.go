// Package config — настройки press-go: профили машин (откуда читать .shp, какие операции),
// хранилище и архив, псевдонимы каналов. Один JSON-файл, его правит админка или человек руками.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/KudinovArtemilus/press-go/internal/features"
)

// Config — весь файл настроек.
type Config struct {
	Listen      string    `json:"listen"`       // адрес веб-сервера: localhost:8080 или :8080 для сети
	DataDir     string    `json:"data_dir"`     // где служба хранит архив, признаки, базу
	FeaturesDir string    `json:"features_dir"` // таблицы признаков features*_OP1.csv.gz (пока — results у run.py)
	NormsDir    string    `json:"norms_dir"`    // хранимые нормы norm_OP1.json; пусто — та же, что features_dir
	Archive     Archive   `json:"archive"`
	Profiles    []Profile `json:"profiles"`
}

// Archive — что хранить из сырых .shp (этап 8). Копии службы, источники не трогаются никогда.
type Archive struct {
	KeepDays  int     `json:"keep_days"`  // все циклы храним столько дней
	KeepEvery int     `json:"keep_every"` // дальше — каждый N-й обычный цикл (плюс важные: повреждённые, перед остановами, тревоги)
	MaxGB     float64 `json:"max_gb"`     // потолок архива на диске
}

// Profile — одна машина: пресс 10, пресс 5, КПС…
type Profile struct {
	ID      string            `json:"id"`      // латиница, для адресов: press10
	Name    string            `json:"name"`    // как показывать: «Пресс 10»
	Kind    string            `json:"kind"`    // тип машины (набор признаков и узлов): press10, press5, kps
	Sources []string          `json:"sources"` // папки с .shp; читаются, но никогда не меняются
	Ops     []string          `json:"ops"`     // операции в именах файлов: OP1, OP2
	TZ      int               `json:"tz"`      // часовой пояс времени в имени файла, часы от UTC
	Enabled bool              `json:"enabled"`
	Aliases map[string]string `json:"aliases"` // своё название: исходное имя канала или признака → как показывать
}

var (
	reID = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
	reOp = regexp.MustCompile(`^OP\d+$`)
)

// Default — настройки по умолчанию: один профиль «Пресс 10».
func Default() *Config {
	return &Config{
		Listen:  "localhost:8080",
		DataDir: `C:\press-go-data`,
		Archive: Archive{KeepDays: 30, KeepEvery: 20, MaxGB: 400},
		Profiles: []Profile{{ID: "press10", Name: "Пресс 10", Kind: "press10", Ops: []string{"OP1", "OP2"},
			TZ: 5, Enabled: true, Aliases: map[string]string{}}},
	}
}

// Validate — проверка перед сохранением: лучше отказать в админке, чем сломать службу.
func (c *Config) Validate() error {
	seen := map[string]bool{}
	for i, p := range c.Profiles {
		if !reID.MatchString(p.ID) {
			return fmt.Errorf("профиль %d: id %q — только строчная латиница, цифры, - и _", i+1, p.ID)
		}
		if seen[p.ID] {
			return fmt.Errorf("профиль %q повторяется", p.ID)
		}
		seen[p.ID] = true
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("профиль %q: пустое название", p.ID)
		}
		if p.TZ < -12 || p.TZ > 14 {
			return fmt.Errorf("профиль %q: часовой пояс %d вне −12…+14", p.ID, p.TZ)
		}
		for _, op := range p.Ops {
			if !reOp.MatchString(op) {
				return fmt.Errorf("профиль %q: операция %q — ждали OP1, OP2…", p.ID, op)
			}
		}
	}
	// Таблицы признаков называются по операции (features_OP3.csv.gz) и лежат в одной папке:
	// два включённых профиля с одной операцией писали бы в одну таблицу и смешали бы машины.
	owner := map[string]string{}
	for _, p := range c.Profiles {
		if !p.Enabled {
			continue
		}
		for _, op := range p.Ops {
			if o, dup := owner[op]; dup && o != p.ID {
				return fmt.Errorf("операция %s есть в двух включённых профилях (%q и %q): таблица признаков у операции одна — "+
					"выключите один профиль или уберите операцию", op, o, p.ID)
			}
			owner[op] = p.ID
		}
	}
	// KeepDays = 0 — не хранить всё подряд (буфер регистратора и так держит ~2 недели), только отобранное
	if c.Archive.KeepDays < 0 || c.Archive.KeepEvery < 1 || c.Archive.MaxGB <= 0 {
		return fmt.Errorf("архив: дни ≥ 0, каждый N-й ≥ 1, потолок > 0 ГБ")
	}
	return nil
}

// EnabledProfiles — все включённые профили по порядку (указатели внутрь c — не менять).
func (c *Config) EnabledProfiles() []*Profile {
	var out []*Profile
	for i := range c.Profiles {
		if c.Profiles[i].Enabled {
			out = append(out, &c.Profiles[i])
		}
	}
	return out
}

// ProfileFor — включённый профиль, которому принадлежит операция (nil, если ничей).
// Операция у включённых профилей одна на всех — это проверяет Validate.
func (c *Config) ProfileFor(op string) *Profile {
	for _, p := range c.EnabledProfiles() {
		for _, o := range p.Ops {
			if strings.EqualFold(o, op) {
				return p
			}
		}
	}
	return nil
}

// Profile — профиль по id (nil, если нет).
func (c *Config) Profile(id string) *Profile {
	for i := range c.Profiles {
		if c.Profiles[i].ID == id {
			return &c.Profiles[i]
		}
	}
	return nil
}

// Active — первый включённый профиль (nil, если ни одного).
func (c *Config) Active() *Profile {
	for i := range c.Profiles {
		if c.Profiles[i].Enabled {
			return &c.Profiles[i]
		}
	}
	return nil
}

// Clone — глубокая копия: админка правит копию, а работающая служба видит старую,
// пока новая не проверена и не сохранена.
func (c *Config) Clone() *Config {
	b, _ := json.Marshal(c)
	var out Config
	json.Unmarshal(b, &out)
	for i := range out.Profiles {
		if out.Profiles[i].Aliases == nil {
			out.Profiles[i].Aliases = map[string]string{}
		}
	}
	return &out
}

// ---------- псевдонимы ----------

var reRawFeature = regexp.MustCompile(`^[ad]\d{3}_(.+?)__(\w+)$`)

// Label — как показывать канал или признак: псевдоним, если он задан, иначе "" (пусть решает вызывающий).
// Для сырых признаков a017_slide_total_force_kN__max ищется псевдоним канала "slide:total force [kN]"
// и добавляется статистика: «Усилие ползуна [max]».
func (p *Profile) Label(name string) string {
	if p == nil {
		return ""
	}
	if a := p.Aliases[name]; a != "" {
		return a
	}
	if m := reRawFeature.FindStringSubmatch(name); m != nil {
		for ch, a := range p.Aliases {
			if a != "" && features.Safe(ch) == m[1] {
				return a + " [" + m[2] + "]"
			}
		}
	}
	return ""
}

// SetAlias задаёт (или снимает, если пусто) своё название.
func (p *Profile) SetAlias(name, alias string) {
	if p.Aliases == nil {
		p.Aliases = map[string]string{}
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		delete(p.Aliases, name)
		return
	}
	p.Aliases[name] = alias
}

// ---------- файл ----------

// Store — настройки в файле + в памяти. Чтение — без блокировки служебных горутин надолго,
// сохранение — целиком новой версией (старую никто не меняет на ходу).
type Store struct {
	path string
	mu   sync.RWMutex
	cfg  *Config
}

// Open читает файл настроек; если его нет — создаёт из def.
func Open(path string, def *Config) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if err := def.Validate(); err != nil {
			return nil, err
		}
		s.cfg = def
		return s, s.write(def)
	case err != nil:
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.cfg = c.Clone()
	return s, nil
}

// Get — текущие настройки (не менять! для правки — Update).
func (s *Store) Get() *Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Path — где лежит файл настроек.
func (s *Store) Path() string { return s.path }

// Update правит копию настроек, проверяет её и сохраняет. Если change или проверка вернули
// ошибку — ничего не меняется ни в файле, ни в памяти.
func (s *Store) Update(change func(c *Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg.Clone()
	if err := change(next); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if err := s.write(next); err != nil {
		return err
	}
	s.cfg = next
	return nil
}

// write — атомарно: временный файл и переименование. Прежняя версия остаётся в .bak.
func (s *Store) write(c *Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" {
		os.MkdirAll(dir, 0o755)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(s.path); err == nil {
		// прошлая версия — в .bak (переименование заменяет прошлый .bak, удалять ничего не нужно)
		if err := os.Rename(s.path, s.path+".bak"); err != nil {
			return err
		}
	}
	return os.Rename(tmp, s.path)
}

// SortedAliases — псевдонимы профиля по алфавиту исходных имён (для страницы).
func (p *Profile) SortedAliases() [][2]string {
	var out [][2]string
	for k, v := range p.Aliases {
		out = append(out, [2]string{k, v})
	}
	slices.SortFunc(out, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
	return out
}
