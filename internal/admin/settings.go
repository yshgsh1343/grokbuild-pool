package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/hot"
	"github.com/yshgsh1343/grokbuild2api/internal/lease"
	"github.com/yshgsh1343/grokbuild2api/internal/refresh"
	"github.com/yshgsh1343/grokbuild2api/internal/selector"
)

// RuntimeSettings 管理台可编辑的全部运行参数。
// 能热更的即时生效；需重启的字段仍持久化到 settings.json 供下次启动/运维参考。
type RuntimeSettings struct {
	// —— 选号 / 热池 ——
	SelectorStrategy      string  `json:"selector_strategy"`
	HotSize               int     `json:"hot_size"`
	MaxInflightPerAccount int32   `json:"max_inflight_per_account"`
	StickyTTLSec          int64   `json:"sticky_ttl_sec"`
	StickyMax             int     `json:"sticky_max"`
	Pow2K                 int     `json:"pow2_k"`
	WPriority             float64 `json:"w_priority"`
	WInflight             float64 `json:"w_inflight"`
	WFailure              float64 `json:"w_failure"`
	JitterAmp             float64 `json:"jitter_amp"`
	SelectorMaxAttempts   int     `json:"selector_max_attempts"`

	// —— 租约 / 防封号冷却 ——
	MaxAttempts                 int   `json:"max_attempts"`
	CooldownBaseSec             int64 `json:"cooldown_base_sec"`
	CooldownCapSec              int64 `json:"cooldown_cap_sec"`
	UnauthorizedCooldownSec     int64 `json:"unauthorized_cooldown_sec"`
	PaymentRequiredCooldownSec  int64 `json:"payment_required_cooldown_sec"`
	UnauthorizedQuarantineAfter int   `json:"unauthorized_quarantine_after"`
	ForbiddenCooldownSec        int64 `json:"forbidden_cooldown_sec"`
	ForbiddenQuarantineAfter    int   `json:"forbidden_quarantine_after"`
	CooldownJitterPct           int   `json:"cooldown_jitter_pct"`
	CooldownExpMax              int   `json:"cooldown_exp_max"`

	// —— 进程限制 ——
	MaxConcurrent     int   `json:"max_concurrent"`
	MaxBodyBytes      int64 `json:"max_body_bytes"`
	RequestTimeoutSec int   `json:"request_timeout_sec"`

	// —— 刷新 workers ——
	RefreshWorkers int     `json:"refresh_workers"`
	RefreshQPS     float64 `json:"refresh_qps"`
	RefreshSkewSec int64   `json:"refresh_skew_sec"`

	// —— 令牌创建默认模板 ——
	TokenDefaultRemainQuota   int64 `json:"token_default_remain_quota"`
	TokenDefaultMaxConcurrent int   `json:"token_default_max_concurrent"`
	TokenDefaultRPM           int   `json:"token_default_rpm"`
	TokenDefaultUnlimited     bool  `json:"token_default_unlimited"`

	// —— 导入（热更限制；SSO endpoint/key 写盘供运维，连接器热更需 endpoint+key）——
	ImportEnabled              bool   `json:"import_enabled"`
	ImportMaxUploadBytes       int64  `json:"import_max_upload_bytes"`
	ImportMaxEntries           int    `json:"import_max_entries"`
	ImportMaxConcurrentJobs    int    `json:"import_max_concurrent_jobs"`
	ImportWorkers              int    `json:"import_workers"`
	ImportMaxNDJSONLineBytes   int    `json:"import_max_ndjson_line_bytes"`
	ImportMaxSSOValueBytes     int    `json:"import_max_sso_value_bytes"`
	ImportJobTimeoutSec        int    `json:"import_job_timeout_sec"`
	ImportStagingStaleAfterSec int    `json:"import_staging_stale_after_sec"`
	ImportAllowServerPath      bool   `json:"import_allow_server_path"`
	ImportSSOEndpoint          string `json:"import_sso_endpoint"`
	ImportSSOAPIKeySet         bool   `json:"import_sso_api_key_set"` // 只读展示
	ImportSSOAPIKey            string `json:"import_sso_api_key,omitempty"` // 仅 PUT 写入，GET 不回传明文
	ImportSSOMaxBatch          int    `json:"import_sso_max_batch"`
	ImportSSOTimeoutSec        int    `json:"import_sso_timeout_sec"`
	ImportSSOAllowInsecure     bool   `json:"import_sso_allow_insecure"`
	ImportSSOWorkers           int    `json:"import_sso_workers"`

	// —— Anthropic / 模型别名（热更）——
	AnthropicEnabled             bool              `json:"anthropic_enabled"`
	AnthropicStripUnknownBetas   bool              `json:"anthropic_strip_unknown_betas"`
	AnthropicCountTokens         bool              `json:"anthropic_count_tokens"`
	AnthropicPassthroughPrefixes []string          `json:"anthropic_passthrough_prefixes"`
	AnthropicModelAliases        map[string]string `json:"anthropic_model_aliases"`

	// —— 部署 / 上游（可编辑并落盘；listen 等绑定需重启才真正换端口）——
	Listen             string `json:"listen"`
	AllowPublicListen  bool   `json:"allow_public_listen"`
	DataDir            string `json:"data_dir"`
	DBPath             string `json:"db_path"`
	MockUpstream       bool   `json:"mock_upstream"`
	UpstreamBaseURL    string `json:"upstream_base_url"`
	OAuthRefreshURL    string `json:"oauth_refresh_url"`
	OAuthClientID      string `json:"oauth_client_id"`
	APIKeyConfigured   bool   `json:"api_key_configured"`
	AdminKeyConfigured bool   `json:"admin_key_configured"`
	// 可选写入新密钥（GET 永不回传明文）
	APIKey   string `json:"api_key,omitempty"`
	AdminKey string `json:"admin_key,omitempty"`
	LoggingLevel string `json:"logging_level"`

	// RestartHint 非空时提示哪些变更需重启
	RestartHint string `json:"restart_hint,omitempty"`
}

