package clusterstate

import (
	"context"
	"sync"
	"time"
)

// Memory is a process-local State for tests and single-binary bootstrap.
// It is intentionally not multi-process safe.
type Memory struct {
	mu       sync.Mutex
	sticky   map[string]StickyBinding
	inflight map[string]int64
	cooldown map[string]int64
	shards   map[int]ShardLease
	workset  map[int][]scoredID
	refresh  map[string]int64 // account -> due unix
	locks    map[string]int64 // account -> lock exp unix
	closed   bool
}

type scoredID struct {
	id    string
	score float64
}

func NewMemory() *Memory {
	return &Memory{
		sticky:   make(map[string]StickyBinding),
		inflight: make(map[string]int64),
		cooldown: make(map[string]int64),
		shards:   make(map[int]ShardLease),
		workset:  make(map[int][]scoredID),
		refresh:  make(map[string]int64),
		locks:    make(map[string]int64),
	}
}

func (m *Memory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *Memory) guard() error {
	if m == nil || m.closed {
		return ErrClosed
	}
	return nil
}

func (m *Memory) GetSticky(ctx context.Context, stickyKey string) (StickyBinding, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return StickyBinding{}, err
	}
	b, ok := m.sticky[stickyKey]
	if !ok || (b.Exp > 0 && b.Exp < time.Now().Unix()) {
		return StickyBinding{}, ErrNotFound
	}
	return b, nil
}

func (m *Memory) PutSticky(ctx context.Context, stickyKey string, b StickyBinding, ttl time.Duration) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return err
	}
	if b.Exp == 0 {
		b.Exp = time.Now().Add(ttl).Unix()
	}
	m.sticky[stickyKey] = b
	return nil
}

func (m *Memory) ClearSticky(ctx context.Context, stickyKey string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return err
	}
	delete(m.sticky, stickyKey)
	return nil
}

func (m *Memory) ClearStickyAccount(ctx context.Context, accountID string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return err
	}
	for k, v := range m.sticky {
		if v.AccountID == accountID {
			delete(m.sticky, k)
		}
	}
	return nil
}

func (m *Memory) IncrInflight(ctx context.Context, accountID string, ttl time.Duration) (int64, error) {
	_ = ctx
	_ = ttl
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return 0, err
	}
	m.inflight[accountID]++
	return m.inflight[accountID], nil
}

func (m *Memory) DecrInflight(ctx context.Context, accountID string) (int64, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return 0, err
	}
	n := m.inflight[accountID] - 1
	if n <= 0 {
		delete(m.inflight, accountID)
		return 0, nil
	}
	m.inflight[accountID] = n
	return n, nil
}

func (m *Memory) GetInflight(ctx context.Context, accountID string) (int64, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return 0, err
	}
	return m.inflight[accountID], nil
}

func (m *Memory) SetCooldown(ctx context.Context, accountID string, until time.Time) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return err
	}
	m.cooldown[accountID] = until.Unix()
	return nil
}

func (m *Memory) GetCooldown(ctx context.Context, accountID string) (time.Time, bool, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return time.Time{}, false, err
	}
	u, ok := m.cooldown[accountID]
	if !ok || u <= time.Now().Unix() {
		return time.Time{}, false, nil
	}
	return time.Unix(u, 0), true, nil
}

func (m *Memory) ClearCooldown(ctx context.Context, accountID string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return err
	}
	delete(m.cooldown, accountID)
	return nil
}

func (m *Memory) TryAcquireShard(ctx context.Context, shardID int, workerID string, ttl time.Duration) (ShardLease, bool, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return ShardLease{}, false, err
	}
	now := time.Now().Unix()
	if cur, ok := m.shards[shardID]; ok && cur.Exp > now && cur.WorkerID != workerID {
		return cur, false, nil
	}
	lease := ShardLease{
		ShardID:  shardID,
		WorkerID: workerID,
		Version:  m.shards[shardID].Version + 1,
		Exp:      time.Now().Add(ttl).Unix(),
	}
	m.shards[shardID] = lease
	return lease, true, nil
}

func (m *Memory) RenewShard(ctx context.Context, shardID int, workerID string, ttl time.Duration) (bool, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return false, err
	}
	cur, ok := m.shards[shardID]
	if !ok || cur.WorkerID != workerID {
		return false, nil
	}
	cur.Exp = time.Now().Add(ttl).Unix()
	m.shards[shardID] = cur
	return true, nil
}

func (m *Memory) ReleaseShard(ctx context.Context, shardID int, workerID string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return err
	}
	if cur, ok := m.shards[shardID]; ok && cur.WorkerID == workerID {
		delete(m.shards, shardID)
	}
	return nil
}

func (m *Memory) GetShardOwner(ctx context.Context, shardID int) (ShardLease, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return ShardLease{}, err
	}
	cur, ok := m.shards[shardID]
	if !ok || cur.Exp <= time.Now().Unix() {
		return ShardLease{}, ErrNotFound
	}
	return cur, nil
}

func (m *Memory) ReplaceWorkset(ctx context.Context, shardID int, accountIDs []string, scores []float64) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return err
	}
	items := make([]scoredID, 0, len(accountIDs))
	for i, id := range accountIDs {
		sc := 0.0
		if i < len(scores) {
			sc = scores[i]
		}
		items = append(items, scoredID{id: id, score: sc})
	}
	m.workset[shardID] = items
	return nil
}

func (m *Memory) ListWorkset(ctx context.Context, shardID int, limit int) ([]string, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return nil, err
	}
	items := m.workset[shardID]
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, items[i].id)
	}
	return out, nil
}

func (m *Memory) AddRefreshDue(ctx context.Context, accountID string, due time.Time) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return err
	}
	m.refresh[accountID] = due.Unix()
	return nil
}

func (m *Memory) ClaimRefreshDue(ctx context.Context, refresherID string, now time.Time, limit int, lockTTL time.Duration) ([]string, error) {
	_ = ctx
	_ = refresherID
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1
	}
	nowU := now.Unix()
	out := make([]string, 0, limit)
	for id, due := range m.refresh {
		if len(out) >= limit {
			break
		}
		if due > nowU {
			continue
		}
		if exp, ok := m.locks[id]; ok && exp > nowU {
			continue
		}
		m.locks[id] = now.Add(lockTTL).Unix()
		delete(m.refresh, id)
		out = append(out, id)
	}
	return out, nil
}
