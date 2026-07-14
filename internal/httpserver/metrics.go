package httpserver

import (
	"fmt"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"
)

// Metrics 保存低基数的进程与池计数（M13 可扩展）。
type Metrics struct {
	requests      atomic.Uint64
	errors        atomic.Uint64
	inflight      atomic.Int64
	rejects       atomic.Uint64
	responseBytes atomic.Uint64
	durationNanos atomic.Uint64

	// 可选采样器填充的池 gauge（原子存储）。
	hotSize             atomic.Int64
	cooldownSize        atomic.Int64
	acquireFail         atomic.Uint64
	failoverTotal       atomic.Uint64
	rateLimitBreak      atomic.Uint64
	stickyPrimaryHits   atomic.Uint64
	stickySecondaryHits atomic.Uint64
	stickyReselects     atomic.Uint64

	// 刷新 / 隔离（后台 ticker 写入）。
	refreshOK       atomic.Int64
	refreshFail     atomic.Int64
	ensureFreshHit  atomic.Int64
	quarantineCount atomic.Int64
}

// IncReject 记录一次 503 并发拒绝。
func (m *Metrics) IncReject() {
	if m != nil {
		m.rejects.Add(1)
	}
}

// SetPoolGauges 更新 /metrics 用的热/冷却 gauge。
func (m *Metrics) SetPoolGauges(hot, cooldown int) {
	if m == nil {
		return
	}
	m.hotSize.Store(int64(hot))
	m.cooldownSize.Store(int64(cooldown))
}

// SetRefreshStats 更新令牌刷新计数（ok/fail/ensureFresh 命中）。
func (m *Metrics) SetRefreshStats(ok, fail, ensureFreshHit int64) {
	if m == nil {
		return
	}
	m.refreshOK.Store(ok)
	m.refreshFail.Store(fail)
	m.ensureFreshHit.Store(ensureFreshHit)
}

// SetQuarantineCount 更新冷库隔离账号数 gauge。
func (m *Metrics) SetQuarantineCount(n int64) {
	if m == nil {
		return
	}
	m.quarantineCount.Store(n)
}

// RefreshOK 供 admin pool/stats。
func (m *Metrics) RefreshOK() int64 {
	if m == nil {
		return 0
	}
	return m.refreshOK.Load()
}

// RefreshFail 供 admin pool/stats。
func (m *Metrics) RefreshFail() int64 {
	if m == nil {
		return 0
	}
	return m.refreshFail.Load()
}

// QuarantineCount 供 admin pool/stats。
func (m *Metrics) QuarantineCount() int64 {
	if m == nil {
		return 0
	}
	return m.quarantineCount.Load()
}

// IncAcquireFail 增加 pool_acquire_fail_total。
func (m *Metrics) IncAcquireFail() {
	if m != nil {
		m.acquireFail.Add(1)
	}
}

// IncFailover 记录一次跨账号故障切换。
func (m *Metrics) IncFailover() {
	if m != nil {
		m.failoverTotal.Add(1)
	}
}

// IncRateLimitBreak 记录 429 熔断提前返回。
func (m *Metrics) IncRateLimitBreak() {
	if m != nil {
		m.rateLimitBreak.Add(1)
	}
}

// FailoverTotal 供 admin pool/stats。
func (m *Metrics) FailoverTotal() int64 {
	if m == nil {
		return 0
	}
	return int64(m.failoverTotal.Load())
}

// RateLimitBreakTotal 供 admin pool/stats。
func (m *Metrics) RateLimitBreakTotal() int64 {
	if m == nil {
		return 0
	}
	return int64(m.rateLimitBreak.Load())
}

// SetStickyStats 写入 sticky 命中统计（绝对值，非增量）。
func (m *Metrics) SetStickyStats(primary, secondary, reselects uint64) {
	if m == nil {
		return
	}
	m.stickyPrimaryHits.Store(primary)
	m.stickySecondaryHits.Store(secondary)
	m.stickyReselects.Store(reselects)
}