// SettingsSnapshot 为 GET 响应：运行时设置 + 可选持久化路径。
type SettingsSnapshot struct {
	RuntimeSettings
	// PersistedPath 非空时表示设置会原子写入该 JSON 文件。
	PersistedPath string `json:"persisted_path,omitempty"`
}

// SettingsController 持有可变运行时参数并应用到 hot/lease/import/anthropic。
type SettingsController struct {
	mu sync.RWMutex
	s  RuntimeSettings

	// Path 持久化文件路径（如 data/settings.json）；空则不落盘。
	Path string

	Hot      *hot.Index
	Lease    *lease.Manager
	Selector *selector.Selector
	Refresh  *refresh.Service
	// SetGlobalMaxConcurrent 可选：更新 HTTP 全局并发（0 = 不限制）
	SetGlobalMaxConcurrent func(n int)
	// SetMaxBodyBytes 可选：热更新请求体上限（0 = 不限制）
	SetMaxBodyBytes func(n int64)
	// SetRequestTimeout 可选：热更新整请求超时（0 = 不设）
	SetRequestTimeout func(d time.Duration)
	// ApplyImport 可选：热更新导入限制（含 SSO 转换器）
	ApplyImport func(in RuntimeSettings) error
	// ApplyAnthropic 可选：热更新 Anthropic 别名/开关
	ApplyAnthropic func(in RuntimeSettings)
	// storedAPIKey / storedAdminKey 仅内存持有明文（GET 不回传）
	storedAPIKey   string
	storedAdminKey string
	// ProcessInfo 进程启动时的绑定信息（listen 等）；密钥类以配置为准
	ProcessInfo RuntimeSettings
	// lastSSOAPIKey 仅内存，供 ApplyImport 在 PUT 未传新 key 时复用
	lastSSOAPIKey string
}

// SeedSecrets 启动时注入配置文件中的密钥（不落盘、GET 不回传）。
func (c *SettingsController) SeedSecrets(apiKey, adminKey, ssoAPIKey string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(apiKey) != "" {
		c.storedAPIKey = strings.TrimSpace(apiKey)
	}
	if strings.TrimSpace(adminKey) != "" {
		c.storedAdminKey = strings.TrimSpace(adminKey)
	}
	if strings.TrimSpace(ssoAPIKey) != "" {
		c.lastSSOAPIKey = strings.TrimSpace(ssoAPIKey)
	}
}

