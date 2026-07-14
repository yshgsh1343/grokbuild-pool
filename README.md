> [!WARNING]
> ## 免责声明
>
> - 本项目仅供学习、研究与个人自用，不提供任何形式的商业服务或可用性保证。
> - 使用者必须确保所使用的账号、凭据及接口访问权限来源合法，并遵守相关服务条款及所在地法律法规。
> - 禁止将本项目用于账号盗用、绕过访问限制、批量滥用、未授权服务转售及其他违法违规用途。
> - 项目不会收集或提供任何上游账号。账号封禁、额度损失、数据泄露、服务中断等风险由使用者自行承担。
> - 上游接口可能随时发生变化，本项目不保证长期兼容性、稳定性或数据完整性。
> - 公网部署前请务必启用强密钥、HTTPS、访问控制和必要的网络隔离。
> - 项目为 **Grok4.5** 编写的 **vibe coding** 玩具，**GPT5.6 sol** 和作者本人进行 **review**。但难免有疏漏之处。同时，本项目因作者日后忙碌 **疏于维护**，需要的可 **自行 Fork 二开**，issue 和 PR 可能无法及时回应。
> - **下载、部署或使用本项目，即代表你已阅读并同意自行承担相关风险。**

# grokbuild-pool

将 Grok Build 转换为 **OpenAI / Anthropic 兼容 API**，并提供面向大规模账号池的调度、令牌管理、额度限制、出站代理池和 Web 管理后台。

## 功能
- OpenAI Chat Completions / OpenAI Responses / Anthropic Messages 支持
- 多账号池自动调度（会话粘性主/次选、模型级冷却、可用性优先默认）
- 两种部署形态：
  - **单机 SQLite**：`pool-proxy` 一进程（默认，推荐先用）
  - **Postgres + Redis 多进程**：Gateway / Worker / ControlPlane / Refresher（Scheme 2，**尚未完成联调验证**）
- SQLite 冷库与内存热池（默认路径）
- 会话粘性（primary + secondary）与 Power-of-Two / stable_rr 选号
- SOCKS5 / HTTP 出站代理池（账号粘 = 出口粘，可选 `require_proxy`）
- API 令牌、额度、RPM 和并发限制
- React 管理后台（仪表盘 / 账号 / Token / 选号模式 / 代理池 / 导入 / 设置）
- Docker 一键部署，默认服务端口：`8080`

## 与其他项目的区别

不同项目的定位不同，不存在绝对的优劣关系，请根据实际需求选择。

| 对比项              | grokbuild-pool                       | CLIProxyAPI                                  | grok2api                             |
| ---------------- | ------------------------------------ | -------------------------------------------- | ------------------------------------ |
| 主要定位             | 面向 Grok Build 的大规模账号池代理              | 面向多种 AI CLI 订阅的统一 API 代理                     | 面向 Grok Build 与 Grok Web 的完整网关       |
| 上游范围             | 专注 Grok Build                         | Claude Code、OpenAI Codex、Gemini、Grok Build 等 | Grok Build、Grok Web                  |
| 多供应商支持           | ✅                                    | ✅                                            | ✅                                    |
| 图片、图片编辑与视频       | ❌                                    | 主要面向文本及多模态输入                                 | ✅                                    |
| 多账号调度            | ✅                                    | ✅                                            | ✅                                    |
| 调度特点             | SQLite 冷库、内存热池、主/次粘性、模型级冷却、Power-of-Two / stable_rr；可选 Postgres/Redis 多进程骨架 | 多 Provider、多账号轮询与负载均衡                        | 优先级、额度门控、会话粘性、冷却与故障切换                |
| 大规模账号池           | **核心设计目标**                           | 支持，但更侧重多 Provider 统一接入                       | 支持，更侧重完整功能与管理体验                      |
| 客户端令牌管理          | ✅                                    | ✅                                            | ✅                                    |
| 令牌额度与并发限制        | ✅                                    | ❌                                            | ✅                                    |
| 内置管理后台           | ✅ React SPA（轻量，Hash 路由）              | ✅提供 Management API，也可搭配第三方 Dashboard         | ✅ 完整 React 管理后台                      |
| HTTP / SOCKS 代理池 | ✅ 文件池 + 账号持久绑定 + `require_proxy`     | ✅                                            | ✅                                    |
| 数据库              | SQLite（默认）/ PostgreSQL（Scheme 2，未验证） |                                              | SQLite / PostgreSQL                  |
| Redis 支持         | Scheme 2 可选（未验证；粘性可走 Redis 次级状态）      | ❌                                            | ✅                                    |
| 更适合              | **只使用 Grok Build**，重视账号池规模、调度性能、防封出口和轻量部署 | **个人使用，希望统一接入多个 AI CLI / OAuth Provider**    | **需要 Grok Build、Grok Web、媒体生成和完整后台** |

