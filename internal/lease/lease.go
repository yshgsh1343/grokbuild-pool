package lease

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/hot"
	"github.com/yshgsh1343/grokbuild2api/internal/selector"
)

// 哨兵错误。
var (
	ErrNoAccount    = errors.New("lease: no eligible account")
	ErrInvalidInput = errors.New("lease: invalid input")
	ErrClosed       = errors.New("lease: closed")
)

// Lease 是一次上游调用期间账号 + access token 的短时绑定。
// AccessToken 为密钥，绝不可记日志。
type Lease struct {
	AccountID   string
	Revision    uint64
	AccessToken string
	ProxyURL    string
	ProxyMode   string
	StickyKey   string
	Model       string
	Attempt     int
}

// String 脱敏密钥与代理 userinfo，便于安全日志。
func (l Lease) String() string {
	return fmt.Sprintf("Lease{AccountID:%q Revision:%d ProxyMode:%q ProxyURL:%q StickyKey:%q Attempt:%d}",
		l.AccountID, l.Revision, l.ProxyMode, redactProxyURL(l.ProxyURL), l.StickyKey, l.Attempt)
}

// redactProxyURL 去掉代理 URL 中的 userinfo，仅保留 scheme/host/path 形态。
func redactProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return "(invalid-proxy-url)"
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	// 不回传 query/fragment 中可能的密钥
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// Result 报告上游调用结果，供 Release 记账。
type Result struct {
	StatusCode int           // 上游 HTTP 状态；0 = 网络/未知
	RetryAfter time.Duration // 可选；>0 时用于 429 冷却
	Success    bool
}

// Manager 串联 catalog（密钥）、热索引（inflight/冷却）与 selector（选号）。
type Manager struct {
	cat *catalog.Catalog
	idx *hot.Index
	sel *selector.Selector

	mu  sync.RWMutex
	cfg Config

	// modelCool: accountID -> model -> unix until（进程内模型级冷却，避免单模型 429 连坐整号）
	modelMu   sync.Mutex
	modelCool map[string]map[string]int64
}

// New 构造 Manager。cat、idx、sel 均不可为 nil。
func New(cat *catalog.Catalog, idx *hot.Index, sel *selector.Selector, cfg Config) *Manager {
	if cat == nil || idx == nil || sel == nil {
		panic("lease: nil catalog, hot index, or selector")
	}
	m := &Manager{
		cat:       cat,
		idx:       idx,
		sel:       sel,
		cfg:       cfg.normalize(),
		modelCool: make(map[string]map[string]int64),
	}
	// 启动时装载未过期模型冷却（失败不阻断）
	if loaded, err := cat.LoadActiveModelCooldowns(time.Now().Unix()); err == nil && len(loaded) > 0 {
		m.modelCool = loaded
	}
	return m
}

// Config 返回当前配置副本。
func (m *Manager) Config() Config {
	if m == nil {
		return DefaultConfig()
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// ApplyConfig 热更新冷却与失败切换参数。
// 管理台路径：尊重显式 0（如关闭某项冷却），不再用 Default* 覆盖。
func (m *Manager) ApplyConfig(cfg Config) {
	if m == nil {
		return
	}
	// 仅安全下限：至少 1 次 attempt，避免 Acquire 空转
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	if cfg.CooldownCapSec > 0 && cfg.CooldownBaseSec > 0 && cfg.CooldownCapSec < cfg.CooldownBaseSec {
		cfg.CooldownCapSec = cfg.CooldownBaseSec
	}
	if cfg.CooldownJitterPct < 0 {
		cfg.CooldownJitterPct = 0
	}
	if cfg.CooldownJitterPct > 100 {
		cfg.CooldownJitterPct = 100
	}
	if cfg.ForbiddenQuarantineAfter < 0 {
		cfg.ForbiddenQuarantineAfter = 0
	}
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
}

// Acquire 选号（最多 MaxAttempts 次切换）、加载令牌并增加 inflight。
func (m *Manager) Acquire(ctx context.Context, stickyKey, model string) (Lease, error) {
	if m == nil {
		return Lease{}, fmt.Errorf("%w: nil manager", ErrInvalidInput)
	}
	maxAttempts := m.Config().MaxAttempts
	tried := make(map[string]struct{}, maxAttempts)
	var last error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Lease{}, err
		}
		lease, err := m.acquireOnce(ctx, stickyKey, model, tried, attempt)
		if err == nil {
			return lease, nil
		}
		last = err
		if errors.Is(err, ErrNoAccount) {
			// 无更多候选；提前结束。
			return Lease{}, ErrNoAccount
		}
		// 短暂的选号/读取/inflight 未命中——扩大 tried 后继续。
	}
	if last == nil {
		last = ErrNoAccount
	}
	return Lease{}, last
}

