# OhMyCine Server

首个可运行版本提供独立 Web 管理端、首次 owner 设置、Cookie Session、CSRF、用户/角色/权限管理和审计基础。媒体连接、STRM、302 与刷新业务从下一纵向切片开始接入；当前界面中的这些入口会明确显示为规划状态。

## 本地开发

```bash
# 后端（需要 Go 1.22+）
go run ./cmd/server

# 另一个终端启动管理端
cd webui
npm install
npm run dev
```

浏览器访问 `http://127.0.0.1:5173`。Vite 将 `/api`、`/ws` 和 `/proxy` 代理到 `http://127.0.0.1:3000`。

默认数据库为 `./data/ohmycine.db`，首次访问会进入 owner 创建页。不要使用真实用户资料执行破坏性测试；需要空数据库时使用单独的临时 `OMC_DATABASE_PATH`。

## 生产单二进制方向

```bash
cd webui
npm ci
npm run build
cd ..
go build -tags webui -o ohmycine-server ./cmd/server
```

默认 Go 构建不引用 `webui/dist`，因此未构建前端时 `go test ./...` 不会因缺少 dist 失败。`webui` build tag 只应在 `npm run build` 成功后使用。

`webui/` 是独立的 Go 模块边界：它让根 Server 模块只编译嵌入适配代码，不会把前端 `node_modules` 中偶然携带的 Go 源码纳入 `go test ./...`。该边界不改变 npm/Vite 的使用方式。

## 配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `OMC_SERVER_HOST` | `127.0.0.1` | 监听地址 |
| `OMC_SERVER_PORT` | `3000` | 监听端口 |
| `OMC_DATABASE_PATH` | `./data/ohmycine.db` | SQLite 路径 |
| `OMC_ENV` | `development` | `development` / `production` |
| `OMC_PUBLIC_ORIGIN` | `http://127.0.0.1:3000` | 状态变更请求允许的精确来源 |
| `OMC_DEV_ORIGIN` | `http://127.0.0.1:5173` | 非生产 Vite 来源 |
| `OMC_COOKIE_SECURE` | 随 public origin 推导 | HTTPS 生产环境应为 `true` |

公网部署必须使用 HTTPS 反向代理并设置真实 `OMC_PUBLIC_ORIGIN`。

## 验证

```bash
go test ./...
go build ./cmd/server

cd webui
npm run typecheck
npm run lint
npm run build
```
