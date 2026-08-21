# OhMyCine Server

当前 Server 提供独立 Web 管理端、数据源与媒体库、下载/整理流水线、STRM 与签名 302、Emby 播放网关、用户权限、审计和结构化运行日志。

## 推荐启动方式（Windows PowerShell）

Windows 本地开发优先使用系统自带的 Windows PowerShell 5.1 或更高版本。可以从仓库根目录或 `server/` 目录调用：

```powershell
.\server\start.ps1
# 或：cd server; .\start.ps1
```

脚本先检查 Node.js/npm，并优先复用 PATH 中满足 `go.mod` 的 Go。Go 缺失或版本过低时，它通过 Windows Package Manager 精确安装官方系统级 `GoLang.Go` 包；安装可能显示 UAC，失败会明确终止，不会下载便携 Go、写入仓库工具链或调用 Docker。安装完成后只刷新当前脚本进程的 PATH。

如构建环境存在 `OHMYCINE_TMDB_READ_ACCESS_TOKEN` 或 `OHMYCINE_TMDB_API_KEY`（必须二选一），Windows/Linux 启动脚本会先移出子进程环境，再通过对应 Go linker `-X` 变量注入只读应用凭据；npm/Vite 与 Server 运行进程不会继承它，脚本也不会打印其值。这些变量只影响本次构建，不作为运行时部署配置。自编译未注入时仍可通过 Web 设置显式选择并保存自定义 Read Access Token/API Key，或由部署环境提供对应的 `OMC_TMDB_*` 变量。

首次运行按 lockfile 执行 `npm ci`，构建 Web UI 与带 `webui` tag 的 EXE，然后以前台方式启动；Ctrl+C 可停止。持久运行数据默认隔离在：

```text
server/.runtime/windows/bin/ohmycine-server.exe
server/.runtime/windows/data/ohmycine.db
server/.runtime/windows/config/server.json
```

Windows 可以把不敏感的监听配置写入 `.runtime/windows/config/server.json`：

```json
{
  "listen_host": "0.0.0.0",
  "port": 3000,
  "public_origin": "http://192.168.1.10:3000"
}
```

优先级为 `OMC_*` 环境变量 > `server.json` > 默认值。配置文件只接受上面三个字段，禁止放 API Key、Cookie、密码或其它凭据。`listen_host` 是进程绑定地址，`public_origin` 是 Web UI CSRF 精确来源，也是 STRM 与 Emby 网关对外生成地址的唯一全局来源。`0.0.0.0` 只能用于监听，不能作为 `public_origin`。

Web UI、STRM 和 Emby 302 网关共用主程序的 3000 端口，不需要为每个 Emby 单独开端口。同机 Emby 可以使用默认的 `http://127.0.0.1:3000` 网关地址；NAS 或其它 Player 跨设备访问时，应把 `public_origin` 配成 Server 实际可达的局域网 IP 或域名。

复用已有 EXE 快速启动以及查看帮助：

```powershell
.\start.ps1 -SkipBuild
.\start.ps1 -Help
```

完整 Windows 质量门使用每次唯一的临时目录和非默认端口，不读取或覆盖持久数据库：

```powershell
.\test.ps1
.\test.ps1 -CheckDependenciesOnly
.\test.ps1 -SkipWebUi -SkipHealthCheck
```

测试依次执行权限生成校验、前端测试/typecheck/lint/build、`CGO_ENABLED=0` 的 Go test/vet/build，以及真实 EXE 的 `/api/v1/health`。成功时只删除本次位于 `.runtime/windows/tests/` 下的目录；失败时保留日志、数据库和 EXE 并打印诊断路径。

## WSL/Linux 兼容启动

现有 WSL/Linux 入口继续保留，可执行一条命令安装或复用 Web UI 依赖、构建管理端、构建带内嵌 Web UI 的 Server 二进制，并以前台正式模式启动：

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

Windows 脚本使用 `.runtime/windows`，Linux 脚本使用 `.runtime`；两者都尊重下列显式环境变量。PowerShell 示例：

```powershell
$env:OMC_SERVER_PORT = '3300'
.\start.ps1
```

`start.sh` 只在对应环境变量未设置时提供以下正式运行默认值。用户显式设置的值会保留；未加方括号的 IPv6 监听地址会规范为 Go 所需的方括号形式：

