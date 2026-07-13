// Package worker owns local hot pools and executes upstream leases.
package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
	"github.com/yshgsh1343/grokbuild2api/internal/config"
	"github.com/yshgsh1343/grokbuild2api/internal/hot"
	"github.com/yshgsh1343/grokbuild2api/internal/lease"
	"github.com/yshgsh1343/grokbuild2api/internal/outbound"
	"github.com/yshgsh1343/grokbuild2api/internal/protocol/anthropic"
	"github.com/yshgsh1343/grokbuild2api/internal/protocol/executor"
	"github.com/yshgsh1343/grokbuild2api/internal/protocol/openai"
	"github.com/yshgsh1343/grokbuild2api/internal/protocol/upstream"
	"github.com/yshgsh1343/grokbuild2api/internal/selector"
	"github.com/yshgsh1343/grokbuild2api/internal/store"
)

// Config for a worker process.
type Config struct {
	WorkerID              string
	Listen                string
	ShardCount            int
	HotSize               int
	MaxInflightPerAccount int32
	MaxAttempts           int
	ShardLeaseTTL         time.Duration
	ShardRenewEvery       time.Duration
	ReconcileEvery        time.Duration
	Pow2K                 int
	StickyTTLSec          int
	UpstreamBaseURL       string
	ClientVersion         string
	ClientIdentifier      string
	UserAgent             string
	TokenAuth             string
	MaxBodyBytes          int64
	APIKey                string // optional static key for direct worker access
}

func (c Config) normalize() Config {
	if c.WorkerID == "" {
		c.WorkerID = "worker-0"
	}
	if c.Listen == "" {
		c.Listen = "0.0.0.0:8081"
	}
	if c.ShardCount <= 0 {
		c.ShardCount = 64
	}
	if c.HotSize <= 0 {
		c.HotSize = 5000
	}
	if c.MaxInflightPerAccount <= 0 {
		c.MaxInflightPerAccount = 2
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 4
	}
	if c.ShardLeaseTTL <= 0 {
		c.ShardLeaseTTL = 45 * time.Second
	}
	if c.ShardRenewEvery <= 0 {
		c.ShardRenewEvery = 15 * time.Second
	}
	if c.ReconcileEvery <= 0 {
		c.ReconcileEvery = 30 * time.Second
	}
	if c.Pow2K <= 0 {
		c.Pow2K = 2
	}
	if c.StickyTTLSec <= 0 {
		c.StickyTTLSec = 1800
	}
	if c.UpstreamBaseURL == "" {
		c.UpstreamBaseURL = config.DefaultUpstreamBaseURL
	}
	if c.ClientVersion == "" {
		c.ClientVersion = "0.2.93"
	}
	if c.ClientIdentifier == "" {
		c.ClientIdentifier = "grok-pager"
	}
	if c.UserAgent == "" {
		c.UserAgent = "grok-pager/0.2.93 grok-shell/0.2.93 (linux; x86_64)"
	}
	if c.TokenAuth == "" {
		c.TokenAuth = "xai-grok-cli"
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = 20 << 20
	}
	return c
}

// Service is the Scheme2 worker runtime.
type Service struct {
	cfg     Config
	store   store.AccountStore
	state   clusterstate.State
	idx     *hot.Index
	sel     *selector.Selector
	leaser  *storeLeaser
	exec    *executor.Executor
	openai  *openai.Handlers
	anth    *anthropic.Handlers
	log     *slog.Logger
	mu      sync.Mutex
	shards  map[int]clusterstate.ShardLease
}

