package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/config"
	"github.com/yshgsh1343/grokbuild2api/internal/hot"
	"github.com/yshgsh1343/grokbuild2api/internal/httpserver"
	"github.com/yshgsh1343/grokbuild2api/internal/lease"
	"github.com/yshgsh1343/grokbuild2api/internal/outbound"
	"github.com/yshgsh1343/grokbuild2api/internal/protocol/executor"
	"github.com/yshgsh1343/grokbuild2api/internal/protocol/upstream"
	"github.com/yshgsh1343/grokbuild2api/internal/refresh"
)

// upstreamStack 上游 poster + OAuth 刷新 + executor。
type upstreamStack struct {
	Poster      executor.UpstreamPoster
	Refresh     *refresh.Service
	RefreshStop context.CancelFunc
	Executor    *executor.Executor
	Outbound    *outbound.Factory
}

// wireUpstream 管线第 3 步：真实上游反代 → refresh → executor（含按账号代理出站）。
// 默认直连 cli-chat-proxy.grok.com；可用配置覆盖 base_url。
func wireUpstream(cfg config.Config, opts Options, pool *poolStack, logger *slog.Logger) *upstreamStack {
	_ = opts
	base := strings.TrimSpace(cfg.Upstream.BaseURL)
	if base == "" {
		base = config.DefaultUpstreamBaseURL
	}
	uc := upstream.NewClient(upstream.Config{
		BaseURL:          base,
		ClientVersion:    cfg.Upstream.ClientVersion,
		ClientIdentifier: cfg.Upstream.ClientIdentifier,
		TokenAuth:        cfg.Upstream.TokenAuth,
		UserAgent:        cfg.Upstream.UserAgent,
		RequestTimeout:   30 * time.Second,
	})
	poster := executor.UpstreamPoster(uc)
	logger.Info("upstream_live", "base_url", uc.BaseURL())

	// OAuth 刷新门禁（真实 HTTP 或禁用；无 mock OAuth）
	var oauth refresh.OAuthClient
	switch {
	case refresh.RealOAuthAllowed(cfg.OAuth.StatusPath):
		oauth = refresh.NewHTTPRefreshClient(refresh.HTTPRefreshConfig{
			RefreshURL: cfg.OAuth.RefreshURL,
			ClientID:   cfg.OAuth.ClientID,
		})
		logger.Info("refresh_oauth_http",
			"refresh_url", firstNonEmpty(cfg.OAuth.RefreshURL, refresh.DefaultXAITokenURL),
			"client_id_set", strings.TrimSpace(cfg.OAuth.ClientID) != "",
			"gate", "POOL_OAUTH_ENABLED+UNLOCK_M12",
		)
	default:
		oauth = refresh.DisabledOAuth{}
		logger.Info("refresh_oauth_disabled",
			"reason", "need POOL_OAUTH_ENABLED=1 and STATUS UNLOCK_M12=true",
			"env_enabled", refresh.OAuthEnvEnabled(),
			"unlock_m12", refresh.StatusUnlockM12(cfg.OAuth.StatusPath),
		)
	}

	refreshCfg := refresh.DefaultConfig()
	refreshSvc := refresh.New(pool.Catalog, oauth, refreshCfg, nil, nil)
	refreshCtx, refreshCancel := context.WithCancel(context.Background())
	if err := refreshSvc.Start(refreshCtx); err != nil {
		refreshCancel()
		fail(logger, "refresh_start_failed", err)
	}
	logger.Info("refresh_workers_started",
		"workers", refreshCfg.Workers,
		"qps", refreshCfg.QPS,
		"skew_sec", refreshCfg.SkewSec,
	)

	refAdapter := &refresh.ExecutorAdapter{
		Service: refreshSvc,
		Catalog: pool.Catalog,
		Hot:     pool.Hot,
	}

	outboundFactory := outbound.NewFactory(upstream.Config{
		BaseURL:          base,
		ClientVersion:    cfg.Upstream.ClientVersion,
		ClientIdentifier: cfg.Upstream.ClientIdentifier,
		UserAgent:        cfg.Upstream.UserAgent,
		TokenAuth:        cfg.Upstream.TokenAuth,
	})

	exec := &executor.Executor{
		Leaser:   pool.Lease,
		Upstream: poster,
		UpstreamFor: func(l lease.Lease) (executor.UpstreamPoster, error) {
			return outboundFactory.ClientFor(l.AccountID, l.ProxyURL)
		},
		Refresher:   refAdapter,
		MaxAttempts: cfg.Lease.MaxAttempts,
		Logger:      logger,
		RequestID:   httpserver.RequestIDFromContext,
		OnDialError: func(accountID, proxyURL string, err error) {
			if accountID != "" {
				outboundFactory.ForgetAccount(accountID)
			}
			if proxyURL != "" {
				outboundFactory.Forget(proxyURL)
			}
			logger.Warn("outbound_forget_on_dial_error",
				"account_id", accountID,
				"proxy_set", proxyURL != "",
				"error", err,
			)
		},
	}

	return &upstreamStack{
		Poster:      poster,
		Refresh:     refreshSvc,
		RefreshStop: refreshCancel,
		Executor:    exec,
		Outbound:    outboundFactory,
	}
}

// ensure types used for interface satisfaction docs
var (
	_ *catalog.Catalog
	_ *hot.Index
)
