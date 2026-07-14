package catalog

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	// upsertChunkSize 限制 UpsertMany 每个事务的行数。
	upsertChunkSize = 500

	schemaSQL = `
CREATE TABLE IF NOT EXISTS accounts (
  id TEXT PRIMARY KEY,
  revision INTEGER NOT NULL DEFAULT 1,
  identity_key TEXT,
  email TEXT,
  name TEXT,
  priority INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  manual_disabled INTEGER NOT NULL DEFAULT 0,
  lifecycle TEXT NOT NULL DEFAULT 'active',
  access_token TEXT NOT NULL,
  refresh_token TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  proxy_mode TEXT,
  proxy_url TEXT,
  failure_count INTEGER NOT NULL DEFAULT 0,
  success_count INTEGER NOT NULL DEFAULT 0,
  cooldown_until INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  last_used_at INTEGER,
  last_success_at INTEGER,
  last_refresh_at INTEGER,
  consecutive_unauthorized INTEGER NOT NULL DEFAULT 0,
  quarantine_fp TEXT,
  purge_after INTEGER,
  billing_json TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_accounts_eligible ON accounts(enabled, lifecycle, cooldown_until, priority);
CREATE INDEX IF NOT EXISTS idx_accounts_expires ON accounts(expires_at);
CREATE INDEX IF NOT EXISTS idx_accounts_identity ON accounts(identity_key);
CREATE TABLE IF NOT EXISTS model_cooldowns (
  account_id TEXT NOT NULL,
  model TEXT NOT NULL,
  cooldown_until INTEGER NOT NULL,
  last_error TEXT,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(account_id, model)
);
CREATE INDEX IF NOT EXISTS idx_model_cooldowns_until ON model_cooldowns(cooldown_until);
`
)

// Catalog 是基于 SQLite WAL 的账号凭证冷存储。
type Catalog struct {
	db   *sql.DB
	path string
	mu   sync.Mutex // serializes write methods for predictable CAS / chunking
}

// Open 以 WAL 模式打开（或创建）path 处的 SQLite 库，并设置 busy timeout。
func Open(path string) (*Catalog, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty path", ErrInvalidInput)
	}
	// modernc.org/sqlite DSN：带查询参数的文件路径。
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("catalog: open %s: %w", path, err)
	}
	// 尽量收紧库文件权限（Windows 上可能 no-op/失败，忽略错误）
	_ = os.Chmod(path, 0o600)

	// 并发负载下 WAL + modernc 最稳妥的是单写连接。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	c := &Catalog{db: db, path: path}
	if err := c.configure(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := c.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return c, nil
}

func (c *Catalog) configure() error {
	// 再次设置 pragma，避免不同版本 DSN 处理差异。
	pragmas := []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
	}
	for _, p := range pragmas {
		if _, err := c.db.Exec(p); err != nil {
			return fmt.Errorf("catalog: %s: %w", p, err)
		}
	}
	var mode string
	if err := c.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("catalog: read journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("catalog: expected WAL journal_mode, got %q", mode)
	}
	return nil
}

func (c *Catalog) migrate() error {
	if _, err := c.db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("catalog: migrate: %w", err)
	}
	if err := c.ensureColumn("accounts", "success_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	// model_cooldowns 表由 schemaSQL 创建；旧库 Exec schema 会 IF NOT EXISTS。
	return nil
}

func (c *Catalog) ensureColumn(table, col, decl string) error {
	q := "PRAGMA table_info(" + table + ")"
	rows, err := c.db.Query(q)
	if err != nil {
		return fmt.Errorf("catalog: pragma table_info %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == col {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = c.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + col + " " + decl)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return nil
		}
		return fmt.Errorf("catalog: add column %s.%s: %w", table, col, err)
	}
	return nil
}

// Close 关闭底层数据库。
func (c *Catalog) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	err := c.db.Close()
	c.db = nil
	return err
}

// Path 返回数据库文件路径。
func (c *Catalog) Path() string {
	return c.path
}

// JournalMode 返回当前 PRAGMA journal_mode（测试/运维用）。
func (c *Catalog) JournalMode() (string, error) {
	if c.db == nil {
		return "", ErrClosed
	}
	var mode string
	if err := c.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return "", err
	}
	return mode, nil
}

