package controlplane_test

import (
	"context"
	"testing"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
	"github.com/yshgsh1343/grokbuild2api/internal/controlplane"
)

type fakeStore struct {
	metas []catalog.HotMeta
}

func (f *fakeStore) Close() error { return nil }
func (f *fakeStore) Get(ctx context.Context, id string) (catalog.Account, error) {
	return catalog.Account{}, catalog.ErrNotFound
}
func (f *fakeStore) UpsertMany(ctx context.Context, accounts []catalog.Account) error {
	return nil
}
func (f *fakeStore) UpsertImportedMany(ctx context.Context, accounts []catalog.Account) error {
	return nil
}
func (f *fakeStore) UpdateTokens(ctx context.Context, id string, expectedRev int64, tokens catalog.TokenSet) error {
	return nil
}
func (f *fakeStore) PatchHealth(ctx context.Context, id string, patch catalog.HealthPatch) error {
	return nil
}
func (f *fakeStore) ListEligible(ctx context.Context, limit int, afterID string) ([]catalog.HotMeta, error) {
	return f.metas, nil
}
func (f *fakeStore) ListExpiring(ctx context.Context, limit int, beforeUnix int64) ([]catalog.Account, error) {
	return nil, nil
}
func (f *fakeStore) ListAccounts(ctx context.Context, limit int, afterID string, filter catalog.AccountListFilter) ([]catalog.AccountSummary, error) {
	return nil, nil
}
func (f *fakeStore) CountAccounts(ctx context.Context) (int, error) { return len(f.metas), nil }
func (f *fakeStore) Stats(ctx context.Context) (catalog.CatalogStats, error) {
	return catalog.CatalogStats{Count: int64(len(f.metas))}, nil
}
func (f *fakeStore) ListWorksetCandidates(ctx context.Context, limit int, now time.Time) ([]catalog.HotMeta, error) {
	if limit > len(f.metas) {
		limit = len(f.metas)
	}
	return f.metas[:limit], nil
}
func (f *fakeStore) ListByShard(ctx context.Context, shardID, limit int, afterID string) ([]catalog.HotMeta, error) {
	return f.ListEligible(ctx, limit, afterID)
}

func TestRefillOnceDistributesShards(t *testing.T) {
	metas := make([]catalog.HotMeta, 0, 100)
	for i := 0; i < 100; i++ {
		metas = append(metas, catalog.HotMeta{ID: "acc-" + itoa(i), Priority: 100, Enabled: true, Lifecycle: "active"})
	}
	st := &fakeStore{metas: metas}
	cs := clusterstate.NewMemory()
	svc := controlplane.New(st, cs, controlplane.Config{ShardCount: 8, WorksetSize: 100, DesiredHotPerShard: 20}, nil)
	if err := svc.RefillOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	total := 0
	for shard := 0; shard < 8; shard++ {
		ids, err := cs.ListWorkset(context.Background(), shard, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) > 20 {
			t.Fatalf("shard %d over capacity: %d", shard, len(ids))
		}
		total += len(ids)
	}
	if total == 0 {
		t.Fatal("expected non-empty workset")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [16]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
