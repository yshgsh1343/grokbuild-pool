package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/admin"
	"github.com/yshgsh1343/grokbuild2api/internal/clients"
	"github.com/yshgsh1343/grokbuild2api/internal/config"
	"github.com/yshgsh1343/grokbuild2api/internal/httpserver"
	"github.com/yshgsh1343/grokbuild2api/internal/importjobs"
	"github.com/yshgsh1343/grokbuild2api/internal/ssoimport"
)

// adminStack 令牌库 + 热配置 + 导入任务 + admin handlers。
type adminStack struct {
	Tokens     *clients.Store
	Settings   *admin.SettingsController
	ImportJobs *importjobs.Manager
	Handlers   *admin.Handlers
	StartedAt  time.Time
}

// wireAdmin 管线第 4 步：tokens.db → settings 热更新 → import jobs（SSO 并行转换）→ admin API。
func wireAdmin(cfg config.Config, pool *poolStack, up *upstreamStack, metrics *httpserver.Metrics, version string, logger *slog.Logger) *adminStack {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		fail(logger, "data_dir", err)
	}
	tokenDB := filepath.Join(cfg.DataDir, "tokens.db")
	tokenStore, err := clients.Open(tokenDB)
	if err != nil {
		fail(logger, "tokens_open", err)
	}
	logger.Info("tokens_store_open", "path", tokenDB)

	startedAt := time.Now()
	anth := cfg.Anthropic
	aliases := map[string]string{}
	for k, v := range anth.ModelAliases {
		aliases[k] = v
	}
	prefixes := append([]string(nil), anth.PassthroughPrefixes...)
	sso := cfg.Imports.SSOConverter
	rt := admin.RuntimeSettings{
		SelectorStrategy:            cfg.Selector.Strategy,
		HotSize:                     cfg.HotSize,
		MaxInflightPerAccount:       pool.MaxInflight,
		StickyTTLSec:                cfg.Selector.StickyTTLSec,
		StickyMax:                   cfg.Selector.StickyMax,
		Pow2K:                       cfg.Selector.Pow2K,
		WPriority:                   cfg.Selector.WPriority,
		WInflight:                   cfg.Selector.WInflight,
		WFailure:                    cfg.Selector.WFailure,
		JitterAmp:                   cfg.Selector.JitterAmp,
		SelectorMaxAttempts:         cfg.Selector.MaxAttempts,
		MaxAttempts:                 cfg.Lease.MaxAttempts,
		CooldownBaseSec:             cfg.Lease.CooldownBaseSec,
		CooldownCapSec:              cfg.Lease.CooldownCapSec,
		UnauthorizedCooldownSec:     cfg.Lease.UnauthorizedCooldownSec,
		PaymentRequiredCooldownSec:  cfg.Lease.PaymentRequiredCooldownSec,
		UnauthorizedQuarantineAfter: cfg.Lease.UnauthorizedQuarantineAfter,
		ForbiddenCooldownSec:        cfg.Lease.ForbiddenCooldownSec,
		ForbiddenQuarantineAfter:    cfg.Lease.ForbiddenQuarantineAfter,
		CooldownJitterPct:           cfg.Lease.CooldownJitterPct,
		CooldownExpMax:              4,
		MaxConcurrent:               cfg.Limits.MaxConcurrent,
		MaxBodyBytes:                cfg.Limits.MaxBodyBytes,
		RequestTimeoutSec:           cfg.Limits.RequestTimeoutSec,
		RefreshWorkers:              3,
		RefreshQPS:                  30,
		RefreshSkewSec:              300,
		TokenDefaultRemainQuota:     1000,
		TokenDefaultMaxConcurrent:   5,
		TokenDefaultRPM:             0,
		TokenDefaultUnlimited:       false,
		ImportEnabled:               cfg.Imports.Enabled,
		ImportMaxUploadBytes:        cfg.Imports.MaxUploadBytes,
		ImportMaxEntries:            cfg.Imports.MaxEntries,
		ImportMaxConcurrentJobs:     cfg.Imports.MaxConcurrentJobs,
		ImportWorkers:               4,
		ImportMaxNDJSONLineBytes:    cfg.Imports.MaxNDJSONLineBytes,
		ImportMaxSSOValueBytes:      cfg.Imports.MaxSSOValueBytes,
		ImportJobTimeoutSec:         cfg.Imports.JobTimeoutSec,
		ImportStagingStaleAfterSec:  cfg.Imports.StagingStaleAfterSec,
		ImportAllowServerPath:       cfg.Imports.AllowServerPath,
		ImportSSOEndpoint:           sso.Endpoint,
		ImportSSOAPIKeySet:          strings.TrimSpace(sso.Endpoint) != "" && strings.TrimSpace(sso.APIKey) != "",
		ImportSSOMaxBatch:           sso.MaxBatch,
		ImportSSOTimeoutSec:         sso.TimeoutSec,
		ImportSSOAllowInsecure:      sso.AllowInsecure,
		ImportSSOWorkers:            4,
		AnthropicEnabled:            anth.Enabled,
		AnthropicStripUnknownBetas:  anth.StripUnknownBetas,
		AnthropicCountTokens:        anth.CountTokens,
		AnthropicPassthroughPrefixes: prefixes,
		AnthropicModelAliases:       aliases,
		Listen:                      cfg.Listen,
		AllowPublicListen:           cfg.AllowPublicListen,
		DataDir:                     cfg.DataDir,
		DBPath:                      pool.DBPath,
		MockUpstream:                false,
		UpstreamBaseURL:             cfg.Upstream.BaseURL,
		OAuthRefreshURL:             cfg.OAuth.RefreshURL,
		OAuthClientID:               cfg.OAuth.ClientID,
		APIKeyConfigured:            strings.TrimSpace(cfg.APIKey) != "",
		AdminKeyConfigured:          strings.TrimSpace(cfg.AdminKey) != "",
		LoggingLevel:                cfg.Logging.Level,
	}

	settingsPath := filepath.Join(cfg.DataDir, "settings.json")
	settingsCtl := &admin.SettingsController{
		Path:     settingsPath,
		Hot:      pool.Hot,
		Lease:    pool.Lease,
		Selector: pool.Selector,
		Refresh:  up.Refresh,
		ProcessInfo: admin.RuntimeSettings{
			Listen:             cfg.Listen,
			AllowPublicListen:  cfg.AllowPublicListen,
			DataDir:            cfg.DataDir,
			DBPath:             pool.DBPath,
			MockUpstream:       false,
			UpstreamBaseURL:    cfg.Upstream.BaseURL,
			OAuthRefreshURL:    cfg.OAuth.RefreshURL,
			OAuthClientID:      cfg.OAuth.ClientID,
			APIKeyConfigured:   strings.TrimSpace(cfg.APIKey) != "",
			AdminKeyConfigured: strings.TrimSpace(cfg.AdminKey) != "",
			ImportSSOAPIKeySet: strings.TrimSpace(sso.Endpoint) != "" && strings.TrimSpace(sso.APIKey) != "",
			LoggingLevel:       cfg.Logging.Level,
		},
	}
	// 启动密钥进入内存持有（GET 不回传）
	settingsCtl.SeedSecrets(cfg.APIKey, cfg.AdminKey, sso.APIKey)
	// 先建 import manager，再挂 ApplyImport，最后 Load/Apply 设置
	var importJobs *importjobs.Manager
	// 始终初始化导入管理器，便于前端热开/热改；enabled=false 时仍可看配置
	{
		var ssoConverter ssoimport.Converter
		converterCfg := cfg.Imports.SSOConverter
		if strings.TrimSpace(converterCfg.Endpoint) != "" && strings.TrimSpace(converterCfg.APIKey) != "" {
			client, err := ssoimport.NewClient(ssoimport.Config{
				Endpoint:      converterCfg.Endpoint,
				APIKey:        converterCfg.APIKey,
				MaxBatch:      converterCfg.MaxBatch,
				Timeout:       time.Duration(converterCfg.TimeoutSec) * time.Second,
				AllowInsecure: converterCfg.AllowInsecure,
				Workers:       4,
			})
			if err != nil {
				logger.Warn("sso_converter_config_invalid", "error", err)
				// 回退内置 Go Device Flow
				ssoConverter = ssoimport.NewLocalConverter()
				logger.Info("sso_converter_local", "mode", "builtin-device-flow")
			} else {
				ssoConverter = client
				logger.Info("sso_converter_remote", "endpoint", converterCfg.Endpoint)
			}
		} else {
			// 默认：内置纯 Go SSO→JSON（Device Flow），无需 Python sidecar
			ssoConverter = ssoimport.NewLocalConverter()
			logger.Info("sso_converter_local", "mode", "builtin-device-flow")
		}
		importJobs, err = importjobs.NewWithOptions(cfg.DataDir, pool.Catalog, importjobs.Options{
			MaxConcurrentJobs:  cfg.Imports.MaxConcurrentJobs,
			Workers:            4,
			MaxEntries:         cfg.Imports.MaxEntries,
			MaxNDJSONLineBytes: cfg.Imports.MaxNDJSONLineBytes,
			MaxSSOValueBytes:   cfg.Imports.MaxSSOValueBytes,
			JobTimeout:         time.Duration(cfg.Imports.JobTimeoutSec) * time.Second,
			StagingStaleAfter:  time.Duration(cfg.Imports.StagingStaleAfterSec) * time.Second,
			AllowServerPath:    cfg.Imports.AllowServerPath,
			Converter:          ssoConverter,
			AfterImport: func() error {
				_, err := pool.Hot.LoadEligible(pool.Catalog)
				return err
			},
		})
		if err != nil {
			fail(logger, "import_jobs_init_failed", err)
		}
	}

	// 热更新导入限制 + SSO 转换器
	var ssoKeyHold string = strings.TrimSpace(sso.APIKey)
	settingsCtl.ApplyImport = func(in admin.RuntimeSettings) error {
		if importJobs == nil {
			return nil
		}
		key := strings.TrimSpace(in.ImportSSOAPIKey)
		if key == "" {
			key = settingsCtl.PeekSSOAPIKey()
		}
		if key == "" {
			key = ssoKeyHold
		}
		var conv ssoimport.Converter
		if strings.TrimSpace(in.ImportSSOEndpoint) != "" && key != "" {
			client, err := ssoimport.NewClient(ssoimport.Config{
				Endpoint:      in.ImportSSOEndpoint,
				APIKey:        key,
				MaxBatch:      in.ImportSSOMaxBatch,
				Timeout:       time.Duration(in.ImportSSOTimeoutSec) * time.Second,
				AllowInsecure: in.ImportSSOAllowInsecure,
				Workers:       in.ImportSSOWorkers,
			})
			if err != nil {
				return err
			}
			conv = client
			ssoKeyHold = key
		} else {
			// 未配置远程转换器时始终使用内置 Go Device Flow
			conv = ssoimport.NewLocalConverter()
		}
		importJobs.ApplyOptions(importjobs.Options{
			MaxConcurrentJobs:  in.ImportMaxConcurrentJobs,
			Workers:            in.ImportWorkers,
			MaxEntries:         in.ImportMaxEntries,
			MaxNDJSONLineBytes: in.ImportMaxNDJSONLineBytes,
			MaxSSOValueBytes:   in.ImportMaxSSOValueBytes,
			JobTimeout:         time.Duration(in.ImportJobTimeoutSec) * time.Second,
			StagingStaleAfter:  time.Duration(in.ImportStagingStaleAfterSec) * time.Second,
			AllowServerPath:    in.ImportAllowServerPath,
			Converter:          conv,
			AfterImport: func() error {
				_, err := pool.Hot.LoadEligible(pool.Catalog)
				return err
			},
		})
		return nil
	}

	if err := settingsCtl.Load(); err != nil {
		logger.Warn("settings_load_failed", "path", settingsPath, "error", err)
	}
	if st, err := os.Stat(settingsPath); err == nil && st.Size() > 0 {
		// 文件覆盖可编辑字段，但补齐零值
		rt = mergeSettingsDefaults(settingsCtl.Snapshot().RuntimeSettings, rt)
	}
	if _, err := settingsCtl.Apply(rt); err != nil {
		logger.Warn("settings_apply_failed", "path", settingsPath, "error", err)
	} else {
		logger.Info("settings_ready", "path", settingsPath)
	}

	adminH := &admin.Handlers{
		AdminKey:   cfg.AdminKey,
		Config:     cfg,
		Tokens:     tokenStore,
		Hot:        httpserver.IndexStats{Index: pool.Hot},
		Metrics:    metrics,
		Version:    version,
		StartedAt:  startedAt,
		Settings:   settingsCtl,
		Catalog:    pool.Catalog,
		AccountHot: pool.Hot,
		ImportJobs: importJobs,
	}

	return &adminStack{
		Tokens:     tokenStore,
		Settings:   settingsCtl,
		ImportJobs: importJobs,
		Handlers:   adminH,
		StartedAt:  startedAt,
	}
}

