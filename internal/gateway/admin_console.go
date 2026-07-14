package gateway

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/adminui"
)

// RuntimeSettings is the subset of admin settings editable on the B gateway.
// Full A-style account/import management still lives on pool-proxy; here we expose
// the same UI shell + gateway-owned knobs (esp. max_concurrent).
type RuntimeSettings struct {
	MaxConcurrent     int64  `json:"max_concurrent"`
	Listen            string `json:"listen"`
	UpstreamNote      string `json:"upstream_note,omitempty"`
	LoggingLevel      string `json:"logging_level"`
	SelectorStrategy  string `json:"selector_strategy"`
	AvailabilityMode  string `json:"availability_mode"`
	HotSize           int    `json:"hot_size"`
	Workers           string `json:"workers"`
	ShardCount        int    `json:"shard_count"`
	StickyTTLSec      int64  `json:"sticky_ttl_sec"`
	RequestTimeoutSec int    `json:"request_timeout_sec"`
	APIKeyConfigured  bool   `json:"api_key_configured"`
	AdminKeyConfigured bool  `json:"admin_key_configured"`
	// Optional secret writes (GET never returns plaintext)
	APIKey   string `json:"api_key,omitempty"`
	AdminKey string `json:"admin_key,omitempty"`
	RestartHint string `json:"restart_hint,omitempty"`
}

// SettingsSnapshot is GET /admin/settings response.
type SettingsSnapshot struct {
	RuntimeSettings
	PersistedPath string `json:"persisted_path,omitempty"`
	Component     string `json:"component"`
	Mode          string `json:"mode"`
}

type settingsStore struct {
	mu   sync.RWMutex
	path string
	s    RuntimeSettings
	// secrets held only in memory
	apiKey   string
	adminKey string
}

func newSettingsStore(path string, seed RuntimeSettings, apiKey, adminKey string) *settingsStore {
	st := &settingsStore{path: path, s: seed, apiKey: strings.TrimSpace(apiKey), adminKey: strings.TrimSpace(adminKey)}
	_ = st.load()
	// re-seed secrets if file had none
	if st.apiKey == "" {
		st.apiKey = strings.TrimSpace(apiKey)
	}
	if st.adminKey == "" {
		st.adminKey = strings.TrimSpace(adminKey)
	}
	st.s.APIKeyConfigured = st.apiKey != ""
	st.s.AdminKeyConfigured = st.adminKey != ""
	return st
}

