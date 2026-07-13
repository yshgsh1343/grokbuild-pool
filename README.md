# grokbuild2api

将 **Grok Build** 能力以 **OpenAI / Anthropic 兼容 API** 暴露给客户端（Claude Code、OpenAI SDK 等），并内置大规模账号池调度。

## 功能

- **协议兼容**：OpenAI Responses / Chat Completions，Anthropic Messages
- **账号池**：SQLite 冷库 + 内存热索引；租约选号（粘性 + Power-of-Two）
- **管理台**：浏览器 UI（`/admin/`），令牌发放、账号导入、运行参数热更新
- **额度与限流**：令牌额度预扣/结算、每令牌并发与 RPM、全局并发上限
- **Docker 一键部署**

默认端口：**8080**

## 快速开始（Docker）

### 1. 准备

```bash
git clone https://github.com/yshgsh1343/grokbuild-pool.git
cd grokbuild-pool
```

### 2. 设置管理密钥后启动

```bash
export ADMIN_KEY="$(openssl rand -hex 24)"
# 默认已直连 https://cli-chat-proxy.grok.com/v1，一般无需再设 UPSTREAM_BASE_URL

docker compose up -d --build
```

> **注意：** `HOT_SIZE` / `MAX_CONCURRENT` 等数字环境变量由纯 Go 的 `render-config` 写入 `config.yaml`。旧版 Python entrypoint 在 `HOT_SIZE=3000` 时会因 `re` 反向引用把 `hot_size` 行写成 `X00` 导致容器 crash loop。

### 3. 检查

```bash
curl -fsS http://127.0.0.1:8080/healthz
# 期望输出：ok

# 管理台
# 浏览器打开 http://127.0.0.1:8080/admin/
# 请求头或登录使用 ADMIN_KEY
```

数据持久化在 Docker volume `pool-data`（容器内 `/data`）：账号库、`tokens.db`、`settings.json`、运行配置。

首次启动若无账号库，会自动创建空的 `/data/pool.db`（`readyz` 可能 `ready=false` 直到导入账号）。可用 `poolctl prove` 或管理台导入填充：

```bash
docker compose exec pool-proxy poolctl prove --count 1000 --data-dir /data
# 或在管理台 /admin/ 导入 JSON / SSO
```

### 常用环境变量

| 变量 | 说明 | 默认 |
|------|------|------|
| `ADMIN_KEY` | 管理台密钥（**必改**） | 示例占位，生产务必替换 |
| `API_KEY` | 静态客户端 Key；空则依赖管理台发放的 `sk-` 令牌 | 空 |
| `UPSTREAM_BASE_URL` | 覆盖默认上游 Base URL | `https://cli-chat-proxy.grok.com/v1` |
| `LISTEN` | 监听地址 | `0.0.0.0:8080` |
| `HOT_SIZE` | 热池容量 | `3000` |
| `MAX_CONCURRENT` | 全局并发硬上限 | `120` |
| `LOG_LEVEL` | 日志级别 | `info` |

示例：

```bash
export ADMIN_KEY=你的强随机密钥
# 可选：export UPSTREAM_BASE_URL="https://其它上游/v1"
docker compose up -d --build
```

### 停止 / 查看日志

```bash
docker compose logs -f pool-proxy
docker compose down
```

## 客户端调用示例

管理台创建令牌后，使用返回的 `sk-...`：

```bash
# OpenAI 兼容
curl -sS http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-你的令牌" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "grok-4.5",
    "messages": [{"role":"user","content":"你好"}]
  }'
```

Anthropic 兼容（Claude Code 等）走 `/v1/messages`，同样带 API Key。

## 本地编译（可选）

需要 Go 1.26+：

```bash
make build
./bin/pool-proxy --config config.example.yaml
```

导入账号可用：

```bash
./bin/poolctl import-json --db ./data/pool.db --in ./auth.json --workers 4
./bin/poolctl import-sso  --db ./data/pool.db --in ./sso.txt \
  --converter-url http://127.0.0.1:8091 --api-key "$KEY"
```

## 配置说明

完整示例见 [`config.example.yaml`](config.example.yaml)。

要点：

1. **公网监听**必须设置非占位 `admin_key`
2. **未知 YAML 字段**会被拒绝，避免拼写错误静默失效
3. 运行中可通过管理台热更新：全局并发、请求体上限、超时、选号权重、刷新 QPS 等
4. 令牌额度在请求前**原子预扣**，结束后按实际 usage 结算；失败会退回

## 目录结构

```text
cmd/pool-proxy     HTTP 服务入口
cmd/poolctl        导入 / 统计 / 压测工具
internal/          核心实现（catalog / hot / selector / lease / refresh / protocol / admin）
deploy/            Docker 入口脚本
Dockerfile
docker-compose.yml
config.example.yaml
```

## 安全建议

- 务必更换 `ADMIN_KEY`，不要使用示例占位值
- 生产环境用反向代理终止 TLS，并限制管理台来源 IP
- 不要把 `data/`、`tokens.db`、含密钥的配置提交到 Git
- 默认反代 `https://cli-chat-proxy.grok.com/v1`；仅在换上游时设置 `UPSTREAM_BASE_URL` / `upstream.base_url`

## 许可证

本项目以 [MIT License](LICENSE) 发布。

使用本软件对接上游服务时，请自行遵守上游服务条款与账号合规要求。
