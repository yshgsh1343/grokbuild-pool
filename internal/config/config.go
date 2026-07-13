// Package config 加载 pool-proxy 的 YAML 运行时配置（Scheme B）。
package config

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	protocolconfig "github.com/yshgsh1343/grokbuild2api/internal/protocol/config"
)

const (
	// DefaultListen 为 Docker / 自托管默认端口。
	DefaultListen = "0.0.0.0:8080"
	// DefaultDataDir 为 Scheme B 本地数据根目录。
	DefaultDataDir = "./data"
	// DefaultHotSize 为热索引目标容量。
	DefaultHotSize = 3000
	// DefaultMaxConcurrent 为流/请求并发硬上限。
	DefaultMaxConcurrent = 120
	// DefaultRequestTimeoutSec 限制含 SSE 的完整请求时长。
	DefaultRequestTimeoutSec = 600
	// DefaultMaxBodyBytes 为请求体最大字节数（20 MiB）。
	DefaultMaxBodyBytes = 20 << 20
	// DefaultMaxAttempts 为 lease 失败切换预算。
	DefaultMaxAttempts = 6
	// Admin 浏览器导入默认资源限制。
	// 主闸门是 max_entries（默认 1 万条）；max_upload_bytes=0 表示不限体积。
	// 若显式配置体积上限，则 1 MiB … MaxImportUploadBytes 之间。
	DefaultImportMaxUploadBytes     = 0 // 0 = 不限体积，只按条数限
	DefaultImportRequestOverhead    = 1 << 20
	DefaultImportMaxRequestBytes    = 0 // 0 = 随上传不限；显式上限时需 ≥ upload+overhead
	DefaultImportMaxEntries         = 10_000
	DefaultImportMaxNDJSONLineBytes = 1 << 20
	DefaultImportMaxSSOValueBytes   = 16 << 10
	DefaultImportMaxConcurrentJobs  = 2
	DefaultImportJobTimeoutSec      = 2 * 60 * 60
	DefaultImportStagingStaleSec    = 24 * 60 * 60
	// MaxImportUploadBytes 为显式体积上限的硬顶（2 GiB）；0 仍表示不限。
	MaxImportUploadBytes = 2 << 30
)

// Config 为 pool-proxy 的根运行时配置。
type Config struct {
	Listen            string `yaml:"listen"`
	AllowPublicListen bool   `yaml:"allow_public_listen"`
	DataDir           string `yaml:"data_dir"`
	// DBPath 可选的显式 catalog 路径。为空时 ResolveDBPath 依次尝试
	// data_dir/pool-10000.db 与 data_dir/pool.db。
	DBPath   string `yaml:"db_path"`
	APIKey   string `yaml:"api_key"`
	AdminKey string `yaml:"admin_key"`

	// HotSize 为热索引容量（默认 3000）。
	HotSize int `yaml:"hot_size"`

	// MockUpstream 已废弃：进程始终反代真实 upstream.base_url。
	// 字段仍解析 YAML 以兼容旧配置，但启动时会被忽略。
	MockUpstream bool `yaml:"mock_upstream"`

	Upstream UpstreamConfig `yaml:"upstream"`
	// OAuth 控制真实 refresh 脚手架（HTTPRefreshClient / XaiOAuth）。
	// 默认不向公网发请求：须 POOL_OAUTH_ENABLED=1 且 STATUS UNLOCK_M12=true。
	OAuth     OAuthConfig                    `yaml:"oauth"`
	Selector  SelectorConfig                 `yaml:"selector"`
	Lease     LeaseConfig                    `yaml:"lease"`
	Anthropic protocolconfig.AnthropicConfig `yaml:"anthropic"`
	Limits    LimitsConfig                   `yaml:"limits"`
	Imports   ImportsConfig                  `yaml:"imports"`
	Logging   LoggingConfig                  `yaml:"logging"`
}

// OAuthConfig 为可配置的 token refresh 端点（xAI 风格文档默认）。
// pool-proxy 仅在环境门控 + STATUS 允许时装配 HTTPRefreshClient。
type OAuthConfig struct {
	// RefreshURL 为 token 端点（POST application/x-www-form-urlencoded）。
	// 空 → 代码默认 https://auth.x.ai/oauth2/token（仅文档；仍受门控）。
	RefreshURL string `yaml:"refresh_url"`
	// ClientID 可选 OAuth client_id（xAI 公开 Grok CLI id 可填）。
	ClientID string `yaml:"client_id"`
	// StatusPath 可选 STATUS.md 路径，用于解析 UNLOCK_M12（默认空）。
	StatusPath string `yaml:"status_path"`
}