如果你：主要使用 Grok Build 且追求轻量化部署，需要管理较大规模的账号池，需要分发功能与 SOCKS/HTTP 出口绑定，不需要 Grok Web 图片或视频能力，本项目会比较适合。

## 快速开始

### 1. 克隆项目

```bash
git clone https://github.com/yshgsh1343/grokbuild-pool.git
cd grokbuild-pool
```

### 2. 启动服务

生成管理密钥并启动：

```bash
export ADMIN_KEY="$(openssl rand -hex 24)"
docker compose -f docker-compose.sqlite.yml up -d --build
```

### 3. 打开管理台并导入账号

浏览器访问 `http://127.0.0.1:8080/admin/`（Hash 路由，例如 `#/dashboard`），用 `ADMIN_KEY` 登录。

首次启动时账号池为空，可在「导入」页上传 JSON / NDJSON / SSO 数据；也可在开启服务端路径导入后浏览服务器目录提交任务。

数据保存在 Docker Volume `pool-data` 中（库、`settings.json`、`proxy_pool.json`、导入暂存等）。

## 两种部署方式

项目现在有两条路径，**默认请先用方式 A**。

| 方式 | Compose 文件 | 组件 | 存储 | 适合规模 | 状态 |
| --- | --- | --- | --- | --- | --- |
| **A. 单机 SQLite** | `docker-compose.sqlite.yml` | 仅 `pool-proxy` | SQLite + 进程内热池/粘性 | 中小规模，先跑通 | **默认可用** |
| **B. Postgres + Redis** | `docker-compose.postgres-redis.yml` | `gateway` + `worker` + `controlplane` + `refresher` | Postgres 冷库 + Redis 跨进程状态 | 目标 10 万级账号池 | **代码骨架已合入 main，尚未完成联调/压测验证** |

> **重要备注**
> - 方式 B（Postgres + Redis 多进程）目前只完成接口、DDL、进程入口和协议挂载骨架。
> - **没有完成真实 Postgres/Redis 环境联调，也没有 14 万导入/压测验收。**
> - 生产/自用请优先方式 A；方式 B 仅供继续开发或实验，不保证可直接上线。

### 方式 A：单机 SQLite（推荐）

就是上面的「快速开始」：一个 `pool-proxy` 进程 + `data/pool.db`。

```bash
# Docker（单机 SQLite）
export ADMIN_KEY="$(openssl rand -hex 24)"
docker compose -f docker-compose.sqlite.yml up -d --build

# 或本地二进制（需本机 Go 1.26+、Node/pnpm 以构建管理台）
# make build
# bin/pool-proxy -config config.example.yaml
```

特点：

- 冷库：SQLite WAL（`data/pool.db`）
- 热池 / sticky / inflight / 模型冷却：进程内内存（模型冷却会落盘）
- 代理池：`data/proxy_pool.json`（可选）
- 管理后台、导入导出、OpenAI/Anthropic 兼容 API 都在同一进程
- 部署最简单，也是当前主路径

### 方式 B：Postgres + Redis 多进程（实验，未验证）

目标形态：

```text
Client → gateway → worker(s)
                 ↗ controlplane（工作集/分片）
                 ↗ refresher（token 刷新队列）
Postgres = 账号冷库
Redis    = sticky / inflight / cooldown / shard lease / workset
```

依赖（示例）：

```bash
# 仅拉起依赖，不包含应用多进程完整编排
docker compose -f docker-compose.postgres-redis.yml up -d
# Postgres: postgres://gbp:gbp@127.0.0.1:5432/grokbuild_pool
# Redis:    redis://127.0.0.1:6379/0
```

迁移：

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/postgres/001_scheme2_init.sql
```

构建 Scheme 2 进程：

```bash
# 需要本机 Go 1.26+
make build-scheme2
# 产物：bin/gateway bin/worker bin/controlplane bin/refresher
```

启动示例（**实验命令，未做完整联调**）：

```bash
export DATABASE_URL='postgres://gbp:gbp@127.0.0.1:5432/grokbuild_pool'
export REDIS_URL='redis://127.0.0.1:6379/0'

