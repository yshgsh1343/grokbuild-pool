// Package executor 使用 Scheme B Lease API 投递已清洗的 Responses 请求体，
// 替代原先的 JSON 凭证库 + lb.Selector。
package executor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/lease"
	"github.com/yshgsh1343/grokbuild2api/internal/protocol/lb"
	"github.com/yshgsh1343/grokbuild2api/internal/protocol/upstream"
)

// DefaultMaxAttempts 在 Executor.MaxAttempts 为零时使用（可用性优先默认 2）。
const DefaultMaxAttempts = 2

// Leaser 为 protocol executor 使用的 lease.Manager 子集。
type Leaser interface {
	Acquire(ctx context.Context, stickyKey, model string) (lease.Lease, error)
	AcquireAttempt(ctx context.Context, stickyKey, model string, tried map[string]struct{}) (lease.Lease, error)
	Release(ctx context.Context, l lease.Lease, result lease.Result) error
}

// UpstreamPoster 向 /v1/responses（或 mock）发 POST。
type UpstreamPoster interface {
	PostResponses(ctx context.Context, body any, opts upstream.PostResponsesOptions) (*http.Response, error)
}

// UpstreamResolver 可选地为每个 lease 构建客户端（如代理 URL 路由）。
type UpstreamResolver func(l lease.Lease) (UpstreamPoster, error)

// Refresher 确保请求路径 access token 可用（与 CPA/ref 对齐）。
// 为 nil 时 executor 原样投递 lease 令牌，并在 401 时切换账号。
type Refresher interface {
	// EnsureFresh 加载/刷新使 access token 可用。
	EnsureFresh(ctx context.Context, accountID string) (accessToken string, rev uint64, err error)
	// ForceRefresh 始终刷新（401 恢复路径）。
	ForceRefresh(ctx context.Context, accountID string) (accessToken string, rev uint64, err error)
}

// Executor 获取 lease、向上游投递，并按健康结果 Release。
//
// 流式规则：一旦任意响应体字节交给调用方（或流式返回 2xx），
// 该请求不再二次 Acquire。故障切换仅发生在响应交付之前。
//
// 401 规则（设置了 Refresher 时）：同账号 ForceRefresh + 同一 lease 重试一次，
// 再做多账号切换。刷新重试不增加 AcquireCount。
type Executor struct {
	Leaser   Leaser
	Upstream UpstreamPoster
	// UpstreamFor 用按 lease 路由的客户端覆盖 Upstream。
	UpstreamFor UpstreamResolver
	// Refresher 可选；设置后在 Acquire 后与 401 时运行 EnsureFresh，
	// 并在故障切换前对同账号 ForceRefresh 一次。
	Refresher Refresher
	// MaxAttempts 限制 lease 故障切换次数。0 使用 DefaultMaxAttempts。
	MaxAttempts int
	// Now 为测试可选时钟注入。
	Now func() time.Time
	// Logger 接收不含令牌的选号结果。
	Logger *slog.Logger
	// RequestID 从 ctx 提取关联 ID。
	RequestID func(context.Context) string
	// OnDialError 连接错误时回调，便于 ForgetAccount / 标记代理（P1.5）。
	OnDialError func(accountID, proxyURL string, err error)
	// OnFailover 跨账号切换时回调（attempt>1）。
	OnFailover func()
	// OnRateLimitBreak 429 熔断提前返回时回调。
	OnRateLimitBreak func()

	// acquireCount 为测试埋点（可选）。
	mu           sync.Mutex
	acquireCount int
}

// AcquireCount 返回成功获取的 lease 数（测试辅助）。
func (e *Executor) AcquireCount() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.acquireCount
}