// AcquireAttempt 执行单次 pick→get→inflight 周期，永不返回 tried 中的 id。
// 软失败（不合格/不在热集）时，若 tried 非 nil 则写入失败 id。
func (m *Manager) AcquireAttempt(ctx context.Context, stickyKey, model string, tried map[string]struct{}) (Lease, error) {
	if m == nil {
		return Lease{}, fmt.Errorf("%w: nil manager", ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	if tried == nil {
		tried = make(map[string]struct{})
	}
	return m.acquireOnce(ctx, stickyKey, model, tried, 1)
}

func (m *Manager) acquireOnce(ctx context.Context, stickyKey, model string, tried map[string]struct{}, attempt int) (Lease, error) {
	_ = ctx // 预留给未来可取消的 catalog 操作
	now := time.Now().Unix()

	id, ok := m.sel.PickExcluding(now, stickyKey, tried)
	if !ok || id == "" {
		return Lease{}, ErrNoAccount
	}

	// 本次 Acquire 后续尝试一律排除该 id。
	if tried != nil {
		tried[id] = struct{}{}
	}

	acct, err := m.cat.Get(id)
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return Lease{}, fmt.Errorf("lease: catalog miss for %s: %w", id, err)
		}
		if errors.Is(err, catalog.ErrClosed) {
			return Lease{}, ErrClosed
		}
		return Lease{}, fmt.Errorf("lease: get %s: %w", id, err)
	}

	if !accountUsable(acct, now) {
		return Lease{}, fmt.Errorf("lease: account %s not usable after pick", id)
	}
	if model != "" && m.modelCooldownActive(id, model, now) {
		return Lease{}, fmt.Errorf("lease: account %s model %s cooling", id, model)
	}

	if err := m.idx.AddInflight(id); err != nil {
		// 竞态：pick 与 acquire 之间已从热集 demote。
		return Lease{}, fmt.Errorf("lease: add inflight %s: %w", id, err)
	}

	rev := acct.Revision
	if rev < 0 {
		rev = 0
	}
	return Lease{
		AccountID:   acct.ID,
		Revision:    uint64(rev),
		AccessToken: acct.AccessToken,
		ProxyURL:    acct.ProxyURL,
		ProxyMode:   acct.ProxyMode,
		StickyKey:   stickyKey,
		Model:       model,
		Attempt:     attempt,
	}, nil
}

func accountUsable(a catalog.Account, now int64) bool {
	if !a.Enabled || a.ManualDisabled {
		return false
	}
	if a.Lifecycle != "" && a.Lifecycle != catalog.LifecycleActive {
		return false
	}
	if a.CooldownUntil > now {
		return false
	}
	if a.AccessToken == "" {
		return false
	}
	return true
}

