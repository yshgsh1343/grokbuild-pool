// Package refresher claims due accounts and refreshes tokens out of band.
package refresher

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
	"github.com/yshgsh1343/grokbuild2api/internal/store"
)

// TokenRefresher performs provider-specific OAuth refresh.
type TokenRefresher interface {
	Refresh(ctx context.Context, acc catalog.Account) (catalog.TokenSet, error)
}

// Config for refresh fleet workers.
type Config struct {
	RefresherID string
	PollEvery   time.Duration
	BatchSize   int
	LockTTL     time.Duration
	GlobalQPS   float64
}

func (c Config) normalize() Config {
	if c.RefresherID == "" {
		c.RefresherID = "refresher-0"
	}
	if c.PollEvery <= 0 {
		c.PollEvery = 2 * time.Second
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 20
	}
	if c.LockTTL <= 0 {
		c.LockTTL = 60 * time.Second
	}
	if c.GlobalQPS <= 0 {
		c.GlobalQPS = 20
	}
	return c
}

// Service drains refresh due queue.
type Service struct {
	cfg   Config
	store store.AccountStore
	state clusterstate.State
	oauth TokenRefresher
	log   *slog.Logger
}

func New(cfg Config, st store.AccountStore, cs clusterstate.State, oauth TokenRefresher, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{cfg: cfg.normalize(), store: st, state: cs, oauth: oauth, log: log}
}

func (s *Service) Run(ctx context.Context) error {
	t := time.NewTicker(s.cfg.PollEvery)
	defer t.Stop()
	// simple spacing for global qps
	minGap := time.Duration(float64(time.Second) / s.cfg.GlobalQPS)
	if minGap <= 0 {
		minGap = 50 * time.Millisecond
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			ids, err := s.state.ClaimRefreshDue(ctx, s.cfg.RefresherID, time.Now(), s.cfg.BatchSize, s.cfg.LockTTL)
			if err != nil {
				s.log.Error("claim_refresh_failed", "err", err)
				continue
			}
			for _, id := range ids {
				if err := s.refreshOne(ctx, id); err != nil {
					s.log.Warn("refresh_one_failed", "account", id, "err", err)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(minGap):
				}
			}
		}
	}
}

func (s *Service) refreshOne(ctx context.Context, id string) error {
	if s.oauth == nil {
		return fmt.Errorf("refresher: nil oauth")
	}
	acc, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	tokens, err := s.oauth.Refresh(ctx, acc)
	if err != nil {
		// cool down and requeue later
		_ = s.state.SetCooldown(ctx, id, time.Now().Add(2*time.Minute))
		_ = s.state.AddRefreshDue(ctx, id, time.Now().Add(5*time.Minute))
		return err
	}
	if err := s.store.UpdateTokens(ctx, id, acc.Revision, tokens); err != nil {
		return err
	}
	s.log.Info("refresh_ok", "account", id, "revision", acc.Revision+1)
	return nil
}

// EnqueueExpiring scans store and pushes due accounts into redis/memory queue.
func (s *Service) EnqueueExpiring(ctx context.Context, within time.Duration, limit int) (int, error) {
	if within <= 0 {
		within = 15 * time.Minute
	}
	if limit <= 0 {
		limit = 1000
	}
	before := time.Now().Add(within).Unix()
	accs, err := s.store.ListExpiring(ctx, limit, before)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range accs {
		due := time.Now()
		if a.ExpiresAt > 0 {
			due = time.Unix(a.ExpiresAt, 0).Add(-10 * time.Minute)
		}
		if err := s.state.AddRefreshDue(ctx, a.ID, due); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