// PeekSSOAPIKey 返回当前内存中的 SSO API key（供 ApplyImport 使用）。
func (c *SettingsController) PeekSSOAPIKey() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastSSOAPIKey
}

// PeekAdminKey 返回当前内存中的 admin key（供鉴权热更新）。
func (c *SettingsController) PeekAdminKey() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.storedAdminKey
}

// PeekAPIKey 返回当前内存中的静态 API key。
func (c *SettingsController) PeekAPIKey() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.storedAPIKey
}

// Snapshot 返回当前设置副本（含可选 PersistedPath）。
// 永不回传 api_key / admin_key / import_sso_api_key 明文。
func (c *SettingsController) Snapshot() SettingsSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := c.s
	out.APIKey = ""
	out.AdminKey = ""
	out.ImportSSOAPIKey = ""
	out.APIKeyConfigured = strings.TrimSpace(c.storedAPIKey) != "" || c.ProcessInfo.APIKeyConfigured
	out.AdminKeyConfigured = strings.TrimSpace(c.storedAdminKey) != "" || c.ProcessInfo.AdminKeyConfigured
	out.ImportSSOAPIKeySet = strings.TrimSpace(out.ImportSSOEndpoint) != "" && (out.ImportSSOAPIKeySet || c.ProcessInfo.ImportSSOAPIKeySet)
	return SettingsSnapshot{
		RuntimeSettings: out,
		PersistedPath:   c.Path,
	}
}

// Load 从 Path 读取 JSON 并覆盖内存设置（不触发 hot/lease 副作用；
// 调用方应在启动时 Load 后再 Apply 以应用运行时副作用）。
// 文件不存在时返回 nil（保持当前默认）。
func (c *SettingsController) Load() error {
	if c == nil {
		return nil
	}
	path := c.Path
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("settings: load %s: %w", path, err)
	}
	var in RuntimeSettings
	if err := json.Unmarshal(data, &in); err != nil {
		return fmt.Errorf("settings: parse %s: %w", path, err)
	}
	c.mu.Lock()
	c.s = in
	c.mu.Unlock()
	return nil
}