# 控制面：构建分片工作集
bin/controlplane --store postgres --database-url "$DATABASE_URL" \
  --state redis --redis-url "$REDIS_URL" \
  --workset 30000 --shards 64

# Worker：本地热池 + /v1 协议
bin/worker --worker-id worker-0 --listen 0.0.0.0:8081 \
  --store postgres --database-url "$DATABASE_URL" \
  --state redis --redis-url "$REDIS_URL" \
  --hot-size 5000 --shards 64

# Gateway：客户端入口（亦挂载管理台）
bin/gateway --listen 0.0.0.0:8080 \
  --workers http://127.0.0.1:8081 \
  --state redis --redis-url "$REDIS_URL"

# Refresher：token 到期队列（当前 OAuth 仍可能是占位/门控）
bin/refresher --store postgres --database-url "$DATABASE_URL" \
  --state redis --redis-url "$REDIS_URL"
```

也可用 SQLite 冷库 + Memory 状态做单机骨架冒烟（**同样不代表方式 B 已验证**）：

```bash
bin/controlplane --store sqlite --db ./data/pool.db --state memory
bin/worker --store sqlite --db ./data/pool.db --state memory --listen 0.0.0.0:8081
bin/gateway --workers http://127.0.0.1:8081 --state memory
```

相关文件：

- `migrations/postgres/001_scheme2_init.sql`
- `scripts/scheme2_smoke_memory.sh`
- `scripts/loadtest/`（导入/选号压测辅助）

## 管理后台

内嵌 React SPA（Vite + React 19 + Tailwind），静态资源挂在 `/admin/`、`/admin/ui/*`，JSON API 走 `/admin/*`。页面使用 **HashRouter**，避免与 Admin JSON 路由冲突。

| 页面 | 路径 | 说明 |
| --- | --- | --- |
| 登录 | `#/login` | `ADMIN_KEY` |
| 仪表盘 | `#/dashboard` | 池状态、粘性命中、故障切换等 |
| 账号 | `#/accounts` | 列表/详情抽屉、邮箱展示、启停隔离、模型冷却 |
| Token | `#/tokens` | 发放/编辑 `sk-` 令牌、额度/并发/RPM |
| 选号模式 | `#/scheduler` | `availability_mode` 与选号相关参数 |
| 代理池 | `#/proxy-pool` | 节点 CRUD、健康数、批量绑定账号 |
| 导入 | `#/imports` | 上传 / 服务端目录浏览提交 / canary |
| 设置 | `#/settings` | 运行参数表单热更 |
| JSON | `#/settings/json` | 原始 JSON 编辑 |

本地开发管理台：

```bash
# 先启动 pool-proxy（默认 :8080）
cd frontend
pnpm install
pnpm dev
# http://127.0.0.1:5173 ，代理 /admin/* 到后端
```

产物经 `make frontend-build` / Docker 多阶段构建同步到 `internal/adminui/dist` 并由 Go embed。

## 调度逻辑

默认按**可用性优先（stable）**：少换号、轻惩罚、429 熔断，避免同一请求扫穿账号池。需要冲吞吐可设 `availability_mode: aggressive`。

进程把账号分成 **冷库** 与 **热池**，请求路径上只对热池做选号，密钥仅在租约窗口内从冷库取出。

### 总流程（一次请求）

```text
HTTP 请求
  → lease.Acquire(stickyKey?, model?, exclude?)
      → selector.PickExcluding(now, stickyKey, exclude)
          ① sticky primary 命中且仍 Eligible → 直接返回
          ② 否则 sticky secondary 命中且 Eligible → 升主并返回
          ③ 否则 stable_rr / pow2Pick：从热池候选打分
          ④ 若 stickyKey 非空，put 新 primary（旧主可降为 secondary）
      → 模型冷却检查（同号其它模型仍可租）
      → catalog 按 accountID 取 token/proxy（密钥仅租约期）
      → 无 proxy 时可从代理池自动绑定并落盘
      → hot.Inflight++
  → executor 反代 cli-chat-proxy（默认）
  → lease.Release
      成功：Inflight--
      失败：模型/账号冷却、failureScore、可能隔离、ClearSticky、exclude 后再 Acquire
```

### SQLite 冷库（catalog）

- 落盘：`data/pool.db`，SQLite WAL。
- 存 **全量** 账号：token、代理、生命周期、隔离状态、模型冷却等。
- 职责：导入/导出、启停、隔离、按 `accountID` 取密钥。
- **不在**每次选号时全表扫描。

### 内存热池（hot.Index）

- 内存中的 **无密钥** 元数据索引（`HotMeta`）。
- 默认容量约 `hot_size = 3000`（启动固定；改大小需热更 Resize 并重建）。
- 每条大致包含：优先级、当前 inflight、失败分、冷却截止、代理信息等。
- 冷却中、超单号并发上限、不合格账号不会进入候选。
- 导入可配置 canary：先只装入 `import_canary_hot_size` 条，并在 hold 窗口内抑制全量热重载。

### 会话粘性（sticky）

- 进程内 LRU：`stickyKey → {primary, secondary}`（默认 TTL 1800s，容量约 10 万）。
- 同一会话优先钉 primary；primary 不可用时尝试 secondary。
- 账号自带代理时，等价于 **账号粘 = 代理粘**。
- 401/402/403 等失败会按策略清粘性；429/5xx 默认**不清**（可由 `clear_sticky_on_429` / `clear_sticky_on_5xx` 打开）。
- Scheme 2 骨架下 Redis 状态同样支持 primary/secondary sticky 字段。

### 选号策略（默认 stable_rr，可选 pow2_least_load）

未命中粘性时：

1. **stable_rr**：在最高 priority 的合格层内 RoundRobin；
2. **pow2_least_load**：从合格热账号中随机抽 **K** 个（默认 `pow2_k = 2`），打分取最高：

```text
score = WPriority * priority
      - WInflight * inflight
      - WFailure  * failureScore
      + U(-JitterAmp, +JitterAmp)
```

3. 默认权重偏惩罚并发与失败，并加小抖动，减轻「全打高优号」的羊群效应。

租约层（lease）在失败时会 `PickExcluding` 换号，直到成功或用尽 `max_attempts`（**stable 默认 2**）。

### 模型级冷却

- 上游 `429` 且请求带 model 时：优先写 **模型级冷却**，不连坐整号账号级 `cooldown_until`。
- 冷却状态进程内生效，并持久化到 SQLite；重启后加载未过期项。
- 同账号其它模型仍可被选中；同模型在冷却期内会被跳过。

### 出站代理池（antiban）

- 文件池：`data/proxy_pool.json`，节点支持 `http` / `https` / `socks5` / `socks5h`。
- SOCKS 走 `golang.org/x/net/proxy` Dialer（`http.ProxyURL` 对 socks 无效）。
- 分配模式：`hash`（按 accountID 稳定哈希）或 `least_accounts`。
- 空 `proxy_url` 的账号可在租约路径或管理台「批量绑定」时写入并持久化。
- `require_proxy=true` 时无可用代理的请求将被拒绝。
- 拨号失败 / 403 / 5xx 可标记节点失败与冷却；成功回写健康。

## 参数说明

### 0. 环境变量（Docker / render-config）

| 环境变量 | 写入配置键 | 默认 | 必填 | 说明 |
|---|---|---|---|---|
| `ADMIN_KEY` | `admin_key` | 空（示例占位不可用于公网） | 公网必填 | 管理台密钥 |
| `API_KEY` | `api_key` | 空 | 否 | 静态客户端 Key；空则靠管理台 `sk-` 令牌 |
| `UPSTREAM_BASE_URL` | `upstream.base_url` | `https://cli-chat-proxy.grok.com/v1` | 否 | 覆盖默认 Grok 上游 |
| `LISTEN` | `listen` | `0.0.0.0:8080` | 否 | 监听地址 |
| `ALLOW_PUBLIC_LISTEN` | `allow_public_listen` | `true` | 否 | 非 loopback 监听 |
| `HOT_SIZE` | `hot_size` | `3000` | 否 | 热池容量 |
| `MAX_CONCURRENT` | `limits.max_concurrent` | Docker Compose 示例 `120`；代码默认 `60` | 否 | 全局并发硬顶 |
| `LOG_LEVEL` | `logging.level` | `info` | 否 | 日志级别 |
| `POOL_DATA_DIR` | `data_dir` | 容器内常为 `/data` | 否 | 数据目录 |
| `POOL_OAUTH_ENABLED` | （门控，非 YAML） | 关 | 否 | `=1` 才允许真 OAuth 刷新 |
| `UNLOCK_M12` | `STATUS` 文件 | 关 | 否 | 与上一项同时满足才启用 HTTP OAuth |

---

### 1. 进程 / 部署

| YAML 键 | 管理台 JSON | 默认 | 生效方式 | 说明 |
|---|---|---|---|---|
| `listen` | `listen` | `0.0.0.0:8080` | 重启 | HTTP 监听 |
| `allow_public_listen` | `allow_public_listen` | `true` | 重启 | 允许 `0.0.0.0` 等公网绑定 |
| `data_dir` | `data_dir` | `./data` | 重启 | 库 / settings / 代理池 / 导入暂存根目录 |
| `db_path` | `db_path` | 空 | 重启 | 空则 `data_dir/pool-10000.db` → `pool.db` |
| `api_key` | `api_key`（仅 PUT） | 空 | 热更* | 静态 API Key；GET 不回说明文 |
| `admin_key` | `admin_key`（仅 PUT） | 示例占位 | 热更* | 管理密钥；公网禁占位符 |
| `hot_size` | `hot_size` | `3000` | 热更 | 热索引容量（保存后 Resize 并重建） |
| `availability_mode` | `availability_mode` | `stable` | 热更/预设 | `stable`/`balanced`/`aggressive` 展开选号与冷却默认 |
| `logging.level` | `logging_level` | `info` | 热更 | `debug` / `info` / `warn` / `error` |

> \* 密钥：PUT 可改内存并落盘；GET 只回 `*_configured` 布尔。

`availability_mode` 预设摘要（仅填充仍为空/0 的字段）：

| 模式 | strategy | max_inflight/账号 | max_attempts | max_concurrent | 其它 |
|---|---|---:|---:|---:|---|
| `stable`（默认） | `stable_rr` | `1` | `2` | `60` | 少换号 |
| `balanced` | `stable_rr` | `2` | `3` | `80` | 折中 |
| `aggressive` | `pow2_least_load` | `4` | `6` | `120` | 开 402 隔离、429 清粘性 |

---

### 2. 上游（默认直连 Grok）

| YAML 键 | 管理台 JSON | 默认 | 生效方式 | 说明 |
|---|---|---|---|---|
| `upstream.base_url` | `upstream_base_url` | `https://cli-chat-proxy.grok.com/v1` | 热更 | 空也回落该默认；保存后直连/出站客户端即时切换 |
| `upstream.client_version` | — | `0.2.93` | 启动 | CLI 版本头 |
| `upstream.client_identifier` | — | `grok-pager` | 启动 | `x-grok-client-identifier` |
| `upstream.user_agent` | — | `grok-pager/0.2.93 grok-shell/0.2.93 (linux; x86_64)` | 启动 | UA |
| `upstream.token_auth` | — | `xai-grok-cli` | 启动 | `X-XAI-Token-Auth` |

---

### 3. OAuth 刷新（默认关闭真刷新）

| YAML 键 | 管理台 JSON | 默认 | 生效方式 | 说明 |
|---|---|---|---|---|
| `oauth.refresh_url` | `oauth_refresh_url` | 空 → `https://auth.x.ai/oauth2/token` | 热更* | Token 端点文档默认；门控开启时即时替换客户端 |
| `oauth.client_id` | `oauth_client_id` | 空（代码里有公开 CLI id 常量） | 热更* | 可选；门控开启时即时替换 |
| `oauth.status_path` | — | 空 | 启动 | 读 `UNLOCK_M12` 的 `STATUS` 路径 |

启用真刷新条件：

```text
POOL_OAUTH_ENABLED=1 且 STATUS 中 UNLOCK_M12=true
```

否则使用 `DisabledOAuth`，不访问公网。

---

### 4. 选号 / 粘性（selector）

| YAML 键 | 管理台 JSON | 默认 | 生效方式 | 说明 |
|---|---|---|---|---|
| `selector.strategy` | `selector_strategy` | `stable_rr` | 热更 | `stable_rr` 最高优层轮询；`pow2_least_load` 为吞吐模式 |
| `selector.hot_size` | （同 `hot_size`） | `3000` | 对齐 | 实际容量在 `hot.Index` |
| `selector.sticky_ttl_sec` | `sticky_ttl_sec` | `1800` | 热更† | 粘性 TTL（秒） |
| `selector.sticky_max` | `sticky_max` | `100000` | 热更† | 粘性 LRU 容量 |
| `selector.pow2_k` | `pow2_k` | `2` | 热更 | Power-of-K 抽样数 |
| `selector.max_attempts` | `selector_max_attempts` | `2` | 热更 | 建议 failover 次数 |
| `selector.max_inflight_per_account` | `max_inflight_per_account` | stable 预设 `1`；示例配置常见 `2` | 热更 | 单账号并发硬上限；`0` = 不硬限 |
| `selector.w_priority` | `w_priority` | `1.0` | 热更 | 优先级权重 |
| `selector.w_inflight` | `w_inflight` | `10.0` | 热更 | inflight 惩罚 |
| `selector.w_failure` | `w_failure` | `5.0` | 热更 | 失败分惩罚 |
| `selector.jitter_amp` | `jitter_amp` | `0.5` | 热更 | 抖动半幅；`0` = 确定性 |

> † 改 sticky 容量 / TTL 会重建空 LRU，旧会话粘性清空。

打分公式：

```text
score = w_priority * priority
      - w_inflight * inflight
      - w_failure  * failureScore
      + U(-jitter_amp, +jitter_amp)
```

---

### 5. 租约 / 冷却（lease）

| YAML 键 | 管理台 JSON | 默认 | 生效方式 | 说明 |
|---|---|---:|---|---|
| `lease.max_attempts` | `max_attempts` | `2` | 热更 | Acquire 失败换号预算 |
| `lease.cooldown_base_sec` | `cooldown_base_sec` | `30` | 热更 | 429 无 `Retry-After` 时基数 |
| `lease.cooldown_cap_sec` | `cooldown_cap_sec` | `300` | 热更 | 冷却上限（秒） |
| `lease.cooldown_exp_max` | `cooldown_exp_max` | `3` | 热更 | 429：`base * 2^min(fail, exp)` |
| `lease.cooldown_jitter_pct` | `cooldown_jitter_pct` | `20` | 热更 | 冷却抖动百分比（`0–50`） |
| `lease.unauthorized_cooldown_sec` | `unauthorized_cooldown_sec` | `60` | 热更 | 401 冷却 |
| `lease.payment_required_cooldown_sec` | `payment_required_cooldown_sec` | `180` | 热更 | 402 冷却 |
| `lease.unauthorized_quarantine_after` | `unauthorized_quarantine_after` | `5` | 热更 | 连续 401 隔离阈值 |
| `lease.forbidden_cooldown_sec` | `forbidden_cooldown_sec` | `300` | 热更 | 403 冷却 |
| `lease.forbidden_quarantine_after` | `forbidden_quarantine_after` | `0` | 热更 | 连续 403 隔离；`0` = 关 |
| `lease.quarantine_on_payment_required` | `quarantine_on_payment_required` | `false` | 热更 | 402 是否隔离 |
| `lease.clear_sticky_on_429` | `clear_sticky_on_429` | `false` | 热更 | 429 是否清粘性 |
| `lease.clear_sticky_on_5xx` | `clear_sticky_on_5xx` | `false` | 热更 | 5xx/网络错误是否清粘性 |

#### 失败语义摘要

| 上游状态 | 默认动作 |
|---|---|
| `429` + 有 model | **模型级冷却**（不连坐整号）；指数退避 + jitter，封顶 `300s`；默认**不清**粘性 |
| `429` 无 model | 账号级冷却；默认**不清**粘性 |
| `401` | 冷却 `60s`；约连续 `5` 次隔离 |
| `402` | 冷却 `180s`；默认**不隔离** |
| `403` | 冷却 `300s`；隔离默认关 |
| 成功 | `Inflight--` |

---

### 6. 代理池 / 防封出口

| 管理台 JSON | 默认 | 生效方式 | 说明 |
|---|---|---|---|
| `proxy_pool_enabled` | `false` | 热更 | 是否启用文件代理池 |
| `proxy_assign_mode` | `hash` | 热更 | `hash` / `least_accounts` |
| `require_proxy` | `false` | 热更 | 无代理则拒绝出站 |
| `import_proxy_url` | 空 | 热更 | 导入时可选统一写入的默认代理 |

管理 API：

- `GET /admin/proxy-pool` / `PUT /admin/proxy-pool`
- `POST /admin/proxy-pool/assign`（给无 `proxy_url` 账号批量绑定，可 `dry_run`）

节点字段：`id` / `url` / `enabled` / `weight` / 失败与冷却计数等。

---

### 7. Token 后台刷新（refresh，代码默认）

| 管理台 JSON | 默认 | 生效方式 | 说明 |
|---|---|---|---|
| `refresh_workers` | `3`（钳制 `2–4`） | 重启 | worker 数启动固定 |
| `refresh_qps` | `30` | 热更 | 全局刷新 QPS |
| `refresh_skew_sec` | `300` | 热更 | `expires_at < now + skew` 时预刷新 |

内部还有：

| 内部参数 | 默认 |
|---|---|
| `scan_interval` | `5s` |
| `scan_limit` | `200` |
| 失败冷却基数 | `60s` |
| 连续失败隔离 | 约 `5` 次 |

---

### 8. 进程限流（limits）

| YAML 键 | 管理台 JSON | 默认 | 生效方式 | 说明 |
|---|---|---:|---|---|
| `limits.max_concurrent` | `max_concurrent` | `60` | 热更 | 全局 in-flight；超限 `503 + Retry-After` |
| `limits.max_body_bytes` | `max_body_bytes` | `20971520`（20MiB） | 热更 | 请求体上限 |
| `limits.request_timeout_sec` | `request_timeout_sec` | `600` | 热更 | 整请求超时（含 SSE） |

---

### 9. 导入（imports）

| YAML 键 | 管理台 JSON | 默认 | 生效方式 | 说明 |
|---|---|---:|---|---|
| `imports.enabled` | `import_enabled` | `true` | 热更 | 总开关 |
| `imports.max_upload_bytes` | `import_max_upload_bytes` | `0` | 热更 | `0` = 不限体积 |
| `imports.max_request_bytes` | — | `0` | 启动 / 对齐 | `0` = 不限 |
| `imports.max_entries` | `import_max_entries` | `10000` | 热更 | 主闸门 |
| `imports.max_ndjson_line_bytes` | `import_max_ndjson_line_bytes` | `1048576` | 热更 | NDJSON 单行 |
| `imports.max_sso_value_bytes` | `import_max_sso_value_bytes` | `16384` | 热更 | SSO 单值 |
| `imports.max_concurrent_jobs` | `import_max_concurrent_jobs` | `2` | 热更 | 并发任务数 |
| `imports.job_timeout_sec` | `import_job_timeout_sec` | `7200` | 热更 | 单任务超时 |
| `imports.staging_stale_after_sec` | `import_staging_stale_after_sec` | `86400` | 热更 | 暂存过期 |
| `imports.allow_server_path` | `import_allow_server_path` | `false` | 热更 | 是否允许服务端路径导入 |
| — | `import_server_dir` | 空=`data_dir` | 热更 | 服务端导入根目录（须开启 allow） |
| — | `import_canary_hot_size` | `0` | 热更 | 导入后先入热池条数；`0`=全量 |
| — | `import_canary_hold_sec` | `0` | 热更 | canary 后抑制全量热重载秒数 |
| （workers） | `import_workers` | 管理台侧可配 | 热更 | 解析 worker |
| `imports.sso_converter.endpoint` | `import_sso_endpoint` | 空 | 热更 | 空则用内置 Go Device Flow |
| `imports.sso_converter.api_key` | `import_sso_api_key`（仅 PUT） | 空 | 热更 | GET 不回传 |
| `imports.sso_converter.max_batch` | `import_sso_max_batch` | `50` | 热更 | 批大小 |
| `imports.sso_converter.timeout_sec` | `import_sso_timeout_sec` | `300` | 热更 | 转换超时 |
| `imports.sso_converter.allow_insecure` | `import_sso_allow_insecure` | `false` | 热更 | 跳过 TLS 校验 |
| — | `import_sso_workers` | 管理台默认约 `4` | 热更 | SSO worker |

导入解析会优先 access_token，并尽量从 JWT 提取邮箱供账号表展示。

---

### 10. Anthropic / 模型别名

| YAML 键 | 管理台 JSON | 默认 | 生效方式 | 说明 |
|---|---|---|---|---|
| `anthropic.enabled` | `anthropic_enabled` | `true` | 热更 | Anthropic 兼容入口 |
| `anthropic.model_aliases` | `anthropic_model_aliases` | 见下表 | 热更 | 请求模型 → 上游模型 |
| `anthropic.passthrough_prefixes` | `anthropic_passthrough_prefixes` | `["grok-"]` | 热更 | 此前缀模型原样透传 |
| `anthropic.strip_unknown_betas` | `anthropic_strip_unknown_betas` | `true` | 热更 | 剥未知 beta |
| `anthropic.count_tokens` | `anthropic_count_tokens` | `false` | 热更 | `count_tokens` 支持 |

#### 默认别名（`/v1/models` 精简列表）

| 客户端模型 | 上游模型 |
|---|---|
| `claude-sonnet-4` / `claude-sonnet-4-6` / `sonnet` | `grok-4.5` |
| `claude-opus-4` / `claude-opus-4-6` / `opus` | `grok-4.5` |
| `claude-haiku-4` / `claude-haiku-4-5` / `haiku` | `grok-composer-2.5-fast` |

请求侧仍兼容更多 `claude-*` 版号（如细分 opus/sonnet/haiku 前缀），即使不出现在模型列表中。

---

### 11. 客户端令牌额度 / 并发 / RPM

发放的 `sk-` 令牌在请求路径上独立闸门（与全局 `limits.max_concurrent`、单账号 `max_inflight_per_account` 叠加）：

| 字段 | 默认 | 说明 |
|---|---:|---|
| `max_concurrent` | 创建时用模板，默认 `5` | 单令牌 in-flight 硬顶；`0` = 不限；超限 `503`「令牌并发已满」 |
| `rpm` | `0` | 每分钟请求上限；`0` = 不限；超限 `503`「令牌 RPM 已达上限」 |
| `remain_quota` / `unlimited_quota` | 见模板 | 额度预扣 + 按 usage 结算 |

- **创建**：`POST /admin/tokens` 指针语义——JSON 里显式 `"max_concurrent": 0` 表示不限，**不会**被默认模板盖成 5；未传字段才用模板。
- **修改**：`PATCH /admin/tokens/{id}` 可改 `name` / `max_concurrent` / `rpm` / `remain_quota` / `unlimited_quota` / `enabled`；**下一请求立即生效**（在途请求不中断）。列表接口回 `inflight` 实时占用。
- **管理台**：令牌页可创建，也可点「编辑」改并发/RPM/额度后保存。

#### 令牌创建默认模板（仅管理台表单 / 未传字段）

| 管理台 JSON | 默认 | 生效方式 | 说明 |
|---|---:|---|---|
| `token_default_remain_quota` | `1000` | 热更 | 默认额度（创建时未传 `remain_quota`） |
| `token_default_max_concurrent` | `5` | 热更 | 默认每令牌并发（创建时未传 `max_concurrent`）；`0` = 默认不限 |
| `token_default_rpm` | `0` | 热更 | 默认 RPM；`0` = 不限 |
| `token_default_unlimited` | `false` | 热更 | 默认是否无限额度 |

---

### 12. 热更 vs 重启（管理台）

| 需手动重启才完全生效 | 保存后即时热更 |
|---|---|
| `listen` / `data_dir` / `db_path` | 选号权重、`pow2_k`、粘性参数、`hot_size`、`availability_mode` |
|  | `upstream_base_url`、OAuth URL/client_id（门控开启时） |
|  | `max_inflight_per_account`、lease 冷却 / 隔离阈值 / 清粘性开关 |
|  | 代理池开关、`require_proxy`、分配模式 |
|  | `max_concurrent` / body / 超时、`logging_level` |
|  | `refresh_workers`（仅增补）/ `refresh_qps` / `refresh_skew_sec` |
|  | 导入限制 / canary / 服务端目录、Anthropic 别名、令牌模板 |

保存不会自动重启进程。仅 `listen` / `data_dir` / `db_path` 变更时 toast / hint 会提示需手动重启；其余参数保存后即时生效。

## 构建说明

```bash
# 默认：先构建 React 管理台，再编译 Go 二进制
make build
# 产物：bin/pool-proxy bin/poolctl bin/render-config
# 管理台产物同步到 internal/adminui/dist

# 仅 Go（使用已有 embed dist）
make build-go

# Scheme 2 多进程骨架
make build-scheme2

make test
make docker   # 镜像名默认 grokbuild2api:latest
```

依赖：

- Go **1.26+**
- 构建管理台：Node 22+、pnpm（Docker 多阶段已内置）

## 鸣谢
- Linux.do：新的理想型社区 https://linux.do/
- CLIProxyAPI：转换接口逻辑参照 https://github.com/router-for-me/CLIProxyAPI
- Grok2api：前端设计与 React 管理台栈借鉴 https://github.com/chenyme/grok2api

## 许可证
MIT
