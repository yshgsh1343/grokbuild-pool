package selector

import (
	"container/list"
	"sync"
)

// stickyEntry 为 stickyKey → primary/secondary account 绑定，含绝对过期时间（unix 秒）。
type stickyEntry struct {
	key       string
	accountID string // primary
	secondary string
	expiresAt int64
}

// stickyLRU 为带每条目 TTL 的并发安全粘性绑定 LRU。
type stickyLRU struct {
	mu     sync.Mutex
	max    int
	ttlSec int64
	ll     *list.List                     // 队首为最近使用
	items  map[string]*list.Element       // stickyKey → 链表元素
	byAcct map[string]map[string]struct{} // accountID → stickyKey 集合
}

func newStickyLRU(max int, ttlSec int64) *stickyLRU {
	if max <= 0 {
		max = DefaultStickyMax
	}
	if ttlSec <= 0 {
		ttlSec = DefaultStickyTTLSec
	}
	return &stickyLRU{
		max:    max,
		ttlSec: ttlSec,
		ll:     list.New(),
		items:  make(map[string]*list.Element, min(max, 1024)),
		byAcct: make(map[string]map[string]struct{}),
	}
}

// get 在 now 未过期时返回 primary accountID。
func (s *stickyLRU) get(now int64, key string) (accountID string, ok bool) {
	primary, _, ok := s.getPair(now, key)
	return primary, ok
}

// getPair 返回 primary/secondary。
func (s *stickyLRU) getPair(now int64, key string) (primary, secondary string, ok bool) {
	if key == "" {
		return "", "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	el, found := s.items[key]
	if !found {
		return "", "", false
	}
	e := el.Value.(*stickyEntry)
	if e.expiresAt > 0 && e.expiresAt <= now {
		s.removeElement(el)
		return "", "", false
	}
	e.expiresAt = now + s.ttlSec
	s.ll.MoveToFront(el)
	return e.accountID, e.secondary, true
}

// put 绑定 primary；若已有不同 primary，则旧 primary 降为 secondary。
func (s *stickyLRU) put(now int64, key, accountID string) {
	s.putPrimary(now, key, accountID, true)
}

// putPrimary 设置 primary；promoteSecondary=false 时不改 secondary。
func (s *stickyLRU) putPrimary(now int64, key, accountID string, shiftOldToSecondary bool) {
	if key == "" || accountID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, found := s.items[key]; found {
		e := el.Value.(*stickyEntry)
		if e.accountID != accountID {
			s.unindexAccount(e.accountID, key)
			if shiftOldToSecondary && e.accountID != "" && e.accountID != accountID {
				// 旧主号降为次选（若次选碰巧等于新主号则清空）
				if e.secondary == accountID {
					e.secondary = ""
				} else if e.secondary == "" {
					e.secondary = e.accountID
					s.indexAccount(e.secondary, key)
				} else {
					// 保留已有 secondary，仅替换 primary
				}
			}
			e.accountID = accountID
			s.indexAccount(accountID, key)
		}
		e.expiresAt = now + s.ttlSec
		s.ll.MoveToFront(el)
		return
	}
	for s.ll.Len() >= s.max {
		oldest := s.ll.Back()
		if oldest == nil {
			break
		}
		s.removeElement(oldest)
	}
	e := &stickyEntry{key: key, accountID: accountID, expiresAt: now + s.ttlSec}
	el := s.ll.PushFront(e)
	s.items[key] = el
	s.indexAccount(accountID, key)
}

// putSecondary 设置/刷新 secondary（不改变 primary）。
func (s *stickyLRU) putSecondary(now int64, key, accountID string) {
	if key == "" || accountID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	el, found := s.items[key]
	if !found {
		// 没有 primary 时不单独建 secondary
		return
	}
	e := el.Value.(*stickyEntry)
	if e.accountID == accountID {
		// secondary 不能等于 primary
		if e.secondary != "" {
			s.unindexAccount(e.secondary, key)
			e.secondary = ""
		}
		e.expiresAt = now + s.ttlSec
		s.ll.MoveToFront(el)
		return
	}
	if e.secondary != "" && e.secondary != accountID {
		s.unindexAccount(e.secondary, key)
	}
	e.secondary = accountID
	s.indexAccount(accountID, key)
	e.expiresAt = now + s.ttlSec
	s.ll.MoveToFront(el)
}

func (s *stickyLRU) deleteKey(key string) {
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.items[key]; ok {
		s.removeElement(el)
	}
}

func (s *stickyLRU) deleteAccount(accountID string) {
	if accountID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, ok := s.byAcct[accountID]
	if !ok {
		return
	}
	toDel := make([]string, 0, len(keys))
	for k := range keys {
		toDel = append(toDel, k)
	}
	for _, k := range toDel {
		el, ok := s.items[k]
		if !ok {
			continue
		}
		e := el.Value.(*stickyEntry)
		// 若仅是 secondary 命中，只清 secondary；primary 命中则整键删除。
		if e.accountID == accountID {
			s.removeElement(el)
			continue
		}
		if e.secondary == accountID {
			s.unindexAccount(accountID, k)
			e.secondary = ""
		}
	}
}

func (s *stickyLRU) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ll.Len()
}

func (s *stickyLRU) removeElement(el *list.Element) {
	e := el.Value.(*stickyEntry)
	s.ll.Remove(el)
	delete(s.items, e.key)
	s.unindexAccount(e.accountID, e.key)
	if e.secondary != "" {
		s.unindexAccount(e.secondary, e.key)
	}
}

func (s *stickyLRU) indexAccount(accountID, key string) {
	if accountID == "" {
		return
	}
	m, ok := s.byAcct[accountID]
	if !ok {
		m = make(map[string]struct{})
		s.byAcct[accountID] = m
	}
	m[key] = struct{}{}
}

func (s *stickyLRU) unindexAccount(accountID, key string) {
	m, ok := s.byAcct[accountID]
	if !ok {
		return
	}
	delete(m, key)
	if len(m) == 0 {
		delete(s.byAcct, accountID)
	}
}