// Post 实现 openai.PostResponsesFunc / anthropic.PostResponsesFunc。
func (e *Executor) Post(ctx context.Context, model, convID string, body []byte, stream bool) (*http.Response, error) {
	if e == nil {
		return nil, fmt.Errorf("executor: nil executor")
	}
	if e.Leaser == nil || (e.Upstream == nil && e.UpstreamFor == nil) {
		return nil, fmt.Errorf("executor: not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	maxAttempts := e.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}

	tried := make(map[string]struct{}, maxAttempts)
	var lastErr error
	var lastResp *http.Response
	// 每个逻辑 Post 一个 Idempotency-Key；跨全部凭证尝试共享。
	idempotencyKey := newIdempotencyKey()
	extraHeaders := idempotencyHeaders(idempotencyKey)
	// 429 换号预算：避免一次客户端请求扫穿整个池子。
	rateLimitFailovers := 0
	const maxRateLimitFailovers = 1

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		l, err := e.Leaser.AcquireAttempt(ctx, convID, model, tried)
		if err != nil {
			if errors.Is(err, lease.ErrNoAccount) {
				if lastResp != nil {
					return lastResp, nil
				}
				if lastErr != nil {
					return nil, lastErr
				}
				return nil, lb.ErrNoCredential
			}
			lastErr = err
			// soft miss already recorded in tried by AcquireAttempt
			continue
		}
		e.mu.Lock()
		e.acquireCount++
		e.mu.Unlock()

		e.log(ctx, slog.LevelDebug, "lease_acquired",
			"account_id", l.AccountID,
			"attempt", attempt,
		)

		poster, err := e.upstreamFor(l)
		if err != nil {
			lastErr = err
			_ = e.Leaser.Release(ctx, l, lease.Result{StatusCode: 0, Success: false})
			continue
		}

		// 请求路径 EnsureFresh：首次 post 前使用可用 access token。
		accessToken := l.AccessToken
		if e.Refresher != nil {
			tok, rev, rerr := e.Refresher.EnsureFresh(ctx, l.AccountID)
			if rerr != nil {
				lastErr = rerr
				e.log(ctx, slog.LevelWarn, "ensure_fresh_failed",
					"account_id", l.AccountID,
					"attempt", attempt,
					"error", rerr,
				)
				_ = e.Leaser.Release(ctx, l, lease.Result{StatusCode: 0, Success: false})
				continue
			}
			if tok != "" {
				accessToken = tok
				l.AccessToken = tok
				if rev > 0 {
					l.Revision = rev
				}
			}
		}

		resp, err := poster.PostResponses(ctx, body, upstream.PostResponsesOptions{
			AccessToken:  accessToken,
			Model:        model,
			ConvID:       convID,
			Stream:       stream,
			ExtraHeaders: extraHeaders,
		})
		if err != nil {
			lastErr = err
			e.log(ctx, slog.LevelWarn, "upstream_request_failed",
				"account_id", l.AccountID,
				"attempt", attempt,
				"error", err,
			)
			// P1.5：连接错误时失效出站亲和缓存
			if e.OnDialError != nil {
				e.OnDialError(l.AccountID, l.ProxyURL, err)
			}
			_ = e.Leaser.Release(ctx, l, lease.Result{StatusCode: 0, Success: false})
			// 尚未向调用方交付字节的网络错误 → 可故障切换。
			continue
		}

		// 426: do not failover.
		if resp.StatusCode == http.StatusUpgradeRequired {
			_ = e.Leaser.Release(ctx, l, lease.Result{StatusCode: resp.StatusCode, Success: false})
			return resp, nil
		}

		// 成功：返回调用方。流式时 body 尚未被我们读取——
		// 包装后在消费者结束后 Release（非流式缓冲后由调用方负责）。
		// 在 body 关闭时按成功 Release，避免长 SSE 占用 inflight。
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			e.log(ctx, slog.LevelDebug, "upstream_request_succeeded",
				"account_id", l.AccountID,
				"attempt", attempt,
				"upstream_status", resp.StatusCode,
			)
			// 包装 body，使 Close 时执行 Release(success)。这是流式交接点：
			// 一旦返回，不允许二次 Acquire。
			resp.Body = &releaseOnClose{
				ReadCloser: resp.Body,
				once:       sync.Once{},
				release: func() {
					_ = e.Leaser.Release(context.Background(), l, lease.Result{
						StatusCode: resp.StatusCode,
						Success:    true,
					})
				},
			}
			return resp, nil
		}

		// 401 + Refresher：同账号 ForceRefresh 一次，再以同一 lease 重试
		//（不计入新的 Acquire）。之后才故障切换。
		if resp.StatusCode == http.StatusUnauthorized && e.Refresher != nil {
			unauthorizedResp := bufferErrorResponse(resp)
			tok, rev, rerr := e.Refresher.ForceRefresh(ctx, l.AccountID)
			if rerr != nil {
				lastErr = rerr
				lastResp = unauthorizedResp
				e.log(ctx, slog.LevelWarn, "force_refresh_failed",
					"account_id", l.AccountID,
					"attempt", attempt,
					"error", rerr,
				)
				_ = e.Leaser.Release(ctx, l, lease.Result{
					StatusCode: http.StatusUnauthorized,
					Success:    false,
				})
				continue
			}
			if tok != "" {
				accessToken = tok
				l.AccessToken = tok
				if rev > 0 {
					l.Revision = rev
				}
			}
			e.log(ctx, slog.LevelInfo, "force_refresh_retry",
				"account_id", l.AccountID,
				"attempt", attempt,
			)
			retry, rerr := poster.PostResponses(ctx, body, upstream.PostResponsesOptions{
				AccessToken:  accessToken,
				Model:        model,
				ConvID:       convID,
				Stream:       stream,
				ExtraHeaders: extraHeaders,
			})
			if rerr != nil {
				lastErr = rerr
				lastResp = unauthorizedResp
				e.log(ctx, slog.LevelWarn, "upstream_request_failed",
					"account_id", l.AccountID,
					"attempt", attempt,
					"error", rerr,
					"after_refresh", true,
				)
				_ = e.Leaser.Release(ctx, l, lease.Result{
					StatusCode: http.StatusUnauthorized,
					Success:    false,
				})
				continue
			}
			if retry.StatusCode == http.StatusUpgradeRequired {
				_ = e.Leaser.Release(ctx, l, lease.Result{StatusCode: retry.StatusCode, Success: false})
				return retry, nil
			}
			if retry.StatusCode >= 200 && retry.StatusCode < 300 {
				e.log(ctx, slog.LevelDebug, "upstream_request_succeeded",
					"account_id", l.AccountID,
					"attempt", attempt,
					"upstream_status", retry.StatusCode,
					"after_refresh", true,
				)
				retry.Body = &releaseOnClose{
					ReadCloser: retry.Body,
					once:       sync.Once{},
					release: func() {
						_ = e.Leaser.Release(context.Background(), l, lease.Result{
							StatusCode: retry.StatusCode,
							Success:    true,
						})
					},
				}
				return retry, nil
			}
			// 刷新后仍失败 → 按失败 release 并切换账号。
			ra := parseRetryAfter(retry.Header.Get("Retry-After"), e.now())
			buffered := bufferErrorResponse(retry)
			status := buffered.StatusCode
			_ = e.Leaser.Release(ctx, l, lease.Result{
				StatusCode: status,
				RetryAfter: ra,
				Success:    false,
			})
			lastResp = buffered
			lastErr = fmt.Errorf("executor: upstream status %d after refresh", status)
			e.log(ctx, slog.LevelWarn, "upstream_retryable_status",
				"account_id", l.AccountID,
				"attempt", attempt,
				"upstream_status", status,
				"after_refresh", true,
			)
			// 429：最多再换 1 个号；防止限流风暴扫池。
			if status == http.StatusTooManyRequests {
				rateLimitFailovers++
				if rateLimitFailovers > maxRateLimitFailovers {
					e.log(ctx, slog.LevelWarn, "rate_limit_breaker",
						"account_id", l.AccountID,
						"attempt", attempt,
						"failovers", rateLimitFailovers,
					)
					if e.OnRateLimitBreak != nil {
						e.OnRateLimitBreak()
					}
					return buffered, nil
				}
			}
			// 仅在尚未向客户端写入 body 字节时故障切换。
			continue
		}

		// 错误状态：缓冲 body，以便 Release 并可选择故障切换，
		// 且重试间不保持连接打开。
		ra := parseRetryAfter(resp.Header.Get("Retry-After"), e.now())
		buffered := bufferErrorResponse(resp)
		status := buffered.StatusCode

		if isRetryableStatus(status) {
			_ = e.Leaser.Release(ctx, l, lease.Result{
				StatusCode: status,
				RetryAfter: ra,
				Success:    false,
			})
			lastResp = buffered
			lastErr = fmt.Errorf("executor: upstream status %d", status)
			e.log(ctx, slog.LevelWarn, "upstream_retryable_status",
				"account_id", l.AccountID,
				"attempt", attempt,
				"upstream_status", status,
			)
			// 429：最多再换 1 个号；防止限流风暴扫池。
			if status == http.StatusTooManyRequests {
				rateLimitFailovers++
				if rateLimitFailovers > maxRateLimitFailovers {
					e.log(ctx, slog.LevelWarn, "rate_limit_breaker",
						"account_id", l.AccountID,
						"attempt", attempt,
						"failovers", rateLimitFailovers,
					)
					if e.OnRateLimitBreak != nil {
						e.OnRateLimitBreak()
					}
					return buffered, nil
				}
			}
			// 仅在尚未向客户端写入 body 字节时故障切换。
			// 此处从未写客户端（仍在 Post 内）。
			continue
		}

		// 不可重试错误：release 并原样返回。
		_ = e.Leaser.Release(ctx, l, lease.Result{
			StatusCode: status,
			RetryAfter: ra,
			Success:    false,
		})
		return buffered, nil
	}

	if lastResp != nil {
		return lastResp, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, lb.ErrNoCredential
}

func (e *Executor) upstreamFor(l lease.Lease) (UpstreamPoster, error) {
	if e.UpstreamFor != nil {
		return e.UpstreamFor(l)
	}
	if e.Upstream == nil {
		return nil, fmt.Errorf("executor: no upstream")
	}
	return e.Upstream, nil
}

func (e *Executor) now() time.Time {
	if e != nil && e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Executor) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if e == nil || e.Logger == nil {
		return
	}
	if e.RequestID != nil {
		if id := e.RequestID(ctx); id != "" {
			args = append([]any{"request_id", id}, args...)
		}
	}
	e.Logger.Log(ctx, level, msg, args...)
}