// mergeSettingsDefaults 用 base 填补 loaded 的零值（避免旧 settings.json 缺字段）。
func mergeSettingsDefaults(loaded, base admin.RuntimeSettings) admin.RuntimeSettings {
	out := loaded
	if out.SelectorStrategy == "" {
		out.SelectorStrategy = base.SelectorStrategy
	}
	if out.HotSize <= 0 {
		out.HotSize = base.HotSize
	}
	if out.MaxInflightPerAccount <= 0 {
		out.MaxInflightPerAccount = base.MaxInflightPerAccount
	}
	if out.StickyTTLSec <= 0 {
		out.StickyTTLSec = base.StickyTTLSec
	}
	if out.StickyMax <= 0 {
		out.StickyMax = base.StickyMax
	}
	if out.Pow2K <= 0 {
		out.Pow2K = base.Pow2K
	}
	if out.SelectorMaxAttempts <= 0 {
		out.SelectorMaxAttempts = base.SelectorMaxAttempts
	}
	if out.MaxAttempts <= 0 {
		out.MaxAttempts = base.MaxAttempts
	}
	if out.CooldownBaseSec <= 0 {
		out.CooldownBaseSec = base.CooldownBaseSec
	}
	if out.CooldownCapSec <= 0 {
		out.CooldownCapSec = base.CooldownCapSec
	}
	if out.MaxConcurrent <= 0 {
		out.MaxConcurrent = base.MaxConcurrent
	}
	if out.MaxBodyBytes <= 0 {
		out.MaxBodyBytes = base.MaxBodyBytes
	}
	if out.RequestTimeoutSec <= 0 {
		out.RequestTimeoutSec = base.RequestTimeoutSec
	}
	if out.RefreshWorkers <= 0 {
		out.RefreshWorkers = base.RefreshWorkers
	}
	if out.RefreshQPS <= 0 {
		out.RefreshQPS = base.RefreshQPS
	}
	if out.RefreshSkewSec <= 0 {
		out.RefreshSkewSec = base.RefreshSkewSec
	}
	if out.CooldownExpMax <= 0 {
		out.CooldownExpMax = base.CooldownExpMax
	}
	if out.ImportMaxUploadBytes <= 0 {
		out.ImportMaxUploadBytes = base.ImportMaxUploadBytes
	}
	if out.ImportMaxEntries <= 0 {
		out.ImportMaxEntries = base.ImportMaxEntries
	}
	if out.ImportMaxConcurrentJobs <= 0 {
		out.ImportMaxConcurrentJobs = base.ImportMaxConcurrentJobs
	}
	if out.ImportWorkers <= 0 {
		out.ImportWorkers = base.ImportWorkers
	}
	if out.ImportMaxNDJSONLineBytes <= 0 {
		out.ImportMaxNDJSONLineBytes = base.ImportMaxNDJSONLineBytes
	}
	if out.ImportMaxSSOValueBytes <= 0 {
		out.ImportMaxSSOValueBytes = base.ImportMaxSSOValueBytes
	}
	if out.ImportJobTimeoutSec <= 0 {
		out.ImportJobTimeoutSec = base.ImportJobTimeoutSec
	}
	if out.ImportStagingStaleAfterSec <= 0 {
		out.ImportStagingStaleAfterSec = base.ImportStagingStaleAfterSec
	}
	if out.ImportSSOEndpoint == "" {
		out.ImportSSOEndpoint = base.ImportSSOEndpoint
	}
	if out.ImportSSOMaxBatch <= 0 {
		out.ImportSSOMaxBatch = base.ImportSSOMaxBatch
	}
	if out.ImportSSOTimeoutSec <= 0 {
		out.ImportSSOTimeoutSec = base.ImportSSOTimeoutSec
	}
	if out.ImportSSOWorkers <= 0 {
		out.ImportSSOWorkers = base.ImportSSOWorkers
	}
	if len(out.AnthropicModelAliases) == 0 {
		out.AnthropicModelAliases = base.AnthropicModelAliases
	}
	if len(out.AnthropicPassthroughPrefixes) == 0 {
		out.AnthropicPassthroughPrefixes = base.AnthropicPassthroughPrefixes
	}
	if out.Listen == "" {
		out.Listen = base.Listen
	}
	if out.DataDir == "" {
		out.DataDir = base.DataDir
	}
	if out.DBPath == "" {
		out.DBPath = base.DBPath
	}
	if out.LoggingLevel == "" {
		out.LoggingLevel = base.LoggingLevel
	}
	if out.OAuthClientID == "" {
		out.OAuthClientID = base.OAuthClientID
	}
	return out
}
