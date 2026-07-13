# Scheme 2 Loadtest & Acceptance (140k)

## 0. Goals

Validate:

1. import path can ingest ~140k accounts in chunks
2. workset stays near target (e.g. 30k)
3. local pick remains fast under load
4. failover/cooldown behavior works
5. no inflight leak / no process crash over 1h soak

This directory is a **skeleton**: it defines commands, metrics, and pass/fail bars.  
Full multi-process load needs Docker/Postgres/Redis + built binaries.

## 1. Fixtures

### Generate synthetic accounts (NDJSON)

```bash
python scripts/loadtest/gen_accounts.py --count 140000 --out /tmp/accounts_140k.ndjson
```

Fields:
- `id`, `access_token`, `refresh_token`, `expires_at`, `priority`, `proxy_url?`

### Chunk for import

```bash
python scripts/loadtest/chunk_ndjson.py --in /tmp/accounts_140k.ndjson --out-dir /tmp/chunks --chunk-size 5000
```

## 2. Scenarios

| ID | Scenario | Command / method | Pass bar |
|---|---|---|---|
| S1 | Import 140k chunked | `import_job` per 5k chunk | all chunks terminal success; duration recorded |
| S2 | Workset fill | controlplane refill | workset within target ±10% |
| S3 | Pick latency | `bench_pick` / worker `/internal/v1/pick` | p99 pick < 2ms local |
| S4 | Concurrent acquire | N clients sticky+nonsticky | no_account_rate < 0.5% under planned concurrency |
| S5 | 429 cooldown | inject 429 | account cooled; sticky cleared; next pick excludes it |
| S6 | 401 refresh enqueue | inject 401 | refresh due enqueued; request switches account |
| S7 | Worker kill | kill one worker | shards reclaimed; gateway success recovers |
| S8 | Soak 1h | steady QPS | no leak, stable hot_size, stable error rate |

## 3. Metrics to capture

- `requests_total`, `success_rate`
- `no_account_rate`
- `failover_attempts_avg`
- `sticky_hit_rate`
- `hot_loaded` per worker
- `workset_size`
- `inflight_global`, `inflight_account_p99`
- `cooldown_accounts`, `quarantined_accounts`
- `refresh_queue_lag`
- `import_rows_per_sec`

## 4. Acceptance checklist

### Functional
- [ ] 140k rows can be inserted via chunked import
- [ ] workset target holds under refill loop
- [ ] sticky returns same account while eligible
- [ ] max_inflight_per_account hard gate holds under concurrency
- [ ] 429/401/402 create cooldown or quarantine path
- [ ] worker loss does not permanently blackhole shards

### Performance (starter targets)
- [ ] local pick p99 < 2ms
- [ ] gateway overhead small vs direct worker baseline
- [ ] no_account_rate < 0.5% in steady state
- [ ] import 140k completes in agreed window on target hardware
- [ ] 1h soak without memory/inflight growth trend

### Ops
- [ ] status endpoints expose worker/shard/hot sizes
- [ ] bad accounts diagnosable by status code + account id
- [ ] configs for weights/cooldown/workset documented

## 5. Bootstrap local commands (no docker)

```bash
# after go toolchain available
make build-scheme2

# single-node bootstrap using sqlite + memory cluster state
bin/controlplane -db ./data/pool.db -workset 3000 -shards 16
bin/worker -db ./data/pool.db -worker-id worker-0 -listen 0.0.0.0:8081 -hot-size 1000 -shards 16
bin/gateway -listen 0.0.0.0:8080 -workers http://127.0.0.1:8081
bin/refresher -db ./data/pool.db
```

Note: memory cluster state is **per process**. Multi-process correctness requires Redis adapter (next PR). For now, contracts/tests validate interfaces and single-process logic.

## 6. Result template

```text
date:
git_sha:
hardware:
accounts_imported:
workset_target:
workers:
pick_p99_ms:
no_account_rate:
success_rate:
import_minutes:
soak_ok:
notes:
```
