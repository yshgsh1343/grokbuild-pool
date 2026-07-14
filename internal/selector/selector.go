package selector

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/hot"
)

// Selector 以粘性亲和 + stable_rr / power-of-two-choices 从合格热账号中选号。
type Selector struct {
	idx *hot.Index

	// mu 保护 cfg 与 sticky 指针；ApplyConfig 可与 Pick 并发。
	mu     sync.RWMutex
	cfg    Config
	sticky *stickyLRU

	// rngMu 保护 rng；测试可通过 SetRand 注入种子源。
	rngMu sync.Mutex
	rng   *rand.Rand

	// rrMu 保护 stable_rr 游标。
	rrMu     sync.Mutex
	rrCursor uint64

	// sticky 命中统计（原子）。
	stickyPrimaryHits   atomic.Uint64
	stickySecondaryHits atomic.Uint64
	stickyReselects     atomic.Uint64
}

// New 基于给定热索引构建 Selector。
// idx 不可为 nil；cfg 零值由 DefaultConfig 填充。
func New(idx *hot.Index, cfg Config) *Selector {
	if idx == nil {
		panic("selector: nil hot index")
	}
	cfg = cfg.normalize()
	return &Selector{
		idx:    idx,
		cfg:    cfg,
		sticky: newStickyLRU(cfg.StickyMax, cfg.StickyTTLSec),
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Config 返回当前配置的副本。
func (s *Selector) Config() Config {
	if s == nil {
		return Config{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// ApplyConfig 热更新打分权重与 pow2_k 等（粘性 LRU 容量/TTL 变更会重建空 LRU）。
func (s *Selector) ApplyConfig(cfg Config) {
	if s == nil {
		return
	}
	cfg = cfg.normalize()
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.cfg
	s.cfg = cfg
	if old.StickyTTLSec != cfg.StickyTTLSec || old.StickyMax != cfg.StickyMax {
		s.sticky = newStickyLRU(cfg.StickyMax, cfg.StickyTTLSec)
	}
}

// SetRand 替换内部 RNG（确定性测试用）。勿用于生产热路径。
func (s *Selector) SetRand(r *rand.Rand) {
	s.rngMu.Lock()
	defer s.rngMu.Unlock()
	if r == nil {
		s.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
		return
	}
	s.rng = r
}

// Pick 选择在 now（unix 秒；<=0 则 time.Now）合格的账号 id。
// stickyKey 可为空（无粘性）。非粘性命中且 stickyKey 非空时写入绑定供后续命中。
func (s *Selector) Pick(now int64, stickyKey string) (id string, ok bool) {
	return s.PickExcluding(now, stickyKey, nil)
}

// PickExcluding 类似 Pick，但绝不返回 exclude 中的 id。
func (s *Selector) PickExcluding(now int64, stickyKey string, exclude map[string]struct{}) (id string, ok bool) {
	if now <= 0 {
		now = time.Now().Unix()
	}

	s.mu.RLock()
	cfg := s.cfg
	sticky := s.sticky
	s.mu.RUnlock()

	// 1) 粘性：先 primary，再 secondary；都不可用才重选。
	var stickyPrimary, stickySecondary string
	if stickyKey != "" {
		if p, sec, hit := sticky.getPair(now, stickyKey); hit {
			stickyPrimary, stickySecondary = p, sec
			if p != "" && !excluded(exclude, p) {
				if m, found := s.idx.Get(p); found && s.idx.EligibleMeta(m, now) {
					s.stickyPrimaryHits.Add(1)
					return p, true
				}
			}
			if sec != "" && !excluded(exclude, sec) {
				if m, found := s.idx.Get(sec); found && s.idx.EligibleMeta(m, now) {
					// secondary 顶上时提升为 primary，旧 primary 仍可保留为 secondary
					sticky.put(now, stickyKey, sec)
					s.stickySecondaryHits.Add(1)
					return sec, true
				}
			}
		}
	}

	// 2) 按策略选号。
	switch cfg.Strategy {
	case StrategyStableRR, "round_robin", "rr":
		id, ok = s.stableRRPick(now, exclude)
	default:
		id, ok = s.pow2Pick(now, exclude, cfg)
	}
	if !ok {
		return "", false
	}
	if stickyKey != "" {
		// 新选号写入 primary；若已有不同旧主号，sticky.put 会自动降为 secondary
		sticky.put(now, stickyKey, id)
		if stickyPrimary != "" || stickySecondary != "" {
			s.stickyReselects.Add(1)
		}
	}
	return id, true
}

// BindSticky 强制绑定 stickyKey → accountID（刷新 TTL）。
func (s *Selector) BindSticky(stickyKey, accountID string) {
	if s == nil {
		return
	}
	now := time.Now().Unix()
	s.mu.RLock()
	sticky := s.sticky
	s.mu.RUnlock()
	sticky.put(now, stickyKey, accountID)
}

// BindStickySecondary 设置会话次选号（主号不可用时优先）。
func (s *Selector) BindStickySecondary(stickyKey, accountID string) {
	if s == nil {
		return
	}
	now := time.Now().Unix()
	s.mu.RLock()
	sticky := s.sticky
	s.mu.RUnlock()
	sticky.putSecondary(now, stickyKey, accountID)
}

// StickyPair 返回会话 primary/secondary（测试/运维）。
func (s *Selector) StickyPair(stickyKey string) (primary, secondary string, ok bool) {
	if s == nil {
		return "", "", false
	}
	now := time.Now().Unix()
	s.mu.RLock()
	sticky := s.sticky
	s.mu.RUnlock()
	return sticky.getPair(now, stickyKey)
}

// ClearStickyKey 删除一条粘性绑定（例如键范围失败后）。
func (s *Selector) ClearStickyKey(stickyKey string) {
	if s == nil {
		return
	}
	s.mu.RLock()
	sticky := s.sticky
	s.mu.RUnlock()
	sticky.deleteKey(stickyKey)
}

// ClearStickyAccount 删除所有指向 accountID 的粘性绑定。
// 在 mark-bad 路径（429/401/402）调用，避免粘性钉死失败账号。
func (s *Selector) ClearStickyAccount(accountID string) {
	if s == nil {
		return
	}
	s.mu.RLock()
	sticky := s.sticky
	s.mu.RUnlock()
	sticky.deleteAccount(accountID)
}

// StickyLen 返回当前粘性 LRU 大小（测试/运维）。
func (s *Selector) StickyLen() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	sticky := s.sticky
	s.mu.RUnlock()
	return sticky.len()
}

// Score 计算 meta 在给定抖动样本下的选号分。
// 越高越好。导出以便确定性单测。
//
//	score = WPriority*priority - WInflight*inflight - WFailure*failureScore + jitter
func (s *Selector) Score(m catalog.HotMeta, jitter float64) float64 {
	cfg := s.Config()
	return scoreWith(cfg, m, jitter)
}

func scoreWith(cfg Config, m catalog.HotMeta, jitter float64) float64 {
	return cfg.WPriority*float64(m.Priority) -
		cfg.WInflight*float64(m.Inflight) -
		cfg.WFailure*float64(m.FailureScore) +
		jitter
}

// stableRRPick 在最高 priority 的可用层内 RoundRobin。
// 对齐 CPA 稳模式：少随机、均匀分摊、避免浪潮式打高优号。
func (s *Selector) stableRRPick(now int64, exclude map[string]struct{}) (string, bool) {
	all := s.idx.Eligible(now)
	if len(all) == 0 {
		return "", false
	}
	// 过滤 exclude，找最高 priority。
	bestPri := int32(-1 << 30)
	cands := make([]catalog.HotMeta, 0, len(all))
	for _, m := range all {
		if excluded(exclude, m.ID) {
			continue
		}
		if m.Priority > bestPri {
			bestPri = m.Priority
			cands = cands[:0]
			cands = append(cands, m)
			continue
		}
		if m.Priority == bestPri {
			cands = append(cands, m)
		}
	}
	if len(cands) == 0 {
		return "", false
	}
	// 稳定排序：ID 升序
	for i := 1; i < len(cands); i++ {
		j := i
		for j > 0 && cands[j].ID < cands[j-1].ID {
			cands[j], cands[j-1] = cands[j-1], cands[j]
			j--
		}
	}
	s.rrMu.Lock()
	idx := int(s.rrCursor % uint64(len(cands)))
	s.rrCursor++
	s.rrMu.Unlock()
	return cands[idx].ID, true
}

// pow2Pick 采样最多 K 个合格候选并返回最高分者。
// 使用 hot.Index.SampleEligible（蓄水池），选号路径不分配整热集快照
// （M11 G2：目标 ≥20k picks/s）。
func (s *Selector) pow2Pick(now int64, exclude map[string]struct{}, cfg Config) (string, bool) {
	k := cfg.Pow2K
	if k <= 0 {
		k = 1
	}

	s.rngMu.Lock()
	// 在 rng 锁下采样，使 Intn 与抖动抽取串行。
	picked := s.idx.SampleEligible(now, k, exclude, s.rng.Intn)
	if len(picked) == 0 {
		s.rngMu.Unlock()
		return "", false
	}

	bestID := ""
	bestScore := 0.0
	for i, m := range picked {
		j := sampleJitterLocked(s.rng, cfg.JitterAmp)
		sc := scoreWith(cfg, m, j)
		if i == 0 || sc > bestScore || (sc == bestScore && m.ID < bestID) {
			bestScore = sc
			bestID = m.ID
		}
	}
	s.rngMu.Unlock()
	return bestID, bestID != ""
}

// sampleJitterLocked 返回 U(-JitterAmp, +JitterAmp)。调用方持有 rngMu。
func sampleJitterLocked(rng *rand.Rand, amp float64) float64 {
	if amp == 0 {
		return 0
	}
	// Float64() 为 [0.0, 1.0)；映射到 [-amp, +amp)
	return (rng.Float64()*2 - 1) * amp
}

func excluded(exclude map[string]struct{}, id string) bool {
	if exclude == nil {
		return false
	}
	_, ok := exclude[id]
	return ok
}

// StickyStats 返回 sticky primary/secondary 命中与重选计数。
func (s *Selector) StickyStats() (primaryHits, secondaryHits, reselects uint64) {
	if s == nil {
		return 0, 0, 0
	}
	return s.stickyPrimaryHits.Load(), s.stickySecondaryHits.Load(), s.stickyReselects.Load()
}
