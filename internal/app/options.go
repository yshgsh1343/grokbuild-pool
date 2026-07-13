package app

// Options 是 CLI/环境注入到启动管线的参数（与 config.yaml 叠加）。
type Options struct {
	Version string

	ConfigPath string
	Listen     string
	DataDir    string
	DBPath     string
}
