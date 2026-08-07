# Server 一键启动与本地运行目录

## Goal

在 `server/` 内提供一个可直接执行的一键启动脚本，默认构建管理网页、生成带 Web UI 的 Server 二进制并以前台正式模式运行。数据库、二进制等运行产物统一保存在 `server/.runtime/`，可长期复用但不进入 Git。

## Requirements

* 新增可执行的 `server/start.sh`，从任意当前目录调用都能正确定位 `server/`。
* 默认执行完整启动：安装/复用 Web UI 依赖、构建 Vue 管理端、构建 `webui` tag Go 二进制、启动 Server。
* 默认运行目录为 `server/.runtime/`：
  * `.runtime/bin/ohmycine-server` 保存构建二进制。
  * `.runtime/data/ohmycine.db` 保存持久化 SQLite 数据。
* `.runtime/`、`webui/dist/`、`webui/node_modules/` 和数据库辅助文件不得进入 Git。
* 默认使用 `OMC_ENV=production`、`127.0.0.1:3000` 和对应 public origin；所有 `OMC_*` 环境变量均允许用户覆盖。
* 脚本必须以前台 `exec` 方式启动 Server，使 Ctrl+C、systemd 和容器信号可以直接到达 Go 进程。
* 支持 `--skip-build`，在二进制已存在时跳过前端/后端构建并快速重启。
* 支持 `--help`，说明运行目录、参数和环境变量覆盖方式。
* 自动探测 `go`、`npm`；若普通 PATH 无 Go，兼容当前 Linuxbrew Go 路径。缺少依赖时给出明确错误。
* 首次缺少 `node_modules` 或 lockfile 已变化时运行 `npm ci`；不要每次启动无条件重装依赖。
* 不自动删除、重置、迁移或覆盖现有数据库。
* 更新 `server/README.md`，把 `./start.sh` 作为推荐启动方式，并保留手动开发模式。

## Acceptance Criteria

* [x] `bash -n server/start.sh` 通过。
* [x] `server/start.sh --help` 可用且不产生运行数据。
* [x] 首次 `server/start.sh` 能构建前后端并从 `http://127.0.0.1:3000` 提供管理网页/API。
* [x] 第二次 `server/start.sh --skip-build` 能复用现有二进制和数据库。
* [x] 运行后产生的 `.runtime`、`dist`、`node_modules` 和 SQLite 文件均被 Git 忽略。
* [x] Go 测试/构建和前端权限检查/typecheck/lint/build 保持通过。

## Definition of Done

* Tests and builds pass.
* Script is executable and documented.
* Runtime data is persistent but Git-ignored.
* Existing Server security defaults and browser same-origin behavior remain unchanged.

## Out of Scope

* 不实现后台守护、PID 文件、自动重启、systemd unit、Docker Compose 或 Windows PowerShell 脚本。
* 不自动开放局域网/公网监听，也不自动配置 HTTPS 或反向代理。
* 不自动清理 `.runtime` 或数据库。

## Technical Approach

使用单一 Bash 入口，以脚本自身目录作为工作根。默认生产嵌入构建保证“一条命令、一个端口、一个进程”；开发调试仍可按 README 分别启动 Gin 和 Vite。运行数据与源代码通过 `server/.runtime/` 明确分离。

## Verification

* `bash -n server/start.sh`、`server/start.sh --help` 与 `git diff --check` 通过；帮助命令未创建 `.runtime`。
* 从 `/tmp` 以 `OMC_SERVER_PORT=43871` 完整启动，`GET /api/v1/health` 返回 success，管理网页 `/` 返回 HTTP 200，确认脚本 PID 为 `ohmycine-server` 后通过 SIGTERM 安全停止。
* 以 `--skip-build` 在端口 `43872` 再次启动；数据库 inode 与二进制修改时间保持不变，健康检查通过并安全停止。
* lockfile 未变化时再次完整启动，确认复用 `node_modules` 且未执行 `npm ci`。
* Server：`go test ./...`、`go vet ./...`、普通/`webui` tag 构建、`go mod verify`、`go list ./...` 通过。
* Web UI：权限检查、typecheck、lint、build、`go test .`、`go mod verify` 通过。
* `golangci-lint` 与 `shellcheck` 当前环境未安装；仓库未配置 golangci-lint 专用配置，分别由 Go 质量门禁与 `bash -n` 覆盖本次检查。

## Decision (ADR-lite)

**Context**: 当前验证需要两个终端，默认数据库位置也容易与测试/正式数据混淆。

**Decision**: `./start.sh` 默认采用嵌入式生产构建并在前台运行，所有本地持久化运行产物放在 `.runtime/`。

**Consequences**: 首次启动需要完成 npm/Go 构建，之后可用 `--skip-build` 快速启动；守护和公网部署留给后续专门任务。

## Technical Notes

* 相关规范：`.trellis/spec/backend/web-admin-guidelines.md`、`quality-guidelines.md`、`directory-structure.md`。
* 现有生产构建顺序：`npm run build` → `go build -tags webui`。
* 当前 Web UI 是独立 Go module，根 Server module 通过 local replace 引用。