// Release 记录成功/失败、减少 inflight，并可能设置冷却/清除粘性。
func (m *Manager) Release(ctx context.Context, lease Lease, result Result) error {
	if m == nil {
		return fmt.Errorf("%w: nil manager", ErrInvalidInput)
	}
	if lease.AccountID == "" {
		return fmt.Errorf("%w: empty lease AccountID", ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		// 仍尽力释放 inflight，避免计数泄漏。
		_ = m.idx.SubInflight(lease.AccountID)
		return err
	}

	// 始终先扣减 inflight。
	if err := m.idx.SubInflight(lease.AccountID); err != nil && !errors.Is(err, hot.ErrNotFound) {
		// 即使 sub 异常也继续健康更新；patch 失败时再暴露。
	}

	now := time.Now().Unix()
	if result.Success {
		return m.releaseSuccess(lease, now)
	}
	return m.releaseFailure(lease, result, now)
}

func (m *Manager) releaseSuccess(lease Lease, now int64) error {
	used := now
	zero := 0
	sc := 1
	fc := 0
	// 合并已有 success_count；成功时衰减 failure_count（防“记仇太久”）
	if cur, err := m.cat.Get(lease.AccountID); err == nil {
		sc = cur.SuccessCount + 1
		fc = decayFailureCount(cur.FailureCount)
	}
	patch := catalog.HealthPatch{
		SuccessCount:            &sc,
		FailureCount:            &fc,
		LastSuccessAt:           &used,
		LastUsedAt:              &used,
		ConsecutiveUnauthorized: &zero,
		ClearLastError:          true,
	}
	if err := m.cat.PatchHealth(lease.AccountID, patch); err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil // 账号已不存在；inflight 已扣减
		}
		return fmt.Errorf("lease: patch success %s: %w", lease.AccountID, err)
	}
	// 同步热池 FailureScore，使 selector 立即减轻惩罚
	if meta, ok := m.idx.Get(lease.AccountID); ok {
		meta.FailureScore = float32(fc)
		_, _ = m.idx.Promote(meta)
	}
	return nil
}

// decayFailureCount 成功请求后的失败分衰减：先减 1，再对剩余折半（下限 0）。
// 例：10→4，5→2，1→0，0→0。比单纯 -1 恢复更快，又不会一次清零历史。
func decayFailureCount(fc int) int {
	if fc <= 0 {
		return 0
	}
	fc--
	return fc / 2
}

