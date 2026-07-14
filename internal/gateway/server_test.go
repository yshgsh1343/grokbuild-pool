package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
)

func TestPickWorkerStickyAndShardOwner(t *testing.T) {
	st := clusterstate.NewMemory()
	ctx := context.Background()
	s := New(Config{
		WorkerBaseURLs: []string{
			"http://127.0.0.1:8081",
			"http://127.0.0.1:8082",
			"http://127.0.0.1:8083",
		},
		ShardCount: 64,
	}, st)

	// sticky wins first
	_ = st.PutSticky(ctx, "sess-1", clusterstate.StickyBinding{
		AccountID: "a1",
		WorkerID:  "worker-2",
		ShardID:   1,
	}, time.Minute)
	base, wid, err := s.pickWorker(ctx, "sess-1", 0)
	if err != nil || wid != "worker-2" || base != "http://127.0.0.1:8083" {
		t.Fatalf("sticky route base=%s wid=%s err=%v", base, wid, err)
	}

	// without sticky: shard owner
	_ = st.ClearSticky(ctx, "sess-1")
	seedShard := hashString("sess-2") % 64
	_, ok, err := st.TryAcquireShard(ctx, seedShard, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("lease shard: ok=%v err=%v", ok, err)
	}
	base, wid, err = s.pickWorker(ctx, "sess-2", 0)
	if err != nil || wid != "worker-1" || base != "http://127.0.0.1:8082" {
		t.Fatalf("shard owner route base=%s wid=%s err=%v", base, wid, err)
	}
}

func TestPickWorkerFallbackWithoutState(t *testing.T) {
	s := New(Config{
		WorkerBaseURLs: []string{"http://w0", "http://w1"},
		ShardCount:     64,
	}, nil)
	base, wid, err := s.pickWorker(context.Background(), "x", 0)
	if err != nil || base == "" || wid == "" {
		t.Fatalf("fallback base=%s wid=%s err=%v", base, wid, err)
	}
}
