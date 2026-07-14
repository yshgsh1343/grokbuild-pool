package refresh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
)

// xAI 风格默认值（文档用；真实请求须经 POOL_OAUTH_ENABLED + STATUS 门控）。
const (
	// DefaultXAITokenURL 为 xAI OIDC token 端点（文档默认；测试可覆盖）。
	DefaultXAITokenURL = "https://auth.x.ai/oauth2/token"
	// DefaultXAIClientID 为公开 Grok CLI OAuth client_id（可选；空则表单不带 client_id）。
	DefaultXAIClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	// DefaultHTTPTimeout 为单次 refresh 的安全超时。
	DefaultHTTPTimeout = 30 * time.Second
	// EnvOAuthEnabled 为真实 OAuth 的环境门控（须 =1/true 才可启用）。
	EnvOAuthEnabled = "POOL_OAUTH_ENABLED"
)

// HTTPRefreshConfig 控制 HTTPRefreshClient / XaiOAuth 行为。
type HTTPRefreshConfig struct {
	// RefreshURL 为 token 端点（POST application/x-www-form-urlencoded）。
	// 空则使用 DefaultXAITokenURL。
	RefreshURL string

	// ClientID 可选；空时表单不带 client_id（部分测试端点不需要）。
	// 生产 xAI 风格建议填 DefaultXAIClientID 或配置 oauth.client_id。
	ClientID string

	// HTTPClient 可选；nil 时使用带 DefaultHTTPTimeout 的默认客户端。
	// 单测注入 httptest.Server.Client()。
	HTTPClient *http.Client

	// Timeout 覆盖默认 HTTP 超时（仅当 HTTPClient 为 nil 时生效）。
	Timeout time.Duration
}

// HTTPRefreshClient 通过可配置 token URL 执行 OAuth refresh_token 授权。
// 亦称 XaiOAuth（类型别名）：默认文档端点为 xAI 风格，但**默认不会**对公网发请求——
// 须由 pool-proxy 在 POOL_OAUTH_ENABLED=1 且 STATUS UNLOCK_M12 允许时显式装配。
// cfg 可经 ApplyConfig 热更。
type HTTPRefreshClient struct {
	mu  sync.RWMutex
	cfg HTTPRefreshConfig
}

// XaiOAuth 是 HTTPRefreshClient 的文档别名（xAI 风格脚手架）。
type XaiOAuth = HTTPRefreshClient

// NewHTTPRefreshClient 构造 HTTP OAuth 刷新客户端。cfg 可零值（用默认 URL）。
func NewHTTPRefreshClient(cfg HTTPRefreshConfig) *HTTPRefreshClient {
	return &HTTPRefreshClient{cfg: cfg.normalize()}
}

// NewXaiOAuth 使用 xAI 文档默认端点与可选 client_id 构造客户端。
func NewXaiOAuth(refreshURL, clientID string) *HTTPRefreshClient {
	return NewHTTPRefreshClient(HTTPRefreshConfig{
		RefreshURL: refreshURL,
		ClientID:   clientID,
	})
}

// ApplyConfig 热更新 token URL / client_id（保留已注入的 HTTPClient）。
func (c *HTTPRefreshClient) ApplyConfig(cfg HTTPRefreshConfig) {
	if c == nil {
		return
	}
	cfg = cfg.normalize()
	c.mu.Lock()
	defer c.mu.Unlock()
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = c.cfg.HTTPClient
	}
	c.cfg = cfg
}

// Config 返回当前配置副本。
func (c *HTTPRefreshClient) Config() HTTPRefreshConfig {
	if c == nil {
		return HTTPRefreshConfig{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

func (c HTTPRefreshConfig) normalize() HTTPRefreshConfig {
	if strings.TrimSpace(c.RefreshURL) == "" {
		c.RefreshURL = DefaultXAITokenURL
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultHTTPTimeout
	}
	return c
}

func (c *HTTPRefreshClient) http() *http.Client {
	if c == nil {
		return &http.Client{Timeout: DefaultHTTPTimeout}
	}
	cfg := c.Config()
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient
	}
	to := DefaultHTTPTimeout
	if cfg.Timeout > 0 {
		to = cfg.Timeout
	}
	return &http.Client{Timeout: to}
}

func (c *HTTPRefreshClient) refreshURL() string {
	if c == nil {
		return DefaultXAITokenURL
	}
	u := strings.TrimSpace(c.Config().RefreshURL)
	if u == "" {
		return DefaultXAITokenURL
	}
	return u
}

func (c *HTTPRefreshClient) clientID() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.Config().ClientID)
}

