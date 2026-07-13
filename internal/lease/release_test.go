package lease

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/hot"
	"github.com/yshgsh1343/grokbuild2api/internal/selector"
)

func openLeaseStack(t *testing.T) (*catalog.Catalog, *hot.Index, *Manager) {
	t.Helper()
	dir := t.TempDir()
	cat, err := catalog.Open(filepath.Join(dir, "pool.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	acc := catalog.Account{
		ID:           "acct-1",
		IdentityKey:  "ik-1",
		Email:        "a@b.c",
		Priority:     10,
		Enabled:      true,
		Lifecycle:    catalog.LifecycleActive,
		AccessToken:  "access-token-1",
		RefreshToken: "refresh-token-1",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		FailureCount: 10,
	}
	if err := cat.UpsertMany([]catalog.Account{acc}); err != nil {
		t.Fatal(err)
	}
	idx := hot.New(hot.Config{HotSize: 32, MaxInflightPerAccount: 4})
	if _, err := idx.LoadEligible(cat); err != nil {
		t.Fatal(err)
	}
	// 注入高失败分到热池
	if meta, ok := idx.Get("acct-1"); ok {
		meta.FailureScore = 10
		_, _ = idx.Promote(meta)
	}
	sel := selector.New(idx, selector.DefaultConfig())
	mgr := New(cat, idx, sel, DefaultConfig())
	return cat, idx, mgr
}

func TestReleaseSuccessDecaysFailureScore(t *testing.T) {
	cat, idx, mgr := openLeaseStack(t)
	l := Lease{AccountID: "acct-1", StickyKey: "s1"}
	// 模拟已 acquire 的 inflight
	if err := idx.AddInflight("acct-1"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Release(context.Background(), l, Result{Success: true}); err != nil {
		t.Fatal(err)
	}
	// 冷库 failure_count: 10 → decay → 4
	got, err := cat.Get("acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.FailureCount != 4 {
		t.Fatalf("failure_count=%d want 4", got.FailureCount)
	}
	if got.SuccessCount < 1 {
		t.Fatalf("success_count=%d", got.SuccessCount)
	}
	meta, ok := idx.Get("acct-1")
	if !ok {
		t.Fatal("missing hot meta")
	}
	if meta.FailureScore != 4 {
		t.Fatalf("hot FailureScore=%v want 4", meta.FailureScore)
	}
}

func TestReleaseFailureRaisesScoreAndCooldown(t *testing.T) {
	_, idx, mgr := openLeaseStack(t)
	// 先把失败分清到 0 再测 +1 路径：直接释放失败
	if err := idx.AddInflight("acct-1"); err != nil {
		t.Fatal(err)
	}
	l := Lease{AccountID: "acct-1"}
	if err := mgr.Release(context.Background(), l, Result{Success: false, StatusCode: 429}); err != nil {
		t.Fatal(err)
	}
	meta, ok := idx.Get("acct-1")
	if !ok {
		t.Fatal("missing hot")
	}
	if meta.FailureScore < 1 {
		t.Fatalf("FailureScore=%v", meta.FailureScore)
	}
	if meta.CooldownUntil <= time.Now().Unix() {
		t.Fatalf("expected cooldown, got %d", meta.CooldownUntil)
	}
}