| 环境变量 | `start.sh` 默认值 | 说明 |
|---|---|---|
| `OMC_RUNTIME_DIR` | `server/.runtime` | 脚本的二进制和数据运行目录 |
| `OMC_BINARY_PATH` | `.runtime/bin/ohmycine-server` | 构建或复用的 Server 二进制路径 |
| `OMC_SERVER_HOST` | `0.0.0.0` | 监听地址；仅表示绑定全部接口 |
| `OMC_SERVER_PORT` | `3000` | 监听端口 |
| `OMC_DATABASE_PATH` | `.runtime/data/ohmycine.db` | SQLite 路径 |
| `OMC_LOG_DIR` | `.runtime/logs` | 结构化运行日志目录；不通过 Web UI 暴露或修改绝对路径 |
| `OMC_CREDENTIAL_KEY_FILE` | SQLite 同目录的 `credentials.key` | Server 凭据 AES-256-GCM 主密钥文件；首次启动原子生成，不要提交或删除 |
| `OMC_CREDENTIAL_MASTER_KEY` | 未设置 | 可选的 Base64 编码 32-byte 部署主密钥；设置后优先于 key 文件，禁止写入日志或仓库 |
| `OMC_TMDB_READ_ACCESS_TOKEN` | 未设置 | 运行时部署级 TMDB Token；优先级低于 Web UI 中 AES-GCM 保存的自定义 Token，高于构建内置 Token |
| `OMC_TMDB_API_KEY` | 未设置 | 运行时部署级 TMDB v3 API Key；与 `OMC_TMDB_READ_ACCESS_TOKEN` 互斥，优先级相同 |
| `OMC_ENV` | `production` | `development` / `production` |
| `OMC_PUBLIC_ORIGIN` | `http://127.0.0.1:3000`（默认端口） | Web UI、STRM 与 Emby 网关使用的精确对外来源；不得使用通配监听地址 |
| `OMC_COOKIE_SECURE` | 随 public origin 推导 | HTTPS 生产环境应为 `true` |

例如在独立端口启动：

```bash
OMC_SERVER_PORT=3300 ./start.sh
```

手动开发模式仍使用 Server 内部默认值，并额外支持 `OMC_DEV_ORIGIN`（默认 `http://127.0.0.1:5173`）作为 Vite 开发来源。

TMDB 有效凭据优先级为：Web UI 加密自定义凭据 → 运行时部署凭据 → 正式构建内置应用凭据。每一级都显式区分 `read_access_token`（Bearer）和 `api_key`（v3 query），不按内容猜测；同一级的两个环境变量不能同时配置。清除自定义凭据会回到下一级；API 永远只返回 `custom/deployment/builtin/none` 来源与安全类型，不回显密文。默认 API 是 `https://api.tmdb.org/3`，仅 DNS、连接或超时错误回退 `https://api.themoviedb.org/3`；任何 HTTP 响应均不回退。自定义 API 和图片 HTTPS 前缀必须在设置页分别测试成功后才会保存。

脚本默认监听 `0.0.0.0`，但默认对外来源仍是回环地址。IPv6 地址可以写成 `::1` 或 `[::1]`，脚本会使用 Go 监听所需的方括号形式。监听 `0.0.0.0` 或 `::` 时，默认浏览器来源分别仍为 `http://127.0.0.1:<端口>` 和 `http://[::1]:<端口>`；从局域网主机名、域名或反向代理访问时，必须将 `OMC_PUBLIC_ORIGIN` 显式设为浏览器实际使用的精确来源。公网部署还必须使用 HTTPS 反向代理；不要把默认配置直接暴露到公网。

`OMC_RUNTIME_DIR`、`OMC_BINARY_PATH`、`OMC_DATABASE_PATH` 和 `OMC_LOG_DIR` 的相对路径均以 `server/` 为基准，因此从任意当前目录调用脚本时行为一致。只有默认的 `server/.runtime/` 运行目录由仓库规则自动忽略；若覆盖到仓库内的其它路径，请自行确认不会误提交运行数据。

运行日志默认同时写入 stdout 与 `runtime.jsonl`，单文件 20 MiB 后切割并 gzip 压缩；默认最多保留 10 个历史分片、30 天且总量不超过 500 MiB，任一条件先触发即清理最旧分片。管理员可以在日志中心调整安全范围内的策略，但物理目录始终由部署环境控制。运行日志与 SQLite 审计日志相互独立。

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
