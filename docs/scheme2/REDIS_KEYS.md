# Scheme 2 Redis Key Contract

Redis holds **cross-instance hot state only**.  
Never store `access_token` / `refresh_token` in Redis.

## Conventions

| Item | Rule |
|---|---|
| Key prefix | `gbp:` (grokbuild-pool) |
| Token secrets | Postgres only |
| Crash safety | every counter/lock has TTL |
| Source of truth | Postgres for account rows; Redis for sticky/inflight/cooldown/shard lease |

## Keys

### Sticky session

```text
gbp:sticky:{stickyKey} -> JSON
{
  "account_id": "...",
  "worker_id": "...",
  "shard_id": 12,
  "exp": 1710000000
}
TTL: sticky_ttl_sec (default 1800)
```

### Account inflight (hard gate)

```text
gbp:inflight:{account_id} -> integer
TTL: 900s (refresh on each acquire)
```

Semantics:

1. `INCR` before upstream call
2. if `> max_inflight_per_account` then `DECR` and pick another account
3. always `DECR` on release / cancel

### Cooldown

```text
gbp:cooldown:{account_id} -> unix_ts_end
TTL: cooldown duration
```

### Failure score cache (optional)

```text
gbp:failscore:{account_id} -> float string
TTL: 3600s
```

### Shard lease

```text
gbp:shard:lease:{shard_id} -> JSON
{
  "worker_id": "worker-a",
  "version": 7,
  "exp": 1710000045
}
TTL: 45s
Renew every 15s with owner compare.
```

Acquire:

```text
SET gbp:shard:lease:{id} <json> NX EX 45
```

Renew:

```text
only if current.worker_id == me
SET ... EX 45 / EXPIRE
```

### Worker heartbeat

```text
gbp:worker:hb:{worker_id} -> unix_ts
TTL: 30s
```

### Workset per shard

```text
gbp:workset:shard:{shard_id} -> ZSET
member = account_id
score  = promotion_score
```

Control plane refills; workers load only owned shards.

### Hot meta cache (no secrets)

```text
gbp:meta:{account_id} -> HASH
fields:
  priority, failure_score, lifecycle, enabled,
  cooldown_until, expires_at, revision, proxy_mode, shard_hint
TTL: 300~900s
```

### Global / worker concurrency

```text
gbp:gate:global_inflight -> integer
gbp:gate:worker_inflight:{worker_id} -> integer
TTL: 120s (or explicit DECR on release)
```

### Refresh due queue

```text
gbp:refresh:due -> ZSET
member = account_id
score  = due_unix_ts

gbp:refresh:lock:{account_id} -> refresher_id
TTL: 60s (NX)
```

### State patch buffer (optional stream)

```text
gbp:state:patches -> STREAM
fields: account_id, success_delta, fail_delta, cooldown_until, failure_score, status_code, ts
```

Workers XADD; flusher batches into Postgres.

## Forbidden

- `gbp:token:*` with raw credentials
- permanent keys without TTL for inflight/locks
- using Redis as only durability for account lifecycle
