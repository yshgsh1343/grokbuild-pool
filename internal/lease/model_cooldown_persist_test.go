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

func TestModelCooldownPersistsAcrossManagers(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "pool.db")
	cat, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	acc := catalog.Account{
		ID: "acct-p", IdentityKey: "ik", Priority: 1, Enabled: true,
		Lifecycle: catalog.LifecycleActive, AccessToken: "a", RefreshToken: "r",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	if err := cat.UpsertMany([]catalog.Account{acc}); err != nil {
		t.Fatal(err)
	}
	idx := hot.New(hot.Config{HotSize: 8, MaxInflightPerAccount: 2})
	if _, err := idx.LoadEligible(cat); err != nil {
		t.Fatal(err)
	}
	sel := selector.New(idx, selector.DefaultConfig())
	mgr := New(cat, idx, sel, DefaultConfig())
	_ = idx.AddInflight("acct-p")
	if err := mgr.Release(context.Background(), Lease{AccountID: "acct-p", Model: "grok-4.5"}, Result{Success: false, StatusCode: 429}); err != nil {
		t.Fatal(err)
	}
	_ = cat.Close()

	cat2, err := catalog.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cat2.Close() })
	idx2 := hot.New(hot.Config{HotSize: 8, MaxInflightPerAccount: 2})
	if _, err := idx2.LoadEligible(cat2); err != nil {
		t.Fatal(err)
	}
	sel2 := selector.New(idx2, selector.DefaultConfig())
	mgr2 := New(cat2, idx2, sel2, DefaultConfig())
	if until := mgr2.ModelCooldownUntil("acct-p", "grok-4.5"); until <= time.Now().Unix() {
		t.Fatalf("expected loaded model cooldown, got %d", until)
	}
}
