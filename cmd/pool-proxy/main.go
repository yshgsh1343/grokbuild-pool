// Command pool-proxy 是大规模账号池的 Scheme B HTTP 入口。
//
// 启动逻辑在 internal/app 管线中：
//
//	config → pool → upstream → admin → http
//
// 默认监听：0.0.0.0:8080（Docker / 自托管）。
// 须配置真实 upstream.base_url，进程直接反代上游（无内置 mock）。
package main

import (
	"flag"
	"fmt"

	"github.com/yshgsh1343/grokbuild2api/internal/app"
)

// version 可在构建时用 -ldflags 覆盖。
var version = "0.1.0-m11"

func main() {
	configPath := flag.String("config", "", "path to config.yaml (optional; defaults apply)")
	showVersion := flag.Bool("version", false, "print version and exit")
	listenOverride := flag.String("listen", "", "override listen address (default 0.0.0.0:8080)")
	dbPathOverride := flag.String("db", "", "override catalog sqlite path")
	dataDirOverride := flag.String("data-dir", "", "override data_dir")
	flag.Parse()

	if *showVersion || (flag.NArg() == 1 && (flag.Arg(0) == "version" || flag.Arg(0) == "--version" || flag.Arg(0) == "-v")) {
		fmt.Printf("pool-proxy %s (scheme-B)\n", version)
		return
	}

	app.Run(app.Options{
		Version:    version,
		ConfigPath: *configPath,
		Listen:     *listenOverride,
		DataDir:    *dataDirOverride,
		DBPath:     *dbPathOverride,
	})
}