// UpstreamConfig 控制如何向上游 Grok 发请求（直接反代）。
type UpstreamConfig struct {
	// BaseURL 为含 /v1 的上游基址（必填），例如 https://cli-chat-proxy.grok.com/v1。
	BaseURL          string `yaml:"base_url"`
	ClientVersion    string `yaml:"client_version"`
	ClientIdentifier string `yaml:"client_identifier"`
	UserAgent        string `yaml:"user_agent"`
	TokenAuth        string `yaml:"token_auth"`
}

// SelectorConfig 镜像 selector.Config 的 YAML 面。
type SelectorConfig struct {
	Strategy     string  `yaml:"strategy"`
	HotSize      int     `yaml:"hot_size"`
	StickyTTLSec int64   `yaml:"sticky_ttl_sec"`
	StickyMax    int     `yaml:"sticky_max"`
	Pow2K        int     `yaml:"pow2_k"`
	MaxAttempts  int     `yaml:"max_attempts"`
	WPriority    float64 `yaml:"w_priority"`
	WInflight    float64 `yaml:"w_inflight"`
	WFailure     float64 `yaml:"w_failure"`
	JitterAmp    float64 `yaml:"jitter_amp"`
	// MaxInflightPerAccount 单账号并发硬上限（防封号），默认 4。
	MaxInflightPerAccount int `yaml:"max_inflight_per_account"`
}

// LeaseConfig 镜像 lease.Config 的 YAML 面。
type LeaseConfig struct {
	MaxAttempts                 int   `yaml:"max_attempts"`
	CooldownBaseSec             int64 `yaml:"cooldown_base_sec"`
	CooldownCapSec              int64 `yaml:"cooldown_cap_sec"`
	UnauthorizedCooldownSec     int64 `yaml:"unauthorized_cooldown_sec"`
	PaymentRequiredCooldownSec  int64 `yaml:"payment_required_cooldown_sec"`
	UnauthorizedQuarantineAfter int   `yaml:"unauthorized_quarantine_after"`
	// ForbiddenCooldownSec 403 冷却秒数（默认 900）。
	ForbiddenCooldownSec int64 `yaml:"forbidden_cooldown_sec"`
	// ForbiddenQuarantineAfter 连续 403 隔离阈值；0=关闭（默认）。
	ForbiddenQuarantineAfter int `yaml:"forbidden_quarantine_after"`
	// CooldownJitterPct 冷却抖动百分比（默认 20）。
	CooldownJitterPct int `yaml:"cooldown_jitter_pct"`
}

// LimitsConfig 强制请求体大小、超时与并发上限。
type LimitsConfig struct {
	MaxBodyBytes      int64 `yaml:"max_body_bytes"`
	RequestTimeoutSec int   `yaml:"request_timeout_sec"`
	MaxConcurrent     int   `yaml:"max_concurrent"`
}

// ImportsConfig 控制 Admin 浏览器上传和异步导入限制。
// 主闸门是 MaxEntries；MaxUploadBytes/MaxRequestBytes 为 0 时不限体积。
type ImportsConfig struct {
	Enabled              bool               `yaml:"enabled"`
	MaxUploadBytes       int64              `yaml:"max_upload_bytes"`  // 0 = 不限
	MaxRequestBytes      int64              `yaml:"max_request_bytes"` // 0 = 不限
	MaxEntries           int                `yaml:"max_entries"`
	MaxNDJSONLineBytes   int                `yaml:"max_ndjson_line_bytes"`
	MaxSSOValueBytes     int                `yaml:"max_sso_value_bytes"`
	MaxConcurrentJobs    int                `yaml:"max_concurrent_jobs"`
	JobTimeoutSec        int                `yaml:"job_timeout_sec"`
	StagingStaleAfterSec int                `yaml:"staging_stale_after_sec"`
	AllowServerPath      bool               `yaml:"allow_server_path"`
	SSOConverter         SSOConverterConfig `yaml:"sso_converter"`
}

// SSOConverterConfig 为受信任的服务端 SSO 转换器配置。
type SSOConverterConfig struct {
	Endpoint      string `yaml:"endpoint"`
	APIKey        string `yaml:"api_key"`
	MaxBatch      int    `yaml:"max_batch"`
	TimeoutSec    int    `yaml:"timeout_sec"`
	AllowInsecure bool   `yaml:"allow_insecure"`
}

