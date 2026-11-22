package web

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// Папки с .shp у сервера — это папки всех включённых профилей (пресс, КПС…).
// Файл в адресе (?f=…) задаётся путём относительно своей папки:
//
//	2026/11/13/x.shp          — папка из запуска (-data, она же первая папка первого профиля);
//	                            так было до многих профилей — старые ссылки и архив пресса не меняются
//	@kps/2026/11/13/x.shp     — первая папка профиля kps
//	@kps.2/2026/11/13/x.shp   — вторая папка профиля kps
//
// Префикс только выбирает папку из настроек; путь после него проверяется как раньше
// (нельзя выйти из папки через ..).

// fileRoot — одна папка с .shp.
type fileRoot struct {
	Key     string // "" — папка из запуска; иначе id профиля, для 2-й и дальше папок — id.N
	Dir     string
	Profile string // id профиля ("" — без настроек)
	Name    string // название профиля для показа
	Src     int    // номер папки в профиле, с 0
}

// roots — все папки: сначала папка из запуска, потом папки включённых профилей (без повторов).
func (s *Server) roots() []fileRoot {
	out := []fileRoot{{Dir: s.data}}
	if s.cfg == nil {
		return out
	}
	c := s.cfg.Get()
	for _, p := range c.EnabledProfiles() {
		for i, dir := range c.Dirs(p) { // у кольцевого буфера — папка с копиями: имена в самом кольце сдвигаются
			if samePath(dir, s.data) {
				if out[0].Profile == "" { // папка из запуска принадлежит этому профилю
					out[0].Profile, out[0].Name, out[0].Src = p.ID, p.Name, i
				}
				continue
			}
			key := p.ID
			if i > 0 {
				key += "." + strconv.Itoa(i+1)
			}
			out = append(out, fileRoot{Key: key, Dir: dir, Profile: p.ID, Name: p.Name, Src: i})
		}
	}
	if out[0].Profile == "" { // папку из запуска ни один профиль не назвал — считаем её первого
		if ps := s.cfg.Get().EnabledProfiles(); len(ps) > 0 {
			out[0].Profile, out[0].Name = ps[0].ID, ps[0].Name
		}
	}
	return out
}

// qualify — адрес файла для ?f=: путь в папке с префиксом папки.
func qualify(root fileRoot, rel string) string {
	if root.Key == "" {
		return rel
	}
	return "@" + root.Key + "/" + rel
}

// resolve — какая папка и какой путь в ней у адреса из запроса.
func (s *Server) resolve(f string) (fileRoot, string, error) {
	rs := s.roots()
	if !strings.HasPrefix(f, "@") {
		return rs[0], f, nil
	}
	i := strings.IndexByte(f, '/')
	if i < 0 {
		return fileRoot{}, "", fmt.Errorf("недопустимый путь: %s", f)
	}
	key := f[1:i]
	for _, r := range rs {
		if r.Key != "" && r.Key == key {
			return r, f[i+1:], nil
		}
	}
	return fileRoot{}, "", errors.New("нет папки «" + key + "» — профиль выключен или папка убрана в настройках")
}

// relData — адрес для ?f= по полному пути из таблицы признаков (\\recorder\shp\2026\11\13\x.shp).
// Ищется папка, внутри которой лежит файл (самая длинная подходящая). Не нашлась — "" (ссылки не будет).
func (s *Server) relData(p string) string {
	r, rel := s.rootOf(p)
	if rel == "" {
		return ""
	}
	return qualify(r, rel)
}

// rootOf — папка файла и путь в ней ("" — файл ни в одной папке).
func (s *Server) rootOf(p string) (fileRoot, string) {
	norm := func(x string) string { return strings.ToLower(strings.ReplaceAll(x, `\`, "/")) }
	full := norm(p)
	best, bestLen := fileRoot{}, -1
	for _, r := range s.roots() {
		root := norm(r.Dir)
		if !strings.HasSuffix(root, "/") {
			root += "/"
		}
		if strings.HasPrefix(full, root) && len(root) > bestLen {
			best, bestLen = r, len(root)
		}
	}
	if bestLen < 0 {
		return fileRoot{}, ""
	}
	return best, strings.ReplaceAll(p, `\`, "/")[bestLen:]
}

// archRel — путь файла в архиве профиля. У первой папки профиля — как в самой папке
// (так архив пресса и был устроен), у второй и дальше — в подпапке src2, src3…, чтобы
// одинаковые пути из разных папок не столкнулись.
func archRel(r fileRoot, rel string) string {
	if r.Src == 0 {
		return rel
	}
	return "src" + strconv.Itoa(r.Src+1) + "/" + rel
}

// rootsLabel — папки для шапки страницы.
func (s *Server) rootsLabel() string {
	var parts []string
	for _, r := range s.roots() {
		if r.Name != "" {
			parts = append(parts, r.Name+": "+filepath.Clean(r.Dir))
		} else {
			parts = append(parts, filepath.Clean(r.Dir))
		}
	}
	return strings.Join(parts, " · ")
}