func (m *Manager) releaseFailure(lease Lease, result Result, now int64) error {
	code := result.StatusCode
	cfg := m.Config()
	// 可用性优先：冷却与清粘性分级，避免 429/5xx 把号打成“假死”。
	applyCooldown := code == 429 || code == 401 || code == 402 || code == 403
	clearSticky := false
	switch code {
	case 401, 402, 403:
		clearSticky = true
	case 429:
		clearSticky = cfg.ClearStickyOn429
	case 0:
		clearSticky = cfg.ClearStickyOn5xx
	default:
		if code >= 500 {
			clearSticky = cfg.ClearStickyOn5xx
		}
	}

	// 读取当前行以合并 failure_count / consecutive_unauthorized。
	cur, err := m.cat.Get(lease.AccountID)
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			if clearSticky {
				m.clearSticky(lease)
			}
			return nil
		}
		return fmt.Errorf("lease: get for release %s: %w", lease.AccountID, err)
	}

	fc := cur.FailureCount + 1
	// 5xx/网络错误少记仇：失败分 +1 但可被成功快速衰减；不强制长冷却。
	used := now
	lastErr := fmt.Sprintf("upstream %d", code)
	if code == 0 {
		lastErr = "upstream network error"
	}
	if code == 403 {
		lastErr = "forbidden"
	}

	patch := catalog.HealthPatch{
		FailureCount: &fc,
		LastUsedAt:   &used,
		LastError:    &lastErr,
	}

	if applyCooldown {
		coolSec := m.cooldownSeconds(code, result.RetryAfter, fc)
		until := now + coolSec
		// 429 + 有 model：优先模型级冷却，不连坐整号（除非显式无 model）
		if code == 429 && strings.TrimSpace(lease.Model) != "" {
			m.setModelCooldownErr(lease.AccountID, lease.Model, until, lastErr)
			// 不写账号级 cooldown_until；仍记 failure/last_error
		} else {
			patch.CooldownUntil = &until
			_ = m.idx.SetCooldown(lease.AccountID, until)
		}

		if code == 401 {
			cu := cur.ConsecutiveUnauthorized + 1
			patch.ConsecutiveUnauthorized = &cu
			if cu >= cfg.UnauthorizedQuarantineAfter {
				lc := catalog.LifecycleQuarantined
				en := false
				patch.Lifecycle = &lc
				patch.Enabled = &en
				if meta, ok := m.idx.Get(lease.AccountID); ok {
					meta.Enabled = false
					meta.Lifecycle = catalog.LifecycleQuarantined
					meta.CooldownUntil = until
					_, _ = m.idx.Promote(meta)
				}
			}
		}

		if code == 403 {
			if thresh := cfg.ForbiddenQuarantineAfter; thresh > 0 {
				streak := 1
				if cur.LastError == "forbidden" {
					streak = cur.FailureCount + 1
					if streak < 2 {
						streak = 2
					}
				}
				if streak >= thresh {
					lc := catalog.LifecycleQuarantined
					en := false
					patch.Lifecycle = &lc
					patch.Enabled = &en
					if meta, ok := m.idx.Get(lease.AccountID); ok {
						meta.Enabled = false
						meta.Lifecycle = catalog.LifecycleQuarantined
						meta.CooldownUntil = until
						_, _ = m.idx.Promote(meta)
					}
				}
			}
		}

		// 402：默认只冷却，不永久隔离（观察期）。显式打开才 quarantine。
		if code == 402 && cfg.QuarantineOnPaymentRequired {
			lc := catalog.LifecycleQuarantined
			en := false
			patch.Lifecycle = &lc
			patch.Enabled = &en
			if meta, ok := m.idx.Get(lease.AccountID); ok {
				meta.Enabled = false
				meta.Lifecycle = catalog.LifecycleQuarantined
				meta.CooldownUntil = until
				_, _ = m.idx.Promote(meta)
			}
		}
	}

	if clearSticky {
		m.clearSticky(lease)
	}

	if err := m.cat.PatchHealth(lease.AccountID, patch); err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("lease: patch failure %s: %w", lease.AccountID, err)
	}

	// 使热层 FailureScore 与 failure_count 大致同步，供 selector 权重使用。
	if meta, ok := m.idx.Get(lease.AccountID); ok {
		meta.FailureScore = float32(fc)
		if patch.CooldownUntil != nil {
			meta.CooldownUntil = *patch.CooldownUntil
		}
		if patch.Enabled != nil {
			meta.Enabled = *patch.Enabled
		}
		if patch.Lifecycle != nil {
			meta.Lifecycle = *patch.Lifecycle
		}
		_, _ = m.idx.Promote(meta)
	}

	return nil
}

func (m *Manager) clearSticky(lease Lease) {
	m.sel.ClearStickyAccount(lease.AccountID)
	if lease.StickyKey != "" {
		m.sel.ClearStickyKey(lease.StickyKey)
	}
}

// cooldownSeconds 计算冷却秒数：429 指数退避 + 抖动；403 更长固定冷却。
func (m *Manager) cooldownSeconds(statusCode int, retryAfter time.Duration, failureCount int) int64 {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	var sec int64
	switch statusCode {
	case 429:
		if retryAfter > 0 {
			sec = int64(retryAfter.Seconds())
			if sec < 1 {
				sec = 1
			}
		} else {
			sec = cfg.CooldownBaseSec
			// 指数：base * 2^min(failureCount-1, ExpMax)，failureCount 至少 1
			exp := failureCount - 1
			if exp < 0 {
				exp = 0
			}
			if exp > cfg.CooldownExpMax {
				exp = cfg.CooldownExpMax
			}
			sec = sec << exp
		}
	case 403:
		// 防封号：403 默认更长冷却，避免立即重试踩风控
		sec = cfg.ForbiddenCooldownSec
		if retryAfter > 0 {
			ra := int64(retryAfter.Seconds())
			if ra > sec {
				sec = ra
			}
		}
	case 401:
		sec = cfg.UnauthorizedCooldownSec
	case 402:
		sec = cfg.PaymentRequiredCooldownSec
	default:
		sec = cfg.CooldownBaseSec
	}
	if sec > cfg.CooldownCapSec {
		sec = cfg.CooldownCapSec
	}
	if sec < 1 {
		sec = 1
	}
	// 抖动，避免雷同账号同一时刻解封
	if j := cfg.CooldownJitterPct; j > 0 {
		// ±j%
		delta := sec * int64(j) / 100
		if delta > 0 {
			// 伪随机：基于 failureCount 与 sec，无需全局锁
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(failureCount)*31 + sec))
			sec = sec - delta + int64(r.Int63n(2*delta+1))
			if sec < 1 {
				sec = 1
			}
		}
	}
	return sec
}

