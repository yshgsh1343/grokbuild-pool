package httpserver

import (
	"encoding/json"
	"strings"
	"net"
	"log/slog"
	"net/http"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/clients"
	"github.com/yshgsh1343/grokbuild2api/internal/config"
	"github.com/yshgsh1343/grokbuild2api/internal/hot"
	"github.com/yshgsh1343/grokbuild2api/internal/protocol/anthropic"
	"github.com/yshgsh1343/grokbuild2api/internal/protocol/openai"
)

// HotStatsProvider 从热索引提供就绪与指标 gauge。
type HotStatsProvider interface {
	// HotLen 为当前热集大小（0 ⇒ 未就绪）。
	HotLen() int
	// PoolStats 返回热/冷却 gauge（可选；可空安全）。
	PoolStats() (hotSize, cooldown int)
}

// IndexStats 将 *hot.Index 适配为 HotStatsProvider。
type IndexStats struct {
	Index *hot.Index
}

// HotLen 实现 HotStatsProvider。
func (s IndexStats) HotLen() int {
	if s.Index == nil {
		return 0
	}
	return s.Index.Len()
}

// PoolStats 实现 HotStatsProvider。
func (s IndexStats) PoolStats() (hotSize, cooldown int) {
	if s.Index == nil {
		return 0, 0
	}
	st := s.Index.Stats(0)
	return st.HotSize, st.CooldownCount
}

// Options 配置 HTTP 服务。
type Options struct {
	Config     config.Config
	OpenAI     *openai.Handlers
	Anthropic  *anthropic.Handlers
	Hot        HotStatsProvider
	Version    string
	Logger     *slog.Logger
	Metrics    *Metrics
	TokenStore *clients.Store // 可选：发放的客户端令牌库
	// AdminKey 非空时非 loopback 访问 /metrics 需匹配；空则仅 loopback。
	AdminKey string
	// MetricsPublic true 时 /metrics 匿名公开（不推荐）。
	MetricsPublic bool
	// ExtraMount 在 /v1 之后挂载管理路由/静态资源（由 main 注入，避免循环依赖）
	ExtraMount func(mux *http.ServeMux)
	// OnMiddleware 在中间件构造后回调，便于热更新全局并发等。
	OnMiddleware func(m *Middleware)
	StartedAt    time.Time
}

// New 构建根 http.Handler。
func New(opts Options) http.Handler {
	metrics := opts.Metrics
	if metrics == nil {
		metrics = &Metrics{}
	}
	mw := &Middleware{
		APIKey:         opts.Config.APIKey,
		TokenStore:     opts.TokenStore,
		MaxBody:        opts.Config.Limits.MaxBodyBytes,
		MaxConcurrent:  opts.Config.Limits.MaxConcurrent,
		RequestTimeout: opts.Config.RequestTimeout(),
		Logger:         opts.Logger,
		Metrics:        metrics,
	}
	if opts.OnMiddleware != nil {
		opts.OnMiddleware(mw)
	}
	if opts.OpenAI != nil {
		opts.OpenAI.MaxBodyFunc = mw.MaxBodyBytes
	}
	if opts.Anthropic != nil {
		opts.Anthropic.MaxBodyFunc = mw.MaxBodyBytes
	}

	mux := http.NewServeMux()

	// 无需鉴权的探活 / 指标。
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleHealthz(w, r)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleReadyz(w, r, opts.Hot, metrics)
	})
	mux.Handle("/metrics", protectMetrics(metrics.Handler(), opts.AdminKey, opts.MetricsPublic))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleRoot(w, r, opts.Version)
	})

	// 客户端 API（可选鉴权 + 并发限制）。
	api := http.NewServeMux()
	if opts.OpenAI != nil {
		api.HandleFunc("POST /v1/responses", opts.OpenAI.HandleResponses)
		api.HandleFunc("POST /v1/chat/completions", opts.OpenAI.HandleChatCompletions)
	}
	if opts.Anthropic != nil && opts.Config.Anthropic.Enabled {
		api.HandleFunc("POST /v1/messages", opts.Anthropic.HandleMessages)
		api.HandleFunc("POST /v1/messages/count_tokens", opts.Anthropic.HandleCountTokens)
	}
	api.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		handleModels(w, r, opts)
	})

	protected := Chain(api, mw.LimitConcurrency, mw.LimitBody, mw.RequireClient)
	mux.Handle("/v1/", protected)

	// 挂载管理 API / 静态管理台（main 注入，避免 import 循环）
	if opts.ExtraMount != nil {
		opts.ExtraMount(mux)
	}

	return mw.Observe(mw.Timeout(mux))
}