func New(cfg Config, st store.AccountStore, cs clusterstate.State, log *slog.Logger) *Service {
	cfg = cfg.normalize()
	if log == nil {
		log = slog.Default()
	}
	idx := hot.New(hot.Config{HotSize: cfg.HotSize, MaxInflightPerAccount: cfg.MaxInflightPerAccount})
	sel := selector.New(idx, selector.Config{
		Strategy:     "pow2_least_load",
		HotSize:      cfg.HotSize,
		StickyTTLSec: int64(cfg.StickyTTLSec),
		StickyMax:    100000,
		Pow2K:        cfg.Pow2K,
	})
	leaser := newStoreLeaser(st, idx, sel, cs, cfg.MaxInflightPerAccount, cfg.MaxAttempts)

	uc := upstream.NewClient(upstream.Config{
		BaseURL:          cfg.UpstreamBaseURL,
		ClientVersion:    cfg.ClientVersion,
		ClientIdentifier: cfg.ClientIdentifier,
		TokenAuth:        cfg.TokenAuth,
		UserAgent:        cfg.UserAgent,
		RequestTimeout:   30 * time.Second,
	})
	outboundFactory := outbound.NewFactory(upstream.Config{
		BaseURL:          cfg.UpstreamBaseURL,
		ClientVersion:    cfg.ClientVersion,
		ClientIdentifier: cfg.ClientIdentifier,
		UserAgent:        cfg.UserAgent,
		TokenAuth:        cfg.TokenAuth,
	})
	exec := &executor.Executor{
		Leaser:   leaser,
		Upstream: uc,
		UpstreamFor: func(l lease.Lease) (executor.UpstreamPoster, error) {
			return outboundFactory.ClientFor(l.AccountID, l.ProxyURL)
		},
		MaxAttempts: cfg.MaxAttempts,
		Logger:      log,
		OnDialError: func(accountID, proxyURL string, err error) {
			if accountID != "" {
				outboundFactory.ForgetAccount(accountID)
			}
			if proxyURL != "" {
				outboundFactory.Forget(proxyURL)
			}
			log.Warn("outbound_forget_on_dial_error", "account_id", accountID, "error", err)
		},
	}
	oai := &openai.Handlers{Post: exec.Post, MaxBody: cfg.MaxBodyBytes}
	anth := &anthropic.Handlers{
		Post:    exec.Post,
		Cfg:     config.Default().Anthropic,
		MaxBody: cfg.MaxBodyBytes,
	}

	return &Service{
		cfg:    cfg,
		store:  st,
		state:  cs,
		idx:    idx,
		sel:    sel,
		leaser: leaser,
		exec:   exec,
		openai: oai,
		anth:   anth,
		log:    log,
		shards: make(map[int]clusterstate.ShardLease),
	}
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		n := len(s.shards)
		s.mu.Unlock()
		if n == 0 {
			http.Error(w, "no shards", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("/internal/v1/status", s.status)
	mux.HandleFunc("/internal/v1/pick", s.pickDebug)

	// Public-compatible protocol surface (gateway reverse-proxies here).
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIKey != "" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+s.cfg.APIKey && r.Header.Get("x-api-key") != s.cfg.APIKey {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		switch {
		case r.URL.Path == "/v1/responses" && r.Method == http.MethodPost:
			s.openai.HandleResponses(w, r)
		case r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost:
			s.openai.HandleChatCompletions(w, r)
		case r.URL.Path == "/v1/messages" && r.Method == http.MethodPost:
			s.anth.HandleMessages(w, r)
		case r.URL.Path == "/v1/messages/count_tokens" && r.Method == http.MethodPost:
			s.anth.HandleCountTokens(w, r)
		case r.URL.Path == "/v1/models" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"grok-4.5","object":"model"}]}`))
		default:
			http.NotFound(w, r)
		}
	})
	mux.Handle("/v1/", api)
	return mux
}

func (s *Service) status(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	ids := make([]int, 0, len(s.shards))
	for id := range s.shards {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	st := s.idx.Stats(0)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"worker_id":  s.cfg.WorkerID,
		"shards":     ids,
		"hot_cap":    s.cfg.HotSize,
		"hot_loaded": st.HotSize,
		"upstream":   s.cfg.UpstreamBaseURL,
	})
}

func (s *Service) pickDebug(w http.ResponseWriter, r *http.Request) {
	sticky := r.URL.Query().Get("sticky")
	id, ok := s.sel.Pick(time.Now().Unix(), sticky)
	if !ok {
		http.Error(w, "no account", http.StatusServiceUnavailable)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"account_id": id, "worker_id": s.cfg.WorkerID})
}

// Run claims shards and reconciles local hot set until cancelled.
func (s *Service) Run(ctx context.Context) error {
	if err := s.claimShards(ctx); err != nil {
		s.log.Error("claim_shards_failed", "err", err)
	}
	renew := time.NewTicker(s.cfg.ShardRenewEvery)
	reconcile := time.NewTicker(s.cfg.ReconcileEvery)
	defer renew.Stop()
	defer reconcile.Stop()
	for {
		select {
		case <-ctx.Done():
			s.releaseAll(ctx)
			return ctx.Err()
		case <-renew.C:
			s.renewShards(ctx)
		case <-reconcile.C:
			if err := s.reconcileHot(ctx); err != nil {
				s.log.Error("reconcile_hot_failed", "err", err)
			}
		}
	}
}

func (s *Service) claimShards(ctx context.Context) error {
	workersGuess := estimateWorkers(s.cfg.HotSize)
	targetShards := max(1, s.cfg.ShardCount/max(1, workersGuess))
	claimed := 0
	for shard := 0; shard < s.cfg.ShardCount && claimed < targetShards; shard++ {
		leaseObj, ok, err := s.state.TryAcquireShard(ctx, shard, s.cfg.WorkerID, s.cfg.ShardLeaseTTL)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		s.mu.Lock()
		s.shards[shard] = leaseObj
		s.mu.Unlock()
		claimed++
	}
	s.log.Info("shards_claimed", "worker", s.cfg.WorkerID, "claimed", claimed, "target", targetShards)
	return s.reconcileHot(ctx)
}

func (s *Service) renewShards(ctx context.Context) {
	s.mu.Lock()
	ids := make([]int, 0, len(s.shards))
	for id := range s.shards {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		ok, err := s.state.RenewShard(ctx, id, s.cfg.WorkerID, s.cfg.ShardLeaseTTL)
		if err != nil || !ok {
			s.mu.Lock()
			delete(s.shards, id)
			s.mu.Unlock()
			s.log.Warn("shard_lease_lost", "shard", id)
		}
	}
}

func (s *Service) releaseAll(ctx context.Context) {
	s.mu.Lock()
	ids := make([]int, 0, len(s.shards))
	for id := range s.shards {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_ = s.state.ReleaseShard(ctx, id, s.cfg.WorkerID)
	}
}

func (s *Service) reconcileHot(ctx context.Context) error {
	s.mu.Lock()
	shardIDs := make([]int, 0, len(s.shards))
	for id := range s.shards {
		shardIDs = append(shardIDs, id)
	}
	s.mu.Unlock()

	metas := make([]catalog.HotMeta, 0, s.cfg.HotSize)
	for _, shard := range shardIDs {
		ids, err := s.state.ListWorkset(ctx, shard, s.cfg.HotSize)
		if err != nil {
			return err
		}
		// If workset empty, fall back to store shard listing.
		if len(ids) == 0 {
			ms, err := s.store.ListByShard(ctx, shard, s.cfg.HotSize, "")
			if err == nil {
				for _, m := range ms {
					metas = append(metas, m)
					if len(metas) >= s.cfg.HotSize {
						break
					}
				}
			}
			continue
		}
		for _, id := range ids {
			acc, err := s.store.Get(ctx, id)
			if err != nil {
				continue
			}
			metas = append(metas, catalog.HotMeta{
				ID:            acc.ID,
				Priority:      int32(acc.Priority),
				CooldownUntil: acc.CooldownUntil,
				ExpiresAt:     acc.ExpiresAt,
				FailureScore:  float32(acc.FailureCount),
				Enabled:       acc.Enabled && !acc.ManualDisabled,
				Lifecycle:     acc.Lifecycle,
				Revision:      acc.Revision,
				IdentityKey:   acc.IdentityKey,
				ProxyMode:     acc.ProxyMode,
				ProxyURL:      acc.ProxyURL,
			})
			if len(metas) >= s.cfg.HotSize {
				break
			}
		}
		if len(metas) >= s.cfg.HotSize {
			break
		}
	}
	// If still empty (single-process bootstrap), load global eligible.
	if len(metas) == 0 {
		if ms, err := s.store.ListEligible(ctx, s.cfg.HotSize, ""); err == nil {
			metas = ms
		}
	}
	s.idx.Resize(s.cfg.HotSize)
	loaded, err := s.idx.LoadMetas(metas)
	if err != nil {
		return err
	}
	s.log.Info("hot_reconciled", "worker", s.cfg.WorkerID, "metas", len(metas), "loaded", loaded, "shards", len(shardIDs))
	return nil
}

func estimateWorkers(hotSize int) int {
	if hotSize <= 0 {
		return 6
	}
	return max(1, 30000/hotSize)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// AcquireWithCluster is retained for tests/debug.
func (s *Service) AcquireWithCluster(ctx context.Context, stickyKey string, maxAttempts int) (string, error) {
	l, err := s.leaser.Acquire(ctx, stickyKey)
	if err != nil {
		return "", err
	}
	_ = maxAttempts
	return l.AccountID, nil
}

