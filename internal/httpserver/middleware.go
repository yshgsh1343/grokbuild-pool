package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/clients"
	"github.com/yshgsh1343/grokbuild2api/internal/protocol/anthropic"
	"github.com/yshgsh1343/grokbuild2api/internal/protocol/openai"
)

type requestIDContextKey struct{}

// RequestIDFromContext 返回 Observe 中间件分配的关联 ID。
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(requestIDContextKey{}).(string)
	return v
}

// Middleware 公共 HTTP 中间件依赖。
type Middleware struct {
	// APIKey 非空时校验静态全局密钥（Bearer / x-api-key）。
	// 为空且 TokenStore 也为空时：关闭客户端鉴权（仅本地开发）。
	APIKey string
	// TokenStore 非空时优先按发放的 sk- 令牌鉴权（额度/并发/RPM）。
	TokenStore *clients.Store
	// MaxBody 请求体上限。
	MaxBody int64
	// MaxConcurrent 全局 in-flight 上限。
	MaxConcurrent int
	// RequestTimeout 整请求超时（含 SSE）。
	RequestTimeout time.Duration
	Logger         *slog.Logger
	Metrics        *Metrics

	runtimeOnce         sync.Once
	limiter             *concurrencyLimiter
	maxBody             atomic.Int64
	requestTimeoutNanos atomic.Int64
}

type concurrencyLimiter struct {
	mu    sync.Mutex
	limit int
	inUse int64
}

func (l *concurrencyLimiter) setLimit(limit int) {
	if limit < 0 {
		limit = 0
	}
	l.mu.Lock()
	l.limit = limit
	l.mu.Unlock()
}

func (l *concurrencyLimiter) tryAcquire() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.limit > 0 && l.inUse >= int64(l.limit) {
		return false
	}
	l.inUse++
	return true
}

func (l *concurrencyLimiter) release() {
	l.mu.Lock()
	if l.inUse > 0 {
		l.inUse--
	}
	l.mu.Unlock()
}

func (l *concurrencyLimiter) inflight() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inUse
}

func (m *Middleware) ensureRuntime() {
	m.runtimeOnce.Do(func() {
		limit := m.MaxConcurrent
		if limit < 0 {
			limit = 0
		}
		m.limiter = &concurrencyLimiter{limit: limit}
		m.maxBody.Store(m.MaxBody)
		m.requestTimeoutNanos.Store(int64(m.RequestTimeout))
	})
}

// SetMaxConcurrent 热更新全局并发上限。降低上限不会取消进行中的请求，
// 只会拒绝新请求，直到 in-flight 回落到新上限以下。0 表示不限制。
func (m *Middleware) SetMaxConcurrent(n int) {
	if m == nil {
		return
	}
	m.ensureRuntime()
	m.limiter.setLimit(n)
}

// MaxConcurrentLimit 返回当前全局并发上限（0=不限）。
func (m *Middleware) MaxConcurrentLimit() int {
	if m == nil {
		return 0
	}
	m.ensureRuntime()
	m.limiter.mu.Lock()
	defer m.limiter.mu.Unlock()
	return m.limiter.limit
}

// SetMaxBody 热更新请求体上限。0 表示不由公共中间件限制。
func (m *Middleware) SetMaxBody(n int64) {
	if m == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	m.ensureRuntime()
	m.maxBody.Store(n)
}

// MaxBodyBytes 返回当前请求体上限。
func (m *Middleware) MaxBodyBytes() int64 {
	if m == nil {
		return 0
	}
	m.ensureRuntime()
	return m.maxBody.Load()
}

// SetRequestTimeout 热更新整请求超时；只影响之后进入中间件的请求。
func (m *Middleware) SetRequestTimeout(timeout time.Duration) {
	if m == nil {
		return
	}
	if timeout < 0 {
		timeout = 0
	}
	m.ensureRuntime()
	m.requestTimeoutNanos.Store(int64(timeout))
}

// CurrentRequestTimeout 返回当前整请求超时。
func (m *Middleware) CurrentRequestTimeout() time.Duration {
	if m == nil {
		return 0
	}
	m.ensureRuntime()
	return time.Duration(m.requestTimeoutNanos.Load())
}

// Inflight 返回当前全局 in-flight 数。
func (m *Middleware) Inflight() int64 {
	if m == nil {
		return 0
	}
	m.ensureRuntime()
	return m.limiter.inflight()
}

// Timeout 在不设 Server.WriteTimeout 的情况下为请求 context 设截止时间。
func (m *Middleware) Timeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timeout := m.CurrentRequestTimeout()
		if timeout <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// clientAuthContextKey 上下文中的令牌鉴权信息。
type clientAuthContextKey struct{}

// ClientAuthFromContext 返回令牌鉴权结果（若有）。
func ClientAuthFromContext(ctx context.Context) (clients.AuthInfo, bool) {
	if ctx == nil {
		return clients.AuthInfo{}, false
	}
	v, ok := ctx.Value(clientAuthContextKey{}).(clients.AuthInfo)
	return v, ok
}

