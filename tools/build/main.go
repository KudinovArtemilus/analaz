// build â€” ÑÐ¾Ð±Ð¸Ñ€Ð°ÐµÑ‚ TypeScript Ð²ÐµÐ±-Ð¸Ð½Ñ‚ÐµÑ€Ñ„ÐµÐ¹ÑÐ° Ð² web/static/app.js Ñ Ð¿Ð¾Ð¼Ð¾Ñ‰ÑŒÑŽ esbuild.
// esbuild ÑÐ°Ð¼ Ð½Ð°Ð¿Ð¸ÑÐ°Ð½ Ð½Ð° Go Ð¸ Ð¿Ð¾Ð´ÐºÐ»ÑŽÑ‡Ð°ÐµÑ‚ÑÑ ÐºÐ°Ðº Ð±Ð¸Ð±Ð»Ð¸Ð¾Ñ‚ÐµÐºÐ°: Node Ð¸ npm Ð½Ðµ Ð½ÑƒÐ¶Ð½Ñ‹.
//
// Ð—Ð°Ð¿ÑƒÑÐº Ð¸Ð· ÐºÐ¾Ñ€Ð½Ñ press-go:
//
//	go run -C tools/build .          # Ð¾Ð´Ð¸Ð½ Ñ€Ð°Ð· ÑÐ¾Ð±Ñ€Ð°Ñ‚ÑŒ
//	go run -C tools/build . -watch   # Ð¿ÐµÑ€ÐµÑÐ¾Ð±Ð¸Ñ€Ð°Ñ‚ÑŒ Ð¿Ñ€Ð¸ ÐºÐ°Ð¶Ð´Ð¾Ð¼ ÑÐ¾Ñ…Ñ€Ð°Ð½ÐµÐ½Ð¸Ð¸ .ts
//
// Ð­Ñ‚Ð¾ Ð¾Ñ‚Ð´ÐµÐ»ÑŒÐ½Ñ‹Ð¹ Ð¼Ð¾Ð´ÑƒÐ»ÑŒ (ÑÐ²Ð¾Ð¹ go.mod): Ð¾ÑÐ½Ð¾Ð²Ð½Ð¾Ð¹ Ð¿Ñ€Ð¾Ð³Ñ€Ð°Ð¼Ð¼Ðµ esbuild Ð½Ðµ Ð½ÑƒÐ¶ÐµÐ½,
// Ð¾Ð½Ð° Ð±ÐµÑ€Ñ‘Ñ‚ ÑƒÐ¶Ðµ ÑÐ¾Ð±Ñ€Ð°Ð½Ð½Ñ‹Ð¹ app.js. Ð¢Ð¸Ð¿Ñ‹ esbuild ÐÐ• Ð¿Ñ€Ð¾Ð²ÐµÑ€ÑÐµÑ‚ â€” ÑÑ‚Ð¾ Ð´ÐµÐ»Ð°ÐµÑ‚ Ñ€ÐµÐ´Ð°ÐºÑ‚Ð¾Ñ€
// Ð¸Ð»Ð¸ `tsc --noEmit -p web/ts` Ð¿ÐµÑ€ÐµÐ´ ÐºÐ¾Ð¼Ð¼Ð¸Ñ‚Ð¾Ð¼.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	watch := flag.Bool("watch", false, "ÑÐ»ÐµÐ´Ð¸Ñ‚ÑŒ Ð·Ð° Ð¸Ð·Ð¼ÐµÐ½ÐµÐ½Ð¸ÑÐ¼Ð¸ Ð¸ Ð¿ÐµÑ€ÐµÑÐ¾Ð±Ð¸Ñ€Ð°Ñ‚ÑŒ")
	flag.Parse()

	opts := api.BuildOptions{
		EntryPoints:       []string{"../../web/ts/main.ts"},
		Outfile:           "../../web/static/app.js",
		Bundle:            true, // uPlot Ð¿Ð¾Ð¿Ð°Ð´Ð°ÐµÑ‚ Ð²Ð½ÑƒÑ‚Ñ€ÑŒ app.js â€” Ð¾Ð´Ð¸Ð½ Ñ„Ð°Ð¹Ð»
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
	log.Print("ÑÐ»ÐµÐ¶Ñƒ Ð·Ð° web/tsâ€¦ (Ctrl+C â€” Ð²Ñ‹Ñ…Ð¾Ð´)")
	select {} // Ð¶Ð´Ñ‘Ð¼ Ð²ÐµÑ‡Ð½Ð¾, esbuild Ð¿ÐµÑ€ÐµÑÐ¾Ð±Ð¸Ñ€Ð°ÐµÑ‚ ÑÐ°Ð¼
}

// Ð² Ñ€ÐµÐ¶Ð¸Ð¼Ðµ Ñ€Ð°Ð·Ñ€Ð°Ð±Ð¾Ñ‚ÐºÐ¸ â€” ÐºÐ°Ñ€Ñ‚Ð° Ð¸ÑÑ…Ð¾Ð´Ð½Ð¸ÐºÐ¾Ð², Ñ‡Ñ‚Ð¾Ð±Ñ‹ Ð² Ð±Ñ€Ð°ÑƒÐ·ÐµÑ€Ðµ Ð¾Ñ‚Ð»Ð°Ð¶Ð¸Ð²Ð°Ñ‚ÑŒ ÑÐ°Ð¼ .ts
func sourcemap(watch bool) api.SourceMap {
	if watch {
		return api.SourceMapInline
	}
	return api.SourceMapNone
}