func (m *Metrics) StickyPrimaryHits() int64 {
	if m == nil {
		return 0
	}
	return int64(m.stickyPrimaryHits.Load())
}
func (m *Metrics) StickySecondaryHits() int64 {
	if m == nil {
		return 0
	}
	return int64(m.stickySecondaryHits.Load())
}
func (m *Metrics) StickyReselects() int64 {
	if m == nil {
		return 0
	}
	return int64(m.stickyReselects.Load())
}

// Requests 返回累计请求数（管理仪表盘）。
func (m *Metrics) Requests() int64 {
	if m == nil {
		return 0
	}
	return int64(m.requests.Load())
}

// Errors 返回累计错误响应数。
func (m *Metrics) Errors() int64 {
	if m == nil {
		return 0
	}
	return int64(m.errors.Load())
}

// Rejects 返回并发拒绝次数。
func (m *Metrics) Rejects() int64 {
	if m == nil {
		return 0
	}
	return int64(m.rejects.Load())
}

// Inflight 返回当前 in-flight（由 Observe 维护；与信号量可能略有差别）。
func (m *Metrics) Inflight() int64 {
	if m == nil {
		return 0
	}
	return m.inflight.Load()
}

// Handler provides minimal Prometheus text exposition.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if r.Method == http.MethodHead {
			return
		}
		if m == nil {
			m = &Metrics{}
		}
		_, _ = fmt.Fprintf(w,
			"# HELP process_sys_bytes Go runtime MemStats.Sys (not true RSS).\n"+
				"# TYPE process_sys_bytes gauge\n"+
				"process_sys_bytes %d\n"+
				"# HELP process_resident_memory_bytes Deprecated alias of process_sys_bytes (not true RSS).\n"+
				"# TYPE process_resident_memory_bytes gauge\n"+
				"process_resident_memory_bytes %d\n"+
				"# TYPE go_goroutines gauge\n"+
				"go_goroutines %d\n"+
				"# TYPE proxy_http_requests_total counter\n"+
				"proxy_http_requests_total %d\n"+
				"# TYPE proxy_http_errors_total counter\n"+
				"proxy_http_errors_total %d\n"+
				"# TYPE proxy_inflight gauge\n"+
				"proxy_inflight %d\n"+
				"# TYPE proxy_reject_total counter\n"+
				"proxy_reject_total %d\n"+
				"# TYPE proxy_http_response_bytes_total counter\n"+
				"proxy_http_response_bytes_total %d\n"+
				"# TYPE proxy_http_request_duration_seconds_sum counter\n"+
				"proxy_http_request_duration_seconds_sum %.6f\n"+
				"# TYPE pool_hot_size gauge\n"+
				"pool_hot_size %d\n"+
				"# TYPE pool_cooldown_size gauge\n"+
				"pool_cooldown_size %d\n"+
				"# TYPE pool_acquire_fail_total counter\n"+
				"pool_acquire_fail_total %d\n"+
				"# TYPE pool_refresh_ok_total counter\n"+
				"pool_refresh_ok_total %d\n"+
				"# TYPE pool_refresh_fail_total counter\n"+
				"pool_refresh_fail_total %d\n"+
				"# TYPE pool_ensure_fresh_hit_total counter\n"+
				"pool_ensure_fresh_hit_total %d\n"+
				"# TYPE pool_quarantine_count gauge\n"+
				"pool_quarantine_count %d\n",
			ms.Sys,
			ms.Sys,
			runtime.NumGoroutine(),
			m.requests.Load(),
			m.errors.Load(),
			m.inflight.Load(),
			m.rejects.Load(),
			m.responseBytes.Load(),
			float64(m.durationNanos.Load())/float64(time.Second),
			m.hotSize.Load(),
			m.cooldownSize.Load(),
			m.acquireFail.Load(),
			m.refreshOK.Load(),
			m.refreshFail.Load(),
			m.ensureFreshHit.Load(),
			m.quarantineCount.Load(),
		)
	})
}
