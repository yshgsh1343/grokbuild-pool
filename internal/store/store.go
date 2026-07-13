// Package store defines cold-account persistence for Scheme 2.
//
// SQLite catalog remains the default implementation for single-binary mode.
// Postgres is the target implementation for ~140k multi-worker deployments.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
)

var (
	ErrNotFound    = errors.New("store: account not found")
	ErrCASConflict = errors.New("store: revision conflict")
	ErrInvalid     = errors.New("store: invalid input")
	ErrClosed      = errors.New("store: closed")
)

// AccountStore is the cold-data source of truth (tokens live here only).
// Methods mirror catalog.Catalog so existing call sites can migrate gradually.
type AccountStore interface {
	Close() error

	Get(ctx context.Context, id string) (catalog.Account, error)
	UpsertMany(ctx context.Context, accounts []catalog.Account) error
	UpsertImportedMany(ctx context.Context, accounts []catalog.Account) error
	UpdateTokens(ctx context.Context, id string, expectedRev int64, tokens catalog.TokenSet) error
	PatchHealth(ctx context.Context, id string, patch catalog.HealthPatch) error

	ListEligible(ctx context.Context, limit int, afterID string) ([]catalog.HotMeta, error)
	ListExpiring(ctx context.Context, limit int, beforeUnix int64) ([]catalog.Account, error)
	ListAccounts(ctx context.Context, limit int, afterID string, filter catalog.AccountListFilter) ([]catalog.AccountSummary, error)
	CountAccounts(ctx context.Context) (int, error)
	Stats(ctx context.Context) (catalog.CatalogStats, error)

	// Scheme2 extensions (Postgres implementations must support; SQLite may no-op/approximate).
	ListWorksetCandidates(ctx context.Context, limit int, now time.Time) ([]catalog.HotMeta, error)
	ListByShard(ctx context.Context, shardID, limit int, afterID string) ([]catalog.HotMeta, error)
}

// ShardStore tracks desired shard topology and optional durable owner hints.
// Runtime ownership still uses Redis leases; this table is control-plane state.
type ShardStore interface {
	EnsureShards(ctx context.Context, shardCount, desiredHotPerShard int) error
	ListShards(ctx context.Context) ([]Shard, error)
	UpsertShard(ctx context.Context, s Shard) error
}

// Shard is durable control-plane shard metadata.
type Shard struct {
	ShardID        int
	OwnerWorkerID  string
	LeaseExpireAt  *time.Time
	DesiredHotSize int
	Status         string
	UpdatedAt      time.Time
}

// ImportStore persists multi-chunk import jobs for 100k+ account loads.
type ImportStore interface {
	CreateJob(ctx context.Context, job ImportJob) error
	UpdateJob(ctx context.Context, job ImportJob) error
	GetJob(ctx context.Context, jobID string) (ImportJob, error)
	CreateChunks(ctx context.Context, chunks []ImportChunk) error
	UpdateChunk(ctx context.Context, chunk ImportChunk) error
	ListChunks(ctx context.Context, jobID string) ([]ImportChunk, error)
}

type ImportJob struct {
	JobID      string
	Status     string
	SourceName string
	Total      int
	Done       int
	Failed     int
	Error      string
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

type ImportChunk struct {
	ChunkID    int64
	JobID      string
	ChunkNo    int
	Status     string
	RowsTotal  int
	RowsOK     int
	RowsFailed int
	Error      string
	CreatedAt  time.Time
	FinishedAt *time.Time
}

// Compile-time convenience aliases so adapters can map catalog errors.
func MapCatalogErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, catalog.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, catalog.ErrCASConflict):
		return ErrCASConflict
	case errors.Is(err, catalog.ErrInvalidInput):
		return ErrInvalid
	case errors.Is(err, catalog.ErrClosed):
		return ErrClosed
	default:
		return err
	}
}