// RequireClient 客户端鉴权：
// 1) TokenStore 存在 → 校验 sk 令牌 + 额度 + 每令牌并发/RPM，并在结束后扣配额；
// 2) 否则若配置了静态 APIKey → 常量时间比较；
// 3) 都未配置 → 放行（仅本地开发；生产请配置令牌或 API_KEY）。
func (m *Middleware) RequireClient(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m == nil {
			next.ServeHTTP(w, r)
			return
		}
		// 令牌库模式
		if m.TokenStore != nil {
			key := extractAPIKey(r)
			if key == "" {
				writeRouteError(w, r, http.StatusUnauthorized, "缺少 api key")
				return
			}
			// 兼容：静态 master APIKey 仍可通行（运维）
			if strings.TrimSpace(m.APIKey) != "" && constantTimeEq(key, m.APIKey) {
				next.ServeHTTP(w, r)
				return
			}
			info, err := m.TokenStore.Authenticate(r.Context(), key)
			if err != nil {
				status := http.StatusUnauthorized
				msg := "无效 api key"
				switch {
				case errors.Is(err, clients.ErrDisabled):
					status, msg = http.StatusForbidden, "令牌已禁用"
				case errors.Is(err, clients.ErrExpired):
					status, msg = http.StatusForbidden, "令牌已过期"
				case errors.Is(err, clients.ErrQuotaExceeded):
					status, msg = http.StatusPaymentRequired, "令牌额度已用尽"
				}
				writeRouteError(w, r, status, msg)
				return
			}
			if err := m.TokenStore.AcquireSlot(info.TokenID, info.MaxConcurrent, info.RPM); err != nil {
				if m.Metrics != nil {
					m.Metrics.IncReject()
				}
				w.Header().Set("Retry-After", "1")
				msg := "令牌并发已满"
				if errors.Is(err, clients.ErrRPMLimit) {
					msg = "令牌 RPM 已达上限"
				} else if errors.Is(err, clients.ErrConcurrencyLimit) {
					msg = "令牌并发已满"
				} else {
					msg = err.Error()
				}
				writeRouteError(w, r, http.StatusServiceUnavailable, msg)
				return
			}
			// 始终释放 inflight（与 Acquire 时 max 无关）
			defer m.TokenStore.ReleaseSlot(info.TokenID, 0)
			// 原子预扣 1 单位额度，防止并发超花；结束后按实际 usage 结算
			const reserveUnits int64 = 1
			if err := m.TokenStore.ReserveQuota(info.TokenID, reserveUnits); err != nil {
				// Reserve 失败时 defer 会 ReleaseSlot；此处直接返回
				status, msg := http.StatusPaymentRequired, "令牌额度已用尽"
				if !errors.Is(err, clients.ErrQuotaExceeded) {
					status, msg = http.StatusUnauthorized, "无效 api key"
				}
				writeRouteError(w, r, status, msg)
				return
			}
			reserved := reserveUnits
			// 成功路径：尽力从响应体 usage 计量；失败 fallback 请求 +1
			rw := &usageRecorder{ResponseWriter: w, code: 200, captureBody: true}
			ctx := context.WithValue(r.Context(), clientAuthContextKey{}, info)
			next.ServeHTTP(rw, r.WithContext(ctx))
			if rw.code >= 200 && rw.code < 500 {
				cost := int64(1)
				if c, ok := rw.parsedCost(); ok {
					cost = c
				}
				_ = m.TokenStore.SettleUsage(info.TokenID, reserved, cost)
			} else {
				// 5xx / 未写出：退回预扣
				_ = m.TokenStore.RefundQuota(info.TokenID, reserved)
			}
			return
		}
		// 静态 APIKey
		if strings.TrimSpace(m.APIKey) == "" {
			next.ServeHTTP(w, r)
			return
		}
		key := extractAPIKey(r)
		if key == "" {
			writeRouteError(w, r, http.StatusUnauthorized, "缺少 api key")
			return
		}
		if !constantTimeEq(key, m.APIKey) {
			writeRouteError(w, r, http.StatusUnauthorized, "无效 api key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// usageRecorder 捕获状态码与响应体片段，用于按协议 usage 计量额度。
// 计量规则见 clients.UsageCostFromTokens：max(1, (in+out)/1000)；解析失败 cost=1。
type usageRecorder struct {
	http.ResponseWriter
	code        int
	captureBody bool
	buf         []byte
	// 限制捕获体积，避免大 SSE 占满内存
	maxCapture int
	wroteHdr   bool
}

const defaultUsageCapture = 512 << 10 // 512 KiB

func (s *usageRecorder) WriteHeader(code int) {
	if s.wroteHdr {
		return
	}
	s.wroteHdr = true
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *usageRecorder) Write(p []byte) (int, error) {
	if !s.wroteHdr {
		s.WriteHeader(http.StatusOK)
	}
	if s.captureBody {
		max := s.maxCapture
		if max <= 0 {
			max = defaultUsageCapture
		}
		// 环形保留尾部（流式 usage 常在结束事件）
		if len(s.buf)+len(p) <= max {
			s.buf = append(s.buf, p...)
		} else {
			// 保留尾部 max 字节
			need := max
			if len(p) >= need {
				s.buf = append(s.buf[:0], p[len(p)-need:]...)
			} else {
				keep := need - len(p)
				if keep > len(s.buf) {
					keep = len(s.buf)
				}
				s.buf = append(s.buf[len(s.buf)-keep:], p...)
			}
		}
	}
	return s.ResponseWriter.Write(p)
}

func (s *usageRecorder) Flush() {
	if flusher, ok := s.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *usageRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// parsedCost 从缓冲响应体尽力解析 usage cost。
func (s *usageRecorder) parsedCost() (int64, bool) {
	if s == nil || len(s.buf) == 0 {
		return 0, false
	}
	// 非流 JSON 优先
	if c, ok := clients.ParseUsageCostFromBody(s.buf); ok {
		return c, true
	}
	// SSE / 混合
	if c, ok := clients.ParseUsageCostFromSSE(s.buf); ok {
		return c, true
	}
	return 0, false
}

// LimitConcurrency 在全局上限满时以 503 + Retry-After 拒绝。
func (m *Middleware) LimitConcurrency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m == nil {
			next.ServeHTTP(w, r)
			return
		}
		m.ensureRuntime()
		if !m.limiter.tryAcquire() {
			if m.Metrics != nil {
				m.Metrics.IncReject()
			}
			w.Header().Set("Retry-After", "1")
			writeRouteError(w, r, http.StatusServiceUnavailable, "too many concurrent requests")
			return
		}
		defer m.limiter.release()
		next.ServeHTTP(w, r)
	})
}

// LimitBody 在当前上限 > 0 时用 MaxBytesReader 包装请求体。
func (m *Middleware) LimitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		maxBody := m.MaxBodyBytes()
		if maxBody > 0 && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		}
		next.ServeHTTP(w, r)
	})
}