// UpsertMany 按分块（默认每事务 500 行）插入或整行替换账号。
// 按 id 冲突时覆盖全部字段，revision 取自入参 Account（为 0 时置 1）。
// 并发局部更新请优先使用 UpdateTokens / PatchHealth。
func (c *Catalog) UpsertMany(accounts []Account) error {
	if c.db == nil {
		return ErrClosed
	}
	if len(accounts) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Unix()
	const q = `
INSERT INTO accounts (
  id, revision, identity_key, email, name, priority, enabled, manual_disabled,
  lifecycle, access_token, refresh_token, expires_at, proxy_mode, proxy_url,
  failure_count, success_count, cooldown_until, last_error, last_used_at, last_success_at,
  last_refresh_at, consecutive_unauthorized, quarantine_fp, purge_after,
  billing_json, created_at, updated_at
) VALUES (
  ?, ?, ?, ?, ?, ?, ?, ?,
  ?, ?, ?, ?, ?, ?,
  ?, ?, ?, ?, ?, ?,
  ?, ?, ?, ?,
  ?, ?, ?
)
ON CONFLICT(id) DO UPDATE SET
  revision=excluded.revision,
  identity_key=excluded.identity_key,
  email=excluded.email,
  name=excluded.name,
  priority=excluded.priority,
  enabled=excluded.enabled,
  manual_disabled=excluded.manual_disabled,
  lifecycle=excluded.lifecycle,
  access_token=excluded.access_token,
  refresh_token=excluded.refresh_token,
  expires_at=excluded.expires_at,
  proxy_mode=excluded.proxy_mode,
  proxy_url=excluded.proxy_url,
  failure_count=excluded.failure_count,
  success_count=excluded.success_count,
  cooldown_until=excluded.cooldown_until,
  last_error=excluded.last_error,
  last_used_at=excluded.last_used_at,
  last_success_at=excluded.last_success_at,
  last_refresh_at=excluded.last_refresh_at,
  consecutive_unauthorized=excluded.consecutive_unauthorized,
  quarantine_fp=excluded.quarantine_fp,
  purge_after=excluded.purge_after,
  billing_json=excluded.billing_json,
  created_at=excluded.created_at,
  updated_at=excluded.updated_at
`

	for i := 0; i < len(accounts); i += upsertChunkSize {
		end := i + upsertChunkSize
		if end > len(accounts) {
			end = len(accounts)
		}
		chunk := accounts[i:end]
		tx, err := c.db.Begin()
		if err != nil {
			return fmt.Errorf("catalog: begin upsert tx: %w", err)
		}
		stmt, err := tx.Prepare(q)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("catalog: prepare upsert: %w", err)
		}
		for _, a := range chunk {
			if err := validateAccountForUpsert(a); err != nil {
				_ = stmt.Close()
				_ = tx.Rollback()
				return err
			}
			rev := a.Revision
			if rev <= 0 {
				rev = 1
			}
			lifecycle := a.Lifecycle
			if lifecycle == "" {
				lifecycle = LifecycleActive
			}
			created := a.CreatedAt
			if created == 0 {
				created = now
			}
			updated := a.UpdatedAt
			if updated == 0 {
				updated = now
			}
			_, err := stmt.Exec(
				a.ID, rev, nullStr(a.IdentityKey), nullStr(a.Email), nullStr(a.Name),
				a.Priority, boolToInt(a.Enabled), boolToInt(a.ManualDisabled),
				lifecycle, a.AccessToken, a.RefreshToken, a.ExpiresAt,
				nullStr(a.ProxyMode), nullStr(a.ProxyURL),
				a.FailureCount, a.SuccessCount, a.CooldownUntil, nullStr(a.LastError),
				nullInt64(a.LastUsedAt), nullInt64(a.LastSuccessAt), nullInt64(a.LastRefreshAt),
				a.ConsecutiveUnauthorized, nullStr(a.QuarantineFP), nullInt64(a.PurgeAfter),
				nullStr(a.BillingJSON), created, updated,
			)
			if err != nil {
				_ = stmt.Close()
				_ = tx.Rollback()
				return fmt.Errorf("catalog: upsert %q: %w", a.ID, err)
			}
		}
		if err := stmt.Close(); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("catalog: commit upsert: %w", err)
		}
	}
	return nil
}

// UpsertImportedMany 更新凭证与身份字段，同时保留已有账号的运行健康和人工管理状态。
func (c *Catalog) UpsertImportedMany(accounts []Account) error {
	if c.db == nil {
		return ErrClosed
	}
	if len(accounts) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Unix()
	const q = `
	INSERT INTO accounts (
	  id, revision, identity_key, email, name, priority, enabled, manual_disabled,
	  lifecycle, access_token, refresh_token, expires_at, proxy_mode, proxy_url,
	  failure_count, success_count, cooldown_until, last_error, last_used_at, last_success_at,
	  last_refresh_at, consecutive_unauthorized, quarantine_fp, purge_after,
	  billing_json, created_at, updated_at
	) VALUES (
	  ?, ?, ?, ?, ?, ?, ?, ?,
	  ?, ?, ?, ?, ?, ?,
	  ?, ?, ?, ?, ?, ?,
	  ?, ?, ?, ?,
	  ?, ?, ?
	)
	ON CONFLICT(id) DO UPDATE SET
	  revision=accounts.revision+1,
	  identity_key=COALESCE(excluded.identity_key, accounts.identity_key),
	  email=COALESCE(excluded.email, accounts.email),
	  name=COALESCE(excluded.name, accounts.name),
	  access_token=excluded.access_token,
	  refresh_token=excluded.refresh_token,
	  expires_at=excluded.expires_at,
	  proxy_mode=COALESCE(excluded.proxy_mode, accounts.proxy_mode),
	  proxy_url=COALESCE(excluded.proxy_url, accounts.proxy_url),
	  updated_at=excluded.updated_at
	`

	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("catalog: begin import upsert tx: %w", err)
	}
	stmt, err := tx.Prepare(q)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("catalog: prepare import upsert: %w", err)
	}
	for _, a := range accounts {
		if err := validateAccountForUpsert(a); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
		rev := a.Revision
		if rev <= 0 {
			rev = 1
		}
		lifecycle := a.Lifecycle
		if lifecycle == "" {
			lifecycle = LifecycleActive
		}
		created := a.CreatedAt
		if created == 0 {
			created = now
		}
		updated := a.UpdatedAt
		if updated == 0 {
			updated = now
		}
		_, err := stmt.Exec(
			a.ID, rev, nullStr(a.IdentityKey), nullStr(a.Email), nullStr(a.Name),
			a.Priority, boolToInt(a.Enabled), boolToInt(a.ManualDisabled),
			lifecycle, a.AccessToken, a.RefreshToken, a.ExpiresAt,
			nullStr(a.ProxyMode), nullStr(a.ProxyURL),
			a.FailureCount, a.SuccessCount, a.CooldownUntil, nullStr(a.LastError),
			nullInt64(a.LastUsedAt), nullInt64(a.LastSuccessAt), nullInt64(a.LastRefreshAt),
			a.ConsecutiveUnauthorized, nullStr(a.QuarantineFP), nullInt64(a.PurgeAfter),
			nullStr(a.BillingJSON), created, updated,
		)
		if err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return fmt.Errorf("catalog: import upsert %q: %w", a.ID, err)
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("catalog: commit import upsert: %w", err)
	}
	return nil
}

