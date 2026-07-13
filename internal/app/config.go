package app

import (
	"log/slog"
	"os"
	"strings"

	"github.com/yshgsh1343/grokbuild2api/internal/config"
)

// loadConfig 管线第 1 步：读 YAML → flag/env 覆盖 → 默认值 → Validate。
func loadConfig(opts Options, logger *slog.Logger) config.Config {
	cfgPath := strings.TrimSpace(opts.ConfigPath)
	if cfgPath == "" {
		cfgPath = defaultConfigPath()
	}

	var cfg config.Config
	var err error
	if cfgPath != "" {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") {
				logger.Info("config_missing_using_defaults", "path", cfgPath)
				cfg = config.Default()
			} else {
				fail(logger, "config_invalid", err)
			}
		} else {
			logger.Info("config_loaded", "path", cfgPath)
		}
	} else {
		cfg = config.Default()
	}

	// 强制走真实上游：忽略遗留 mock_upstream / 空 base_url 的 mock 语义
	cfg.MockUpstream = false
	if v := strings.TrimSpace(opts.Listen); v != "" {
		cfg.Listen = v
	}
	if v := strings.TrimSpace(os.Getenv("LISTEN")); v != "" {
		cfg.Listen = v
	}
	if v := strings.TrimSpace(opts.DataDir); v != "" {
		cfg.DataDir = v
	}
	if v := strings.TrimSpace(opts.DBPath); v != "" {
		cfg.DBPath = v
	}
	if envTrue("ALLOW_PUBLIC_LISTEN") {
		cfg.AllowPublicListen = true
	}
	cfg.ApplyDefaultsPublic()
	if err := cfg.Validate(); err != nil {
		fail(logger, "config_invalid", err)
	}
	return cfg
}
