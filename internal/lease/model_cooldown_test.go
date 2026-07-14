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

func TestModelCooldownDoesNotAccountCooldown(t *testing.T) {
	dir := t.TempDir()
	cat, err := catalog.Open(filepath.Join(dir, "pool.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	acc := catalog.Account{
		ID: "acct-m", IdentityKey: "ik", Priority: 1, Enabled: true,
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
	if err := idx.AddInflight("acct-m"); err != nil {
		t.Fatal(err)
	}
	l := Lease{AccountID: "acct-m", Model: "grok-4.5"}
	if err := mgr.Release(context.Background(), l, Result{Success: false, StatusCode: 429}); err != nil {
		t.Fatal(err)
	}
	got, err := cat.Get("acct-m")
	if err != nil {
		t.Fatal(err)
	}
	if got.CooldownUntil > time.Now().Unix() {
		t.Fatalf("account-level cooldown should not be set for model 429, got %d", got.CooldownUntil)
	}
	until := mgr.ModelCooldownUntil("acct-m", "grok-4.5")
	if until <= time.Now().Unix() {
		t.Fatalf("model cooldown missing: %d", until)
	}
	// acquire same model should fail soft
	_, err = mgr.AcquireAttempt(context.Background(), "", "grok-4.5", map[string]struct{}{})
	if err == nil {
		t.Fatal("expected model cooling error")
	}
	// different model still acquirable
	l2, err := mgr.AcquireAttempt(context.Background(), "", "other-model", map[string]struct{}{})
	if err != nil {
		t.Fatalf("other model should acquire: %v", err)
	}
	if l2.AccountID != "acct-m" {
		t.Fatalf("account=%s", l2.AccountID)
	}
}
