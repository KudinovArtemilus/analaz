// server — веб-интерфейс press-go.
//
// С файлом настроек (рекомендуется): профили машин, папки, псевдонимы каналов, админка /admin.
//
//	server -config C:\press-go\pressgo.json
//	server -config C:\press-go\pressgo.json -data \\recorder\shp -features D:\press_shp_analysis\results
//
// Если файла настроек нет, он создаётся; -data и -features в первый раз записываются в него.
// Дальше папки меняются в админке, а флаги нужны только чтобы временно переопределить их.
//
// Без настроек — как раньше:
//
//	server -data \\recorder\shp [-features D:\press_shp_analysis\results] [-addr :8080]
//
// Всё (страницы, стили, графики) вшито в программу — рядом ничего класть не нужно.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/KudinovArtemilus/press-go/internal/config"
	"github.com/KudinovArtemilus/press-go/web"
)

func main() {
	cfgPath := flag.String("config", "", "файл настроек pressgo.json (создаётся, если нет)")
	data := flag.String("data", "", "папка с .shp (внутри можно ГГГГ\\ММ\\ДД)")
	addr := flag.String("addr", "", "адрес сервера; :8080 — доступ с других компьютеров сети (по умолчанию localhost:8080)")
	tz := flag.Int("tz", 5, "часовой пояс времени в имени файла, часы от UTC")
	feats := flag.String("features", "", "папка с таблицами признаков features*_OP1.csv.gz — включает анализ")
	norms := flag.String("norms", "", "папка с хранимыми нормами norm_OP1.json (по умолчанию — та же, что -features)")
	flag.Parse()
	given := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { given[f.Name] = true })

	var store *config.Store
	if *cfgPath != "" {
		def := config.Default()
		if *data != "" {
			def.Profiles[0].Sources = []string{*data}
		}
		def.FeaturesDir, def.NormsDir = *feats, *norms
		if *addr != "" {
			def.Listen = *addr
		}
		var err error
		if store, err = config.Open(*cfgPath, def); err != nil {
			log.Fatal(err)
		}
		c := store.Get()
		p := c.Active()
		// флаги главнее файла: так можно временно запустить с другой папкой, не трогая настройки
		if *data == "" && p != nil && len(p.Sources) > 0 {
			*data = p.Sources[0]
		}
		if !given["tz"] && p != nil {
			*tz = p.TZ
		}
		if *addr == "" {
			*addr = c.Listen
		}
		log.Printf("настройки: %s (профиль: %s)", *cfgPath, profileName(p))
	}
	if *data == "" {
		*data = "."
	}
	if *addr == "" {
		*addr = "localhost:8080"
	}

	if st, err := os.Stat(*data); err != nil || !st.IsDir() {
		log.Printf("ВНИМАНИЕ: папка данных недоступна: %s — просмотр циклов будет пустым, проверьте в /admin", *data)
	}
	srv, err := web.New(*data, time.FixedZone(fmt.Sprintf("UTC%+d", *tz), *tz*3600))
	if err != nil {
		log.Fatal(err)
	}
	if store != nil {
		srv.UseConfig(store)
	}

	// С настройками папку анализа берёт из них сам сервер (и подхватывает правку в админке).
	// Флаг -features — главнее: на этот запуск папка фиксируется.
	if store != nil && !given["features"] {
		log.Printf("анализ: таблицы признаков из настроек (%s) — меняются в /admin", orNone(store.Get().FeaturesDir))
	} else if *feats != "" {
		nd := *norms
		if nd == "" {
			nd = *feats
		}
		if err := srv.EnableAnalysis(*feats, nd); err != nil {
			log.Printf("ВНИМАНИЕ: анализ выключен: %v", err)
		} else {
			log.Printf("анализ: таблицы из %s, нормы из %s", *feats, nd)
		}
	}

	// этап 8: признаки новых циклов — раз в 5 минут, архив важных циклов — раз в 10
	if store != nil {
		srv.StartFeatures(5 * time.Minute)
		srv.StartArchive(10 * time.Minute)
	}

	hs := &http.Server{
		Addr:    *addr,
		Handler: srv.Handler(),
		// таймауты, чтобы зависший браузер не держал соединение вечно
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      120 * time.Second, // первый анализ большой таблицы может считаться десятки секунд
	}
	log.Printf("press-go: http://%s  (данные: %s)", *addr, *data)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func profileName(p *config.Profile) string {
	if p == nil {
		return "нет включённых"
	}
	return p.Name
}

func orNone(s string) string {
	if s == "" {
		return "не указана"
	}
	return s
}
