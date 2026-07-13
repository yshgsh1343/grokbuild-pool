package clients

import "time"

// Token 客户端 API 密钥记录。
// 鉴权以 key_hash 为准；明文仅在 Create 响应中返回一次，不再持久化/列表回读。
type Token struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	KeyPrefix      string `json:"key_prefix"` // 展示用前缀，如 sk-ab12…
	KeyHash        string `json:"-"`
	// APIKey 仅出现在 CreateResult；List 恒为空（兼容旧 JSON 字段名）。
	APIKey         string `json:"api_key,omitempty"`
	Enabled        bool   `json:"enabled"`
	RemainQuota    int64  `json:"remain_quota"` // 有限额度；与 UnlimitedQuota 联用
	UnlimitedQuota bool   `json:"unlimited_quota"`
	// MaxConcurrent 单令牌 in-flight 上限；0 表示不限（仍受全局 max_concurrent 约束）
	MaxConcurrent int   `json:"max_concurrent"`
	RPM           int   `json:"rpm"` // 每分钟请求上限；0 不限
	UsedQuota     int64 `json:"used_quota"`
	RequestCount  int64 `json:"request_count"`
	ExpiresAt     int64 `json:"expires_at"` // unix 秒；0 永不过期
	CreatedAt     int64 `json:"created_at"`
	UpdatedAt     int64 `json:"updated_at"`
	LastUsedAt    int64 `json:"last_used_at,omitempty"`
}

// CreateRequest 管理端创建/批量发放请求体。
type CreateRequest struct {
	Name           string `json:"name"`
	RemainQuota    int64  `json:"remain_quota"`
	UnlimitedQuota bool   `json:"unlimited_quota"`
	MaxConcurrent  int    `json:"max_concurrent"`
	RPM            int    `json:"rpm"`
	ExpiresAt      int64  `json:"expires_at"`
	Count          int    `json:"count"` // 批量数量，默认 1，最大 100
}

// CreateResult 返回元数据 + 一次性明文密钥。
type CreateResult struct {
	Token     Token  `json:"token"`
	APIKey    string `json:"api_key"`
	Plaintext string `json:"plaintext"`
}

// AuthInfo 鉴权成功后写入请求上下文。
type AuthInfo struct {
	TokenID       string
	Name          string
	MaxConcurrent int
	RPM           int
}

func nowUnix() int64 { return time.Now().Unix() }
