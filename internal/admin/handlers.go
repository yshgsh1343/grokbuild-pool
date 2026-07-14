package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/clients"
	"github.com/yshgsh1343/grokbuild2api/internal/config"
	"github.com/yshgsh1343/grokbuild2api/internal/importjobs"
)

// adminAuthLimiter 进程内管理鉴权失败限速（按客户端 IP）。
type adminAuthLimiter struct {
	mu     sync.Mutex
	fails  map[string]*adminAuthWindow
	limit  int
	window time.Duration
}

type adminAuthWindow struct {
	start time.Time
	count int
}

var defaultAdminAuthLimiter = &adminAuthLimiter{
	fails:  make(map[string]*adminAuthWindow),
	limit:  20,
	window: time.Minute,
}

func (l *adminAuthLimiter) allow(ip string) bool {
	if l == nil {
		return true
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fails == nil {
		l.fails = make(map[string]*adminAuthWindow)
	}
	w := l.fails[ip]
	if w == nil || now.Sub(w.start) >= l.window {
		l.fails[ip] = &adminAuthWindow{start: now, count: 0}
		w = l.fails[ip]
	}
	if w.count >= l.limit {
		return false
	}
	return true
}

func (l *adminAuthLimiter) fail(ip string) {
	if l == nil {
		return
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fails == nil {
		l.fails = make(map[string]*adminAuthWindow)
	}
	w := l.fails[ip]
	if w == nil || now.Sub(w.start) >= l.window {
		l.fails[ip] = &adminAuthWindow{start: now, count: 1}
		return
	}
	w.count++
}

func (l *adminAuthLimiter) success(ip string) {
	if l == nil {
		return
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}

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

// constantTimeAdminKeyEq 比较管理密钥：对两侧做 SHA-256 后常量时间比较，避免长度泄漏与逐字节计时。
func constantTimeAdminKeyEq(got, want string) bool {
	if want == "" {
		return false
	}
	sumGot := sha256.Sum256([]byte(got))
	sumWant := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(sumGot[:], sumWant[:]) == 1
}

// HotStats 热池只读指标（供仪表盘）。
type HotStats interface {
	HotLen() int
	Cap() int
	PoolStats() (hotSize, cooldown int)
}

// MetricsView 进程指标只读视图（由 httpserver.Metrics 实现，避免循环依赖）。
type MetricsView interface {
	Requests() int64
	Errors() int64
	Rejects() int64
	Inflight() int64
}

// Handlers 管理 API 处理器集合。
type Handlers struct {
	AdminKey  string
	Config    config.Config
	Tokens    *clients.Store
	Hot       HotStats
	Metrics   MetricsView
	Version   string
	StartedAt time.Time
	Settings  *SettingsController
	// Catalog 冷存储（账号列表 / 启停）；可空则相关路由 503。
	Catalog *catalog.Catalog
	// Lease 可选：模型冷却查询。
	Lease interface {
		ListModelCooldowns(accountID string) []catalog.ModelCooldown
	}
	// AccountHot 热池同步（启停账号）；可空则只改冷存储。
	AccountHot AccountHot
	// Outbound 出站工厂（账号测活 /billing）；可空则测活路由 503。
	Outbound AccountProbeClient
	// ImportJobs 异步 bulkimport 任务表（P1）；可空则相关路由 503。
	ImportJobs *importjobs.Manager
	// ProxyPool SOCKS5/HTTP 代理池；可空则相关路由 503。
	ProxyPool ProxyPoolAPI
}

// effectiveAdminKey 优先使用设置页热更新后的密钥。
func (h *Handlers) effectiveAdminKey() string {
	if h != nil && h.Settings != nil {
		if k := strings.TrimSpace(h.Settings.PeekAdminKey()); k != "" {
			return k
		}
	}
	if h == nil {
		return ""
	}
	return strings.TrimSpace(h.AdminKey)
}

// RequireAdmin 校验 admin_key（Bearer 或 x-admin-key）。
// 使用摘要常量时间比较，并对失败尝试按 IP 限速。
func (h *Handlers) RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := ""
		if h != nil {
			want = h.effectiveAdminKey()
		}
		if want == "" {
			writeErr(w, http.StatusServiceUnavailable, "未配置 admin_key")
			return
		}
		ip := clientIP(r)
		if !defaultAdminAuthLimiter.allow(ip) {
			w.Header().Set("Retry-After", "60")
			writeErr(w, http.StatusTooManyRequests, "admin 鉴权尝试过于频繁")
			return
		}
		key := extractAdminKey(r)
		if !constantTimeAdminKeyEq(key, want) {
			defaultAdminAuthLimiter.fail(ip)
			writeErr(w, http.StatusUnauthorized, "admin 鉴权失败")
			return
		}
		defaultAdminAuthLimiter.success(ip)
		next(w, r)
	}
}

func extractAdminKey(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("x-admin-key")); v != "" {
		return v
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

// Mount 将管理路由挂到 mux（JSON 需 admin；静态 UI 由 adminui 单独挂载）。
func (h *Handlers) Mount(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /admin/pool/stats", h.RequireAdmin(h.PoolStats))
	mux.HandleFunc("GET /admin/tokens", h.RequireAdmin(h.ListTokens))
	mux.HandleFunc("POST /admin/tokens", h.RequireAdmin(h.CreateTokens))
	mux.HandleFunc("POST /admin/tokens/batch", h.RequireAdmin(h.BatchTokens))
	mux.HandleFunc("GET /admin/tokens/{id}", h.RequireAdmin(h.GetToken))
	mux.HandleFunc("DELETE /admin/tokens/{id}", h.RequireAdmin(h.DeleteToken))
	mux.HandleFunc("POST /admin/tokens/{id}/disable", h.RequireAdmin(h.DisableToken))
	mux.HandleFunc("POST /admin/tokens/{id}/enable", h.RequireAdmin(h.EnableToken))
	mux.HandleFunc("PATCH /admin/tokens/{id}", h.RequireAdmin(h.PatchToken))
	mux.HandleFunc("GET /admin/config", h.RequireAdmin(h.SafeConfig))
	mux.HandleFunc("GET /admin/settings", h.RequireAdmin(h.GetSettings))
	mux.HandleFunc("PUT /admin/settings", h.RequireAdmin(h.PutSettings))
	// SOCKS5/HTTP 代理池
	mux.HandleFunc("GET /admin/proxy-pool", h.RequireAdmin(h.GetProxyPool))
	mux.HandleFunc("PUT /admin/proxy-pool", h.RequireAdmin(h.PutProxyPool))
	mux.HandleFunc("POST /admin/proxy-pool/assign", h.RequireAdmin(h.AssignProxyPool))
	// 账号目录（脱敏分页 / 手动启停 / 批量 / 代理 / 导出 / 测活）
	mux.HandleFunc("GET /admin/accounts", h.RequireAdmin(h.ListAccounts))
	// 导出与批量路由须先于 /{id}/… 注册
	mux.HandleFunc("GET /admin/accounts/export", h.RequireAdmin(h.ExportAccounts))
	mux.HandleFunc("POST /admin/accounts/batch", h.RequireAdmin(h.BatchAccounts))
	mux.HandleFunc("POST /admin/accounts/probe", h.RequireAdmin(h.BatchProbeAccounts))
	mux.HandleFunc("POST /admin/accounts/{id}/probe", h.RequireAdmin(h.ProbeAccount))
	mux.HandleFunc("GET /admin/accounts/{id}/model-cooldowns", h.RequireAdmin(h.ListAccountModelCooldowns))
	mux.HandleFunc("POST /admin/accounts/{id}/disable", h.RequireAdmin(h.DisableAccount))
	mux.HandleFunc("POST /admin/accounts/{id}/enable", h.RequireAdmin(h.EnableAccount))
	mux.HandleFunc("PATCH /admin/accounts/{id}", h.RequireAdmin(h.PatchAccountProxy))
	// 导入任务（P1 最小接线）
	mux.HandleFunc("GET /admin/import/jobs", h.RequireAdmin(h.ListImportJobs))
		mux.HandleFunc("GET /admin/import/server-dir", h.RequireAdmin(h.ListServerImportDir))
	mux.HandleFunc("POST /admin/import/jobs", h.RequireAdmin(h.CreateImportJob))
	mux.HandleFunc("GET /admin/import/jobs/{id}", h.RequireAdmin(h.GetImportJob))
}

// PoolStats 返回仪表盘 KPI（不含密钥明文）。
func (h *Handlers) PoolStats(w http.ResponseWriter, r *http.Request) {
	hotSize, cooldown := 0, 0
	if h.Hot != nil {
		hotSize, cooldown = h.Hot.PoolStats()
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	reqTotal, errTotal, reject, inflight := int64(0), int64(0), int64(0), int64(0)
	if h.Metrics != nil {
		reqTotal = h.Metrics.Requests()
		errTotal = h.Metrics.Errors()
		reject = h.Metrics.Rejects()
		inflight = h.Metrics.Inflight()
	}
	successRate := 1.0
	if reqTotal > 0 {
		ok := reqTotal - errTotal
		if ok < 0 {
			ok = 0
		}
		successRate = float64(ok) / float64(reqTotal)
	}
	tokTotal, tokEnabled, tokExhausted := 0, 0, 0
	if h.Tokens != nil {
		tokTotal, tokEnabled, tokExhausted, _ = h.Tokens.Stats()
	}
	uptime := 0.0
	if !h.StartedAt.IsZero() {
		uptime = time.Since(h.StartedAt).Seconds()
	}
	hotCap := h.Config.HotSize
	if h.Hot != nil {
		if c := h.Hot.Cap(); c > 0 {
			hotCap = c
		}
	}
	out := map[string]any{
		"version":            h.Version,
		"uptime_seconds":     uptime,
		"requests_total":     reqTotal,
		"errors_total":       errTotal,
		"success_rate":       successRate,
		"proxy_reject_total": reject,
		"proxy_inflight":     inflight,
		"pool_hot_size":      hotSize,
		"pool_cooldown_size": cooldown,
		// 历史字段名 process_rss_bytes 实际为 MemStats.Sys，保留键名兼容并增加准确别名
		"process_rss_bytes": ms.Sys,
		"process_sys_bytes": ms.Sys,
		"go_goroutines":     runtime.NumGoroutine(),
		"tokens_total":      tokTotal,
		"tokens_enabled":    tokEnabled,
		"tokens_exhausted":  tokExhausted,
		"listen":            h.Config.Listen,
		"hot_cap":           hotCap,
		"max_concurrent":    h.Config.Limits.MaxConcurrent,
	}
	// 热更新后以 settings 为准（启动时 Config 快照不会跟着变）
	if h.Settings != nil {
		snap := h.Settings.Snapshot().RuntimeSettings
		out["max_concurrent"] = snap.MaxConcurrent
		if snap.Listen != "" {
			out["listen"] = snap.Listen
		}
	}
	// P1：refresh / quarantine
	if x, ok := h.Metrics.(interface {
		RefreshOK() int64
		RefreshFail() int64
		QuarantineCount() int64
	}); ok {
		out["refresh_ok_total"] = x.RefreshOK()
		out["refresh_fail_total"] = x.RefreshFail()
		out["pool_quarantine_count"] = x.QuarantineCount()
	}
	if h.Catalog != nil {
		if st, err := h.Catalog.Stats(); err == nil {
			out["catalog_count"] = st.Count
			out["catalog_enabled"] = st.EnabledCount
			out["catalog_active"] = st.ActiveCount
			out["catalog_disabled"] = st.DisabledCount
			out["catalog_cooldown"] = st.CooldownCount
			out["catalog_quarantine"] = st.QuarantineCount
			out["pool_quarantine_count"] = st.QuarantineCount
			// 可用账号：启用且 active（冷却/隔离另计）
			out["accounts_available"] = st.ActiveCount
			out["accounts_total"] = st.Count
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ListTokens 列出令牌（含 api_key 明文，供管理台查看/复制）。
func (h *Handlers) ListTokens(w http.ResponseWriter, r *http.Request) {
	if h.Tokens == nil {
		writeErr(w, http.StatusServiceUnavailable, "令牌存储未启用")
		return
	}
	list, err := h.Tokens.List(200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []clients.Token{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": list})
}

// CreateTokens 快速创建/批量发放密钥（明文写入库，并可在 List/Get 再次读取）。
// 指针字段语义：nil=未传 → 用默认模板；显式 0/false 保留，绝不被默认覆盖。
func (h *Handlers) CreateTokens(w http.ResponseWriter, r *http.Request) {
	if h.Tokens == nil {
		writeErr(w, http.StatusServiceUnavailable, "令牌存储未启用")
		return
	}
	var req clients.CreateRequest
	if err := decodeJSON(r, 1<<20, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		req.Name = "client"
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	// 未显式填写时使用管理台默认模板（仅填 nil 字段）
	if h.Settings != nil {
		d := h.Settings.Snapshot()
		if req.RemainQuota == nil {
			if d.TokenDefaultUnlimited {
				z := int64(0)
				req.RemainQuota = &z
			} else if d.TokenDefaultRemainQuota > 0 {
				v := d.TokenDefaultRemainQuota
				req.RemainQuota = &v
			} else {
				z := int64(0)
				req.RemainQuota = &z
			}
		}
		if req.UnlimitedQuota == nil {
			u := d.TokenDefaultUnlimited
			req.UnlimitedQuota = &u
		}
		if req.MaxConcurrent == nil {
			// 默认并发：配置值；0 表示不限
			v := d.TokenDefaultMaxConcurrent
			req.MaxConcurrent = &v
		}
		if req.RPM == nil {
			v := d.TokenDefaultRPM
			req.RPM = &v
		}
	} else {
		if req.RemainQuota == nil {
			z := int64(0)
			req.RemainQuota = &z
		}
		if req.UnlimitedQuota == nil {
			f := false
			req.UnlimitedQuota = &f
		}
		if req.MaxConcurrent == nil {
			z := 0
			req.MaxConcurrent = &z
		}
		if req.RPM == nil {
			z := 0
			req.RPM = &z
		}
	}
	res, err := h.Tokens.Create(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// 单条兼容字段
	out := map[string]any{"created": len(res), "tokens": res}
	if len(res) == 1 {
		out["token"] = res[0].Token
		out["api_key"] = res[0].APIKey
		out["plaintext"] = res[0].Plaintext
	}
	writeJSON(w, http.StatusCreated, out)
}

// DeleteToken 删除令牌。
func (h *Handlers) DeleteToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if h.Tokens == nil {
		writeErr(w, http.StatusServiceUnavailable, "令牌存储未启用")
		return
	}
	if err := h.Tokens.Delete(id); err != nil {
		if errors.Is(err, clients.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "令牌不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// BatchTokens POST /admin/tokens/batch
// body: {"action":"delete","ids":["..."]}，ids 上限 500；单事务批量删除。
func (h *Handlers) BatchTokens(w http.ResponseWriter, r *http.Request) {
	if h.Tokens == nil {
		writeErr(w, http.StatusServiceUnavailable, "令牌存储未启用")
		return
	}
	var body struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := decodeJSON(r, 1<<20, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	action := strings.ToLower(strings.TrimSpace(body.Action))
	if action != "delete" {
		writeErr(w, http.StatusBadRequest, "action 目前仅支持 delete")
		return
	}
	if len(body.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "ids 不能为空")
		return
	}
	if len(body.IDs) > 500 {
		writeErr(w, http.StatusBadRequest, "ids 最多 500 个")
		return
	}
	n, err := h.Tokens.DeleteMany(body.IDs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"action":  action,
		"ok":      n,
		"failed":  len(body.IDs) - n,
		"deleted": n,
	})
}

// DisableToken 禁用。
func (h *Handlers) DisableToken(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, false)
}

// EnableToken 启用。
func (h *Handlers) EnableToken(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, true)
}

func (h *Handlers) setEnabled(w http.ResponseWriter, r *http.Request, en bool) {
	id := r.PathValue("id")
	if h.Tokens == nil {
		writeErr(w, http.StatusServiceUnavailable, "令牌存储未启用")
		return
	}
	if err := h.Tokens.SetEnabled(id, en); err != nil {
		if errors.Is(err, clients.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "令牌不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "enabled": en})
}

// PatchToken 调整名称/额度/并发/RPM/启用状态。
// 指针字段 nil=不改；显式 0 可把并发/RPM 改为「不限」。
// 返回更新后的完整令牌（含 inflight），前端可直接刷新行。
func (h *Handlers) PatchToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if h.Tokens == nil {
		writeErr(w, http.StatusServiceUnavailable, "令牌存储未启用")
		return
	}
	var body clients.PatchRequest
	if err := decodeJSON(r, 1<<20, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Name == nil && body.RemainQuota == nil && body.UnlimitedQuota == nil &&
		body.MaxConcurrent == nil && body.RPM == nil && body.Enabled == nil && body.ExpiresAt == nil {
		writeErr(w, http.StatusBadRequest, "无任何可更新字段")
		return
	}
	tok, err := h.Tokens.Patch(id, body)
	if err != nil {
		if errors.Is(err, clients.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "令牌不存在")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "patched": true, "token": tok})
}

// GetToken 单条令牌详情（含 inflight）。
func (h *Handlers) GetToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if h.Tokens == nil {
		writeErr(w, http.StatusServiceUnavailable, "令牌存储未启用")
		return
	}
	tok, err := h.Tokens.Get(id)
	if err != nil {
		if errors.Is(err, clients.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "令牌不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok})
}

// SafeConfig 返回可展示配置（无密钥）。
func (h *Handlers) SafeConfig(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"listen":         h.Config.Listen,
		"data_dir":       h.Config.DataDir,
		"hot_size":       h.Config.HotSize,
		"max_concurrent": h.Config.Limits.MaxConcurrent,
		"api_key_set":    strings.TrimSpace(h.Config.APIKey) != "",
		"admin_key_set":  strings.TrimSpace(h.Config.AdminKey) != "",
		"version":        h.Version,
		"imports": map[string]any{
			"enabled":                  h.importEnabled(),
			"max_upload_bytes":         h.effectiveImportMaxUploadBytes(),
			"max_entries":              h.effectiveImportMaxEntries(),
			"allow_server_path":        h.importAllowServerPath(),
			"sso_converter_configured": h.ssoConverterConfigured(),
		},
		"note": "可热更新参数见 GET/PUT /admin/settings",
	}
	if h.Settings != nil {
		snap := h.Settings.Snapshot()
		out["runtime"] = snap
		out["max_concurrent"] = snap.MaxConcurrent
		if snap.Listen != "" {
			out["listen"] = snap.Listen
		}
		if snap.HotSize > 0 {
			out["hot_size"] = snap.HotSize
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func decodeJSON(r *http.Request, max int64, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, max))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// ListAccountModelCooldowns GET /admin/accounts/{id}/model-cooldowns
func (h *Handlers) ListAccountModelCooldowns(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing id")
		return
	}
	var rows []catalog.ModelCooldown
	if h.Lease != nil {
		rows = h.Lease.ListModelCooldowns(id)
	} else if h.Catalog != nil {
		var err error
		rows, err = h.Catalog.ListModelCooldowns(id, 0)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if rows == nil {
		rows = []catalog.ModelCooldown{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"account_id": id, "model_cooldowns": rows})
}