// releaseOnClose 在 body 关闭时恰好执行一次 release。
type releaseOnClose struct {
	io.ReadCloser
	once    sync.Once
	release func()
	// n 跟踪成功读取的字节（测试/诊断）。
	n int64
}

func (r *releaseOnClose) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.n += int64(n)
	return n, err
}

func (r *releaseOnClose) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(func() {
		if r.release != nil {
			r.release()
		}
	})
	return err
}

// BytesRead 返回已读 body 字节数（测试辅助）。
func (r *releaseOnClose) BytesRead() int64 {
	if r == nil {
		return 0
	}
	return r.n
}

func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusPaymentRequired, // 402 — billing-dead accounts fail over
		http.StatusTooManyRequests, // 429
		http.StatusForbidden,       // 403
		http.StatusUnauthorized,    // 401 (when Refresher is nil)
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return code >= 500
	}
}

func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if sec, err := strconv.Atoi(v); err == nil {
		if sec < 0 {
			return 0
		}
		return time.Duration(sec) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := t.Sub(now)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

func bufferErrorResponse(resp *http.Response) *http.Response {
	if resp == nil {
		return nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	out := *resp
	out.Body = io.NopCloser(bytes.NewReader(raw))
	out.ContentLength = int64(len(raw))
	return &out
}

func newIdempotencyKey() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return "grokbuild-" + hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("grokbuild-%d", time.Now().UnixNano())
}

func idempotencyHeaders(key string) http.Header {
	headers := make(http.Header)
	headers.Set("Idempotency-Key", key)
	headers.Set("X-Idempotency-Key", key)
	return headers
}
