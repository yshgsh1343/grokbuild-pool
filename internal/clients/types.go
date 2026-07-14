package clients

import "time"

// Token 客户端 API 密钥记录。
// 鉴权以 key_hash 为准；key_plain 持久化以便管理台再次查看/复制（管理员接口返回）。
type Token struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	KeyPrefix      string `json:"key_prefix"` // 展示用前缀，如 sk-ab12…
	KeyHash        string `json:"-"`
	// APIKey 明文密钥；Create / List / Get 在管理端均可能返回。
	APIKey         string `json:"api_key,omitempty"`
	Enabled        bool   `json:"enabled"`
	RemainQuota    int64  `json:"remain_quota"` // 有限额度；与 UnlimitedQuota 联用
	UnlimitedQuota bool   `json:"unlimited_quota"`
	// MaxConcurrent 单令牌 in-flight 上限；0 表示不限（仍受全局 max_concurrent 约束）
	MaxConcurrent int   `json:"max_concurrent"`
	RPM           int   `json:"rpm"` // 每分钟请求上限；0 不限
	UsedQuota     int64 `json:"used_quota"`
	RequestCount  int64 `json:"request_count"`
	// Inflight 进程内当前占用（不落库；列表/详情时填充）
	Inflight   int   `json:"inflight"`
	ExpiresAt  int64 `json:"expires_at"` // unix 秒；0 永不过期
	CreatedAt  int64 `json:"created_at"`
	UpdatedAt  int64 `json:"updated_at"`
	LastUsedAt int64 `json:"last_used_at,omitempty"`
}

// CreateRequest 管理端创建/批量发放请求体。
// 指针字段：nil = 未传 → 管理台默认模板；非 nil（含 0/false）= 显式值，绝不被默认覆盖。
// 这是「改并发跟没改一样」的根因之一：旧版用值类型 0 与「未传」无法区分。
type CreateRequest struct {
	Name           string `json:"name"`
	RemainQuota    *int64 `json:"remain_quota"`
	UnlimitedQuota *bool  `json:"unlimited_quota"`
	MaxConcurrent  *int   `json:"max_concurrent"`
	RPM            *int   `json:"rpm"`
	ExpiresAt      int64  `json:"expires_at"`
	Count          int    `json:"count"` // 批量数量，默认 1，最大 100
}

// CreateResult 返回元数据 + 明文密钥（亦已写入 key_plain，可再次从 List/Get 读取）。
type CreateResult struct {
	Token     Token  `json:"token"`
	APIKey    string `json:"api_key"`
	Plaintext string `json:"plaintext"`
}

// AuthInfo 鉴权成功后写入请求上下文。
// MaxConcurrent/RPM 每次请求从 DB 读最新值，PATCH 后下一请求即生效。
type AuthInfo struct {
	TokenID       string
	Name          string
	MaxConcurrent int
	RPM           int
}

// PatchRequest 管理端 PATCH /admin/tokens/{id}。
// 指针 nil = 不改；非 nil 即写入（含 0/false）。
type PatchRequest struct {
	Name           *string `json:"name"`
	RemainQuota    *int64  `json:"remain_quota"`
	UnlimitedQuota *bool   `json:"unlimited_quota"`
	MaxConcurrent  *int    `json:"max_concurrent"`
	RPM            *int    `json:"rpm"`
	Enabled        *bool   `json:"enabled"`
	ExpiresAt      *int64  `json:"expires_at"`
}

func nowUnix() int64 { return time.Now().Unix() }