func (st *settingsStore) load() error {
	if st.path == "" {
		return nil
	}
	data, err := os.ReadFile(st.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var in RuntimeSettings
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	// merge non-zero
	if in.MaxConcurrent > 0 {
		st.s.MaxConcurrent = in.MaxConcurrent
	}
	if in.Listen != "" {
		st.s.Listen = in.Listen
	}
	if in.LoggingLevel != "" {
		st.s.LoggingLevel = in.LoggingLevel
	}
	if in.SelectorStrategy != "" {
		st.s.SelectorStrategy = in.SelectorStrategy
	}
	if in.AvailabilityMode != "" {
		st.s.AvailabilityMode = in.AvailabilityMode
	}
	if in.HotSize > 0 {
		st.s.HotSize = in.HotSize
	}
	if in.Workers != "" {
		st.s.Workers = in.Workers
	}
	if in.ShardCount > 0 {
		st.s.ShardCount = in.ShardCount
	}
	if in.StickyTTLSec > 0 {
		st.s.StickyTTLSec = in.StickyTTLSec
	}
	if in.RequestTimeoutSec > 0 {
		st.s.RequestTimeoutSec = in.RequestTimeoutSec
	}
	return nil
}

func (st *settingsStore) snapshot() SettingsSnapshot {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := st.s
	out.APIKey = ""
	out.AdminKey = ""
	out.APIKeyConfigured = st.apiKey != ""
	out.AdminKeyConfigured = st.adminKey != ""
	return SettingsSnapshot{
		RuntimeSettings: out,
		PersistedPath:   st.path,
		Component:       "gateway",
		Mode:            "scheme-b",
	}
}

func (st *settingsStore) apply(in RuntimeSettings) (RuntimeSettings, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	prev := st.s
	if in.MaxConcurrent < 0 {
		in.MaxConcurrent = 0
	}
	if in.MaxConcurrent > 100000 {
		in.MaxConcurrent = 100000
	}
	if in.MaxConcurrent == 0 {
		in.MaxConcurrent = prev.MaxConcurrent
	}
	if in.Listen == "" {
		in.Listen = prev.Listen
	}
	if in.LoggingLevel == "" {
		in.LoggingLevel = prev.LoggingLevel
	}
	if in.SelectorStrategy == "" {
		in.SelectorStrategy = prev.SelectorStrategy
	}
	if in.AvailabilityMode == "" {
		in.AvailabilityMode = prev.AvailabilityMode
	}
	if in.HotSize <= 0 {
		in.HotSize = prev.HotSize
	}
	if in.Workers == "" {
		in.Workers = prev.Workers
	}
	if in.ShardCount <= 0 {
		in.ShardCount = prev.ShardCount
	}
	if in.StickyTTLSec <= 0 {
		in.StickyTTLSec = prev.StickyTTLSec
	}
	if in.RequestTimeoutSec <= 0 {
		in.RequestTimeoutSec = prev.RequestTimeoutSec
	}
	// secrets
	if k := strings.TrimSpace(in.APIKey); k != "" {
		st.apiKey = k
	}
	if k := strings.TrimSpace(in.AdminKey); k != "" {
		st.adminKey = k
	}
	in.APIKey = ""
	in.AdminKey = ""
	in.APIKeyConfigured = st.apiKey != ""
	in.AdminKeyConfigured = st.adminKey != ""

	// only listen truly needs restart on gateway
	if prev.Listen != "" && in.Listen != "" && in.Listen != prev.Listen {
		in.RestartHint = "listen 已保存，需手动重启 gateway 进程后才换端口"
	} else {
		in.RestartHint = ""
	}
	// workers list change needs restart (proxy targets fixed at start unless we add hot swap later)
	if prev.Workers != "" && in.Workers != "" && in.Workers != prev.Workers {
		if in.RestartHint != "" {
			in.RestartHint += "；workers 变更也需重启"
		} else {
			in.RestartHint = "workers 已保存，需手动重启 gateway 后才切换后端列表"
		}
	}

	st.s = in
	if err := st.persistLocked(); err != nil {
		return in, err
	}
	out := in
	out.APIKey = ""
	out.AdminKey = ""
	return out, nil
}

func (st *settingsStore) persistLocked() error {
	if st.path == "" {
		return nil
	}
	snap := st.s
	snap.APIKey = ""
	snap.AdminKey = ""
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(st.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, st.path); err != nil {
		return err
	}
	ok = true
	return nil
}

func (st *settingsStore) peekAdminKey() string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.adminKey
}

func (st *settingsStore) peekAPIKey() string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.apiKey
}

// EnableAdminConsole mounts /admin UI + JSON APIs on the gateway.
// dataDir holds settings.json for gateway-owned knobs (max_concurrent etc.).
func (s *Server) EnableAdminConsole(dataDir string) {
	if s == nil {
		return
	}
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "./data-gateway"
	}
	workers := strings.Join(s.cfg.WorkerBaseURLs, ",")
	seed := RuntimeSettings{
		MaxConcurrent:      s.cfg.MaxConcurrent,
		Listen:             s.cfg.Listen,
		LoggingLevel:       "info",
		SelectorStrategy:   "stable_rr",
		AvailabilityMode:   "stable",
		HotSize:            0,
		Workers:            workers,
		ShardCount:         s.cfg.ShardCount,
		StickyTTLSec:       int64(s.cfg.StickyTTL / time.Second),
		RequestTimeoutSec:  int(s.cfg.RequestTimeout / time.Second),
		APIKeyConfigured:   strings.TrimSpace(s.cfg.APIKey) != "",
		AdminKeyConfigured: strings.TrimSpace(s.cfg.AdminKey) != "",
		UpstreamNote:       "Scheme B gateway：并发/粘性在此热更；账号库在 worker/Postgres",
	}
	path := filepath.Join(dataDir, "settings.json")
	st := newSettingsStore(path, seed, s.cfg.APIKey, s.cfg.AdminKey)
	// apply loaded max concurrent immediately
	if st.s.MaxConcurrent > 0 {
		atomic.StoreInt64(&s.maxConcurrent, st.s.MaxConcurrent)
		s.cfg.MaxConcurrent = st.s.MaxConcurrent
	} else {
		atomic.StoreInt64(&s.maxConcurrent, s.cfg.MaxConcurrent)
	}
	s.settings = st
	s.startedAt = time.Now()
	s.adminEnabled = true
}