// tokenResponse 解析 OAuth token 端点 JSON。
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// Refresh 实现 OAuthClient：POST grant_type=refresh_token 到可配置 URL。
// 不打印令牌内容。
func (c *HTTPRefreshClient) Refresh(ctx context.Context, refreshToken string) (catalog.TokenSet, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return catalog.TokenSet{}, fmt.Errorf("oauth refresh: empty refresh_token")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return catalog.TokenSet{}, err
	}

	endpoint := c.refreshURL()
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	if cid := c.clientID(); cid != "" {
		form.Set("client_id", cid)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return catalog.TokenSet{}, fmt.Errorf("oauth refresh: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http().Do(req)
	if err != nil {
		return catalog.TokenSet{}, fmt.Errorf("oauth refresh: post: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return catalog.TokenSet{}, fmt.Errorf("oauth refresh: read body: %w", err)
	}

	var tr tokenResponse
	if len(body) > 0 {
		if jerr := json.Unmarshal(body, &tr); jerr != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return catalog.TokenSet{}, fmt.Errorf("oauth refresh: decode json: %w", jerr)
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(tr.Error)
		if tr.ErrorDesc != "" {
			if msg != "" {
				msg += ": "
			}
			msg += tr.ErrorDesc
		}
		if msg == "" {
			msg = fmt.Sprintf("http %d", resp.StatusCode)
		}
		return catalog.TokenSet{}, fmt.Errorf("oauth refresh: %s", msg)
	}

	if tr.Error != "" {
		msg := tr.Error
		if tr.ErrorDesc != "" {
			msg += ": " + tr.ErrorDesc
		}
		return catalog.TokenSet{}, fmt.Errorf("oauth refresh: %s", msg)
	}

	at := strings.TrimSpace(tr.AccessToken)
	if at == "" {
		return catalog.TokenSet{}, fmt.Errorf("oauth refresh: empty access_token in response")
	}
	rt := strings.TrimSpace(tr.RefreshToken)
	if rt == "" {
		// 服务端未轮换 refresh_token 时保留原值。
		rt = refreshToken
	}
	expiresIn := tr.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return catalog.TokenSet{
		AccessToken:  at,
		RefreshToken: rt,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Unix(),
	}, nil
}

// OAuthEnvEnabled 报告环境变量 POOL_OAUTH_ENABLED 是否开启。
func OAuthEnvEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(EnvOAuthEnabled)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// StatusUnlockM12 从 STATUS.md 解析 UNLOCK_M12 是否为 true。
// path 为空时尝试 STATUS.md。
// 文件缺失或字段为 false → false（安全默认）。
func StatusUnlockM12(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "STATUS.md"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	// 简单行扫描：UNLOCK_M12: true / "true" / 1
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 允许 YAML 风格
		lower := strings.ToLower(line)
		if !strings.HasPrefix(lower, "unlock_m12") {
			continue
		}
		// UNLOCK_M12: true
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		val := strings.TrimSpace(strings.ToLower(parts[1]))
		val = strings.Trim(val, `"'`)
		return val == "true" || val == "1" || val == "yes" || val == "on"
	}
	return false
}

// RealOAuthAllowed 为真实 HTTP OAuth 的组合门控：
// POOL_OAUTH_ENABLED=1 且 STATUS UNLOCK_M12=true。
// statusPath 可空（默认 STATUS.md）。
func RealOAuthAllowed(statusPath string) bool {
	return OAuthEnvEnabled() && StatusUnlockM12(statusPath)
}
