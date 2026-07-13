// Package gateway routes client traffic to workers with sticky affinity.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/clusterstate"
)

// Config is gateway runtime config.
type Config struct {
	Listen            string
	WorkerBaseURLs    []string // index = worker slot; production should resolve via shard owner map
	MaxConcurrent     int64
	MaxWorkerRetry    int
	RequestTimeout    time.Duration
	StickyTTL         time.Duration
	AdminKey          string
	APIKey            string
	ShardCount        int
}

func (c Config) normalize() Config {
	if c.Listen == "" {
		c.Listen = "0.0.0.0:8080"
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 2000
	}
	if c.MaxWorkerRetry <= 0 {
		c.MaxWorkerRetry = 2
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = 600 * time.Second
	}
	if c.StickyTTL <= 0 {
		c.StickyTTL = 1800 * time.Second
	}
	if c.ShardCount <= 0 {
		c.ShardCount = 64
	}
	return c
}

// Server is a thin reverse proxy entry.
type Server struct {
	cfg   Config
	state clusterstate.State
	inflight atomic.Int64
	client *http.Client
}

func New(cfg Config, state clusterstate.State) *Server {
	cfg = cfg.normalize()
	return &Server{
		cfg:   cfg,
		state: state,
		client: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if len(s.cfg.WorkerBaseURLs) == 0 {
			http.Error(w, "no workers", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("/v1/", s.proxyAPI)
	mux.HandleFunc("/admin/scheme2/status", s.adminStatus)
	return mux
}

func (s *Server) proxyAPI(w http.ResponseWriter, r *http.Request) {
	if s.cfg.APIKey != "" {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+s.cfg.APIKey && r.Header.Get("x-api-key") != s.cfg.APIKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if n := s.inflight.Add(1); n > s.cfg.MaxConcurrent {
		s.inflight.Add(-1)
		w.Header().Set("Retry-After", "1")
		http.Error(w, "gateway overloaded", http.StatusServiceUnavailable)
		return
	}
	defer s.inflight.Add(-1)

	stickyKey := r.Header.Get("X-Session-Id")
	if stickyKey == "" {
		stickyKey = r.Header.Get("X-Sticky-Key")
	}

	var lastErr error
	for attempt := 0; attempt <= s.cfg.MaxWorkerRetry; attempt++ {
		base, workerID, err := s.pickWorker(r.Context(), stickyKey, attempt)
		if err != nil {
			lastErr = err
			continue
		}
		target, err := url.Parse(base)
		if err != nil {
			lastErr = err
			continue
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		orig := proxy.Director
		proxy.Director = func(req *http.Request) {
			orig(req)
			req.Header.Set("X-Scheme2-Worker", workerID)
			if stickyKey != "" {
				req.Header.Set("X-Sticky-Key", stickyKey)
			}
		}
		// Best-effort proxy; detailed failover is worker-local.
		proxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, e error) {
			lastErr = e
			http.Error(rw, "worker unavailable", http.StatusBadGateway)
		}
		proxy.ServeHTTP(w, r)
		return
	}
	http.Error(w, fmt.Sprintf("no worker: %v", lastErr), http.StatusServiceUnavailable)
}

func (s *Server) pickWorker(ctx context.Context, stickyKey string, attempt int) (baseURL, workerID string, err error) {
	if len(s.cfg.WorkerBaseURLs) == 0 {
		return "", "", errors.New("no worker urls configured")
	}
	// sticky worker preference
	if stickyKey != "" && s.state != nil && attempt == 0 {
		if b, e := s.state.GetSticky(ctx, stickyKey); e == nil && b.WorkerID != "" {
			if u, ok := s.workerURLByID(b.WorkerID); ok {
				return u, b.WorkerID, nil
			}
		}
	}
	// hash sticky/attempt into worker slot
	seed := stickyKey
	if seed == "" {
		seed = fmt.Sprintf("attempt-%d", attempt)
	}
	idx := hashString(seed+fmt.Sprint(attempt)) % len(s.cfg.WorkerBaseURLs)
	return s.cfg.WorkerBaseURLs[idx], fmt.Sprintf("worker-%d", idx), nil
}

func (s *Server) workerURLByID(workerID string) (string, bool) {
	// convention worker-%d
	var idx int
	if _, err := fmt.Sscanf(workerID, "worker-%d", &idx); err != nil {
		return "", false
	}
	if idx < 0 || idx >= len(s.cfg.WorkerBaseURLs) {
		return "", false
	}
	return s.cfg.WorkerBaseURLs[idx], true
}

func (s *Server) adminStatus(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdminKey != "" && r.Header.Get("X-Admin-Key") != s.cfg.AdminKey {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"component":      "gateway",
		"workers":        len(s.cfg.WorkerBaseURLs),
		"inflight":       s.inflight.Load(),
		"max_concurrent": s.cfg.MaxConcurrent,
		"shard_count":    s.cfg.ShardCount,
	})
}

func hashString(s string) int {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return int(h & 0x7fffffff)
}