// NewServer 构建对 SSE 友好超时的 *http.Server。
func NewServer(addr string, handler http.Handler, requestTimeout time.Duration) *http.Server {
	if requestTimeout <= 0 {
		requestTimeout = 600 * time.Second
	}
	_ = requestTimeout // 由中间件 context 强制，而非 WriteTimeout
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
		ReadHeaderTimeout: 10 * time.Second,
		// 勿设 WriteTimeout：SSE 流中途不可被切断。
		IdleTimeout: 120 * time.Second,
	}
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("ok\n"))
	}
}

func handleReadyz(w http.ResponseWriter, r *http.Request, hotProv HotStatsProvider, metrics *Metrics) {
	hotLen := 0
	cooldown := 0
	if hotProv != nil {
		hotLen = hotProv.HotLen()
		if hs, cd := hotProv.PoolStats(); true {
			_ = hs
			cooldown = cd
		}
		if metrics != nil {
			h, c := hotProv.PoolStats()
			metrics.SetPoolGauges(h, c)
			cooldown = c
			hotLen = h
		}
	}
	ready := hotLen > 0
	status := http.StatusOK
	reason := "ready"
	if !ready {
		status = http.StatusServiceUnavailable
		reason = "hot index empty"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ready":          ready,
		"reason":         reason,
		"hot_size":       hotLen,
		"cooldown_size":  cooldown,
		"available_hint": hotLen,
	})
}

func handleRoot(w http.ResponseWriter, r *http.Request, version string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	msg := "pool-proxy (scheme-B)"
	if version != "" {
		msg += " " + version
	}
	msg += "\nlisten-default: 0.0.0.0:8080\n"
	_, _ = w.Write([]byte(msg))
}

func handleModels(w http.ResponseWriter, r *http.Request, opts Options) {
	type modelRow struct {
		ID      string `json:"id"`
		Object  string `json:"object,omitempty"`
		Created int64  `json:"created,omitempty"`
		OwnedBy string `json:"owned_by,omitempty"`
		Name    string `json:"name,omitempty"`
	}
	rows := make([]modelRow, 0, 32)
	seen := map[string]struct{}{}
	add := func(id, owned string) {
		id = stringsTrim(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		if owned == "" {
			owned = "xai"
		}
		rows = append(rows, modelRow{
			ID:      id,
			Object:  "model",
			OwnedBy: owned,
			Name:    id,
		})
	}
	// Claude Code 与常见 Grok id 的静态发现（不调真实上游）。
	for alias, target := range opts.Config.Anthropic.ModelAliases {
		add(alias, "anthropic")
		add(target, "xai")
	}
	add("grok-4.5", "xai")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   rows,
	})
}

func stringsTrim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// protectMetrics 默认仅 loopback 或持有 admin_key 可访问 /metrics。
func protectMetrics(next http.Handler, adminKey string, public bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if public {
			next.ServeHTTP(w, r)
			return
		}
		if isLoopbackRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		want := strings.TrimSpace(adminKey)
		if want != "" {
			got := extractAPIKey(r)
			if got != "" && constantTimeEq(got, want) {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, "metrics forbidden", http.StatusForbidden)
	})
}

func isLoopbackRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
