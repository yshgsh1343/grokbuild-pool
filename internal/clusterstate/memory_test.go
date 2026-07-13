package clusterstate_test

import (
	"context"
	"testing"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
)

func TestMemoryInflightAndCooldown(t *testing.T) {
	m := clusterstate.NewMemory()
	ctx := context.Background()

	n, err := m.IncrInflight(ctx, "a1", time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("incr1: n=%d err=%v", n, err)
	}
	n, err = m.IncrInflight(ctx, "a1", time.Minute)
	if err != nil || n != 2 {
		t.Fatalf("incr2: n=%d err=%v", n, err)
	}
	n, err = m.DecrInflight(ctx, "a1")
	if err != nil || n != 1 {
		t.Fatalf("decr: n=%d err=%v", n, err)
	}

	until := time.Now().Add(2 * time.Minute)
	if err := m.SetCooldown(ctx, "a1", until); err != nil {
		t.Fatal(err)
	}
	got, active, err := m.GetCooldown(ctx, "a1")
	if err != nil || !active || got.Before(time.Now()) {
		t.Fatalf("cooldown active=%v got=%v err=%v", active, got, err)
	}
}

func TestMemoryShardLease(t *testing.T) {
	m := clusterstate.NewMemory()
	ctx := context.Background()
	l1, ok, err := m.TryAcquireShard(ctx, 1, "w0", 2*time.Second)
	if err != nil || !ok || l1.WorkerID != "w0" {
		t.Fatalf("acquire w0: ok=%v err=%v lease=%+v", ok, err, l1)
	}
	_, ok, err = m.TryAcquireShard(ctx, 1, "w1", 2*time.Second)
	if err != nil || ok {
		t.Fatalf("acquire w1 should fail: ok=%v err=%v", ok, err)
	}
	ok, err = m.RenewShard(ctx, 1, "w0", 2*time.Second)
	if err != nil || !ok {
		t.Fatalf("renew: ok=%v err=%v", ok, err)
	}
	if err := m.ReleaseShard(ctx, 1, "w0"); err != nil {
		t.Fatal(err)
	}
	_, ok, err = m.TryAcquireShard(ctx, 1, "w1", 2*time.Second)
	if err != nil || !ok {
		t.Fatalf("acquire after release: ok=%v err=%v", ok, err)
	}
}

func TestMemoryStickyAndWorkset(t *testing.T) {
	m := clusterstate.NewMemory()
	ctx := context.Background()
	if err := m.PutSticky(ctx, "s1", clusterstate.StickyBinding{AccountID: "a1", WorkerID: "w0", ShardID: 3}, time.Minute); err != nil {
		t.Fatal(err)
	}
	b, err := m.GetSticky(ctx, "s1")
	if err != nil || b.AccountID != "a1" {
		t.Fatalf("sticky: %+v err=%v", b, err)
	}
	if err := m.ReplaceWorkset(ctx, 3, []string{"a1", "a2"}, []float64{10, 9}); err != nil {
		t.Fatal(err)
	}
	ids, err := m.ListWorkset(ctx, 3, 10)
	if err != nil || len(ids) != 2 || ids[0] != "a1" {
		t.Fatalf("workset=%v err=%v", ids, err)
	}
}

func TestMemoryRefreshClaim(t *testing.T) {
	m := clusterstate.NewMemory()
	ctx := context.Background()
	_ = m.AddRefreshDue(ctx, "a1", time.Now().Add(-time.Second))
	_ = m.AddRefreshDue(ctx, "a2", time.Now().Add(time.Hour))
	ids, err := m.ClaimRefreshDue(ctx, "r0", time.Now(), 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "a1" {
		t.Fatalf("claimed=%v", ids)
	}
}