// Observe 分配请求 ID、记录指标，并为每请求打一行日志。
func (m *Middleware) Observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := normalizeRequestID(r.Header.Get("X-Request-Id"))
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-Id", requestID)
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		r = r.WithContext(ctx)

		metrics := m.Metrics
		if metrics != nil {
			metrics.requests.Add(1)
			metrics.inflight.Add(1)
			defer metrics.inflight.Add(-1)
		}
		start := time.Now()
		recorder := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		elapsed := time.Since(start)
		if metrics != nil {
			// 仅 5xx 计为 errors，避免 401/404/422 等客户端噪声拉低成功率
			if recorder.status >= 500 {
				metrics.errors.Add(1)
			}
			if recorder.bytes > 0 {
				metrics.responseBytes.Add(uint64(recorder.bytes))
			}
			metrics.durationNanos.Add(uint64(elapsed))
		}
		logger := slog.Default()
		if m != nil && m.Logger != nil {
			logger = m.Logger
		}
		route := r.Pattern
		if route == "" {
			route = routeLabel(r.URL.Path)
		}
		logger.InfoContext(ctx, "http_request",
			"request_id", requestID,
			"method", r.Method,
			"route", route,
			"status", recorder.status,
			"duration_ms", float64(elapsed.Microseconds())/1000,
			"response_bytes", recorder.bytes,
		)
	})
}

// Chain 按顺序应用中间件（第一个为最外层）。
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

func extractAPIKey(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v := strings.TrimSpace(r.Header.Get("x-api-key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("X-Api-Key")); v != "" {
		return v
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authz) >= 7 && strings.EqualFold(authz[:7], "Bearer ") {
		return strings.TrimSpace(authz[7:])
	}
	if v := strings.TrimSpace(r.Header.Get("anthropic-api-key")); v != "" {
		return v
	}
	return ""
}

func constantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func writeRouteError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if r != nil && strings.HasPrefix(r.URL.Path, "/v1/messages") {
		anthropic.WriteError(w, status, message)
		return
	}
	openai.WriteError(w, status, message, "", "")
}

func normalizeRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' {
			continue
		}
		return ""
	}
	return value
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))
	}
	return hex.EncodeToString(b[:])
}

func routeLabel(path string) string {
	switch {
	case path == "/healthz":
		return "/healthz"
	case path == "/readyz":
		return "/readyz"
	case path == "/metrics":
		return "/metrics"
	case strings.HasPrefix(path, "/v1/messages"):
		return "/v1/messages"
	case strings.HasPrefix(path, "/v1/chat/completions"):
		return "/v1/chat/completions"
	case strings.HasPrefix(path, "/v1/responses"):
		return "/v1/responses"
	case strings.HasPrefix(path, "/v1/models"):
		return "/v1/models"
	default:
		return "other"
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
