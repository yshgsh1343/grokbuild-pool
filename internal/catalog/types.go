package catalog

import "errors"

// catalog 操作的哨兵错误。
var (
	ErrNotFound     = errors.New("catalog: account not found")
	ErrCASConflict  = errors.New("catalog: revision conflict")
	ErrInvalidInput = errors.New("catalog: invalid input")
	ErrClosed       = errors.New("catalog: closed")
)

// accounts.lifecycle 字段的取值。
const (
	LifecycleActive      = "active"
	LifecycleQuarantined = "quarantined"
	LifecyclePurged      = "purged"
)

// Account 是包含密钥的完整冷存储行。
// 令牌绝不可记录日志，也不可通过 ListEligible / HotMeta 返回。
type Account struct {
	ID                      string
	Revision                int64
	IdentityKey             string
	Email                   string
	Name                    string
	Priority                int
	Enabled                 bool
	ManualDisabled          bool
	Lifecycle               string
	AccessToken             string
	RefreshToken            string
	ExpiresAt               int64 // unix seconds
	ProxyMode               string
	ProxyURL                string
	FailureCount            int
	SuccessCount            int
	CooldownUntil           int64 // unix seconds; 0 = none
	LastError               string
	LastUsedAt              *int64
	LastSuccessAt           *int64
	LastRefreshAt           *int64
	ConsecutiveUnauthorized int
	QuarantineFP            string
	PurgeAfter              *int64
	BillingJSON             string
	CreatedAt               int64
	UpdatedAt               int64
}

// TokenSet 是 UpdateTokens 写入的可变 OAuth 令牌字段。
type TokenSet struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64 // unix seconds
}

// HealthPatch 为部分健康/状态更新；nil 指针字段保持不变。
type HealthPatch struct {
	Enabled                 *bool
	ManualDisabled          *bool
	Lifecycle               *string
	FailureCount            *int
	SuccessCount            *int
	CooldownUntil           *int64
	LastError               *string
	LastUsedAt              *int64
	LastSuccessAt           *int64
	LastRefreshAt           *int64
	ConsecutiveUnauthorized *int
	QuarantineFP            *string
	PurgeAfter              *int64
	BillingJSON             *string
	// ClearLastError 为 true 时将 last_error 置空（即使 LastError 为 nil）。
	ClearLastError bool
}

// HotMeta 是供热索引与选择器使用的无密钥投影。
// 有意不包含 AccessToken / RefreshToken。
type HotMeta struct {
	ID            string
	Priority      int32
	CooldownUntil int64
	ExpiresAt     int64
	Inflight      int32 // always 0 from catalog; hot layer owns inflight
	FailureScore  float32
	Enabled       bool
	Lifecycle     string
	Revision      int64
	IdentityKey   string
	ProxyMode     string
	ProxyURL      string
}

// CatalogStats 汇总冷存储中的账号规模与状态分布。
type CatalogStats struct {
	Count           int64
	EnabledCount    int64
	ActiveCount     int64
	CooldownCount   int64
	QuarantineCount int64
	DisabledCount   int64
}

// AccountSummary 是账号脱敏摘要，不含 access/refresh 明文，仅布尔位表示是否有令牌。
// 供管理台分页列表使用。
type AccountSummary struct {
	ID             string `json:"id"`
	Email          string `json:"email"`
	Name           string `json:"name"`
	Lifecycle      string `json:"lifecycle"`
	ProxyMode      string `json:"proxy_mode"`
	ProxyURL       string `json:"proxy_url"`
	Priority       int    `json:"priority"`
	Enabled        bool   `json:"enabled"`
	ManualDisabled bool   `json:"manual_disabled"`
	ExpiresAt      int64  `json:"expires_at"`
	CooldownUntil  int64  `json:"cooldown_until"`
	FailureCount   int64  `json:"failure_count"`
	SuccessCount   int64  `json:"success_count"`
	LastSuccessAt  *int64 `json:"last_success_at,omitempty"`
	Revision       int64  `json:"revision"`
	HasAccess      bool   `json:"has_access"`
	HasRefresh     bool   `json:"has_refresh"`
	LastError      string `json:"last_error"`
	LastUsedAt     *int64 `json:"last_used_at,omitempty"`
	// Alive 运行态：启用+active+有令牌+未冷却
	Alive bool `json:"alive"`
	// SuccessRate = success/(success+failure)；无样本时为 null（JSON 省略用指针）
	SuccessRate *float64 `json:"success_rate,omitempty"`
	// Inflight 热池实时占用（列表时由 admin 层填充；catalog 恒 0）
	Inflight int32 `json:"inflight"`
	// Billing 最近一次测活/额度快照（脱敏，来自 billing_json）
	Billing *AccountBillingView `json:"billing,omitempty"`
	// CooldownRemainingSec 列表时计算：max(0, cooldown_until-now)
	CooldownRemainingSec int64 `json:"cooldown_remaining_sec"`
	// StatusReason 人类可读原因：冷却/隔离/禁用/无令牌/测活失败等
	StatusReason string `json:"status_reason,omitempty"`
}

// AccountBillingView 管理台展示用额度/测活摘要（无密钥）。
type AccountBillingView struct {
	MonthlyUsed        *float64 `json:"monthly_used,omitempty"`
	MonthlyLimit       *float64 `json:"monthly_limit,omitempty"`
	WeeklyUsagePercent *float64 `json:"weekly_usage_percent,omitempty"`
	GrokBuildPercent   *float64 `json:"grok_build_percent,omitempty"`
	PeriodEnd          string   `json:"period_end,omitempty"`
	ProbeOK            *bool    `json:"probe_ok,omitempty"`
	ProbeStatus        int      `json:"probe_status,omitempty"`
	ProbeError         string   `json:"probe_error,omitempty"`
	ProbedAt           int64    `json:"probed_at,omitempty"`
	UpdatedAt          int64    `json:"updated_at,omitempty"`
}

// ProxyAssignment 批量设置代理的一项（SetProxies）。
// ProxyMode 空表示保留原 mode，仅改 ProxyURL；ProxyURL 空串表示清空为直连。
type ProxyAssignment struct {
	ID        string
	ProxyURL  string
	ProxyMode string
}
