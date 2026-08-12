# Windows 原生 Server 启动与测试脚本

## Goal

让只安装了 PowerShell 与 Node.js 的 Windows 用户，可以从仓库直接安装系统级 Go、构建、测试并启动 OhMyCine Server；所有运行数据、临时数据库和构建产物均不得进入 Git。

## Background

- 当前只有 `server/start.sh`，脚本面向 WSL/Linux，并主动排除 Windows `go.exe`、`node.exe` 与 `npm`。
- 当前 `gorm.io/driver/sqlite` 默认使用 `go-sqlite3`。Windows 上 `CGO_ENABLED=0` 时数据库启动失败；启用 CGO 又要求额外的 C 编译器。
- 当前 Windows checkout 将生成权限文件写成 CRLF，而生成器按 LF 字节比较，导致 `permissions:check`、typecheck 和 Web UI build 误报过期。
- `server/.gitignore` 与根 `.gitignore` 已忽略 `.runtime`、数据库、前端依赖和常见构建产物，但 Windows 子目录及其用途尚未形成明确契约。

## Requirements

1. 新增 Windows 原生 PowerShell 启动脚本：
   - 可从仓库根目录或 `server/` 调用。
   - 检测 Node.js/npm；缺失时给出明确错误，不静默修改系统。
   - 优先使用 PATH 上兼容的 Go；缺失或版本过低时，使用 Windows Package Manager 安装官方 `GoLang.Go` 系统包，明确报告 UAC/安装失败，不静默降级为仓库内工具链。
   - 安装完成后只为当前脚本进程刷新 PATH；永久系统注册由官方安装包负责。不调用 Docker。
   - 首次运行安装/复用 Web UI lockfile 依赖，构建 Web UI 与带 `webui` tag 的 Server EXE。
   - 默认绑定 `127.0.0.1:3000`，使用持久但隔离的 Windows 数据库目录，前台运行并可用 Ctrl+C 停止。
   - 支持跳过构建复用已有二进制，并提供帮助。
2. 新增 Windows 原生测试脚本：
   - 使用独立的 Windows 测试根目录，不读取或覆盖正式运行数据库。
   - 执行权限生成校验、前端单测/typecheck/lint/build、Go test/vet/build，以及隔离数据库的真实启动健康检查。
   - 每次健康检查使用唯一临时子目录和非默认可用端口；只清理自己创建且已经验证位于测试根下的目录，失败时保留诊断产物并打印路径。
   - 支持只检查依赖/跳过较慢步骤的参数，并以非零退出码报告失败。
3. 移除 Windows 对 CGO/C 编译器的硬依赖，同时继续使用 GORM + SQLite，并保持现有数据库/API 行为。
4. 修复权限生成校验的 CRLF/LF 跨平台误报，不掩盖真实内容漂移。
5. 明确并验证 Git 忽略规则覆盖以下内容：
   - Windows 缓存和构建二进制；系统级 Go 不属于仓库产物。
   - 持久运行数据库、WAL/SHM、日志。
   - 每次测试的临时数据库、进程输出和诊断目录。
   - Web UI `node_modules` 与 `dist`。
6. 更新 Server README，Windows/PowerShell 作为本地开发首选入口，保留现有 WSL/Linux 脚本。

## Acceptance Criteria

- [ ] 在 PATH 中有 Go 的 Windows 环境运行启动脚本，可构建单 EXE、启动 Server，并通过 `/api/v1/health`。
- [ ] 在脚本模拟的“PATH 中无 Go”场景，脚本能生成正确的 `winget install --id GoLang.Go` 安装流程；安装后可发现系统 Go 并完成构建，不要求 C 编译器。
- [ ] 测试脚本能在隔离目录运行全部前后端质量检查和真实健康检查，不接触持久运行数据库。
- [ ] `CGO_ENABLED=0 go test ./...` 与 `CGO_ENABLED=0 go build ./cmd/server` 通过。
- [ ] Windows CRLF checkout 下 `npm run permissions:check`、typecheck、lint、test、build 通过。
- [ ] `git check-ignore` 能确认所有 Windows 运行/测试产物路径被忽略，`git status` 不出现这些产物。
- [ ] 脚本默认仅监听 localhost，不含默认账号、密码或秘密，不输出敏感环境变量。
- [ ] 文档给出启动、测试、复用构建与隔离目录说明。

## Out of Scope

- 安装或配置 Docker、WSL，以及除官方 Go 之外的系统开发工具。
- 将 Server 注册为 Windows Service、开机启动或开放防火墙。
- 实现尚未落地的 Connection、STRM、302 或媒体流水线功能。
- 修改现有持久 Server 数据或迁移用户的外部数据库。
