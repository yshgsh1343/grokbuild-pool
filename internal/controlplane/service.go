// Package controlplane builds worksets and reconciles shard topology.
package controlplane

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
	"github.com/yshgsh1343/grokbuild2api/internal/store"
)

// Config controls workset construction.
type Config struct {
	ShardCount       int
	WorksetSize      int
	DesiredHotPerShard int
	RefillInterval   time.Duration
}

func (c Config) normalize() Config {
	if c.ShardCount <= 0 {
		c.ShardCount = 64
	}
	if c.WorksetSize <= 0 {
		c.WorksetSize = 30000
	}
	if c.DesiredHotPerShard <= 0 {
		c.DesiredHotPerShard = max(1, c.WorksetSize/c.ShardCount)
	}
	if c.RefillInterval <= 0 {
		c.RefillInterval = 45 * time.Second
	}
	return c
}

// Service periodically materializes per-shard worksets into cluster state.
type Service struct {
	store store.AccountStore
	state clusterstate.State
	cfg   Config
	log   *slog.Logger
}

func New(store store.AccountStore, state clusterstate.State, cfg Config, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: store, state: state, cfg: cfg.normalize(), log: log}
}

// RefillOnce loads candidates and distributes them into shard worksets.
func (s *Service) RefillOnce(ctx context.Context) error {
	if s == nil || s.store == nil || s.state == nil {
		return fmt.Errorf("controlplane: nil deps")
	}
	cfg := s.cfg
	metas, err := s.store.ListWorksetCandidates(ctx, cfg.WorksetSize, time.Now())
	if err != nil {
		return err
	}

	// bucket by shard_hint if present in future; for now hash by id.
	buckets := make([][]string, cfg.ShardCount)
	scores := make([][]float64, cfg.ShardCount)
	for _, m := range metas {
		shard := shardOf(m.ID, cfg.ShardCount)
		// keep shard under desired size
		if len(buckets[shard]) >= cfg.DesiredHotPerShard {
			continue
		}
		buckets[shard] = append(buckets[shard], m.ID)
		scores[shard] = append(scores[shard], float64(m.Priority)-float64(m.FailureScore))
	}
	for shard := 0; shard < cfg.ShardCount; shard++ {
		if err := s.state.ReplaceWorkset(ctx, shard, buckets[shard], scores[shard]); err != nil {
			return fmt.Errorf("replace workset shard=%d: %w", shard, err)
		}
	}
	s.log.Info("workset_refilled", "candidates", len(metas), "shards", cfg.ShardCount, "desired_per_shard", cfg.DesiredHotPerShard)
	return nil
}

// Run refills until ctx cancelled.
func (s *Service) Run(ctx context.Context) error {
	if err := s.RefillOnce(ctx); err != nil {
		s.log.Error("workset_refill_failed", "err", err)
	}
	t := time.NewTicker(s.cfg.RefillInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := s.RefillOnce(ctx); err != nil {
				s.log.Error("workset_refill_failed", "err", err)
			}
		}
	}
}

func shardOf(id string, n int) int {
	if n <= 0 {
		return 0
	}
	var h uint32
	for i := 0; i < len(id); i++ {
		h = h*16777619 ^ uint32(id[i])
	}
	return int(h % uint32(n))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
