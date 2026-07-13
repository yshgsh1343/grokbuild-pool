package refresh

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
)

// 哨兵错误。
var (
	ErrInvalidInput = errors.New("refresh: invalid input")
	ErrClosed       = errors.New("refresh: closed")
	ErrNotRunning   = errors.New("refresh: workers not running")
)

// Service 执行后台预刷新与请求路径 EnsureFresh。
type Service struct {
	cat   *catalog.Catalog
	oauth OAuthClient
	cfg   Config
	lim   *rate.Limiter
	sf    singleflight.Group
	fail  FailurePolicy
	succ  SuccessPolicy

	// 统计
	refreshOK   atomic.Int64
	refreshFail atomic.Int64
	ensureHit   atomic.Int64 // 已新鲜而短路

	mu      sync.Mutex
	running bool
	stop    context.CancelFunc
	jobs    chan string
	wg      sync.WaitGroup
}

// New 构造 refresh Service。cat 与 oauth 不可为 nil。
// fail/succ 可为 nil → 二者均使用由 cfg 构建的 DefaultPolicy。
func New(cat *catalog.Catalog, oauth OAuthClient, cfg Config, fail FailurePolicy, succ SuccessPolicy) *Service {
	if cat == nil || oauth == nil {
		panic("refresh: nil catalog or oauth client")
	}
	cfg = cfg.normalize()
	if fail == nil || succ == nil {
		def := NewDefaultPolicy(cfg)
		if fail == nil {
			fail = def
		}
		if succ == nil {
			succ = def
		}
	}
	return &Service{
		cat:   cat,
		oauth: oauth,
		cfg:   cfg,
		lim:   rate.NewLimiter(rate.Limit(cfg.QPS), cfg.Burst),
		fail:  fail,
		succ:  succ,
	}
}

// Config 返回当前配置。
// ApplyConfig 热更新 QPS/Skew 等（已启动的 worker 数量不变）。
func (s *Service) ApplyConfig(cfg Config) {
	if s == nil {
		return
	}
	cfg = cfg.normalize()
	s.mu.Lock()
	s.cfg = cfg
	if s.lim != nil {
		s.lim.SetLimit(rate.Limit(cfg.QPS))
		b := cfg.Burst
		if b <= 0 {
			b = 1
		}
		s.lim.SetBurst(b)
	}
	s.mu.Unlock()
}

func (s *Service) Config() Config {
	if s == nil {
		return Config{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// Stats 返回测试/运维用计数。
func (s *Service) Stats() (ok, fail, ensureFreshHit int64) {
	return s.refreshOK.Load(), s.refreshFail.Load(), s.ensureHit.Load()
}

// Start 启动后台扫描器与 worker 池。
// 仅可安全调用一次；第二次返回 ErrInvalidInput。
func (s *Service) Start(parent context.Context) error {
	if s == nil {
		return fmt.Errorf("%w: nil service", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return fmt.Errorf("%w: already running", ErrInvalidInput)
	}
	ctx, cancel := context.WithCancel(parent)
	s.stop = cancel
	s.jobs = make(chan string, s.cfg.JobBuffer)
	s.running = true

	for i := 0; i < s.cfg.Workers; i++ {
		s.wg.Add(1)
		go s.workerLoop(ctx)
	}
	s.wg.Add(1)
	go s.scanLoop(ctx)
	return nil
}

// Stop 取消 worker 并等待退出（最多 15s，避免滚动发布挂死）。
func (s *Service) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	cancel := s.stop
	s.running = false
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		// 超时放弃等待；worker 可能仍在 OAuth 网络调用上，进程退出后由 OS 回收
	}
}

func (s *Service) workerLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case id, ok := <-s.jobs:
			if !ok {
				return
			}
			// 尽力后台刷新；错误由 policy 记录。
			_, _ = s.EnsureFresh(ctx, id)
		}
	}
}

func (s *Service) scanLoop(ctx context.Context) {
	defer s.wg.Done()
	// 立即首扫，再按 ticker。
	s.enqueueExpiring(ctx)
	t := time.NewTicker(s.cfg.ScanInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.enqueueExpiring(ctx)
		}
	}
}

func (s *Service) enqueueExpiring(ctx context.Context) {
	before := time.Now().Unix() + s.cfg.SkewSec
	list, err := s.cat.ListExpiring(s.cfg.ScanLimit, before)
	if err != nil {
		return
	}
	for _, a := range list {
		select {
		case <-ctx.Done():
			return
		case s.jobs <- a.ID:
		default:
			// 缓冲满：本轮跳过剩余；下次扫描重试。
			return
		}
	}
}