// LoggingConfig 控制结构化日志详细程度。
type LoggingConfig struct {
	Level string `yaml:"level"`
}

// Default 返回默认值（监听 0.0.0.0:8080，热 3000，max_concurrent 120）。
func Default() Config {
	proto := protocolconfig.Default()
	return Config{
		Listen:            DefaultListen,
		AllowPublicListen: true,
		DataDir:           DefaultDataDir,
		DBPath:            "",
		APIKey:            "",
		AdminKey:          "",
		HotSize:           DefaultHotSize,
		MockUpstream:      false,
		Upstream: UpstreamConfig{
			// 空 base_url → mock 模式，无真实 Grok 时安全冒烟。
			BaseURL:          "",
			ClientVersion:    "0.2.93",
			ClientIdentifier: "grok-pager",
			UserAgent:        "grok-pager/0.2.93 grok-shell/0.2.93 (linux; x86_64)",
			TokenAuth:        "xai-grok-cli",
		},
		// OAuth 文档默认；真实启用仍需 POOL_OAUTH_ENABLED + UNLOCK_M12。
		OAuth: OAuthConfig{
			RefreshURL: "", // → refresh.DefaultXAITokenURL
			ClientID:   "", // 可选；启用时可填 xAI 公开 client_id
			StatusPath: "",
		},
		Selector: SelectorConfig{
			MaxInflightPerAccount: 4,
			Strategy:              "pow2_least_load",
			HotSize:               DefaultHotSize,
			StickyTTLSec:          1800,
			StickyMax:             100_000,
			Pow2K:                 2,
			MaxAttempts:           DefaultMaxAttempts,
			WPriority:             1.0,
			WInflight:             10.0,
			WFailure:              5.0,
			JitterAmp:             0.5,
		},
		Lease: LeaseConfig{
			MaxAttempts:                 DefaultMaxAttempts,
			CooldownBaseSec:             60,
			CooldownCapSec:              900,
			UnauthorizedCooldownSec:     120,
			PaymentRequiredCooldownSec:  300,
			UnauthorizedQuarantineAfter: 3,
			ForbiddenCooldownSec:        900,
			ForbiddenQuarantineAfter:    0,
			CooldownJitterPct:           20,
		},
		Anthropic: proto.Anthropic,
		Limits: LimitsConfig{
			MaxBodyBytes:      DefaultMaxBodyBytes,
			RequestTimeoutSec: DefaultRequestTimeoutSec,
			MaxConcurrent:     DefaultMaxConcurrent,
		},
		Imports: ImportsConfig{
			Enabled:              true,
			MaxUploadBytes:       DefaultImportMaxUploadBytes,
			MaxRequestBytes:      DefaultImportMaxRequestBytes,
			MaxEntries:           DefaultImportMaxEntries,
			MaxNDJSONLineBytes:   DefaultImportMaxNDJSONLineBytes,
			MaxSSOValueBytes:     DefaultImportMaxSSOValueBytes,
			MaxConcurrentJobs:    DefaultImportMaxConcurrentJobs,
			JobTimeoutSec:        DefaultImportJobTimeoutSec,
			StagingStaleAfterSec: DefaultImportStagingStaleSec,
			AllowServerPath:      false,
			SSOConverter: SSOConverterConfig{
				MaxBatch:   50,
				TimeoutSec: 300,
			},
		},
		Logging: LoggingConfig{Level: "info"},
	}
}

