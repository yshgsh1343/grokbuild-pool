package clients

import (
	"os"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound      = errors.New("clients: token not found")
	ErrDisabled      = errors.New("clients: token disabled")
	ErrExpired       = errors.New("clients: token expired")
	ErrQuotaExceeded = errors.New("clients: quota exceeded")
	ErrUnauthorized  = errors.New("clients: invalid api key")
)

// Store SQLite 令牌库 + 进程内并发/RPM 闸门。
type Store struct {
	db *sql.DB
	mu sync.Mutex // serializes writes; matches catalog single-writer style

	// 进程内实时闸门
	gateMu   sync.Mutex
	inflight map[string]int
	rpmWin   map[string]*rpmWindow
}

type rpmWindow struct {
	start time.Time
	count int
}

// Open 打开或创建 tokens.db（WAL）。
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("clients: empty path")
	}
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("clients: open: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	s := &Store{db: db, inflight: make(map[string]int), rpmWin: make(map[string]*rpmWindow)}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS api_tokens (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  key_prefix TEXT NOT NULL,
  key_hash TEXT NOT NULL UNIQUE,
  key_plain TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  remain_quota INTEGER NOT NULL DEFAULT 0,
  unlimited_quota INTEGER NOT NULL DEFAULT 0,
  max_concurrent INTEGER NOT NULL DEFAULT 0,
  rpm INTEGER NOT NULL DEFAULT 0,
  used_quota INTEGER NOT NULL DEFAULT 0,
  request_count INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_tokens_hash ON api_tokens(key_hash);
CREATE INDEX IF NOT EXISTS idx_tokens_enabled ON api_tokens(enabled);
`)
	if err != nil {
		return err
	}
	// 旧库兼容：保留 key_plain 列，但新写入恒为空；并清空历史明文。
		_, _ = s.db.Exec(`ALTER TABLE api_tokens ADD COLUMN key_plain TEXT NOT NULL DEFAULT ''`)
		_, _ = s.db.Exec(`UPDATE api_tokens SET key_plain='' WHERE key_plain != ''`)
		return nil
	}

	// Create 发放一把或多把令牌；明文仅在此返回，不写入 key_plain。
func (s *Store) Create(req CreateRequest) ([]CreateResult, error) {
	if s == nil {
		return nil, fmt.Errorf("clients: nil store")
	}
	n := req.Count
	if n <= 0 {
		n = 1
	}
	if n > 100 {
		return nil, fmt.Errorf("clients: count max 100")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "token"
	}
	now := nowUnix()
	out := make([]CreateResult, 0, n)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for i := 0; i < n; i++ {
		plain, prefix, hash, err := newAPIKey()
		if err != nil {
			return nil, err
		}
		id := "tok_" + randomHex(8)
		itemName := name
		if n > 1 {
			itemName = fmt.Sprintf("%s-%d", name, i+1)
		}
		t := Token{
			ID: id, Name: itemName, KeyPrefix: prefix, KeyHash: hash, APIKey: plain,
			Enabled: true, RemainQuota: req.RemainQuota, UnlimitedQuota: req.UnlimitedQuota,
			MaxConcurrent: req.MaxConcurrent, RPM: req.RPM, ExpiresAt: req.ExpiresAt,
			CreatedAt: now, UpdatedAt: now,
		}
		_, err = tx.Exec(`INSERT INTO api_tokens(
id,name,key_prefix,key_hash,key_plain,enabled,remain_quota,unlimited_quota,max_concurrent,rpm,
used_quota,request_count,expires_at,created_at,updated_at,last_used_at
) VALUES(?,?,?,?,'',1,?,?,?,?,0,0,?,?,?,0)`,
			t.ID, t.Name, t.KeyPrefix, t.KeyHash, t.RemainQuota, boolInt(t.UnlimitedQuota),
			t.MaxConcurrent, t.RPM, t.ExpiresAt, t.CreatedAt, t.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("clients: insert: %w", err)
		}
		out = append(out, CreateResult{Token: t, APIKey: plain, Plaintext: plain})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// List 按创建时间倒序列出（不含明文密钥；仅 key_prefix 可供识别）。
func (s *Store) List(limit int) ([]Token, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,name,key_prefix,enabled,remain_quota,unlimited_quota,max_concurrent,rpm,
used_quota,request_count,expires_at,created_at,updated_at,last_used_at
FROM api_tokens ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		var t Token
		var en, unlim int
		if err := rows.Scan(&t.ID, &t.Name, &t.KeyPrefix, &en, &t.RemainQuota, &unlim, &t.MaxConcurrent, &t.RPM,
			&t.UsedQuota, &t.RequestCount, &t.ExpiresAt, &t.CreatedAt, &t.UpdatedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		t.Enabled = en != 0
		t.UnlimitedQuota = unlim != 0
		t.APIKey = "" // 永不从库回读明文
		out = append(out, t)
	}
	return out, rows.Err()
}
// Delete 按 id 删除令牌。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM api_tokens WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMany 批量删除令牌（单事务）。返回实际删除数量。
func (s *Store) DeleteMany(ids []string) (int, error) {
	if s == nil {
		return 0, fmt.Errorf("clients: nil store")
	}
	seen := make(map[string]struct{}, len(ids))
	clean := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	if len(clean) == 0 {
		return 0, fmt.Errorf("clients: empty ids")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	deleted := 0
	const chunk = 400
	for i := 0; i < len(clean); i += chunk {
		j := i + chunk
		if j > len(clean) {
			j = len(clean)
		}
		part := clean[i:j]
		ph := make([]string, len(part))
		args := make([]any, len(part))
		for k, id := range part {
			ph[k] = "?"
			args[k] = id
		}
		res, err := tx.Exec(`DELETE FROM api_tokens WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		deleted += int(n)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

// SetEnabled 启用/禁用。
func (s *Store) SetEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE api_tokens SET enabled=?, updated_at=? WHERE id=?`, boolInt(enabled), nowUnix(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// PatchQuota 更新额度/并发/RPM。
func (s *Store) PatchQuota(id string, remain *int64, unlimited *bool, maxConc *int, rpm *int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.getByID(id)
	if err != nil {
		return err
	}
	if remain != nil {
		t.RemainQuota = *remain
	}
	if unlimited != nil {
		t.UnlimitedQuota = *unlimited
	}
	if maxConc != nil {
		t.MaxConcurrent = *maxConc
	}
	if rpm != nil {
		t.RPM = *rpm
	}
	_, err = s.db.Exec(`UPDATE api_tokens SET remain_quota=?, unlimited_quota=?, max_concurrent=?, rpm=?, updated_at=? WHERE id=?`,
		t.RemainQuota, boolInt(t.UnlimitedQuota), t.MaxConcurrent, t.RPM, nowUnix(), id)
	return err
}

// Authenticate 校验 Bearer/x-api-key。
func (s *Store) Authenticate(ctx context.Context, plaintext string) (AuthInfo, error) {
	_ = ctx
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return AuthInfo{}, ErrUnauthorized
	}
	hash := hashKey(plaintext)
	var t Token
	var en, unlim int
	err := s.db.QueryRow(`SELECT id,name,enabled,remain_quota,unlimited_quota,max_concurrent,rpm,expires_at
FROM api_tokens WHERE key_hash=?`, hash).Scan(
		&t.ID, &t.Name, &en, &t.RemainQuota, &unlim, &t.MaxConcurrent, &t.RPM, &t.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthInfo{}, ErrUnauthorized
	}
	if err != nil {
		return AuthInfo{}, err
	}
	t.Enabled = en != 0
	t.UnlimitedQuota = unlim != 0
	if !t.Enabled {
		return AuthInfo{}, ErrDisabled
	}
	if t.ExpiresAt > 0 && nowUnix() > t.ExpiresAt {
		return AuthInfo{}, ErrExpired
	}
	if !t.UnlimitedQuota && t.RemainQuota <= 0 {
		return AuthInfo{}, ErrQuotaExceeded
	}
	return AuthInfo{TokenID: t.ID, Name: t.Name, MaxConcurrent: t.MaxConcurrent, RPM: t.RPM}, nil
}

// AcquireSlot 占用每令牌并发与 RPM；调用方必须 ReleaseSlot。
func (s *Store) AcquireSlot(tokenID string, maxConcurrent, rpm int) error {
	s.gateMu.Lock()
	defer s.gateMu.Unlock()
	if maxConcurrent > 0 {
		if s.inflight[tokenID] >= maxConcurrent {
			return fmt.Errorf("clients: token concurrency limit %d", maxConcurrent)
		}
	}
	if rpm > 0 {
		w := s.rpmWin[tokenID]
		now := time.Now()
		if w == nil || now.Sub(w.start) >= time.Minute {
			s.rpmWin[tokenID] = &rpmWindow{start: now, count: 0}
			w = s.rpmWin[tokenID]
		}
		if w.count >= rpm {
			return fmt.Errorf("clients: token rpm limit %d", rpm)
		}
		w.count++
	}
	if maxConcurrent > 0 {
		s.inflight[tokenID]++
	}
	return nil
}

// ReleaseSlot 释放每令牌 in-flight。
func (s *Store) ReleaseSlot(tokenID string, maxConcurrent int) {
	if maxConcurrent <= 0 {
		return
	}
	s.gateMu.Lock()
	defer s.gateMu.Unlock()
	if s.inflight[tokenID] > 0 {
		s.inflight[tokenID]--
	}
}

// ReserveQuota 原子预扣额度。amount<=0 时按 1 计。
// 有限令牌在 remain_quota 不足时返回 ErrQuotaExceeded；无限令牌不改 remain。
// 调用方在请求结束后应 SettleUsage 或 RefundQuota。
func (s *Store) ReserveQuota(tokenID string, amount int64) error {
	if amount <= 0 {
		amount = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := nowUnix()
	res, err := s.db.Exec(`
UPDATE api_tokens SET
  remain_quota = CASE WHEN unlimited_quota=1 THEN remain_quota ELSE remain_quota - ? END,
  updated_at = ?
WHERE id=? AND enabled=1
  AND (unlimited_quota=1 OR remain_quota >= ?)`, amount, now, tokenID, amount)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// 区分 not found / disabled / 额度不足
		t, err := s.getByID(tokenID)
		if err != nil {
			return err
		}
		if !t.Enabled {
			return ErrDisabled
		}
		if !t.UnlimitedQuota && t.RemainQuota < amount {
			return ErrQuotaExceeded
		}
		return ErrNotFound
	}
	return nil
}

// RefundQuota 退回预扣额度（5xx / 未完成请求）。
func (s *Store) RefundQuota(tokenID string, amount int64) error {
	if amount <= 0 {
		amount = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := nowUnix()
	res, err := s.db.Exec(`
UPDATE api_tokens SET
  remain_quota = CASE WHEN unlimited_quota=1 THEN remain_quota ELSE remain_quota + ? END,
  updated_at = ?
WHERE id=? AND enabled=1`, amount, now, tokenID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SettleUsage 按实际 cost 结算：相对 reserved 多退少补，并累加 used/request 计数。
// actual/reserved <=0 时按 1 计。若需补扣但余额不足，尽力扣至 0 并返回 ErrQuotaExceeded。
func (s *Store) SettleUsage(tokenID string, reserved, actual int64) error {
	if reserved <= 0 {
		reserved = 1
	}
	if actual <= 0 {
		actual = 1
	}
	delta := actual - reserved
	s.mu.Lock()
	defer s.mu.Unlock()
	now := nowUnix()

	// 先取当前状态做有界补扣
	t, err := s.getByID(tokenID)
	if err != nil {
		return err
	}
	if !t.Enabled {
		return ErrDisabled
	}

	extra := int64(0)
	shortfall := false
	if delta > 0 && !t.UnlimitedQuota {
		if t.RemainQuota >= delta {
			extra = delta
		} else {
			extra = t.RemainQuota
			shortfall = true
		}
	}
	refund := int64(0)
	if delta < 0 && !t.UnlimitedQuota {
		refund = -delta
	}

	_, err = s.db.Exec(`
UPDATE api_tokens SET
  used_quota = used_quota + ?,
  request_count = request_count + 1,
  remain_quota = CASE
    WHEN unlimited_quota=1 THEN remain_quota
    ELSE remain_quota - ? + ?
  END,
  last_used_at = ?,
  updated_at = ?
WHERE id=? AND enabled=1`, actual, extra, refund, now, now, tokenID)
	if err != nil {
		return err
	}
	if shortfall {
		return ErrQuotaExceeded
	}
	return nil
}

// RecordUsage 一次性扣减额度（若有限）并累加计数；cost<=0 时按 1 计。
// 对有限令牌使用条件更新：余额不足时返回 ErrQuotaExceeded，避免并发超扣。
func (s *Store) RecordUsage(tokenID string, cost int64) error {
	if cost <= 0 {
		cost = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := nowUnix()
	res, err := s.db.Exec(`
UPDATE api_tokens SET
  used_quota = used_quota + ?,
  request_count = request_count + 1,
  remain_quota = CASE WHEN unlimited_quota=1 THEN remain_quota ELSE remain_quota - ? END,
  last_used_at = ?,
  updated_at = ?
WHERE id=? AND enabled=1
  AND (unlimited_quota=1 OR remain_quota >= ?)`, cost, cost, now, now, tokenID, cost)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		t, err := s.getByID(tokenID)
		if err != nil {
			return err
		}
		if !t.Enabled {
			return ErrDisabled
		}
		if !t.UnlimitedQuota && t.RemainQuota < cost {
			return ErrQuotaExceeded
		}
		return ErrNotFound
	}
	return nil
}

// Stats 聚合令牌表供仪表盘。
func (s *Store) Stats() (total, enabled, exhausted int, err error) {
	err = s.db.QueryRow(`SELECT
  COUNT(*),
  COALESCE(SUM(CASE WHEN enabled=1 THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN unlimited_quota=0 AND remain_quota<=0 THEN 1 ELSE 0 END),0)
FROM api_tokens`).Scan(&total, &enabled, &exhausted)
	return
}

func (s *Store) getByID(id string) (Token, error) {
	var t Token
	var en, unlim int
	err := s.db.QueryRow(`SELECT id,name,key_prefix,enabled,remain_quota,unlimited_quota,max_concurrent,rpm,
used_quota,request_count,expires_at,created_at,updated_at,last_used_at FROM api_tokens WHERE id=?`, id).
		Scan(&t.ID, &t.Name, &t.KeyPrefix, &en, &t.RemainQuota, &unlim, &t.MaxConcurrent, &t.RPM,
			&t.UsedQuota, &t.RequestCount, &t.ExpiresAt, &t.CreatedAt, &t.UpdatedAt, &t.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, ErrNotFound
	}
	if err != nil {
		return Token{}, err
	}
	t.Enabled = en != 0
	t.UnlimitedQuota = unlim != 0
	return t, nil
}

func newAPIKey() (plain, prefix, hash string, err error) {
	b := make([]byte, 24)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	plain = "sk-" + hex.EncodeToString(b)
	if len(plain) > 10 {
		prefix = plain[:7] + "…" + plain[len(plain)-4:]
	} else {
		prefix = plain
	}
	hash = hashKey(plain)
	return plain, prefix, hash, nil
}

func hashKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