func (s *Server) mountAdmin(mux *http.ServeMux) {
	if s == nil || !s.adminEnabled {
		return
	}
	adminui.Mount(mux)
	// Authenticated JSON API used by the embedded admin UI.
	mux.HandleFunc("GET /admin/pool/stats", s.requireAdmin(s.adminPoolStats))
	mux.HandleFunc("GET /admin/settings", s.requireAdmin(s.adminGetSettings))
	mux.HandleFunc("PUT /admin/settings", s.requireAdmin(s.adminPutSettings))
	mux.HandleFunc("GET /admin/config", s.requireAdmin(s.adminSafeConfig))
	mux.HandleFunc("GET /admin/tokens", s.requireAdmin(s.adminEmptyTokens))
	mux.HandleFunc("GET /admin/accounts", s.requireAdmin(s.adminEmptyAccounts))
	mux.HandleFunc("GET /admin/import/jobs", s.requireAdmin(s.adminEmptyImports))
	// keep scheme2 status
	mux.HandleFunc("GET /admin/scheme2/status", s.requireAdmin(s.adminStatusJSON))
}

func constantTimeKeyEq(got, want string) bool {
	if want == "" {
		return false
	}
	a := sha256.Sum256([]byte(got))
	b := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func extractAdminKey(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("x-admin-key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("X-Admin-Key")); v != "" {
		return v
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := ""
		if s.settings != nil {
			want = s.settings.peekAdminKey()
		}
		if want == "" {
			want = strings.TrimSpace(s.cfg.AdminKey)
		}
		if want == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "未配置 admin_key"})
			return
		}
		if !constantTimeKeyEq(extractAdminKey(r), want) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "admin 鉴权失败"})
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) adminGetSettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "settings unavailable"})
		return
	}
	// reflect live concurrent
	snap := s.settings.snapshot()
	snap.MaxConcurrent = atomic.LoadInt64(&s.maxConcurrent)
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) adminPutSettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "settings unavailable"})
		return
	}
	var in RuntimeSettings
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	out, err := s.settings.apply(in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	// hot-apply max concurrent immediately
	if out.MaxConcurrent > 0 {
		atomic.StoreInt64(&s.maxConcurrent, out.MaxConcurrent)
		s.cfg.MaxConcurrent = out.MaxConcurrent
	}
	// sticky ttl / request timeout: update client timeout best-effort
	if out.RequestTimeoutSec > 0 {
		s.cfg.RequestTimeout = time.Duration(out.RequestTimeoutSec) * time.Second
		s.client.Timeout = s.cfg.RequestTimeout
	}
	if out.StickyTTLSec > 0 {
		s.cfg.StickyTTL = time.Duration(out.StickyTTLSec) * time.Second
	}
	if out.ShardCount > 0 {
		s.cfg.ShardCount = out.ShardCount
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"persisted": strings.TrimSpace(s.settings.path) != "",
		"settings":  out,
	})
}

