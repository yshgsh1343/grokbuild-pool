# Scheme 2 — 14万账号池改造启动说明

分支：`feat/scheme2-scale`  
状态：**骨架已落地，可编译扩展；多进程真共享状态仍需 Redis/Postgres 适配器**

## 本分支已交付（对应你要的 1/2/3/4）

1. **Postgres + Redis 合同**
   - `migrations/postgres/001_scheme2_init.sql`
   - `docs/scheme2/REDIS_KEYS.md`
2. **Go 接口改造骨架 + PR 拆分**
   - `internal/store`（AccountStore + SQLite adapter）
   - `internal/clusterstate`（State + Memory）
   - `docs/scheme2/PR_SPLIT.md`
3. **Gateway / Worker / ControlPlane / Refresher 合同与入口**
   - `docs/scheme2/API_CONTRACTS.md`
   - `cmd/gateway` `cmd/worker` `cmd/controlplane` `cmd/refresher`
   - `internal/gateway` `internal/worker` `internal/controlplane` `internal/refresher`
4. **14 万压测与验收**
   - `docs/scheme2/LOADTEST_ACCEPTANCE.md`
   - `scripts/loadtest/*`

## 快速构建

```bash
make build-scheme2
make test-scheme2
```

产物：

```text
bin/gateway
bin/worker
bin/controlplane
bin/refresher
```

## 本机限制（当前执行环境）

- 未检测到 `docker` / `go` 在 PATH 中
- 因此这里完成的是**分支落地与启动准备**，不是完整联调压测
- 你在有 Go 1.26+ 的机器上执行 `make build-scheme2` 即可继续

## 单机 bootstrap（SQLite + Memory）

> Memory 状态不跨进程共享。这只验证进程能起、接口能通。

```bash
# 既有单机代理仍可用
make build
bin/pool-proxy -config config.example.yaml

# Scheme2 进程（需先有 sqlite db）
bin/controlplane -db ./data/pool.db -workset 3000 -shards 16
bin/worker -db ./data/pool.db -worker-id worker-0 -listen 0.0.0.0:8081 -hot-size 1000 -shards 16
bin/gateway -listen 0.0.0.0:8080 -workers http://127.0.0.1:8081
bin/refresher -db ./data/pool.db
```

## 依赖服务（有 Docker 时）

```bash
docker compose -f deploy/scheme2/docker-compose.yml up -d
psql postgres://gbp:gbp@127.0.0.1:5432/grokbuild_pool -c '\dt'
```

## 下一步实现顺序

1. Redis adapter for `clusterstate.State`
2. Postgres adapter for `store.AccountStore`
3. 把现有 protocol handlers 挂到 worker
4. Gateway 真正按 shard owner 路由（而不是静态 worker 列表哈希）
5. 140k 导入链路接到 controlplane import store

详见 `docs/scheme2/PR_SPLIT.md`。
