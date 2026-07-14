package clusterstate_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
)

func newTestRedis(t *testing.T) (*clusterstate.Redis, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	st, err := clusterstate.NewRedisFromClient(rdb)
	if err != nil {
		mr.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = st.Close()
		mr.Close()
	})
	return st, mr
}

func TestRedisStickyInflightCooldown(t *testing.T) {
	st, _ := newTestRedis(t)
	ctx := context.Background()

	if err := st.PutSticky(ctx, "s1", clusterstate.StickyBinding{AccountID: "a1", WorkerID: "worker-0", ShardID: 3}, time.Minute); err != nil {
		t.Fatal(err)
	}
	b, err := st.GetSticky(ctx, "s1")
	if err != nil || b.AccountID != "a1" || b.WorkerID != "worker-0" {
		t.Fatalf("sticky=%+v err=%v", b, err)
	}

	n, err := st.IncrInflight(ctx, "a1", time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("incr n=%d err=%v", n, err)
	}
	n, err = st.IncrInflight(ctx, "a1", time.Minute)
	if err != nil || n != 2 {
		t.Fatalf("incr2 n=%d err=%v", n, err)
	}
	n, err = st.DecrInflight(ctx, "a1")
	if err != nil || n != 1 {
		t.Fatalf("decr n=%d err=%v", n, err)
	}

	until := time.Now().Add(2 * time.Minute)
	if err := st.SetCooldown(ctx, "a1", until); err != nil {
		t.Fatal(err)
	}
	got, active, err := st.GetCooldown(ctx, "a1")
	if err != nil || !active || got.Before(time.Now()) {
		t.Fatalf("cooldown active=%v got=%v err=%v", active, got, err)
	}
}

func TestRedisShardLeaseAndWorkset(t *testing.T) {
	st, _ := newTestRedis(t)
	ctx := context.Background()

	l1, ok, err := st.TryAcquireShard(ctx, 7, "worker-0", 5*time.Second)
	if err != nil || !ok || l1.WorkerID != "worker-0" {
		t.Fatalf("acquire: ok=%v err=%v lease=%+v", ok, err, l1)
	}
	_, ok, err = st.TryAcquireShard(ctx, 7, "worker-1", 5*time.Second)
	if err != nil || ok {
		t.Fatalf("second acquire should fail: ok=%v err=%v", ok, err)
	}
	ok, err = st.RenewShard(ctx, 7, "worker-0", 5*time.Second)
	if err != nil || !ok {
		t.Fatalf("renew: ok=%v err=%v", ok, err)
	}
	owner, err := st.GetShardOwner(ctx, 7)
	if err != nil || owner.WorkerID != "worker-0" {
		t.Fatalf("owner=%+v err=%v", owner, err)
	}

	if err := st.ReplaceWorkset(ctx, 7, []string{"a9", "a1", "a3"}, []float64{1, 10, 5}); err != nil {
		t.Fatal(err)
	}
	ids, err := st.ListWorkset(ctx, 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	// highest score first
	if len(ids) != 3 || ids[0] != "a1" {
		t.Fatalf("workset order=%v", ids)
	}
}

func TestRedisRefreshClaim(t *testing.T) {
	st, _ := newTestRedis(t)
	ctx := context.Background()
	_ = st.AddRefreshDue(ctx, "due1", time.Now().Add(-time.Second))
	_ = st.AddRefreshDue(ctx, "later", time.Now().Add(time.Hour))
	ids, err := st.ClaimRefreshDue(ctx, "r0", time.Now(), 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "due1" {
		t.Fatalf("claimed=%v", ids)
	}
	// second claim should not re-get locked/removed id
	ids2, err := st.ClaimRefreshDue(ctx, "r1", time.Now(), 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids2) != 0 {
		t.Fatalf("expected empty second claim, got %v", ids2)
	}
}
