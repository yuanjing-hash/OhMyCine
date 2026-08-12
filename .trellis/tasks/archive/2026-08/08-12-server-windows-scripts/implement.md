# Implementation Plan

1. 调整 SQLite driver，使 Server 在 `CGO_ENABLED=0` 下可测试、构建和启动。
2. 修复权限生成器的跨平台换行比较，并添加回归验证。
3. 实现 PowerShell 公共工具链/路径安全模块和系统级 Go 安装流程。
4. 实现 Windows `start.ps1` 与 `test.ps1`。
5. 补充 `.gitignore`、`.gitattributes` 和 Server README 的 Windows 契约。
6. 运行 PowerShell parser、依赖模拟、前端质量门、`CGO_ENABLED=0` Go test/vet/build、真实健康检查与 `git check-ignore`。
7. 检查工作树，确保只包含任务内源码/文档和 Trellis 任务文件，不纳入任何运行产物。

## Validation Commands

```powershell
cd server
$env:CGO_ENABLED = '0'
go test ./...
go vet ./...
go build ./cmd/server
.\test.ps1
.\start.ps1 -Help
git check-ignore .runtime/windows/bin/ohmycine-server.exe .runtime/windows/data/ohmycine.db .runtime/windows/tests/example/data/test.db webui/node_modules webui/dist
git status --short
```

## Risk and Rollback Points

- SQLite driver change：用现有迁移、HTTP 集成测试和真实数据库启动验证兼容性；失败则回退 driver 变更，不以静默跳过数据库测试代替。
- Go bootstrap：只允许精确的官方 winget package ID `GoLang.Go`；安装失败或安装后版本仍不合格时立即失败，不下载替代二进制。
- Test cleanup：目录边界断言失败即停止清理并保留诊断数据。