func (s *Server) adminPoolStats(w http.ResponseWriter, r *http.Request) {
	uptime := 0.0
	if !s.startedAt.IsZero() {
		uptime = time.Since(s.startedAt).Seconds()
	}
	maxC := atomic.LoadInt64(&s.maxConcurrent)
	if maxC <= 0 {
		maxC = s.cfg.MaxConcurrent
	}
	// best-effort aggregate worker status
	workersOK, workersTotal := 0, len(s.cfg.WorkerBaseURLs)
	var hotLoaded, hotCap int
	for _, base := range s.cfg.WorkerBaseURLs {
		u := strings.TrimRight(base, "/") + "/internal/v1/status"
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		resp, err := s.client.Do(req)
		if err != nil {
			continue
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			workersOK++
			if v, ok := body["hot_loaded"].(float64); ok {
				hotLoaded += int(v)
			}
			if v, ok := body["hot_cap"].(float64); ok {
				hotCap += int(v)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":            "scheme-b-gateway",
		"component":          "gateway",
		"uptime_seconds":     uptime,
		"proxy_inflight":     s.inflight.Load(),
		"max_concurrent":     maxC,
		"listen":             s.cfg.Listen,
		"workers_total":      workersTotal,
		"workers_ok":         workersOK,
		"pool_hot_size":      hotLoaded,
		"hot_cap":            hotCap,
		"pool_cooldown_size": 0,
		"requests_total":     0,
		"errors_total":       0,
		"success_rate":       1.0,
		"tokens_total":       0,
		"tokens_enabled":     0,
		"tokens_exhausted":   0,
		"accounts_available": hotLoaded,
		"accounts_total":     hotCap,
		"note":               "B 网关仪表盘：账号明细在 worker/Postgres；此处展示网关并发与 worker 热池汇总",
	})
}

func (s *Server) adminSafeConfig(w http.ResponseWriter, r *http.Request) {
	maxC := atomic.LoadInt64(&s.maxConcurrent)
	out := map[string]any{
		"listen":         s.cfg.Listen,
		"max_concurrent": maxC,
		"workers":        s.cfg.WorkerBaseURLs,
		"shard_count":    s.cfg.ShardCount,
		"component":      "gateway",
		"mode":           "scheme-b",
		"admin_key_set":  s.settings != nil && s.settings.peekAdminKey() != "",
		"api_key_set":    s.settings != nil && s.settings.peekAPIKey() != "",
		"note":           "Scheme B gateway 管理台；max_concurrent 保存后即时热更",
	}
	if s.settings != nil {
		out["runtime"] = s.settings.snapshot()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminEmptyTokens(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tokens": []any{},
		"note":   "Scheme B gateway 未内置令牌库；请在 A(pool-proxy) 或后续 controlplane 管理",
	})
}

func (s *Server) adminEmptyAccounts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"accounts":    []any{},
		"next_cursor": "",
		"limit":       50,
		"total":       0,
		"note":        "账号目录在 worker/Postgres；gateway 仅路由。请用导入脚本/psql 或后续接入 store 管理 API",
	})
}

func (s *Server) adminEmptyImports(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs": []any{},
		"limits": map[string]any{
			"enabled":                  false,
			"max_upload_bytes":         0,
			"max_entries":              0,
			"sso_converter_configured": false,
		},
		"note": "Scheme B 导入请走 Postgres/控制面路径；gateway 管理台暂不接上传",
	})
}

func (s *Server) adminStatusJSON(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"component":      "gateway",
		"workers":        len(s.cfg.WorkerBaseURLs),
		"inflight":       s.inflight.Load(),
		"max_concurrent": atomic.LoadInt64(&s.maxConcurrent),
		"shard_count":    s.cfg.ShardCount,
		"listen":         s.cfg.Listen,
	})
}

// currentMaxConcurrent returns live limit for proxy path.
func (s *Server) currentMaxConcurrent() int64 {
	if s == nil {
		return 0
	}
	if n := atomic.LoadInt64(&s.maxConcurrent); n > 0 {
		return n
	}
	return s.cfg.MaxConcurrent
}

// clientIP helper reserved for future rate limits.
func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// ensure unused import guard in case build tags change
var _ = fmt.Sprintf
