// build — собирает TypeScript веб-интерфейса в web/static/app.js с помощью esbuild.
// esbuild сам написан на Go и подключается как библиотека: Node и npm не нужны.
//
// Запуск из корня press-go:
//
//	go run -C tools/build .          # один раз собрать
//	go run -C tools/build . -watch   # пересобирать при каждом сохранении .ts
//
// Это отдельный модуль (свой go.mod): основной программе esbuild не нужен,
// она берёт уже собранный app.js. Типы esbuild НЕ проверяет — это делает редактор
// или `tsc --noEmit -p web/ts` перед коммитом.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	watch := flag.Bool("watch", false, "следить за изменениями и пересобирать")
	flag.Parse()

	opts := api.BuildOptions{
		EntryPoints:       []string{"../../web/ts/main.ts"},
		Outfile:           "../../web/static/app.js",
		Bundle:            true, // uPlot попадает внутрь app.js — один файл
		Format:            api.FormatIIFE,
		Target:            api.ES2020,
		MinifyWhitespace:  !*watch,
		MinifySyntax:      !*watch,
		MinifyIdentifiers: !*watch,
		Sourcemap:         sourcemap(*watch),
		Write:             true,
		LogLevel:          api.LogLevelInfo,
	}

	if !*watch {
		res := api.Build(opts)
		if len(res.Errors) > 0 {
			os.Exit(1)
		}
		return
	}
	ctx, err := api.Context(opts)
	if err != nil {
		log.Fatal(err)
	}
	if err := ctx.Watch(api.WatchOptions{}); err != nil {
		log.Fatal(err)
	}
	log.Print("слежу за web/ts… (Ctrl+C — выход)")
	select {} // ждём вечно, esbuild пересобирает сам
}

// в режиме разработки — карта исходников, чтобы в браузере отлаживать сам .ts
func sourcemap(watch bool) api.SourceMap {
	if watch {
		return api.SourceMapInline
	}
	return api.SourceMapNone
}