// RefreshExpiring 列出最多 limit 个临近过期账号并对每个 EnsureFresh。
// 阻塞至已提交工作完成（singleflight + QPS）。
// 用于测试与一次性管理操作；后台路径用 Start()。
func (s *Service) RefreshExpiring(ctx context.Context, limit int) (int, error) {
	if s == nil {
		return 0, fmt.Errorf("%w: nil service", ErrInvalidInput)
	}
	if limit <= 0 {
		limit = s.cfg.ScanLimit
	}
	before := time.Now().Unix() + s.cfg.SkewSec
	list, err := s.cat.ListExpiring(limit, before)
	if err != nil {
		return 0, err
	}
	if len(list) == 0 {
		return 0, nil
	}

	// 用信号量将并行度限制在 worker 数。
	sem := make(chan struct{}, s.cfg.Workers)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		nOK  int
		last error
	)
	for _, a := range list {
		if err := ctx.Err(); err != nil {
			break
		}
		id := a.ID
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			_, err := s.EnsureFresh(ctx, id)
			mu.Lock()
			if err == nil {
				nOK++
			} else {
				last = err
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return nOK, last
}

// EnsureFresh 返回 accountID 可用的 TokenSet；当 expires_at < now+SkewSec
// 时在 singleflight 下刷新。
func (s *Service) EnsureFresh(ctx context.Context, accountID string) (catalog.TokenSet, error) {
	if s == nil {
		return catalog.TokenSet{}, fmt.Errorf("%w: nil service", ErrInvalidInput)
	}
	if accountID == "" {
		return catalog.TokenSet{}, fmt.Errorf("%w: empty account id", ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return catalog.TokenSet{}, err
	}

	acct, err := s.cat.Get(accountID)
	if err != nil {
		return catalog.TokenSet{}, err
	}
	now := time.Now().Unix()
	if acct.ExpiresAt >= now+s.cfg.SkewSec && acct.AccessToken != "" {
		s.ensureHit.Add(1)
		return catalog.TokenSet{
			AccessToken:  acct.AccessToken,
			RefreshToken: acct.RefreshToken,
			ExpiresAt:    acct.ExpiresAt,
		}, nil
	}

	v, err, _ := s.sf.Do(accountID, func() (any, error) {
		// singleflight 内再检查：其他等待者可能已刷新。
		cur, err := s.cat.Get(accountID)
		if err != nil {
			return catalog.TokenSet{}, err
		}
		now := time.Now().Unix()
		if cur.ExpiresAt >= now+s.cfg.SkewSec && cur.AccessToken != "" {
			return catalog.TokenSet{
				AccessToken:  cur.AccessToken,
				RefreshToken: cur.RefreshToken,
				ExpiresAt:    cur.ExpiresAt,
			}, nil
		}
		return s.refreshAndPersist(ctx, cur)
	})
	if err != nil {
		return catalog.TokenSet{}, err
	}
	set, ok := v.(catalog.TokenSet)
	if !ok {
		return catalog.TokenSet{}, fmt.Errorf("refresh: unexpected singleflight type %T", v)
	}
	return set, nil
}

// ForceRefresh 始终对 accountID 执行 OAuth 刷新（401 恢复 / 管理强制）。
// 与 EnsureFresh 不同，不会因 access token 仍有效而短路。
// 同一 id 的并发 ForceRefresh 共享与 EnsureFresh 分离的 singleflight 键。
func (s *Service) ForceRefresh(ctx context.Context, accountID string) (catalog.TokenSet, error) {
	if s == nil {
		return catalog.TokenSet{}, fmt.Errorf("%w: nil service", ErrInvalidInput)
	}
	if accountID == "" {
		return catalog.TokenSet{}, fmt.Errorf("%w: empty account id", ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return catalog.TokenSet{}, err
	}

	// 使用独立键，避免并发 EnsureFresh 短路满足 401 路径所需的强制网络刷新。
	v, err, _ := s.sf.Do("force:"+accountID, func() (any, error) {
		cur, err := s.cat.Get(accountID)
		if err != nil {
			return catalog.TokenSet{}, err
		}
		return s.refreshAndPersist(ctx, cur)
	})
	if err != nil {
		return catalog.TokenSet{}, err
	}
	set, ok := v.(catalog.TokenSet)
	if !ok {
		return catalog.TokenSet{}, fmt.Errorf("refresh: unexpected singleflight type %T", v)
	}
	return set, nil
}

// refreshAndPersist 限流、调用 OAuth、CAS 落库（冲突时重试一次）。
func (s *Service) refreshAndPersist(ctx context.Context, acct catalog.Account) (catalog.TokenSet, error) {
	if err := s.lim.Wait(ctx); err != nil {
		return catalog.TokenSet{}, err
	}
	if err := ctx.Err(); err != nil {
		return catalog.TokenSet{}, err
	}

	tokens, err := s.oauth.Refresh(ctx, acct.RefreshToken)
	if err != nil {
		s.refreshFail.Add(1)
		if s.fail != nil {
			_ = s.fail.OnRefreshFailure(ctx, s.cat, acct.ID, err)
		}
		return catalog.TokenSet{}, fmt.Errorf("refresh: oauth %s: %w", acct.ID, err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		s.refreshFail.Add(1)
		err := fmt.Errorf("%w: empty tokens from oauth", ErrInvalidInput)
		if s.fail != nil {
			_ = s.fail.OnRefreshFailure(ctx, s.cat, acct.ID, err)
		}
		return catalog.TokenSet{}, err
	}

	err = s.cat.UpdateTokens(acct.ID, acct.Revision, tokens)
	if errors.Is(err, catalog.ErrCASConflict) {
		// 重新读取并重试一次。
		cur, getErr := s.cat.Get(acct.ID)
		if getErr != nil {
			s.refreshFail.Add(1)
			return catalog.TokenSet{}, getErr
		}
		now := time.Now().Unix()
		// 对端已刷新足够远——使用其结果。
		if cur.ExpiresAt >= now+s.cfg.SkewSec && cur.AccessToken != "" {
			s.refreshOK.Add(1)
			return catalog.TokenSet{
				AccessToken:  cur.AccessToken,
				RefreshToken: cur.RefreshToken,
				ExpiresAt:    cur.ExpiresAt,
			}, nil
		}
		err = s.cat.UpdateTokens(acct.ID, cur.Revision, tokens)
	}
	if err != nil {
		s.refreshFail.Add(1)
		if s.fail != nil {
			_ = s.fail.OnRefreshFailure(ctx, s.cat, acct.ID, err)
		}
		return catalog.TokenSet{}, fmt.Errorf("refresh: persist %s: %w", acct.ID, err)
	}

	s.refreshOK.Add(1)
	if s.succ != nil {
		_ = s.succ.OnRefreshSuccess(ctx, s.cat, acct.ID)
	}
	return tokens, nil
}
