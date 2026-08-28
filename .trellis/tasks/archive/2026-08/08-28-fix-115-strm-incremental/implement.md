# 实施计划

1. 为 signed proxy 增加内部签名 inspection/显式到期签发能力和 injected-clock 回归，保持外部路由格式兼容。
2. 添加 additive migration/model 字段，覆盖 fresh/upgrade/repeat 和旧行默认值。
3. 重构 artifact worker：一次加载 manifest、形成 desired map；对现有 STRM先验证并复用有效租约，续期窗口内才重签；provider binding rebind 不计文件更新。
4. 保留 NFO/JPG/source asset 的确定性 bytes fingerprint，并共享批量 manifest diff/短事务写入。
5. 在 media-library reconciliation 增加安全 schedule gate，保留显式运行和 pending change carry-forward。
6. 增加 unchanged 两 generation、provider rebind、新增、rename、complete delete、partial preserve、near-expiry renewal、旧 v1 惰性回填、key/origin 变化、unmanaged collision、cleanup replay 和批量 SQL 计数测试。
7. 同步 STRM 管理统计文案、OpenAPI、架构/安全/spec，并运行 focused tests、`go test ./...`、`go vet ./...`、`golangci-lint run`、WebUI test/typecheck/lint/build、`git diff --check` 与 Windows Server gate。

## 风险文件与回滚点

- `server/internal/services/signed_proxy.go`
- `server/internal/services/media_artifact.go`
- `server/internal/services/media_library.go`
- `server/internal/services/strm_management.go`
- `server/internal/models/models.go`
- `server/internal/database/migrations.go`

先完成“有效现有 URL 可复用”的 focused test，再引入 generation gate；若 gate 影响 pending media change，保留 correctness 修复并暂时关闭 gate，而不能回退为每轮改写。
