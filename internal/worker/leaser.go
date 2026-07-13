package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
	"github.com/yshgsh1343/grokbuild2api/internal/hot"
	"github.com/yshgsh1343/grokbuild2api/internal/lease"
	"github.com/yshgsh1343/grokbuild2api/internal/selector"
	"github.com/yshgsh1343/grokbuild2api/internal/store"
)

// storeLeaser adapts AccountStore + hot + selector + cluster inflight/cooldown
// to executor.Leaser without requiring concrete *catalog.Catalog.
type storeLeaser struct {
	store       store.AccountStore
	idx         *hot.Index
	sel         *selector.Selector
	state       clusterstate.State
	maxInflight int32
	maxAttempts int
}

func newStoreLeaser(st store.AccountStore, idx *hot.Index, sel *selector.Selector, state clusterstate.State, maxInflight int32, maxAttempts int) *storeLeaser {
	if maxInflight <= 0 {
		maxInflight = 2
	}
	if maxAttempts <= 0 {
		maxAttempts = 4
	}
	return &storeLeaser{store: st, idx: idx, sel: sel, state: state, maxInflight: maxInflight, maxAttempts: maxAttempts}
}

func (m *storeLeaser) Acquire(ctx context.Context, stickyKey string) (lease.Lease, error) {
	tried := make(map[string]struct{}, m.maxAttempts)
	var last error
	for attempt := 1; attempt <= m.maxAttempts; attempt++ {
		l, err := m.AcquireAttempt(ctx, stickyKey, tried)
		if err == nil {
			return l, nil
		}
		last = err
		if errors.Is(err, lease.ErrNoAccount) {
			return lease.Lease{}, lease.ErrNoAccount
		}
	}
	if last == nil {
		last = lease.ErrNoAccount
	}
	return lease.Lease{}, last
}

func (m *storeLeaser) AcquireAttempt(ctx context.Context, stickyKey string, tried map[string]struct{}) (lease.Lease, error) {
	if err := ctx.Err(); err != nil {
		return lease.Lease{}, err
	}
	if tried == nil {
		tried = map[string]struct{}{}
	}
	now := time.Now().Unix()
	id, ok := m.sel.PickExcluding(now, stickyKey, tried)
	if !ok || id == "" {
		return lease.Lease{}, lease.ErrNoAccount
	}
	tried[id] = struct{}{}

	// cluster hard gates
	if m.state != nil {
		if until, active, _ := m.state.GetCooldown(ctx, id); active && until.After(time.Now()) {
			return lease.Lease{}, fmt.Errorf("lease: account %s cooling down", id)
		}
		n, err := m.state.IncrInflight(ctx, id, 15*time.Minute)
		if err != nil {
			return lease.Lease{}, err
		}
		if n > int64(m.maxInflight) {
			_, _ = m.state.DecrInflight(ctx, id)
			return lease.Lease{}, fmt.Errorf("lease: account %s over inflight", id)
		}
	}

	acc, err := m.store.Get(ctx, id)
	if err != nil {
		if m.state != nil {
			_, _ = m.state.DecrInflight(ctx, id)
		}
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, catalog.ErrNotFound) {
			return lease.Lease{}, fmt.Errorf("lease: catalog miss for %s: %w", id, err)
		}
		return lease.Lease{}, fmt.Errorf("lease: get %s: %w", id, err)
	}
	if !accountUsable(acc, now) {
		if m.state != nil {
			_, _ = m.state.DecrInflight(ctx, id)
		}
		return lease.Lease{}, fmt.Errorf("lease: account %s not usable", id)
	}
	// local hot inflight for selector scoring
	_ = m.idx.AddInflight(id)

	return lease.Lease{
		AccountID:   acc.ID,
		Revision:    uint64(acc.Revision),
		AccessToken: acc.AccessToken,
		ProxyURL:    acc.ProxyURL,
		ProxyMode:   acc.ProxyMode,
		StickyKey:   stickyKey,
		Attempt:     len(tried),
	}, nil
}

func (m *storeLeaser) Release(ctx context.Context, l lease.Lease, result lease.Result) error {
	if l.AccountID != "" {
		_ = m.idx.SubInflight(l.AccountID)
		if m.state != nil {
			_, _ = m.state.DecrInflight(ctx, l.AccountID)
		}
	}
	if result.Success {
		if l.StickyKey != "" && m.state != nil {
			_ = m.state.PutSticky(ctx, l.StickyKey, clusterstate.StickyBinding{
				AccountID: l.AccountID,
				Exp:       time.Now().Add(30 * time.Minute).Unix(),
			}, 30*time.Minute)
		}
		return nil
	}

	// failure bookkeeping
	cooldown := cooldownForStatus(result.StatusCode, result.RetryAfter)
	if cooldown > 0 {
		until := time.Now().Add(cooldown)
		_ = m.idx.SetCooldown(l.AccountID, until.Unix())
		if m.state != nil {
			_ = m.state.SetCooldown(ctx, l.AccountID, until)
			if l.StickyKey != "" {
				_ = m.state.ClearSticky(ctx, l.StickyKey)
			}
			_ = m.state.ClearStickyAccount(ctx, l.AccountID)
		}
		m.sel.ClearStickyAccount(l.AccountID)
		// async-ish durable patch
		cu := until.Unix()
		_ = m.store.PatchHealth(ctx, l.AccountID, catalog.HealthPatch{
			CooldownUntil: &cu,
			LastError:     strPtr(fmt.Sprintf("status=%d", result.StatusCode)),
		})
	}
	return nil
}

func accountUsable(a catalog.Account, now int64) bool {
	if !a.Enabled || a.ManualDisabled {
		return false
	}
	if a.Lifecycle != "" && a.Lifecycle != catalog.LifecycleActive {
		return false
	}
	if a.AccessToken == "" {
		return false
	}
	if a.CooldownUntil > now {
		return false
	}
	return true
}

func cooldownForStatus(code int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	switch code {
	case 401:
		return 2 * time.Minute
	case 402:
		return 5 * time.Minute
	case 403:
		return 15 * time.Minute
	case 429:
		return 1 * time.Minute
	case 0:
		return 30 * time.Second
	default:
		if code >= 500 {
			return 30 * time.Second
		}
		return 0
	}
}

func strPtr(s string) *string { return &s }
