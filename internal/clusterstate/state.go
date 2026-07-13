// Package clusterstate holds cross-instance hot coordination state.
// Production backend is Redis; Memory is for tests and single-process bootstrap.
package clusterstate

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("clusterstate: not found")
	ErrConflict = errors.New("clusterstate: conflict")
	ErrClosed   = errors.New("clusterstate: closed")
)

// StickyBinding routes a conversation/session to an account+worker.
type StickyBinding struct {
	AccountID string `json:"account_id"`
	WorkerID  string `json:"worker_id"`
	ShardID   int    `json:"shard_id"`
	Exp       int64  `json:"exp"`
}

// ShardLease is the runtime owner of a shard.
type ShardLease struct {
	ShardID   int    `json:"shard_id"`
	WorkerID  string `json:"worker_id"`
	Version   int64  `json:"version"`
	Exp       int64  `json:"exp"`
}

// State is the Redis-shaped coordination API used by gateway/worker/refresher.
type State interface {
	Close() error

	// Sticky
	GetSticky(ctx context.Context, stickyKey string) (StickyBinding, error)
	PutSticky(ctx context.Context, stickyKey string, b StickyBinding, ttl time.Duration) error
	ClearSticky(ctx context.Context, stickyKey string) error
	ClearStickyAccount(ctx context.Context, accountID string) error

	// Inflight hard gate
	IncrInflight(ctx context.Context, accountID string, ttl time.Duration) (int64, error)
	DecrInflight(ctx context.Context, accountID string) (int64, error)
	GetInflight(ctx context.Context, accountID string) (int64, error)

	// Cooldown
	SetCooldown(ctx context.Context, accountID string, until time.Time) error
	GetCooldown(ctx context.Context, accountID string) (time.Time, bool, error)
	ClearCooldown(ctx context.Context, accountID string) error

	// Shard leases
	TryAcquireShard(ctx context.Context, shardID int, workerID string, ttl time.Duration) (ShardLease, bool, error)
	RenewShard(ctx context.Context, shardID int, workerID string, ttl time.Duration) (bool, error)
	ReleaseShard(ctx context.Context, shardID int, workerID string) error
	GetShardOwner(ctx context.Context, shardID int) (ShardLease, error)

	// Workset members (account IDs only)
	ReplaceWorkset(ctx context.Context, shardID int, accountIDs []string, scores []float64) error
	ListWorkset(ctx context.Context, shardID int, limit int) ([]string, error)

	// Refresh due queue
	AddRefreshDue(ctx context.Context, accountID string, due time.Time) error
	ClaimRefreshDue(ctx context.Context, refresherID string, now time.Time, limit int, lockTTL time.Duration) ([]string, error)
}