// Load 从 path 读取 YAML。path 为空时返回 Default() 且不报错；
// 非空 path 必须存在。
func Load(path string) (Config, error) {
	cfg := Default()
	path = strings.TrimSpace(path)
	if path == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("config: parse %s: multiple YAML documents are not allowed", path)
		}
		return Config{}, fmt.Errorf("config: parse trailing document %s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyDefaults 在 YAML 合并后填充零值字段。
func (c *Config) applyDefaults() {
	d := Default()
	if strings.TrimSpace(c.Listen) == "" {
		c.Listen = d.Listen
	}
	if strings.TrimSpace(c.DataDir) == "" {
		c.DataDir = d.DataDir
	}
	if c.HotSize <= 0 {
		c.HotSize = d.HotSize
	}
	if c.Selector.HotSize <= 0 {
		c.Selector.HotSize = c.HotSize
	}
	if c.Selector.Strategy == "" {
		c.Selector.Strategy = d.Selector.Strategy
	}
	if c.Selector.StickyTTLSec <= 0 {
		c.Selector.StickyTTLSec = d.Selector.StickyTTLSec
	}
	if c.Selector.StickyMax <= 0 {
		c.Selector.StickyMax = d.Selector.StickyMax
	}
	if c.Selector.Pow2K <= 0 {
		c.Selector.Pow2K = d.Selector.Pow2K
	}
	if c.Selector.MaxAttempts <= 0 {
		c.Selector.MaxAttempts = d.Selector.MaxAttempts
	}
	if c.Selector.WPriority == 0 {
		c.Selector.WPriority = d.Selector.WPriority
	}
	if c.Selector.WInflight == 0 {
		c.Selector.WInflight = d.Selector.WInflight
	}
	if c.Selector.WFailure == 0 {
		c.Selector.WFailure = d.Selector.WFailure
	}
	if c.Selector.JitterAmp < 0 {
		c.Selector.JitterAmp = d.Selector.JitterAmp
	}
	if c.Lease.MaxAttempts <= 0 {
		c.Lease.MaxAttempts = d.Lease.MaxAttempts
	}
	if c.Lease.CooldownBaseSec <= 0 {
		c.Lease.CooldownBaseSec = d.Lease.CooldownBaseSec
	}
	if c.Lease.CooldownCapSec <= 0 {
		c.Lease.CooldownCapSec = d.Lease.CooldownCapSec
	}
	if c.Lease.UnauthorizedCooldownSec <= 0 {
		c.Lease.UnauthorizedCooldownSec = d.Lease.UnauthorizedCooldownSec
	}
	if c.Lease.PaymentRequiredCooldownSec <= 0 {
		c.Lease.PaymentRequiredCooldownSec = d.Lease.PaymentRequiredCooldownSec
	}
	if c.Lease.UnauthorizedQuarantineAfter <= 0 {
		c.Lease.UnauthorizedQuarantineAfter = d.Lease.UnauthorizedQuarantineAfter
	}
	if c.Lease.ForbiddenCooldownSec <= 0 {
		c.Lease.ForbiddenCooldownSec = d.Lease.ForbiddenCooldownSec
	}
	// ForbiddenQuarantineAfter: 0 合法（关闭），负值归零
	if c.Lease.ForbiddenQuarantineAfter < 0 {
		c.Lease.ForbiddenQuarantineAfter = 0
	}
	if c.Limits.MaxBodyBytes <= 0 {
		c.Limits.MaxBodyBytes = d.Limits.MaxBodyBytes
	}
	if c.Limits.RequestTimeoutSec <= 0 {
		c.Limits.RequestTimeoutSec = d.Limits.RequestTimeoutSec
	}
	if c.Limits.MaxConcurrent <= 0 {
		c.Limits.MaxConcurrent = d.Limits.MaxConcurrent
	}
	// MaxUploadBytes / MaxRequestBytes：0 表示不限体积，不回填默认正数。
	// 仅负值视为未配置并回退默认（默认亦为 0）。
	if c.Imports.MaxUploadBytes < 0 {
		c.Imports.MaxUploadBytes = d.Imports.MaxUploadBytes
	}
	if c.Imports.MaxRequestBytes < 0 {
		c.Imports.MaxRequestBytes = d.Imports.MaxRequestBytes
	}
	if c.Imports.MaxEntries <= 0 {
		c.Imports.MaxEntries = d.Imports.MaxEntries
	}
	if c.Imports.MaxNDJSONLineBytes <= 0 {
		c.Imports.MaxNDJSONLineBytes = d.Imports.MaxNDJSONLineBytes
	}
	if c.Imports.MaxSSOValueBytes <= 0 {
		c.Imports.MaxSSOValueBytes = d.Imports.MaxSSOValueBytes
	}
	if c.Imports.MaxConcurrentJobs <= 0 {
		c.Imports.MaxConcurrentJobs = d.Imports.MaxConcurrentJobs
	}
	if c.Imports.JobTimeoutSec <= 0 {
		c.Imports.JobTimeoutSec = d.Imports.JobTimeoutSec
	}
	if c.Imports.StagingStaleAfterSec <= 0 {
		c.Imports.StagingStaleAfterSec = d.Imports.StagingStaleAfterSec
	}
	if c.Imports.SSOConverter.MaxBatch <= 0 {
		c.Imports.SSOConverter.MaxBatch = d.Imports.SSOConverter.MaxBatch
	}
	if c.Imports.SSOConverter.TimeoutSec <= 0 {
		c.Imports.SSOConverter.TimeoutSec = d.Imports.SSOConverter.TimeoutSec
	}
	if strings.TrimSpace(c.Logging.Level) == "" {
		c.Logging.Level = d.Logging.Level
	}
	if c.Anthropic.ModelAliases == nil {
		c.Anthropic = d.Anthropic
	} else if !c.Anthropic.Enabled && len(c.Anthropic.ModelAliases) == 0 {
		// 显式全空则保持禁用；否则在默认启用标志缺失时填充默认
	}
	// 不再在 applyDefaults 里把 Anthropic 整段重置为 Default()：
	// 旧逻辑会在 `anthropic.enabled: false` 且未写 aliases 时把 Enabled 打回 true。
	// Load 以 Default() 为底再 Decode，省略 anthropic 段时仍保持默认启用。
	if c.Anthropic.ModelAliases == nil {
		c.Anthropic.ModelAliases = d.Anthropic.ModelAliases
	}
	if c.Anthropic.PassthroughPrefixes == nil {
		c.Anthropic.PassthroughPrefixes = append([]string(nil), d.Anthropic.PassthroughPrefixes...)
	}
	if c.Upstream.ClientVersion == "" {
		c.Upstream.ClientVersion = d.Upstream.ClientVersion
	}
	if c.Upstream.ClientIdentifier == "" {
		c.Upstream.ClientIdentifier = d.Upstream.ClientIdentifier
	}
	if c.Upstream.UserAgent == "" {
		c.Upstream.UserAgent = d.Upstream.UserAgent
	}
	if c.Upstream.TokenAuth == "" {
		c.Upstream.TokenAuth = d.Upstream.TokenAuth
	}
	if strings.TrimSpace(c.OAuth.StatusPath) == "" {
		c.OAuth.StatusPath = d.OAuth.StatusPath
	}
}

// Validate 检查监听绑定策略与基本边界。
func (c Config) Validate() error {
	if err := c.ValidateListen(c.Listen); err != nil {
		return err
	}
	if strings.TrimSpace(c.Upstream.BaseURL) == "" {
		return fmt.Errorf("config: upstream.base_url is required (direct reverse-proxy; mock upstream removed)")
	}
	adminKey := strings.TrimSpace(c.AdminKey)
	if isPlaceholderAdminKey(adminKey) {
		return fmt.Errorf("config: refusing placeholder admin_key")
	}
	if !isLoopbackListen(c.Listen) && adminKey == "" {
		return fmt.Errorf("config: public listen requires a non-empty admin_key")
	}
	if c.Limits.MaxConcurrent > 10_000 {
		return fmt.Errorf("config: max_concurrent %d too large", c.Limits.MaxConcurrent)
	}
	if c.HotSize > 200_000 {
		return fmt.Errorf("config: hot_size %d too large", c.HotSize)
	}
	// 0 = 不限体积（只按 max_entries）；>0 时 1 MiB … MaxImportUploadBytes。
	if c.Imports.MaxUploadBytes < 0 || c.Imports.MaxUploadBytes > MaxImportUploadBytes {
		return fmt.Errorf("config: imports.max_upload_bytes must be 0 (unlimited) or between 1 and %d", MaxImportUploadBytes)
	}
	if c.Imports.MaxUploadBytes > 0 && c.Imports.MaxUploadBytes < 1<<20 {
		return fmt.Errorf("config: imports.max_upload_bytes must be 0 (unlimited) or at least 1 MiB")
	}
	if c.Imports.MaxRequestBytes < 0 || c.Imports.MaxRequestBytes > MaxImportUploadBytes+DefaultImportRequestOverhead {
		return fmt.Errorf("config: imports.max_request_bytes must be 0 (unlimited) or <= %d", MaxImportUploadBytes+DefaultImportRequestOverhead)
	}
	if c.Imports.MaxUploadBytes > 0 && c.Imports.MaxRequestBytes > 0 &&
		c.Imports.MaxRequestBytes < c.Imports.MaxUploadBytes+DefaultImportRequestOverhead {
		return fmt.Errorf("config: imports.max_request_bytes must exceed max_upload_bytes by at least 1 MiB when both are set")
	}
	if c.Imports.MaxEntries < 1 || c.Imports.MaxEntries > 100_000 {
		return fmt.Errorf("config: imports.max_entries must be between 1 and 100000")
	}
	if c.Imports.MaxNDJSONLineBytes < 4<<10 || c.Imports.MaxNDJSONLineBytes > 4<<20 {
		return fmt.Errorf("config: imports.max_ndjson_line_bytes must be between 4 KiB and 4 MiB")
	}
	if c.Imports.MaxSSOValueBytes < 1<<10 || c.Imports.MaxSSOValueBytes > 64<<10 {
		return fmt.Errorf("config: imports.max_sso_value_bytes must be between 1 KiB and 64 KiB")
	}
	if c.Imports.MaxConcurrentJobs < 1 || c.Imports.MaxConcurrentJobs > 8 {
		return fmt.Errorf("config: imports.max_concurrent_jobs must be between 1 and 8")
	}
	if c.Imports.JobTimeoutSec < 60 || c.Imports.JobTimeoutSec > 24*60*60 {
		return fmt.Errorf("config: imports.job_timeout_sec must be between 60 and 86400")
	}
	if c.Imports.StagingStaleAfterSec < c.Imports.JobTimeoutSec {
		return fmt.Errorf("config: imports.staging_stale_after_sec must be >= job_timeout_sec")
	}
	if c.Imports.SSOConverter.MaxBatch < 1 || c.Imports.SSOConverter.MaxBatch > 100 {
		return fmt.Errorf("config: imports.sso_converter.max_batch must be between 1 and 100")
	}
	if c.Imports.SSOConverter.TimeoutSec < 1 || c.Imports.SSOConverter.TimeoutSec > 300 {
		return fmt.Errorf("config: imports.sso_converter.timeout_sec must be between 1 and 300")
	}
	return nil
}

func isPlaceholderAdminKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "dev-admin-change-me", "change-me", "changeme", "replace-me":
		return true
	default:
		return false
	}
}

func isLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateListen 拒绝非 loopback 绑定，除非设置 AllowPublicListen。
func (c Config) ValidateListen(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("config: empty listen address")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("config: listen %q: %w", addr, err)
	}
	if strings.TrimSpace(port) == "" {
		return fmt.Errorf("config: listen %q: missing port", addr)
	}
	if c.AllowPublicListen {
		return nil
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return fmt.Errorf("config: public listen %q requires allow_public_listen: true", addr)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		// hostname — only localhost allowed without flag
		if !strings.EqualFold(host, "localhost") {
			return fmt.Errorf("config: non-loopback listen host %q requires allow_public_listen: true", host)
		}
		return nil
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("config: non-loopback listen %q requires allow_public_listen: true", addr)
	}
	return nil
}

// RequestTimeout 返回配置的请求超时时长。
func (c Config) RequestTimeout() time.Duration {
	sec := c.Limits.RequestTimeoutSec
	if sec <= 0 {
		sec = DefaultRequestTimeoutSec
	}
	return time.Duration(sec) * time.Second
}

// UseMockUpstream 始终 false：内置 mock 上游已移除，仅反代真实 upstream.base_url。
// 保留方法以兼容旧调用点（admin 快照等）。
func (c Config) UseMockUpstream() bool {
	return false
}

