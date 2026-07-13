# Scheme 2 — 14万账号池改造启动说明

状态：已合入 `main`  
结论：**骨架已落地，可编译；Postgres/Redis 多进程路径尚未完成联调验证**

## 和主路径的关系

| 路径 | 说明 | 是否推荐现在用 |
| --- | --- | --- |
| 单机 SQLite `pool-proxy` | 默认生产/自用路径 | **是** |
| Scheme 2 Postgres + Redis | 多进程扩展骨架 | **否（实验）** |

> 主 README 的「两种部署方式」一节已同步说明：  
> **Postgres + Redis 方式没有完成真实环境验证。**

## 已交付

1. **Postgres + Redis 合同**
   - `migrations/postgres/001_scheme2_init.sql`
   - `docs/scheme2/REDIS_KEYS.md`
2. **Go 接口改造骨架**
   - `internal/store`（AccountStore + SQLite/Postgres adapter）
   - `internal/clusterstate`（State + Memory/Redis）
   - `docs/scheme2/PR_SPLIT.md`
3. **Gateway / Worker / ControlPlane / Refresher**
   - `docs/scheme2/API_CONTRACTS.md`
   - `cmd/gateway` `cmd/worker` `cmd/controlplane` `cmd/refresher`
   - Worker 已挂 `/v1` OpenAI/Anthropic handlers
4. **14 万压测与验收文档**
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

## 验证边界（务必读）

已做：

- 代码编译（gateway/worker/controlplane/refresher/pool-proxy）
- Memory clusterstate 单测
- Controlplane workset 单测
- Gateway `/healthz` 冒烟

**未做（因此不叫“可用”）**：

- 真实 Postgres 导入/查询联调
- 真实 Redis 跨进程 sticky/inflight/shard lease 联调
- 多 worker 分片故障恢复
- 14 万账号导入与压测验收
- Refresher 真实 OAuth 端到端

## 单机 bootstrap（SQLite + Memory）

> Memory 状态不跨进程共享。这只验证进程能起、接口能通。

```bash
# 既有单机代理仍可用
make build
bin/pool-proxy -config config.example.yaml

# Scheme2 进程（需先有 sqlite db）
bin/controlplane -db ./data/pool.db -workset 3000 -shards 16 --state memory
bin/worker -db ./data/pool.db -worker-id worker-0 -listen 0.0.0.0:8081 -hot-size 1000 -shards 16 --state memory
bin/gateway -listen 0.0.0.0:8080 -workers http://127.0.0.1:8081 --state memory
bin/refresher -db ./data/pool.db --state memory
```

## 依赖服务（有 Docker 时，仍属未验证路径）

```bash
docker compose -f docker-compose.postgres-redis.yml up -d
psql postgres://gbp:gbp@127.0.0.1:5432/grokbuild_pool -c '\dt'
```

实验启动：

```bash
export DATABASE_URL='postgres://gbp:gbp@127.0.0.1:5432/grokbuild_pool'
export REDIS_URL='redis://127.0.0.1:6379/0'

bin/controlplane --store postgres --database-url "$DATABASE_URL" --state redis --redis-url "$REDIS_URL"
bin/worker --store postgres --database-url "$DATABASE_URL" --state redis --redis-url "$REDIS_URL" --listen 0.0.0.0:8081
bin/gateway --workers http://127.0.0.1:8081 --state redis --redis-url "$REDIS_URL"
bin/refresher --store postgres --database-url "$DATABASE_URL" --state redis --redis-url "$REDIS_URL"
```

## 下一步

1. Postgres/Redis 真机联调与最小 e2e
2. 导入 14 万到 Postgres
3. Gateway 按 shard owner 路由
4. 现有 OAuth refresh 接到 refresher
5. 140k 压测验收

详见 `docs/scheme2/PR_SPLIT.md`。
