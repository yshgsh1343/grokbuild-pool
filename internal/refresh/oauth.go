package refresh

import (
	"context"
	"errors"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
)

// OAuthClient 向身份提供方执行令牌刷新。
// 真实 HTTP 实现见 HTTPRefreshClient / XaiOAuth；默认进程用 DisabledOAuth 或 mock。
// 生产启用须同时满足：环境变量 POOL_OAUTH_ENABLED=1 且 STATUS UNLOCK_M12=true。
// 测试通过适配器使用 mockup.MockOAuth，或用 httptest 测 HTTPRefreshClient。
type OAuthClient interface {
	Refresh(ctx context.Context, refreshToken string) (catalog.TokenSet, error)
}

// TokenSet 为 catalog.TokenSet 的别名，便于本包表述。
type TokenSet = catalog.TokenSet

// ErrOAuthDisabled 在 M12 解锁前由 DisabledOAuth 返回。
var ErrOAuthDisabled = errors.New("oauth disabled until M12")

// DisabledOAuth 是拒绝真实网络刷新的桩 OAuthClient。
// 未过门控时接入此实现，保留结构且不访问 auth.x.ai
//（UNLOCK_M12 为 false 或 POOL_OAUTH_ENABLED 未开）。
type DisabledOAuth struct{}

// Refresh 实现 OAuthClient。
func (DisabledOAuth) Refresh(context.Context, string) (catalog.TokenSet, error) {
	return catalog.TokenSet{}, ErrOAuthDisabled
}