// ResolveDBPath 选择 catalog SQLite 文件。
//
// 优先顺序：
//  1. 已设置的 cfg.DBPath（可指向尚不存在的路径，由 catalog.Open 创建）
//  2. 若存在 data_dir/pool-10000.db
//  3. 若存在 data_dir/pool.db
//  4. 默认 data_dir/pool.db（首次启动自动创建空库）
func (c Config) ResolveDBPath() (string, error) {
	if p := strings.TrimSpace(c.DBPath); p != "" {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return "", fmt.Errorf("config: mkdir for db_path %s: %w", p, err)
		}
		return p, nil
	}
	dir := strings.TrimSpace(c.DataDir)
	if dir == "" {
		dir = DefaultDataDir
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("config: mkdir data_dir %s: %w", dir, err)
	}
	candidates := []string{
		filepath.Join(dir, "pool-10000.db"),
		filepath.Join(dir, "pool.db"),
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	// 首次启动：返回默认路径，catalog.Open 会创建空库
	return filepath.Join(dir, "pool.db"), nil
}

// EffectiveHotSize 从根或 selector 段返回热索引容量。
func (c Config) EffectiveHotSize() int {
	if c.HotSize > 0 {
		return c.HotSize
	}
	if c.Selector.HotSize > 0 {
		return c.Selector.HotSize
	}
	return DefaultHotSize
}

// ApplyDefaultsPublic 在 flag/env 覆盖后再次应用零值默认。
func (c *Config) ApplyDefaultsPublic() {
	c.applyDefaults()
}
