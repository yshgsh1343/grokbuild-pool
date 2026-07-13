# Scheme 2 Go Refactor PR Split

Branch: `feat/scheme2-scale`  
Goal: keep current PoT/lease semantics, introduce store/cluster interfaces, multi-process skeleton.

## PR-1 — Storage interfaces + Postgres DDL
- `migrations/postgres/001_scheme2_init.sql`
- `internal/store` AccountStore interfaces
- docs: Redis key contract
- no runtime cutover yet

## PR-2 — Cluster state (Redis adapters, memory fallback)
- `internal/clusterstate`
- sticky / inflight / cooldown / shard lease interfaces
- in-memory implementation for unit tests

## PR-3 — Wire lease/hot to interfaces
- `lease.Manager` depends on `store.AccountStore` + `clusterstate.State`
- keep SQLite catalog as first AccountStore implementation
- feature flag: `SCHEME2_CLUSTER_STATE=memory|redis`

## PR-4 — Control plane process
- `cmd/controlplane`
- workset builder + shard table reconcile
- import job chunk orchestration stubs

## PR-5 — Worker process
- `cmd/worker`
- local hot index + selector + executor
- claims shards, serves internal proxy API

## PR-6 — Gateway process
- `cmd/gateway`
- auth/rate-limit/sticky route to worker
- retry budget across workers

## PR-7 — Refresher process
- `cmd/refresher`
- due-queue claim + oauth refresh + CAS token update

## PR-8 — Loadtest + acceptance
- `scripts/loadtest/*`
- import 140k / hotset / failover scenarios
- acceptance checklist

## Compatibility

Until PR-5/6 are complete, `cmd/pool-proxy` remains the supported single-binary path.  
Scheme 2 binaries are additive and default-off.