func validateAccountForUpsert(a Account) error {
	if a.ID == "" {
		return fmt.Errorf("%w: empty account id", ErrInvalidInput)
	}
	if a.AccessToken == "" {
		return fmt.Errorf("%w: empty access_token for %q", ErrInvalidInput, a.ID)
	}
	if a.RefreshToken == "" {
		return fmt.Errorf("%w: empty refresh_token for %q", ErrInvalidInput, a.ID)
	}
	return nil
}

// Get 按 id 返回完整账号行（含令牌）。
func (c *Catalog) Get(id string) (Account, error) {
	if c.db == nil {
		return Account{}, ErrClosed
	}
	if id == "" {
		return Account{}, fmt.Errorf("%w: empty id", ErrInvalidInput)
	}
	const q = `
SELECT id, revision, identity_key, email, name, priority, enabled, manual_disabled,
  lifecycle, access_token, refresh_token, expires_at, proxy_mode, proxy_url,
  failure_count, success_count, cooldown_until, last_error, last_used_at, last_success_at,
  last_refresh_at, consecutive_unauthorized, quarantine_fp, purge_after,
  billing_json, created_at, updated_at
FROM accounts WHERE id = ?`
	row := c.db.QueryRow(q, id)
	a, err := scanAccount(row)
	if err == sql.ErrNoRows {
		return Account{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Account{}, fmt.Errorf("catalog: get %q: %w", id, err)
	}
	return a, nil
}

// UpdateTokens 以 CAS 方式更新令牌：仅当当前 revision == expectedRev 时成功。
// 成功后 revision 变为 expectedRev+1。
func (c *Catalog) UpdateTokens(id string, expectedRev int64, tokens TokenSet) error {
	if c.db == nil {
		return ErrClosed
	}
	if id == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidInput)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return fmt.Errorf("%w: tokens must be non-empty", ErrInvalidInput)
	}
	if expectedRev < 1 {
		return fmt.Errorf("%w: expected revision must be >= 1", ErrInvalidInput)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Unix()
	const q = `
UPDATE accounts SET
  access_token = ?,
  refresh_token = ?,
  expires_at = ?,
  revision = ?,
  last_refresh_at = ?,
  updated_at = ?
WHERE id = ? AND revision = ?`
	res, err := c.db.Exec(q,
		tokens.AccessToken, tokens.RefreshToken, tokens.ExpiresAt,
		expectedRev+1, now, now, id, expectedRev,
	)
	if err != nil {
		return fmt.Errorf("catalog: update tokens %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	// 区分不存在与 CAS 冲突。
	var exists int
	err = c.db.QueryRow(`SELECT 1 FROM accounts WHERE id = ?`, id).Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("catalog: update tokens check %q: %w", id, err)
	}
	return fmt.Errorf("%w: id=%s expected_rev=%d", ErrCASConflict, id, expectedRev)
}

// BatchSetManualEnabled 批量手动启停：单事务一次 UPDATE … WHERE id IN (…)
// 比逐条 PatchHealth（每条读-改-写）快一个数量级，专供管理台批量操作。
// 返回实际命中的 id 列表（不存在的 id 不会出现在 ok 中）。
func (c *Catalog) BatchSetManualEnabled(ids []string, enable bool) (okIDs []string, err error) {
	if c.db == nil {
		return nil, ErrClosed
	}
	// 规范化
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
		return nil, fmt.Errorf("%w: empty ids", ErrInvalidInput)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	tx, err := c.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("catalog: begin batch enable: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// SQLite 变量上限约 999；分块更新
	const chunk = 400
	now := time.Now().Unix()
	en := boolToInt(enable)
	manual := boolToInt(!enable)
	okIDs = make([]string, 0, len(clean))
	for i := 0; i < len(clean); i += chunk {
		j := i + chunk
		if j > len(clean) {
			j = len(clean)
		}
		part := clean[i:j]
		ph := make([]string, len(part))
		args := make([]any, 0, 4+len(part))
		args = append(args, en, manual, now)
		for k, id := range part {
			ph[k] = "?"
			args = append(args, id)
		}
		q := `UPDATE accounts SET enabled = ?, manual_disabled = ?, revision = revision + 1, updated_at = ?
WHERE id IN (` + strings.Join(ph, ",") + `)`
		if _, err := tx.Exec(q, args...); err != nil {
			return nil, fmt.Errorf("catalog: batch set enabled: %w", err)
		}
		// 查出实际存在的 id
		sel := `SELECT id FROM accounts WHERE id IN (` + strings.Join(ph, ",") + `)`
		selArgs := make([]any, len(part))
		for k, id := range part {
			selArgs[k] = id
		}
		rows, err := tx.Query(sel, selArgs...)
		if err != nil {
			return nil, fmt.Errorf("catalog: batch select: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			okIDs = append(okIDs, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("catalog: commit batch enable: %w", err)
	}
	return okIDs, nil
}

// BatchDelete 物理删除账号（单事务），返回实际删除的 id。
func (c *Catalog) BatchDelete(ids []string) (deleted []string, err error) {
	if c.db == nil {
		return nil, ErrClosed
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
		return nil, fmt.Errorf("%w: empty ids", ErrInvalidInput)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	tx, err := c.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("catalog: begin batch delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const chunk = 400
	deleted = make([]string, 0, len(clean))
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
		// 先记下存在的
		sel := `SELECT id FROM accounts WHERE id IN (` + strings.Join(ph, ",") + `)`
		rows, err := tx.Query(sel, args...)
		if err != nil {
			return nil, fmt.Errorf("catalog: batch delete select: %w", err)
		}
		var hit []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			hit = append(hit, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(hit) == 0 {
			continue
		}
		del := `DELETE FROM accounts WHERE id IN (` + strings.Join(ph, ",") + `)`
		if _, err := tx.Exec(del, args...); err != nil {
			return nil, fmt.Errorf("catalog: batch delete: %w", err)
		}
		deleted = append(deleted, hit...)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("catalog: commit batch delete: %w", err)
	}
	return deleted, nil
}

// PatchHealth 应用部分健康更新，并将 revision 加 1。
func (c *Catalog) PatchHealth(id string, patch HealthPatch) error {
	if c.db == nil {
		return ErrClosed
	}
	if id == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidInput)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 先读出当前行再合并补丁（稀疏补丁简单且正确）。
	cur, err := c.getUnlocked(id)
	if err != nil {
		return err
	}
	if patch.Enabled != nil {
		cur.Enabled = *patch.Enabled
	}
	if patch.ManualDisabled != nil {
		cur.ManualDisabled = *patch.ManualDisabled
	}
	if patch.Lifecycle != nil {
		if *patch.Lifecycle == "" {
			return fmt.Errorf("%w: empty lifecycle", ErrInvalidInput)
		}
		cur.Lifecycle = *patch.Lifecycle
	}
	if patch.FailureCount != nil {
		cur.FailureCount = *patch.FailureCount
	}
	if patch.SuccessCount != nil {
		cur.SuccessCount = *patch.SuccessCount
	}
	if patch.CooldownUntil != nil {
		cur.CooldownUntil = *patch.CooldownUntil
	}
	if patch.ClearLastError {
		cur.LastError = ""
	} else if patch.LastError != nil {
		cur.LastError = *patch.LastError
	}
	if patch.LastUsedAt != nil {
		cur.LastUsedAt = patch.LastUsedAt
	}
	if patch.LastSuccessAt != nil {
		cur.LastSuccessAt = patch.LastSuccessAt
	}
	if patch.LastRefreshAt != nil {
		cur.LastRefreshAt = patch.LastRefreshAt
	}
	if patch.ConsecutiveUnauthorized != nil {
		cur.ConsecutiveUnauthorized = *patch.ConsecutiveUnauthorized
	}
	if patch.QuarantineFP != nil {
		cur.QuarantineFP = *patch.QuarantineFP
	}
	if patch.PurgeAfter != nil {
		cur.PurgeAfter = patch.PurgeAfter
	}
	if patch.BillingJSON != nil {
		cur.BillingJSON = *patch.BillingJSON
	}

	now := time.Now().Unix()
	const q = `
UPDATE accounts SET
  enabled = ?,
  manual_disabled = ?,
  lifecycle = ?,
  failure_count = ?,
  success_count = ?,
  cooldown_until = ?,
  last_error = ?,
  last_used_at = ?,
  last_success_at = ?,
  last_refresh_at = ?,
  consecutive_unauthorized = ?,
  quarantine_fp = ?,
  purge_after = ?,
  billing_json = ?,
  revision = revision + 1,
  updated_at = ?
WHERE id = ?`
	_, err = c.db.Exec(q,
		boolToInt(cur.Enabled), boolToInt(cur.ManualDisabled), cur.Lifecycle,
		cur.FailureCount, cur.SuccessCount, cur.CooldownUntil, nullStr(cur.LastError),
		nullInt64(cur.LastUsedAt), nullInt64(cur.LastSuccessAt), nullInt64(cur.LastRefreshAt),
		cur.ConsecutiveUnauthorized, nullStr(cur.QuarantineFP), nullInt64(cur.PurgeAfter),
		nullStr(cur.BillingJSON), now, id,
	)
	if err != nil {
		return fmt.Errorf("catalog: patch health %q: %w", id, err)
	}
	return nil
}

func (c *Catalog) getUnlocked(id string) (Account, error) {
	const q = `
SELECT id, revision, identity_key, email, name, priority, enabled, manual_disabled,
  lifecycle, access_token, refresh_token, expires_at, proxy_mode, proxy_url,
  failure_count, success_count, cooldown_until, last_error, last_used_at, last_success_at,
  last_refresh_at, consecutive_unauthorized, quarantine_fp, purge_after,
  billing_json, created_at, updated_at
FROM accounts WHERE id = ?`
	a, err := scanAccount(c.db.QueryRow(q, id))
	if err == sql.ErrNoRows {
		return Account{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Account{}, err
	}
	return a, nil
}

// ListEligible 返回最多 limit 条当前可晋升热池的无密钥 HotMeta：
// enabled=1、lifecycle=active、cooldown_until <= now。
// 按 priority DESC、id ASC 排序。afterID 用于同优先级键集分页（首页传 ""）。
func (c *Catalog) ListEligible(limit int, afterID string) ([]HotMeta, error) {
	if c.db == nil {
		return nil, ErrClosed
	}
	if limit <= 0 {
		return nil, fmt.Errorf("%w: limit must be > 0", ErrInvalidInput)
	}
	now := time.Now().Unix()

	var (
		rows *sql.Rows
		err  error
	)
	// 键集分页：提供 afterID 时以其 (priority, id) 为起点。
	// probe_ok=false 不进热池；未测活或 probe_ok=true/缺字段仍可进。
	if afterID == "" {
		q := `
SELECT id, revision, identity_key, priority, enabled, lifecycle,
  cooldown_until, expires_at, failure_count, proxy_mode, proxy_url
FROM accounts
WHERE enabled = 1
  AND manual_disabled = 0
  AND lifecycle = ?
  AND cooldown_until <= ?
  AND (access_token IS NOT NULL AND access_token != '')
  AND (billing_json IS NULL OR billing_json = ''
    OR json_extract(billing_json, '$.probe_ok') IS NULL
    OR json_extract(billing_json, '$.probe_ok') = 1)
ORDER BY priority DESC, id ASC
LIMIT ?`
		rows, err = c.db.Query(q, LifecycleActive, now, limit)
	} else {
		q := `
SELECT a.id, a.revision, a.identity_key, a.priority, a.enabled, a.lifecycle,
  a.cooldown_until, a.expires_at, a.failure_count, a.proxy_mode, a.proxy_url
FROM accounts a
JOIN accounts cur ON cur.id = ?
WHERE a.enabled = 1
  AND a.manual_disabled = 0
  AND a.lifecycle = ?
  AND a.cooldown_until <= ?
  AND (a.access_token IS NOT NULL AND a.access_token != '')
  AND (a.billing_json IS NULL OR a.billing_json = ''
    OR json_extract(a.billing_json, '$.probe_ok') IS NULL
    OR json_extract(a.billing_json, '$.probe_ok') = 1)
  AND (
    a.priority < cur.priority
    OR (a.priority = cur.priority AND a.id > cur.id)
  )
ORDER BY a.priority DESC, a.id ASC
LIMIT ?`
		rows, err = c.db.Query(q, afterID, LifecycleActive, now, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: list eligible: %w", err)
	}
	defer rows.Close()

	out := make([]HotMeta, 0, limit)
	for rows.Next() {
		var (
			m            HotMeta
			enabled      int
			failureCount int
			identityKey  sql.NullString
			proxyMode    sql.NullString
			proxyURL     sql.NullString
		)
		if err := rows.Scan(
			&m.ID, &m.Revision, &identityKey, &m.Priority, &enabled, &m.Lifecycle,
			&m.CooldownUntil, &m.ExpiresAt, &failureCount, &proxyMode, &proxyURL,
		); err != nil {
			return nil, fmt.Errorf("catalog: list eligible scan: %w", err)
		}
		m.Enabled = enabled != 0
		m.IdentityKey = identityKey.String
		m.ProxyMode = proxyMode.String
		m.ProxyURL = proxyURL.String
		m.Inflight = 0
		// 热层失败分投影（暂用 failure_count）；后续可细化。
		m.FailureScore = float32(failureCount)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListExpiring 返回最多 limit 条完整账号行（含令牌），条件为
// expires_at < beforeUnix、enabled=1、lifecycle=active。
// 按 expires_at ASC、id ASC，优先返回即将过期的账号。
// 供 refresh worker 使用；调用方不得记录令牌。
func (c *Catalog) ListExpiring(limit int, beforeUnix int64) ([]Account, error) {
	if c.db == nil {
		return nil, ErrClosed
	}
	if limit <= 0 {
		return nil, fmt.Errorf("%w: limit must be > 0", ErrInvalidInput)
	}
	const q = `
SELECT id, revision, identity_key, email, name, priority, enabled, manual_disabled,
  lifecycle, access_token, refresh_token, expires_at, proxy_mode, proxy_url,
  failure_count, success_count, cooldown_until, last_error, last_used_at, last_success_at,
  last_refresh_at, consecutive_unauthorized, quarantine_fp, purge_after,
  billing_json, created_at, updated_at
FROM accounts
WHERE enabled = 1
  AND lifecycle = ?
  AND expires_at < ?
ORDER BY expires_at ASC, id ASC
LIMIT ?`
	rows, err := c.db.Query(q, LifecycleActive, beforeUnix, limit)
	if err != nil {
		return nil, fmt.Errorf("catalog: list expiring: %w", err)
	}
	defer rows.Close()

	out := make([]Account, 0, limit)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog: list expiring scan: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SetProxy 更新单账号 ProxyURL/ProxyMode，revision+1。
// proxyMode 空则保留原 mode；proxyURL 空串表示直连（清空）。
func (c *Catalog) SetProxy(id, proxyURL, proxyMode string) error {
	return c.SetProxies([]ProxyAssignment{{ID: id, ProxyURL: proxyURL, ProxyMode: proxyMode}})
}

// SetProxies 批量更新账号代理（同一事务，每行 revision+1）。
// 任一条 id 不存在则整批回滚并返回 ErrNotFound。
func (c *Catalog) SetProxies(items []ProxyAssignment) error {
	if c.db == nil {
		return ErrClosed
	}
	if len(items) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("catalog: begin set proxies: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	const qKeepMode = `
UPDATE accounts SET
  proxy_url = ?,
  revision = revision + 1,
  updated_at = ?
WHERE id = ?`
	const qWithMode = `
UPDATE accounts SET
  proxy_url = ?,
  proxy_mode = ?,
  revision = revision + 1,
  updated_at = ?
WHERE id = ?`

	for _, it := range items {
		id := strings.TrimSpace(it.ID)
		if id == "" {
			return fmt.Errorf("%w: empty id in SetProxies", ErrInvalidInput)
		}
		proxyURL := strings.TrimSpace(it.ProxyURL)
		proxyMode := strings.TrimSpace(it.ProxyMode)
		var res sql.Result
		var err error
		if proxyMode == "" {
			// 仅改 URL；空串写入 NULL 表示直连
			res, err = tx.Exec(qKeepMode, nullStr(proxyURL), now, id)
		} else {
			res, err = tx.Exec(qWithMode, nullStr(proxyURL), proxyMode, now, id)
		}
		if err != nil {
			return fmt.Errorf("catalog: set proxy %q: %w", id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("catalog: commit set proxies: %w", err)
	}
	return nil
}

// ListAccounts 按 id 升序游标分页返回脱敏账号摘要。
// 不 SELECT access_token/refresh_token 全文，仅用表达式生成 has_access/has_refresh 布尔。
// AccountListFilter 账号列表筛选（管理台）。
type AccountListFilter struct {
	// Status: ""|alive|dead|enabled|disabled|cooldown|quarantine|no_token
	Status string
	// Lifecycle exact: active|quarantined|purged（可空）
	Lifecycle string
	// Query 匹配 id/email/name 子串（大小写不敏感）
	Query string
}

// ListAccounts 按 id 升序游标分页返回脱敏账号摘要。
// filter 可选；afterID 为上一页最后 id（首页空）。
func (c *Catalog) ListAccounts(limit int, afterID string, filter AccountListFilter) ([]AccountSummary, error) {
	if c.db == nil {
		return nil, ErrClosed
	}
	if limit <= 0 {
		return nil, fmt.Errorf("%w: limit must be > 0", ErrInvalidInput)
	}
	now := time.Now().Unix()
	where := []string{"(? = '' OR id > ?)"}
	args := []any{afterID, afterID}

	st := strings.ToLower(strings.TrimSpace(filter.Status))
	switch st {
	case "", "all":
	case "alive":
		// 基础可用，且（未测活 或 probe_ok=true）
		where = append(where, "enabled = 1", "manual_disabled = 0", "lifecycle = ?", "cooldown_until <= ?",
			"(access_token IS NOT NULL AND access_token != '' OR refresh_token IS NOT NULL AND refresh_token != '')",
			`(billing_json IS NULL OR billing_json = ''
			  OR json_extract(billing_json, '$.probe_ok') IS NULL
			  OR json_extract(billing_json, '$.probe_ok') = 1)`)
		args = append(args, LifecycleActive, now)
	case "dead":
		// 非存活：基础不可用，或已测活失败
		where = append(where, `(enabled = 0 OR manual_disabled = 1 OR lifecycle != ? OR cooldown_until > ?
			OR ((access_token IS NULL OR access_token = '') AND (refresh_token IS NULL OR refresh_token = ''))
			OR json_extract(billing_json, '$.probe_ok') = 0)`)
		args = append(args, LifecycleActive, now)
	case "probe_ok":
		where = append(where, "json_extract(billing_json, '$.probe_ok') = 1")
	case "probe_fail", "probe_failed":
		where = append(where, "json_extract(billing_json, '$.probe_ok') = 0")
	case "unprobed":
		where = append(where, `(billing_json IS NULL OR billing_json = ''
			OR json_extract(billing_json, '$.probe_ok') IS NULL)`)
	case "enabled":
		where = append(where, "enabled = 1")
	case "disabled":
		where = append(where, "enabled = 0")
	case "cooldown":
		where = append(where, "cooldown_until > ?")
		args = append(args, now)
	case "quarantine", "quarantined":
		where = append(where, "lifecycle = ?")
		args = append(args, LifecycleQuarantined)
	case "no_token":
		where = append(where, "((access_token IS NULL OR access_token = '') AND (refresh_token IS NULL OR refresh_token = ''))")
	default:
		return nil, fmt.Errorf("%w: unknown status filter %q", ErrInvalidInput, filter.Status)
	}

	if lc := strings.TrimSpace(filter.Lifecycle); lc != "" {
		where = append(where, "lifecycle = ?")
		args = append(args, lc)
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		where = append(where, "(LOWER(id) LIKE ? OR LOWER(COALESCE(email,'')) LIKE ? OR LOWER(COALESCE(name,'')) LIKE ?)")
		args = append(args, like, like, like)
	}

	args = append(args, limit)
	sqlStr := `
SELECT
  id,
  COALESCE(email, ''),
  COALESCE(name, ''),
  lifecycle,
  COALESCE(proxy_mode, ''),
  COALESCE(proxy_url, ''),
  priority,
  enabled,
  manual_disabled,
  expires_at,
  cooldown_until,
  failure_count,
  success_count,
  last_success_at,
  revision,
  (access_token IS NOT NULL AND access_token != '') AS has_access,
  (refresh_token IS NOT NULL AND refresh_token != '') AS has_refresh,
  COALESCE(last_error, ''),
  last_used_at,
  COALESCE(billing_json, '')
FROM accounts
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY id ASC
LIMIT ?`

	rows, err := c.db.Query(sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: list accounts: %w", err)
	}
	defer rows.Close()

	out := make([]AccountSummary, 0, limit)
	for rows.Next() {
		var (
			s           AccountSummary
			enabled     int
			manual      int
			hasAcc      int
			hasRef      int
			lastUsed    sql.NullInt64
			lastSuccess sql.NullInt64
			billingJSON string
		)
		if err := rows.Scan(
			&s.ID, &s.Email, &s.Name, &s.Lifecycle, &s.ProxyMode, &s.ProxyURL,
			&s.Priority, &enabled, &manual, &s.ExpiresAt, &s.CooldownUntil,
			&s.FailureCount, &s.SuccessCount, &lastSuccess, &s.Revision, &hasAcc, &hasRef, &s.LastError, &lastUsed,
			&billingJSON,
		); err != nil {
			return nil, fmt.Errorf("catalog: list accounts scan: %w", err)
		}
		s.Enabled = enabled != 0
		s.ManualDisabled = manual != 0
		s.HasAccess = hasAcc != 0
		s.HasRefresh = hasRef != 0
		s.LastUsedAt = nullInt64Ptr(lastUsed)
		s.LastSuccessAt = nullInt64Ptr(lastSuccess)
		// billing / probe 先解析，alive 可参考 probe_ok
		s.Billing = ParseAccountBillingView(billingJSON)
		// alive：基础可用 + 若已测活则必须 probe_ok
		baseAlive := s.Enabled && !s.ManualDisabled && s.Lifecycle == LifecycleActive &&
			s.CooldownUntil <= now && (s.HasAccess || s.HasRefresh)
		if !baseAlive {
			s.Alive = false
		} else if s.Billing != nil && s.Billing.ProbeOK != nil {
			s.Alive = *s.Billing.ProbeOK
		} else {
			s.Alive = true // 未测活：按冷存储启发式
		}
		total := s.SuccessCount + s.FailureCount
		if total > 0 {
			rate := float64(s.SuccessCount) / float64(total)
			s.SuccessRate = &rate
		}
		if s.CooldownUntil > now {
			s.CooldownRemainingSec = s.CooldownUntil - now
		}
		s.StatusReason = accountStatusReason(s, now)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CountAccountsFiltered 按与 ListAccounts 相同筛选条件计数。
func (c *Catalog) CountAccountsFiltered(filter AccountListFilter) (int, error) {
	if c.db == nil {
		return 0, ErrClosed
	}
	now := time.Now().Unix()
	where := []string{"1=1"}
	args := []any{}
	st := strings.ToLower(strings.TrimSpace(filter.Status))
	switch st {
	case "", "all":
	case "alive":
		where = append(where, "enabled = 1", "manual_disabled = 0", "lifecycle = ?", "cooldown_until <= ?",
			"(access_token IS NOT NULL AND access_token != '' OR refresh_token IS NOT NULL AND refresh_token != '')",
			`(billing_json IS NULL OR billing_json = ''
			  OR json_extract(billing_json, '$.probe_ok') IS NULL
			  OR json_extract(billing_json, '$.probe_ok') = 1)`)
		args = append(args, LifecycleActive, now)
	case "dead":
		where = append(where, `(enabled = 0 OR manual_disabled = 1 OR lifecycle != ? OR cooldown_until > ?
			OR ((access_token IS NULL OR access_token = '') AND (refresh_token IS NULL OR refresh_token = ''))
			OR json_extract(billing_json, '$.probe_ok') = 0)`)
		args = append(args, LifecycleActive, now)
	case "probe_ok":
		where = append(where, "json_extract(billing_json, '$.probe_ok') = 1")
	case "probe_fail", "probe_failed":
		where = append(where, "json_extract(billing_json, '$.probe_ok') = 0")
	case "unprobed":
		where = append(where, `(billing_json IS NULL OR billing_json = ''
			OR json_extract(billing_json, '$.probe_ok') IS NULL)`)
	case "enabled":
		where = append(where, "enabled = 1")
	case "disabled":
		where = append(where, "enabled = 0")
	case "cooldown":
		where = append(where, "cooldown_until > ?")
		args = append(args, now)
	case "quarantine", "quarantined":
		where = append(where, "lifecycle = ?")
		args = append(args, LifecycleQuarantined)
	case "no_token":
		where = append(where, "((access_token IS NULL OR access_token = '') AND (refresh_token IS NULL OR refresh_token = ''))")
	default:
		return 0, fmt.Errorf("%w: unknown status filter %q", ErrInvalidInput, filter.Status)
	}
	if lc := strings.TrimSpace(filter.Lifecycle); lc != "" {
		where = append(where, "lifecycle = ?")
		args = append(args, lc)
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		where = append(where, "(LOWER(id) LIKE ? OR LOWER(COALESCE(email,'')) LIKE ? OR LOWER(COALESCE(name,'')) LIKE ?)")
		args = append(args, like, like, like)
	}
	var n int
	err := c.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE `+strings.Join(where, " AND "), args...).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// Stats 返回冷存储的聚合计数。
func (c *Catalog) Stats() (CatalogStats, error) {
	if c.db == nil {
		return CatalogStats{}, ErrClosed
	}
	now := time.Now().Unix()
	var s CatalogStats
	err := c.db.QueryRow(`
SELECT
  COUNT(*),
  COALESCE(SUM(CASE WHEN enabled = 1 THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN enabled = 1 AND lifecycle = ? THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN cooldown_until > ? THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN lifecycle = ? THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN enabled = 0 THEN 1 ELSE 0 END), 0)
FROM accounts`, LifecycleActive, now, LifecycleQuarantined).Scan(
		&s.Count, &s.EnabledCount, &s.ActiveCount, &s.CooldownCount, &s.QuarantineCount, &s.DisabledCount,
	)
	if err != nil {
		return CatalogStats{}, fmt.Errorf("catalog: stats: %w", err)
	}
	return s, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanAccount(row scannable) (Account, error) {
	var (
		a            Account
		identityKey  sql.NullString
		email        sql.NullString
		name         sql.NullString
		enabled      int
		manual       int
		proxyMode    sql.NullString
		proxyURL     sql.NullString
		lastError    sql.NullString
		lastUsed     sql.NullInt64
		lastSuccess  sql.NullInt64
		lastRefresh  sql.NullInt64
		quarantineFP sql.NullString
		purgeAfter   sql.NullInt64
		billingJSON  sql.NullString
	)
	err := row.Scan(
		&a.ID, &a.Revision, &identityKey, &email, &name, &a.Priority, &enabled, &manual,
		&a.Lifecycle, &a.AccessToken, &a.RefreshToken, &a.ExpiresAt, &proxyMode, &proxyURL,
		&a.FailureCount, &a.SuccessCount, &a.CooldownUntil, &lastError, &lastUsed, &lastSuccess,
		&lastRefresh, &a.ConsecutiveUnauthorized, &quarantineFP, &purgeAfter,
		&billingJSON, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return Account{}, err
	}
	a.IdentityKey = identityKey.String
	a.Email = email.String
	a.Name = name.String
	a.Enabled = enabled != 0
	a.ManualDisabled = manual != 0
	a.ProxyMode = proxyMode.String
	a.ProxyURL = proxyURL.String
	a.LastError = lastError.String
	a.LastUsedAt = nullInt64Ptr(lastUsed)
	a.LastSuccessAt = nullInt64Ptr(lastSuccess)
	a.LastRefreshAt = nullInt64Ptr(lastRefresh)
	a.QuarantineFP = quarantineFP.String
	a.PurgeAfter = nullInt64Ptr(purgeAfter)
	a.BillingJSON = billingJSON.String
	return a, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullInt64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func accountStatusReason(s AccountSummary, now int64) string {
	if s.ManualDisabled {
		return "手动禁用"
	}
	if !s.Enabled {
		return "已禁用"
	}
	switch s.Lifecycle {
	case LifecycleQuarantined:
		if s.LastError != "" {
			return "隔离: " + s.LastError
		}
		return "隔离"
	case LifecyclePurged:
		return "已清理"
	}
	if s.CooldownUntil > now {
		left := s.CooldownUntil - now
		msg := "冷却中"
		if s.LastError != "" {
			msg += " (" + s.LastError + ")"
		}
		msg += "; 剩余约 "
		switch {
		case left < 60:
			msg += fmt.Sprintf("%ds", left)
		case left < 3600:
			msg += fmt.Sprintf("%dm", (left+59)/60)
		default:
			msg += fmt.Sprintf("%dh", (left+3599)/3600)
		}
		return msg
	}
	if !s.HasAccess && !s.HasRefresh {
		return "无令牌"
	}
	if s.Billing != nil && s.Billing.ProbeOK != nil && !*s.Billing.ProbeOK {
		if s.Billing.ProbeError != "" {
			return "测活失败: " + s.Billing.ProbeError
		}
		return "测活失败"
	}
	if s.LastError != "" && !s.Alive {
		return s.LastError
	}
	if s.Alive {
		return "可用"
	}
	return "不可用"
}

// ModelCooldown 账号-模型冷却行。
type ModelCooldown struct {
	AccountID     string `json:"account_id"`
	Model         string `json:"model"`
	CooldownUntil int64  `json:"cooldown_until"`
	LastError     string `json:"last_error,omitempty"`
	UpdatedAt     int64  `json:"updated_at"`
	RemainingSec  int64  `json:"remaining_sec,omitempty"`
}

// UpsertModelCooldown 写入/更新模型冷却。
func (c *Catalog) UpsertModelCooldown(accountID, model string, until int64, lastErr string) error {
	if c == nil || c.db == nil {
		return ErrClosed
	}
	accountID = strings.TrimSpace(accountID)
	model = strings.TrimSpace(model)
	if accountID == "" || model == "" || until <= 0 {
		return fmt.Errorf("%w: model cooldown args", ErrInvalidInput)
	}
	now := time.Now().Unix()
	_, err := c.db.Exec(`
INSERT INTO model_cooldowns(account_id, model, cooldown_until, last_error, updated_at)
VALUES(?,?,?,?,?)
ON CONFLICT(account_id, model) DO UPDATE SET
  cooldown_until=excluded.cooldown_until,
  last_error=excluded.last_error,
  updated_at=excluded.updated_at
`, accountID, model, until, lastErr, now)
	if err != nil {
		return fmt.Errorf("catalog: upsert model cooldown: %w", err)
	}
	return nil
}

// ClearModelCooldown 删除一条模型冷却。
func (c *Catalog) ClearModelCooldown(accountID, model string) error {
	if c == nil || c.db == nil {
		return ErrClosed
	}
	_, err := c.db.Exec(`DELETE FROM model_cooldowns WHERE account_id=? AND model=?`, accountID, model)
	if err != nil {
		return fmt.Errorf("catalog: clear model cooldown: %w", err)
	}
	return nil
}

// ListModelCooldowns 列出账号当前未过期模型冷却；accountID 空则全表活跃冷却。
func (c *Catalog) ListModelCooldowns(accountID string, now int64) ([]ModelCooldown, error) {
	if c == nil || c.db == nil {
		return nil, ErrClosed
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	var (
		rows *sql.Rows
		err  error
	)
	if strings.TrimSpace(accountID) == "" {
		rows, err = c.db.Query(`
SELECT account_id, model, cooldown_until, COALESCE(last_error,''), updated_at
FROM model_cooldowns
WHERE cooldown_until > ?
ORDER BY cooldown_until DESC, account_id, model
LIMIT 5000`, now)
	} else {
		rows, err = c.db.Query(`
SELECT account_id, model, cooldown_until, COALESCE(last_error,''), updated_at
FROM model_cooldowns
WHERE account_id=? AND cooldown_until > ?
ORDER BY cooldown_until DESC, model
LIMIT 500`, accountID, now)
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: list model cooldowns: %w", err)
	}
	defer rows.Close()
	out := make([]ModelCooldown, 0, 16)
	for rows.Next() {
		var m ModelCooldown
		if err := rows.Scan(&m.AccountID, &m.Model, &m.CooldownUntil, &m.LastError, &m.UpdatedAt); err != nil {
			return nil, err
		}
		if m.CooldownUntil > now {
			m.RemainingSec = m.CooldownUntil - now
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// LoadActiveModelCooldowns 返回 map[account]map[model]until，供 lease 启动装载。
func (c *Catalog) LoadActiveModelCooldowns(now int64) (map[string]map[string]int64, error) {
	rows, err := c.ListModelCooldowns("", now)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]int64)
	for _, r := range rows {
		mm := out[r.AccountID]
		if mm == nil {
			mm = make(map[string]int64)
			out[r.AccountID] = mm
		}
		mm[r.Model] = r.CooldownUntil
	}
	return out, nil
}

// PurgeExpiredModelCooldowns 清理过期行。
func (c *Catalog) PurgeExpiredModelCooldowns(now int64) (int64, error) {
	if c == nil || c.db == nil {
		return 0, ErrClosed
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	res, err := c.db.Exec(`DELETE FROM model_cooldowns WHERE cooldown_until <= ?`, now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
