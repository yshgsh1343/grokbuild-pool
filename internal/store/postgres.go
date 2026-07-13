package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
)

// PostgresAccountStore is the Scheme2 cold store for large account pools.
type PostgresAccountStore struct {
	pool *pgxpool.Pool
}

func OpenPostgres(ctx context.Context, databaseURL string) (*PostgresAccountStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("%w: empty database url", ErrInvalid)
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parse database url: %w", err)
	}
	if cfg.MaxConns == 0 {
		cfg.MaxConns = 16
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: open postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping postgres: %w", err)
	}
	return &PostgresAccountStore{pool: pool}, nil
}

func (s *PostgresAccountStore) Close() error {
	if s == nil || s.pool == nil {
		return nil
	}
	s.pool.Close()
	return nil
}

func (s *PostgresAccountStore) Get(ctx context.Context, id string) (catalog.Account, error) {
	if id == "" {
		return catalog.Account{}, ErrInvalid
	}
	const q = `
SELECT account_id, revision, COALESCE(identity_key,''), COALESCE(email,''), COALESCE(name,''),
  priority, enabled, manual_disabled, COALESCE(lifecycle,'active'),
  access_token, refresh_token,
  COALESCE(EXTRACT(EPOCH FROM token_expires_at)::bigint,0),
  COALESCE(proxy_mode,''), COALESCE(proxy_url,''),
  failure_count, success_count,
  COALESCE(EXTRACT(EPOCH FROM cooldown_until)::bigint,0),
  COALESCE(last_error,''),
  EXTRACT(EPOCH FROM last_used_at)::bigint,
  EXTRACT(EPOCH FROM last_success_at)::bigint,
  EXTRACT(EPOCH FROM last_refresh_at)::bigint,
  consecutive_unauthorized, COALESCE(quarantine_fp,''),
  EXTRACT(EPOCH FROM purge_after)::bigint,
  COALESCE(billing_json::text, '{}'),
  COALESCE(EXTRACT(EPOCH FROM created_at)::bigint,0),
  COALESCE(EXTRACT(EPOCH FROM updated_at)::bigint,0)
FROM accounts WHERE account_id=$1`
	row := s.pool.QueryRow(ctx, q, id)
	a, err := scanAccountRow(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return catalog.Account{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return catalog.Account{}, err
	}
	return a, nil
}

func (s *PostgresAccountStore) UpsertMany(ctx context.Context, accounts []catalog.Account) error {
	return s.upsert(ctx, accounts)
}

func (s *PostgresAccountStore) UpsertImportedMany(ctx context.Context, accounts []catalog.Account) error {
	return s.upsert(ctx, accounts)
}

func (s *PostgresAccountStore) upsert(ctx context.Context, accounts []catalog.Account) error {
	if len(accounts) == 0 {
		return nil
	}
	const q = `
INSERT INTO accounts (
  account_id, revision, identity_key, email, name, priority, enabled, manual_disabled,
  lifecycle, access_token, refresh_token, token_expires_at, proxy_mode, proxy_url,
  failure_count, success_count, cooldown_until, last_error, consecutive_unauthorized,
  quarantine_fp, billing_json, shard_hint, created_at, updated_at
) VALUES (
  $1, GREATEST(1,$2), $3, $4, $5, $6, $7, $8,
  $9, $10, $11, to_timestamp(NULLIF($12,0)), $13, $14,
  $15, $16, to_timestamp(NULLIF($17,0)), $18, $19,
  $20, COALESCE($21::jsonb, '{}'::jsonb), $22, now(), now()
)
ON CONFLICT (account_id) DO UPDATE SET
  revision = accounts.revision + 1,
  identity_key = EXCLUDED.identity_key,
  email = EXCLUDED.email,
  name = EXCLUDED.name,
  priority = EXCLUDED.priority,
  enabled = EXCLUDED.enabled,
  manual_disabled = EXCLUDED.manual_disabled,
  lifecycle = EXCLUDED.lifecycle,
  access_token = EXCLUDED.access_token,
  refresh_token = EXCLUDED.refresh_token,
  token_expires_at = EXCLUDED.token_expires_at,
  proxy_mode = EXCLUDED.proxy_mode,
  proxy_url = EXCLUDED.proxy_url,
  billing_json = EXCLUDED.billing_json,
  shard_hint = EXCLUDED.shard_hint,
  updated_at = now()`
	for _, a := range accounts {
		if a.ID == "" || a.AccessToken == "" {
			return fmt.Errorf("%w: incomplete account", ErrInvalid)
		}
		if a.Lifecycle == "" {
			a.Lifecycle = catalog.LifecycleActive
		}
		if a.Revision < 1 {
			a.Revision = 1
		}
		_, err := s.pool.Exec(ctx, q,
			a.ID, a.Revision, a.IdentityKey, a.Email, a.Name, a.Priority, a.Enabled, a.ManualDisabled,
			a.Lifecycle, a.AccessToken, a.RefreshToken, a.ExpiresAt, a.ProxyMode, a.ProxyURL,
			a.FailureCount, a.SuccessCount, a.CooldownUntil, a.LastError, a.ConsecutiveUnauthorized,
			a.QuarantineFP, nullJSON(a.BillingJSON), shardHint(a.ID, 64),
		)
		if err != nil {
			return fmt.Errorf("store: upsert %s: %w", a.ID, err)
		}
	}
	return nil
}

func (s *PostgresAccountStore) UpdateTokens(ctx context.Context, id string, expectedRev int64, tokens catalog.TokenSet) error {
	if id == "" || tokens.AccessToken == "" || tokens.RefreshToken == "" || expectedRev < 1 {
		return ErrInvalid
	}
	const q = `
UPDATE accounts SET
  access_token=$1,
  refresh_token=$2,
  token_expires_at=to_timestamp($3),
  revision=$4,
  last_refresh_at=now(),
  updated_at=now()
WHERE account_id=$5 AND revision=$6`
	tag, err := s.pool.Exec(ctx, q, tokens.AccessToken, tokens.RefreshToken, tokens.ExpiresAt, expectedRev+1, id, expectedRev)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists int
	err = s.pool.QueryRow(ctx, `SELECT 1 FROM accounts WHERE account_id=$1`, id).Scan(&exists)
	if err == pgx.ErrNoRows {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: id=%s expected_rev=%d", ErrCASConflict, id, expectedRev)
}

func (s *PostgresAccountStore) PatchHealth(ctx context.Context, id string, patch catalog.HealthPatch) error {
	if id == "" {
		return ErrInvalid
	}
	acc, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if patch.Enabled != nil {
		acc.Enabled = *patch.Enabled
	}
	if patch.ManualDisabled != nil {
		acc.ManualDisabled = *patch.ManualDisabled
	}
	if patch.Lifecycle != nil {
		acc.Lifecycle = *patch.Lifecycle
	}
	if patch.FailureCount != nil {
		acc.FailureCount = *patch.FailureCount
	}
	if patch.SuccessCount != nil {
		acc.SuccessCount = *patch.SuccessCount
	}
	if patch.CooldownUntil != nil {
		acc.CooldownUntil = *patch.CooldownUntil
	}
	if patch.LastError != nil {
		acc.LastError = *patch.LastError
	}
	if patch.ClearLastError {
		acc.LastError = ""
	}
	if patch.ConsecutiveUnauthorized != nil {
		acc.ConsecutiveUnauthorized = *patch.ConsecutiveUnauthorized
	}
	if patch.QuarantineFP != nil {
		acc.QuarantineFP = *patch.QuarantineFP
	}
	if patch.BillingJSON != nil {
		acc.BillingJSON = *patch.BillingJSON
	}
	const q = `
UPDATE accounts SET
  enabled=$1, manual_disabled=$2, lifecycle=$3,
  failure_count=$4, success_count=$5,
  cooldown_until=to_timestamp(NULLIF($6,0)),
  last_error=$7, consecutive_unauthorized=$8, quarantine_fp=$9,
  billing_json=COALESCE($10::jsonb,'{}'::jsonb),
  updated_at=now()
WHERE account_id=$11`
	_, err = s.pool.Exec(ctx, q,
		acc.Enabled, acc.ManualDisabled, acc.Lifecycle,
		acc.FailureCount, acc.SuccessCount, acc.CooldownUntil,
		acc.LastError, acc.ConsecutiveUnauthorized, acc.QuarantineFP,
		nullJSON(acc.BillingJSON), id,
	)
	return err
}

func (s *PostgresAccountStore) ListEligible(ctx context.Context, limit int, afterID string) ([]catalog.HotMeta, error) {
	if limit <= 0 {
		return nil, ErrInvalid
	}
	now := time.Now().Unix()
	var rows pgx.Rows
	var err error
	if afterID == "" {
		const q = `
SELECT account_id, revision, COALESCE(identity_key,''), priority, enabled, COALESCE(lifecycle,''),
  COALESCE(EXTRACT(EPOCH FROM cooldown_until)::bigint,0),
  COALESCE(EXTRACT(EPOCH FROM token_expires_at)::bigint,0),
  failure_count, COALESCE(proxy_mode,''), COALESCE(proxy_url,'')
FROM accounts
WHERE enabled=true AND manual_disabled=false AND lifecycle='active'
  AND (cooldown_until IS NULL OR EXTRACT(EPOCH FROM cooldown_until)::bigint <= $1)
  AND access_token <> ''
ORDER BY priority DESC, account_id ASC
LIMIT $2`
		rows, err = s.pool.Query(ctx, q, now, limit)
	} else {
		const q = `
SELECT a.account_id, a.revision, COALESCE(a.identity_key,''), a.priority, a.enabled, COALESCE(a.lifecycle,''),
  COALESCE(EXTRACT(EPOCH FROM a.cooldown_until)::bigint,0),
  COALESCE(EXTRACT(EPOCH FROM a.token_expires_at)::bigint,0),
  a.failure_count, COALESCE(a.proxy_mode,''), COALESCE(a.proxy_url,'')
FROM accounts a
JOIN accounts cur ON cur.account_id=$1
WHERE a.enabled=true AND a.manual_disabled=false AND a.lifecycle='active'
  AND (a.cooldown_until IS NULL OR EXTRACT(EPOCH FROM a.cooldown_until)::bigint <= $2)
  AND a.access_token <> ''
  AND (a.priority < cur.priority OR (a.priority = cur.priority AND a.account_id > cur.account_id))
ORDER BY a.priority DESC, a.account_id ASC
LIMIT $3`
		rows, err = s.pool.Query(ctx, q, afterID, now, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHotMetas(rows)
}

func (s *PostgresAccountStore) ListWorksetCandidates(ctx context.Context, limit int, now time.Time) ([]catalog.HotMeta, error) {
	_ = now
	return s.ListEligible(ctx, limit, "")
}

func (s *PostgresAccountStore) ListByShard(ctx context.Context, shardID, limit int, afterID string) ([]catalog.HotMeta, error) {
	if limit <= 0 || shardID < 0 {
		return nil, ErrInvalid
	}
	now := time.Now().Unix()
	const q = `
SELECT account_id, revision, COALESCE(identity_key,''), priority, enabled, COALESCE(lifecycle,''),
  COALESCE(EXTRACT(EPOCH FROM cooldown_until)::bigint,0),
  COALESCE(EXTRACT(EPOCH FROM token_expires_at)::bigint,0),
  failure_count, COALESCE(proxy_mode,''), COALESCE(proxy_url,'')
FROM accounts
WHERE shard_hint=$1
  AND enabled=true AND manual_disabled=false AND lifecycle='active'
  AND (cooldown_until IS NULL OR EXTRACT(EPOCH FROM cooldown_until)::bigint <= $2)
  AND access_token <> ''
  AND ($3 = '' OR account_id > $3)
ORDER BY priority DESC, account_id ASC
LIMIT $4`
	rows, err := s.pool.Query(ctx, q, shardID, now, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHotMetas(rows)
}

func (s *PostgresAccountStore) ListExpiring(ctx context.Context, limit int, beforeUnix int64) ([]catalog.Account, error) {
	if limit <= 0 {
		return nil, ErrInvalid
	}
	const q = `
SELECT account_id, revision, COALESCE(identity_key,''), COALESCE(email,''), COALESCE(name,''),
  priority, enabled, manual_disabled, COALESCE(lifecycle,'active'),
  access_token, refresh_token,
  COALESCE(EXTRACT(EPOCH FROM token_expires_at)::bigint,0),
  COALESCE(proxy_mode,''), COALESCE(proxy_url,''),
  failure_count, success_count,
  COALESCE(EXTRACT(EPOCH FROM cooldown_until)::bigint,0),
  COALESCE(last_error,''),
  EXTRACT(EPOCH FROM last_used_at)::bigint,
  EXTRACT(EPOCH FROM last_success_at)::bigint,
  EXTRACT(EPOCH FROM last_refresh_at)::bigint,
  consecutive_unauthorized, COALESCE(quarantine_fp,''),
  EXTRACT(EPOCH FROM purge_after)::bigint,
  COALESCE(billing_json::text, '{}'),
  COALESCE(EXTRACT(EPOCH FROM created_at)::bigint,0),
  COALESCE(EXTRACT(EPOCH FROM updated_at)::bigint,0)
FROM accounts
WHERE enabled=true AND lifecycle='active'
  AND token_expires_at IS NOT NULL
  AND EXTRACT(EPOCH FROM token_expires_at)::bigint < $1
ORDER BY token_expires_at ASC, account_id ASC
LIMIT $2`
	rows, err := s.pool.Query(ctx, q, beforeUnix, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]catalog.Account, 0, limit)
	for rows.Next() {
		a, err := scanAccountRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PostgresAccountStore) ListAccounts(ctx context.Context, limit int, afterID string, filter catalog.AccountListFilter) ([]catalog.AccountSummary, error) {
	metas, err := s.ListEligible(ctx, limit, afterID)
	if err != nil {
		return nil, err
	}
	_ = filter
	out := make([]catalog.AccountSummary, 0, len(metas))
	for _, m := range metas {
		out = append(out, catalog.AccountSummary{
			ID:           m.ID,
			Lifecycle:    m.Lifecycle,
			ProxyMode:    m.ProxyMode,
			ProxyURL:     m.ProxyURL,
			Priority:     int(m.Priority),
			Enabled:      m.Enabled,
			ExpiresAt:    m.ExpiresAt,
			CooldownUntil: m.CooldownUntil,
			Revision:     m.Revision,
			HasAccess:    true,
			FailureCount: int64(m.FailureScore),
			Alive:        m.Enabled && m.Lifecycle == catalog.LifecycleActive,
		})
	}
	return out, nil
}

func (s *PostgresAccountStore) CountAccounts(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM accounts`).Scan(&n)
	return n, err
}

func (s *PostgresAccountStore) Stats(ctx context.Context) (catalog.CatalogStats, error) {
	var st catalog.CatalogStats
	err := s.pool.QueryRow(ctx, `
SELECT
  count(*),
  count(*) FILTER (WHERE enabled),
  count(*) FILTER (WHERE lifecycle='active' AND enabled AND NOT manual_disabled),
  count(*) FILTER (WHERE cooldown_until IS NOT NULL AND cooldown_until > now()),
  count(*) FILTER (WHERE lifecycle='quarantined'),
  count(*) FILTER (WHERE NOT enabled OR manual_disabled)
FROM accounts`).Scan(&st.Count, &st.EnabledCount, &st.ActiveCount, &st.CooldownCount, &st.QuarantineCount, &st.DisabledCount)
	return st, err
}

type scannable interface {
	Scan(dest ...any) error
}

func scanAccountRow(row scannable) (catalog.Account, error) {
	var (
		a                                   catalog.Account
		lastUsed, lastSucc, lastRef, purge  *int64
	)
	err := row.Scan(
		&a.ID, &a.Revision, &a.IdentityKey, &a.Email, &a.Name, &a.Priority, &a.Enabled, &a.ManualDisabled,
		&a.Lifecycle, &a.AccessToken, &a.RefreshToken, &a.ExpiresAt, &a.ProxyMode, &a.ProxyURL,
		&a.FailureCount, &a.SuccessCount, &a.CooldownUntil, &a.LastError, &lastUsed, &lastSucc,
		&lastRef, &a.ConsecutiveUnauthorized, &a.QuarantineFP, &purge, &a.BillingJSON, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return catalog.Account{}, err
	}
	a.LastUsedAt = lastUsed
	a.LastSuccessAt = lastSucc
	a.LastRefreshAt = lastRef
	a.PurgeAfter = purge
	return a, nil
}

func scanHotMetas(rows pgx.Rows) ([]catalog.HotMeta, error) {
	out := make([]catalog.HotMeta, 0, 64)
	for rows.Next() {
		var m catalog.HotMeta
		var failCount int
		if err := rows.Scan(
			&m.ID, &m.Revision, &m.IdentityKey, &m.Priority, &m.Enabled, &m.Lifecycle,
			&m.CooldownUntil, &m.ExpiresAt, &failCount, &m.ProxyMode, &m.ProxyURL,
		); err != nil {
			return nil, err
		}
		m.FailureScore = float32(failCount)
		out = append(out, m)
	}
	return out, rows.Err()
}

func nullJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "{}"
	}
	return s
}

func shardHint(id string, n int) int {
	if n <= 0 {
		return 0
	}
	var h uint32 = 2166136261
	for i := 0; i < len(id); i++ {
		h ^= uint32(id[i])
		h *= 16777619
	}
	return int(h % uint32(n))
}
