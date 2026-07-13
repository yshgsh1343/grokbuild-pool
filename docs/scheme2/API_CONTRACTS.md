# Scheme 2 Component Contracts

Status: bootstrap on branch `feat/scheme2-scale`  
Scope: Gateway / Worker / Control Plane / Refresher

## 1. Process roles

| Process | Public surface | Internal surface | Owns |
|---|---|---|---|
| `gateway` | `:8080` client API | routes to workers | auth, global concurrency, sticky route |
| `worker` | none (internal) | `:8081+` internal API | local hot set, PoT pick, upstream execute |
| `controlplane` | none | management later | workset build, shard desired size |
| `refresher` | none | none | token refresh due queue drain |
| Postgres | - | - | account cold truth |
| Redis | - | - | sticky/inflight/cooldown/shard lease/workset |

Single-binary `pool-proxy` remains supported until multi-process cutover.

## 2. Identity headers

| Header | Who sets | Meaning |
|---|---|---|
| `Authorization: Bearer <API_KEY>` or `x-api-key` | client | client auth |
| `X-Session-Id` / `X-Sticky-Key` | client or gateway | sticky conversation key |
| `X-Admin-Key` | operator | admin endpoints |
| `X-Scheme2-Worker` | gateway | chosen worker id (debug/trace) |
| `X-Request-Id` | gateway (optional) | request correlation |

## 3. Gateway HTTP contract

### `GET /healthz`
- 200 `ok`

### `GET /readyz`
- 200 if at least one worker URL configured
- 503 otherwise

### `ANY /v1/*`
- Client API passthrough to worker
- Auth if `API_KEY` configured
- Global concurrency gate (`503 Retry-After: 1` when exceeded)
- Sticky route:
  1. Redis/memory sticky worker if present
  2. else hash(`stickyKey|attempt`) into worker list
- Retry budget: `max_worker_retry` (default 2) only on worker connect failures

### `GET /admin/scheme2/status`
- requires `X-Admin-Key` when configured
- JSON:
```json
{
  "component": "gateway",
  "workers": 6,
  "inflight": 12,
  "max_concurrent": 2000,
  "shard_count": 64
}
```

## 4. Worker internal HTTP contract

Bootstrap endpoints (public protocol handlers land in later PR):

### `GET /healthz` → 200
### `GET /readyz`
- 200 if worker holds ≥1 shard lease
- 503 if no shards

### `GET /internal/v1/status`
```json
{
  "worker_id": "worker-0",
  "shards": [1, 7, 12],
  "hot_cap": 5000,
  "hot_loaded": 4980
}
```

### `GET /internal/v1/pick?sticky=...`
Debug/local selector probe:
```json
{"account_id":"...","worker_id":"worker-0"}
```
- 503 if no eligible local hot account

### Future (PR-5/6)
- `/internal/v1/openai/*`, `/internal/v1/anthropic/*` or direct reuse of current protocol handlers
- request body streaming preserved end-to-end
- lease acquire/release around upstream

## 5. Control plane contract

Process loop (no HTTP in bootstrap):

1. `ListWorksetCandidates(limit=workset_size)`
2. bucket by `hash(account_id) % shard_count`
3. cap each shard to `desired_hot_per_shard`
4. `ReplaceWorkset(shard, ids, scores)`
5. sleep `refill_interval` (default 45s)

CLI:
```bash
controlplane -db ./data/pool.db -workset 30000 -shards 64
```

## 6. Refresher contract

1. Optional startup: `ListExpiring(now+15m)` → `AddRefreshDue`
2. Loop:
   - `ClaimRefreshDue(batch, lockTTL)`
   - oauth refresh
   - `UpdateTokens` CAS by revision
   - on failure: cooldown + requeue

CLI:
```bash
refresher -db ./data/pool.db -id refresher-0
```

Bootstrap ships with `noopOAuth` (fails closed) so wiring is explicit.

## 7. Retry budgets

| Layer | Budget | Notes |
|---|---:|---|
| Gateway worker retry | 1~2 | only transport / worker down |
| Worker account attempts | 3~6 | exclude tried accounts |
| Refresh retries | 3 | with cooldown between |
| Total wall time | `request_timeout_sec` | default 600s |

Rules:
- Never infinite failover
- Prefer same-worker account switch before cross-worker
- After successful response body handoff: no second acquire

## 8. Error model

| Condition | HTTP | Body/behavior |
|---|---:|---|
| bad client key | 401 | unauthorized |
| gateway overloaded | 503 | `Retry-After: 1` |
| no worker | 503 | no worker |
| worker has no shard | 503 ready fail | readiness only |
| no eligible account | 503 | no account / no credential |
| upstream 429 | pass-through after local cooldown bookkeeping | worker may switch account before deliver if still pre-response |
| upstream 401 | enqueue refresh + switch account | do not block request on refresh |

## 9. Config template (logical)

```yaml
cluster:
  workers: 6
  shards: 64
  workset_size: 30000

gateway:
  listen: 0.0.0.0:8080
  max_concurrent: 2000
  max_worker_retry: 2
  sticky_ttl_sec: 1800

worker:
  hot_size: 5000
  max_inflight_per_account: 2
  max_attempts: 4
  pow2_k: 2
  shard_lease_ttl_sec: 45
  shard_renew_sec: 15

controlplane:
  refill_interval_sec: 45
  desired_hot_per_shard: 470

refresher:
  global_qps: 20
  due_window_sec: 900
  lock_sec: 60
```

## 10. Compatibility promise

- Existing `cmd/pool-proxy` path unchanged by these additive packages
- Scheme 2 commands are opt-in
- SQLite adapter keeps local bootstrap possible without Postgres/Redis
- Redis adapter and Postgres AccountStore are next implementation PRs
