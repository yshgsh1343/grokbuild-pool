-- Scheme 2: cold store + control-plane schema for ~140k accounts.
-- Apply:
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/postgres/001_scheme2_init.sql

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------------------
-- accounts: full credential cold rows (tokens never enter hot index)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS accounts (
    account_id                text PRIMARY KEY,
    revision                  bigint NOT NULL DEFAULT 1,
    identity_key              text NOT NULL DEFAULT '',
    email                     text NOT NULL DEFAULT '',
    name                      text NOT NULL DEFAULT '',
    priority                  integer NOT NULL DEFAULT 100,
    enabled                   boolean NOT NULL DEFAULT true,
    manual_disabled           boolean NOT NULL DEFAULT false,
    lifecycle                 text NOT NULL DEFAULT 'active'
        CHECK (lifecycle IN ('active', 'quarantined', 'purged', 'disabled')),
    access_token              text NOT NULL DEFAULT '',
    refresh_token             text NOT NULL DEFAULT '',
    token_expires_at          timestamptz,
    proxy_mode                text NOT NULL DEFAULT '',
    proxy_url                 text NOT NULL DEFAULT '',
    failure_count             integer NOT NULL DEFAULT 0,
    success_count             integer NOT NULL DEFAULT 0,
    failure_score             double precision NOT NULL DEFAULT 0,
    cooldown_until            timestamptz,
    quarantine_until          timestamptz,
    last_error                text NOT NULL DEFAULT '',
    last_used_at              timestamptz,
    last_success_at           timestamptz,
    last_fail_at              timestamptz,
    last_refresh_at           timestamptz,
    consecutive_unauthorized  integer NOT NULL DEFAULT 0,
    quarantine_fp             text NOT NULL DEFAULT '',
    purge_after               timestamptz,
    billing_json              jsonb NOT NULL DEFAULT '{}'::jsonb,
    shard_hint                integer NOT NULL DEFAULT 0,
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_accounts_workset
    ON accounts (lifecycle, cooldown_until, failure_score, priority DESC, account_id)
    WHERE enabled = true AND manual_disabled = false;

CREATE INDEX IF NOT EXISTS idx_accounts_refresh
    ON accounts (lifecycle, token_expires_at)
    WHERE enabled = true AND lifecycle = 'active';

CREATE INDEX IF NOT EXISTS idx_accounts_shard
    ON accounts (shard_hint, lifecycle, priority DESC, account_id);

CREATE INDEX IF NOT EXISTS idx_accounts_identity
    ON accounts (identity_key)
    WHERE identity_key <> '';

-- ---------------------------------------------------------------------------
-- shards: ownership / desired hot size for workers
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS shards (
    shard_id          integer PRIMARY KEY,
    owner_worker_id   text,
    lease_expire_at   timestamptz,
    desired_hot_size  integer NOT NULL DEFAULT 470,
    status            text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'draining', 'disabled')),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- import jobs / chunks
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS import_jobs (
    job_id        text PRIMARY KEY,
    status        text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    source_name   text NOT NULL DEFAULT '',
    total         integer NOT NULL DEFAULT 0,
    done          integer NOT NULL DEFAULT 0,
    failed        integer NOT NULL DEFAULT 0,
    error         text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    finished_at   timestamptz
);

CREATE TABLE IF NOT EXISTS import_chunks (
    chunk_id      bigserial PRIMARY KEY,
    job_id        text NOT NULL REFERENCES import_jobs(job_id) ON DELETE CASCADE,
    chunk_no      integer NOT NULL,
    status        text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    rows_total    integer NOT NULL DEFAULT 0,
    rows_ok       integer NOT NULL DEFAULT 0,
    rows_failed   integer NOT NULL DEFAULT 0,
    error         text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz,
    UNIQUE (job_id, chunk_no)
);

CREATE INDEX IF NOT EXISTS idx_import_chunks_job
    ON import_chunks (job_id, status, chunk_no);

-- ---------------------------------------------------------------------------
-- sampled account events (not on request hot path)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS account_events (
    id            bigserial PRIMARY KEY,
    account_id    text NOT NULL,
    event_type    text NOT NULL,
    status_code   integer NOT NULL DEFAULT 0,
    detail        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_account_events_account_time
    ON account_events (account_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- schema version
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     text PRIMARY KEY,
    applied_at  timestamptz NOT NULL DEFAULT now()
);

INSERT INTO schema_migrations(version)
VALUES ('001_scheme2_init')
ON CONFLICT (version) DO NOTHING;

-- bootstrap 64 shards (S=64)
INSERT INTO shards (shard_id, desired_hot_size, status)
SELECT g, 470, 'active'
FROM generate_series(0, 63) AS g
ON CONFLICT (shard_id) DO NOTHING;

COMMIT;
