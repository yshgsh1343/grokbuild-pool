package store

import (
	"context"
	"fmt"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
)

// SQLiteAccountStore adapts existing catalog.Catalog to AccountStore.
// This keeps single-binary mode working while Scheme 2 multi-process lands.
type SQLiteAccountStore struct {
	Cat *catalog.Catalog
}

func NewSQLiteAccountStore(cat *catalog.Catalog) *SQLiteAccountStore {
	if cat == nil {
		panic("store: nil catalog")
	}
	return &SQLiteAccountStore{Cat: cat}
}

func (s *SQLiteAccountStore) Close() error { return s.Cat.Close() }

func (s *SQLiteAccountStore) Get(ctx context.Context, id string) (catalog.Account, error) {
	_ = ctx
	acc, err := s.Cat.Get(id)
	return acc, MapCatalogErr(err)
}

func (s *SQLiteAccountStore) UpsertMany(ctx context.Context, accounts []catalog.Account) error {
	_ = ctx
	return MapCatalogErr(s.Cat.UpsertMany(accounts))
}

func (s *SQLiteAccountStore) UpsertImportedMany(ctx context.Context, accounts []catalog.Account) error {
	_ = ctx
	return MapCatalogErr(s.Cat.UpsertImportedMany(accounts))
}

func (s *SQLiteAccountStore) UpdateTokens(ctx context.Context, id string, expectedRev int64, tokens catalog.TokenSet) error {
	_ = ctx
	return MapCatalogErr(s.Cat.UpdateTokens(id, expectedRev, tokens))
}

func (s *SQLiteAccountStore) PatchHealth(ctx context.Context, id string, patch catalog.HealthPatch) error {
	_ = ctx
	return MapCatalogErr(s.Cat.PatchHealth(id, patch))
}

func (s *SQLiteAccountStore) ListEligible(ctx context.Context, limit int, afterID string) ([]catalog.HotMeta, error) {
	_ = ctx
	out, err := s.Cat.ListEligible(limit, afterID)
	return out, MapCatalogErr(err)
}

func (s *SQLiteAccountStore) ListExpiring(ctx context.Context, limit int, beforeUnix int64) ([]catalog.Account, error) {
	_ = ctx
	out, err := s.Cat.ListExpiring(limit, beforeUnix)
	return out, MapCatalogErr(err)
}

func (s *SQLiteAccountStore) ListAccounts(ctx context.Context, limit int, afterID string, filter catalog.AccountListFilter) ([]catalog.AccountSummary, error) {
	_ = ctx
	out, err := s.Cat.ListAccounts(limit, afterID, filter)
	return out, MapCatalogErr(err)
}

func (s *SQLiteAccountStore) CountAccounts(ctx context.Context) (int, error) {
	_ = ctx
	n, err := s.Cat.CountAccounts()
	return n, MapCatalogErr(err)
}

func (s *SQLiteAccountStore) Stats(ctx context.Context) (catalog.CatalogStats, error) {
	_ = ctx
	st, err := s.Cat.Stats()
	return st, MapCatalogErr(err)
}

// ListWorksetCandidates approximates Scheme2 workset from ListEligible.
func (s *SQLiteAccountStore) ListWorksetCandidates(ctx context.Context, limit int, now time.Time) ([]catalog.HotMeta, error) {
	_ = now
	return s.ListEligible(ctx, limit, "")
}

// ListByShard is not first-class in SQLite; returns eligible page as fallback.
func (s *SQLiteAccountStore) ListByShard(ctx context.Context, shardID, limit int, afterID string) ([]catalog.HotMeta, error) {
	if shardID < 0 {
		return nil, fmt.Errorf("%w: shard_id", ErrInvalid)
	}
	return s.ListEligible(ctx, limit, afterID)
}