func (m *Manager) setModelCooldown(accountID, model string, until int64) {
	m.setModelCooldownErr(accountID, model, until, "")
}

func (m *Manager) setModelCooldownErr(accountID, model string, until int64, lastErr string) {
	if m == nil || accountID == "" || model == "" || until <= 0 {
		return
	}
	m.modelMu.Lock()
	if m.modelCool == nil {
		m.modelCool = make(map[string]map[string]int64)
	}
	mm := m.modelCool[accountID]
	if mm == nil {
		mm = make(map[string]int64)
		m.modelCool[accountID] = mm
	}
	mm[model] = until
	m.modelMu.Unlock()
	// 持久化；失败仅忽略（内存仍生效）
	if m.cat != nil {
		_ = m.cat.UpsertModelCooldown(accountID, model, until, lastErr)
	}
}

func (m *Manager) modelCooldownActive(accountID, model string, now int64) bool {
	if m == nil || accountID == "" || model == "" {
		return false
	}
	m.modelMu.Lock()
	defer m.modelMu.Unlock()
	mm := m.modelCool[accountID]
	if mm == nil {
		return false
	}
	until := mm[model]
	if until <= now {
		delete(mm, model)
		if len(mm) == 0 {
			delete(m.modelCool, accountID)
		}
		return false
	}
	return true
}

// ModelCooldownUntil 返回模型冷却截止（测试/运维）。
func (m *Manager) ModelCooldownUntil(accountID, model string) int64 {
	if m == nil {
		return 0
	}
	m.modelMu.Lock()
	defer m.modelMu.Unlock()
	if m.modelCool == nil {
		return 0
	}
	return m.modelCool[accountID][model]
}

// ListModelCooldowns 返回账号模型冷却（优先内存，合并 DB）。
func (m *Manager) ListModelCooldowns(accountID string) []catalog.ModelCooldown {
	if m == nil {
		return nil
	}
	now := time.Now().Unix()
	out := []catalog.ModelCooldown{}
	// memory snapshot
	m.modelMu.Lock()
	if accountID != "" {
		if mm := m.modelCool[accountID]; mm != nil {
			for model, until := range mm {
				if until > now {
					out = append(out, catalog.ModelCooldown{AccountID: accountID, Model: model, CooldownUntil: until, RemainingSec: until - now})
				}
			}
		}
	} else {
		for acc, mm := range m.modelCool {
			for model, until := range mm {
				if until > now {
					out = append(out, catalog.ModelCooldown{AccountID: acc, Model: model, CooldownUntil: until, RemainingSec: until - now})
				}
			}
		}
	}
	m.modelMu.Unlock()
	if m.cat != nil {
		if dbRows, err := m.cat.ListModelCooldowns(accountID, now); err == nil {
			// merge by account+model, prefer later until
			idx := map[string]int{}
			for i, r := range out {
				idx[r.AccountID+"|"+r.Model] = i
			}
			for _, r := range dbRows {
				k := r.AccountID + "|" + r.Model
				if i, ok := idx[k]; ok {
					if r.CooldownUntil > out[i].CooldownUntil {
						out[i] = r
					}
				} else {
					out = append(out, r)
					idx[k] = len(out) - 1
				}
			}
		}
	}
	return out
}
