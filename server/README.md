# OhMyCine Server

首个可运行版本提供独立 Web 管理端、首次 owner 设置、Cookie Session、CSRF、用户/角色/权限管理和审计基础。媒体连接、STRM、302 与刷新业务从下一纵向切片开始接入；当前界面中的这些入口会明确显示为规划状态。

## 推荐启动方式

在 WSL/Linux 中执行一条命令即可安装或复用 Web UI 依赖、构建管理端、构建带内嵌 Web UI 的 Server 二进制，并以前台正式模式启动：

```bash
cd server
./start.sh
```

也可以从任意当前目录直接调用脚本，例如在仓库根目录运行 `./server/start.sh`。首次启动完成后访问 `http://127.0.0.1:3000` 创建 owner。脚本默认把可长期复用的运行文件放在：

```text
server/.runtime/bin/ohmycine-server
server/.runtime/data/ohmycine.db
```

这些运行产物均已被 Git 忽略。脚本不会删除、重置或覆盖已有数据库；需要空数据库做测试时，请显式指定一个独立的临时 `OMC_DATABASE_PATH`，不要清理现有运行目录。

仅需快速重启且二进制已经存在时，可以跳过前后端构建：

```bash
./start.sh --skip-build
```

使用 `./start.sh --help` 查看完整参数和环境变量说明。脚本最终使用 `exec` 前台运行 Go 进程，因此 Ctrl+C、systemd 或容器停止信号会直接到达 Server。

## 手动开发模式

```bash
# 后端（需要 Go 1.23+）
go run ./cmd/server

# 另一个终端启动管理端
cd webui
npm install
npm run dev
```

浏览器访问 `http://127.0.0.1:5173`。Vite 将 `/api`、`/ws` 和 `/proxy` 代理到 `http://127.0.0.1:3000`。

手动开发模式的默认数据库为 `./data/ohmycine.db`。不要使用真实用户资料执行破坏性测试；需要空数据库时使用单独的临时 `OMC_DATABASE_PATH`。

## 手动构建单二进制

日常启动优先使用 `./start.sh`。需要逐步执行生产构建时，可以运行：

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

`start.sh` 只在对应环境变量未设置时提供以下正式运行默认值。用户显式设置的值会保留；未加方括号的 IPv6 监听地址会规范为 Go 所需的方括号形式：

| 环境变量 | `start.sh` 默认值 | 说明 |
|---|---|---|
| `OMC_RUNTIME_DIR` | `server/.runtime` | 脚本的二进制和数据运行目录 |
| `OMC_BINARY_PATH` | `.runtime/bin/ohmycine-server` | 构建或复用的 Server 二进制路径 |
| `OMC_SERVER_HOST` | `127.0.0.1` | 监听地址 |
| `OMC_SERVER_PORT` | `3000` | 监听端口 |
| `OMC_DATABASE_PATH` | `.runtime/data/ohmycine.db` | SQLite 路径 |
| `OMC_ENV` | `production` | `development` / `production` |
| `OMC_PUBLIC_ORIGIN` | 按监听地址和端口生成 | 状态变更请求允许的精确浏览器来源 |
| `OMC_COOKIE_SECURE` | 随 public origin 推导 | HTTPS 生产环境应为 `true` |

例如在独立端口启动：

```bash
OMC_SERVER_PORT=3300 ./start.sh
```

手动开发模式仍使用 Server 内部默认值，并额外支持 `OMC_DEV_ORIGIN`（默认 `http://127.0.0.1:5173`）作为 Vite 开发来源。

脚本默认只监听本机。IPv6 地址可以写成 `::1` 或 `[::1]`，脚本会使用 Go 监听所需的方括号形式。若显式监听 `0.0.0.0` 或 `::`，默认浏览器来源分别仍为 `http://127.0.0.1:<端口>` 和 `http://[::1]:<端口>`；从局域网主机名、域名或反向代理访问时，必须将 `OMC_PUBLIC_ORIGIN` 显式设为浏览器实际使用的精确来源。公网部署还必须使用 HTTPS 反向代理；不要把默认配置直接暴露到公网。

`OMC_RUNTIME_DIR`、`OMC_BINARY_PATH` 和 `OMC_DATABASE_PATH` 的相对路径均以 `server/` 为基准，因此从任意当前目录调用脚本时行为一致。只有默认的 `server/.runtime/` 运行目录由仓库规则自动忽略；若覆盖到仓库内的其它路径，请自行确认不会误提交运行数据。

## 验证

```bash
go test ./...
go vet ./...
go build ./cmd/server
go build -tags webui ./cmd/server
go mod verify

cd webui
npm run permissions:check
npm run typecheck
npm run lint
npm run build
go test .
go mod verify
```