// persistLocked 原子写入 JSON：先写临时文件再 rename。
// 调用方须在持锁或持有稳定快照后调用；本方法自行读 c.s。
func (c *SettingsController) persist() error {
	path := c.Path
	if path == "" {
		return nil
	}
	c.mu.RLock()
	snap := c.s
	c.mu.RUnlock()
	// 落盘绝不写密钥明文
	snap.APIKey = ""
	snap.AdminKey = ""
	snap.ImportSSOAPIKey = ""

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("settings: marshal: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("settings: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("settings: temp: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("settings: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("settings: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("settings: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("settings: rename: %w", err)
	}
	ok = true
	return nil
}

// Apply 校验并应用设置（部分参数即时生效）；成功后原子落盘。
func (c *SettingsController) Apply(in RuntimeSettings) (RuntimeSettings, error) {
	// 边界
	if in.MaxInflightPerAccount < 0 {
		in.MaxInflightPerAccount = 0
	}
	if in.MaxInflightPerAccount > 64 {
		in.MaxInflightPerAccount = 64
	}
	if in.CooldownBaseSec < 1 {
		in.CooldownBaseSec = 1
	}
	if in.CooldownCapSec < in.CooldownBaseSec {
		in.CooldownCapSec = in.CooldownBaseSec
	}
	if in.ForbiddenCooldownSec < 1 {
		in.ForbiddenCooldownSec = 1
	}
	if in.CooldownJitterPct < 0 {
		in.CooldownJitterPct = 0
	}
	if in.CooldownJitterPct > 50 {
		in.CooldownJitterPct = 50
	}
	if in.MaxConcurrent < 0 {
		in.MaxConcurrent = 0
	}
	if in.MaxConcurrent > 10000 {
		in.MaxConcurrent = 10000
	}
	if in.Pow2K < 1 {
		in.Pow2K = 1
	}
	if in.Pow2K > 16 {
		in.Pow2K = 16
	}
	if in.MaxAttempts < 1 {
		in.MaxAttempts = 1
	}
	if in.MaxAttempts > 32 {
		in.MaxAttempts = 32
	}
	if in.StickyMax < 0 {
		in.StickyMax = 0
	}
	if in.StickyMax > 1_000_000 {
		in.StickyMax = 1_000_000
	}
	if in.StickyTTLSec < 0 {
		in.StickyTTLSec = 0
	}
	if in.ForbiddenQuarantineAfter < 0 {
		in.ForbiddenQuarantineAfter = 0
	}
	if in.RefreshWorkers < 0 {
		in.RefreshWorkers = 0
	}
	if in.RefreshWorkers > 4 {
		in.RefreshWorkers = 4
	}
	if in.RefreshQPS < 0 {
		in.RefreshQPS = 0
	}
	if in.RefreshQPS > 1000 {
		in.RefreshQPS = 1000
	}
	if in.MaxBodyBytes < 0 {
		in.MaxBodyBytes = 0
	}
	if in.RequestTimeoutSec < 0 {
		in.RequestTimeoutSec = 0
	}
	// 与旧快照合并：空字段保留原值（全量表单 PUT 也会带齐；兼容部分字段）
	prev := c.Snapshot().RuntimeSettings
	c.mu.Lock()
	prevStoredAPI := c.storedAPIKey
	prevStoredAdmin := c.storedAdminKey
	pi := c.ProcessInfo
	c.mu.Unlock()

	if in.SelectorStrategy == "" {
		in.SelectorStrategy = prev.SelectorStrategy
		if in.SelectorStrategy == "" {
			in.SelectorStrategy = "pow2_least_load"
		}
	}
	if in.HotSize <= 0 {
		in.HotSize = prev.HotSize
	}
	if in.Listen == "" {
		in.Listen = prev.Listen
	}
	if in.DataDir == "" {
		in.DataDir = prev.DataDir
	}
	if in.DBPath == "" {
		in.DBPath = prev.DBPath
	}
	if in.LoggingLevel == "" {
		in.LoggingLevel = prev.LoggingLevel
	}
	if in.OAuthClientID == "" {
		in.OAuthClientID = prev.OAuthClientID
	}
	if in.ImportMaxUploadBytes <= 0 {
		in.ImportMaxUploadBytes = prev.ImportMaxUploadBytes
	}
	if in.ImportMaxEntries <= 0 {
		in.ImportMaxEntries = prev.ImportMaxEntries
	}
	if in.ImportMaxConcurrentJobs <= 0 {
		in.ImportMaxConcurrentJobs = prev.ImportMaxConcurrentJobs
	}
	if in.ImportWorkers <= 0 {
		in.ImportWorkers = prev.ImportWorkers
	}
	if in.ImportMaxNDJSONLineBytes <= 0 {
		in.ImportMaxNDJSONLineBytes = prev.ImportMaxNDJSONLineBytes
	}
	if in.ImportMaxSSOValueBytes <= 0 {
		in.ImportMaxSSOValueBytes = prev.ImportMaxSSOValueBytes
	}
	if in.ImportJobTimeoutSec <= 0 {
		in.ImportJobTimeoutSec = prev.ImportJobTimeoutSec
	}
	if in.ImportStagingStaleAfterSec <= 0 {
		in.ImportStagingStaleAfterSec = prev.ImportStagingStaleAfterSec
	}
	if in.ImportSSOEndpoint == "" {
		in.ImportSSOEndpoint = prev.ImportSSOEndpoint
	}
	if in.ImportSSOMaxBatch <= 0 {
		in.ImportSSOMaxBatch = prev.ImportSSOMaxBatch
	}
	if in.ImportSSOTimeoutSec <= 0 {
		in.ImportSSOTimeoutSec = prev.ImportSSOTimeoutSec
	}
	if in.ImportSSOWorkers <= 0 {
		in.ImportSSOWorkers = prev.ImportSSOWorkers
	}
	if len(in.AnthropicModelAliases) == 0 && len(prev.AnthropicModelAliases) > 0 {
		in.AnthropicModelAliases = prev.AnthropicModelAliases
	}
	if len(in.AnthropicPassthroughPrefixes) == 0 && len(prev.AnthropicPassthroughPrefixes) > 0 {
		in.AnthropicPassthroughPrefixes = prev.AnthropicPassthroughPrefixes
	}

	// 密钥：空表示不改；非空则更新内存持有
	newAPI := strings.TrimSpace(in.APIKey)
	newAdmin := strings.TrimSpace(in.AdminKey)
	newSSOKey := strings.TrimSpace(in.ImportSSOAPIKey)
	in.APIKey = ""
	in.AdminKey = ""
	in.ImportSSOAPIKey = ""

	// 导入数值边界
	if in.ImportMaxUploadBytes > 256<<20 {
		in.ImportMaxUploadBytes = 256 << 20
	}
	if in.ImportMaxEntries > 100_000 {
		in.ImportMaxEntries = 100_000
	}
	if in.ImportMaxConcurrentJobs > 8 {
		in.ImportMaxConcurrentJobs = 8
	}
	if in.ImportWorkers > 16 {
		in.ImportWorkers = 16
	}
	if in.ImportSSOMaxBatch > 100 {
		in.ImportSSOMaxBatch = 100
	}
	if in.ImportSSOWorkers > 16 {
		in.ImportSSOWorkers = 16
	}

	// 重启提示：端口/数据路径/mock 切换
	var restart []string
	if pi.Listen != "" && in.Listen != "" && in.Listen != pi.Listen {
		restart = append(restart, "listen")
	}
	if pi.DataDir != "" && in.DataDir != "" && in.DataDir != pi.DataDir {
		restart = append(restart, "data_dir")
	}
	if pi.DBPath != "" && in.DBPath != "" && in.DBPath != pi.DBPath {
		restart = append(restart, "db_path")
	}
	if in.MockUpstream != pi.MockUpstream {
		restart = append(restart, "mock_upstream")
	}
	if in.UpstreamBaseURL != pi.UpstreamBaseURL {
		restart = append(restart, "upstream_base_url")
	}
	// hot_size / refresh_workers 持久化但运行时无法完整热更（索引容量与 worker 数在启动时固定）
	if in.HotSize != prev.HotSize {
		restart = append(restart, "hot_size")
	}
	if in.RefreshWorkers != prev.RefreshWorkers {
		restart = append(restart, "refresh_workers")
	}
	if len(restart) > 0 {
		in.RestartHint = "以下字段已保存，需重启进程后完全生效: " + strings.Join(restart, ", ")
	} else {
		in.RestartHint = ""
	}

	// 密钥配置标志
	storedAPI := prevStoredAPI
	if newAPI != "" {
		storedAPI = newAPI
	}
	storedAdmin := prevStoredAdmin
	if newAdmin != "" {
		storedAdmin = newAdmin
	}
	in.APIKeyConfigured = strings.TrimSpace(storedAPI) != "" || pi.APIKeyConfigured
	in.AdminKeyConfigured = strings.TrimSpace(storedAdmin) != "" || pi.AdminKeyConfigured
	in.ImportSSOAPIKeySet = strings.TrimSpace(in.ImportSSOEndpoint) != "" && (newSSOKey != "" || prev.ImportSSOAPIKeySet || pi.ImportSSOAPIKeySet)

	c.mu.Lock()
	c.s = in
	if newAPI != "" {
		c.storedAPIKey = newAPI
	}
	if newAdmin != "" {
		c.storedAdminKey = newAdmin
	}
	// 把 SSO key 暂存在 ProcessInfo 旁路字段不合适；经 ApplyImport 回调传入
	c.mu.Unlock()

	// 即时生效：账号 inflight 硬限
	if c.Hot != nil {
		c.Hot.SetMaxInflightPerAccount(in.MaxInflightPerAccount)
	}
	// 即时生效：租约冷却
	if c.Lease != nil {
		lc := c.Lease.Config()
		lc.MaxAttempts = in.MaxAttempts
		lc.CooldownBaseSec = in.CooldownBaseSec
		lc.CooldownCapSec = in.CooldownCapSec
		lc.UnauthorizedCooldownSec = in.UnauthorizedCooldownSec
		lc.PaymentRequiredCooldownSec = in.PaymentRequiredCooldownSec
		lc.UnauthorizedQuarantineAfter = in.UnauthorizedQuarantineAfter
		lc.ForbiddenCooldownSec = in.ForbiddenCooldownSec
		lc.ForbiddenQuarantineAfter = in.ForbiddenQuarantineAfter
		lc.CooldownJitterPct = in.CooldownJitterPct
		lc.CooldownExpMax = in.CooldownExpMax
		c.Lease.ApplyConfig(lc)
	}
	// 即时生效：选号权重 / sticky / strategy
	if c.Selector != nil {
		sc := c.Selector.Config()
		if in.SelectorStrategy != "" {
			sc.Strategy = in.SelectorStrategy
		}
		sc.HotSize = in.HotSize
		sc.StickyTTLSec = in.StickyTTLSec
		sc.StickyMax = in.StickyMax
		sc.Pow2K = in.Pow2K
		sc.MaxAttempts = in.SelectorMaxAttempts
		if sc.MaxAttempts <= 0 {
			sc.MaxAttempts = in.MaxAttempts
		}
		sc.WPriority = in.WPriority
		sc.WInflight = in.WInflight
		sc.WFailure = in.WFailure
		sc.JitterAmp = in.JitterAmp
		sc.MaxInflightPerAccount = in.MaxInflightPerAccount
		c.Selector.ApplyConfig(sc)
	}
	// 即时生效：刷新 QPS/Skew
	if c.Refresh != nil {
		rc := c.Refresh.Config()
		rc.Workers = in.RefreshWorkers
		rc.QPS = in.RefreshQPS
		rc.SkewSec = in.RefreshSkewSec
		c.Refresh.ApplyConfig(rc)
	}
	// HTTP 全局并发 / body / 超时（允许 0）
	if c.SetGlobalMaxConcurrent != nil {
		c.SetGlobalMaxConcurrent(in.MaxConcurrent)
	}
	if c.SetMaxBodyBytes != nil {
		c.SetMaxBodyBytes(in.MaxBodyBytes)
	}
	if c.SetRequestTimeout != nil {
		c.SetRequestTimeout(time.Duration(in.RequestTimeoutSec) * time.Second)
	}
	// 导入 + Anthropic
	if c.ApplyAnthropic != nil {
		c.ApplyAnthropic(in)
	}
	if c.ApplyImport != nil {
		// 把本次新 SSO key 临时塞回（不落 JSON 明文时可在回调内使用后丢弃）
		pass := in
		pass.ImportSSOAPIKey = newSSOKey
		if err := c.ApplyImport(pass); err != nil {
			return in, err
		}
		// 成功后标记已配置
		if newSSOKey != "" {
			c.mu.Lock()
			c.s.ImportSSOAPIKeySet = true
			c.lastSSOAPIKey = newSSOKey
			c.mu.Unlock()
			in.ImportSSOAPIKeySet = true
		}
	}

	// 成功后原子落盘（失败不回滚内存，但返回错误以便调用方感知）
	if err := c.persist(); err != nil {
		return in, err
	}
	// 返回给客户端的快照去掉密钥
	out := in
	out.APIKey = ""
	out.AdminKey = ""
	out.ImportSSOAPIKey = ""
	return out, nil
}

// GetSettings GET /admin/settings
func (h *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
	if h.Settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "设置控制器未启用")
		return
	}
	writeJSON(w, http.StatusOK, h.Settings.Snapshot())
}

// PutSettings PUT /admin/settings — 全量更新可热改参数
func (h *Handlers) PutSettings(w http.ResponseWriter, r *http.Request) {
	if h.Settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "设置控制器未启用")
		return
	}
	var in RuntimeSettings
	if err := decodeJSON(r, 1<<20, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := h.Settings.Apply(in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// persisted:true 表示已原子写入 Path（Path 为空时为 false，仅内存生效）
	persisted := strings.TrimSpace(h.Settings.Path) != ""
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"persisted": persisted,
		"settings":  out,
	})
}
