# Grokbuild-pool Admin Frontend

React 管理后台（对齐 chenyme/grok2api 栈，裁剪到本项目 5 页）。

## 技术栈

- React 19 + TypeScript + Vite
- Tailwind CSS 4 + Radix / shadcn 风格组件
- TanStack Query · React Router HashRouter

## 开发

先启动 `pool-proxy`（默认 `:8080`），再：

```bash
cd frontend
pnpm install
pnpm dev
```

开发服务器 `http://127.0.0.1:5173`，代理 `/admin/*` API 到后端。页面走 Hash 路由：`/admin/ui/#/dashboard`。

```bash
VITE_DEV_API_TARGET=http://127.0.0.1:9000 pnpm dev
```

## 构建

```bash
pnpm build
```

产物在 `frontend/dist/`。通过 Makefile `frontend-build` 同步到 `internal/adminui/dist` 供 Go embed。

## 路由约定

- 静态壳：`/admin/` → index.html
- 静态资源：`/admin/ui/*`
- JSON API：`/admin/pool/stats` 等（与页面 path 同前缀，故 **必须用 HashRouter**）
