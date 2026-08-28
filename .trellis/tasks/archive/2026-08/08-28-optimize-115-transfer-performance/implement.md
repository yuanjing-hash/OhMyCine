# 实施计划

1. 在 `pkg/cloud` 定义可选批量 mutation 接口和有界结果类型；为 115 client 增加批量 move/copy/recycle，保留共享风险控制、context cancellation 和敏感数据边界。
2. 扩展 `transfer_cloud.go`：构建一次来源证明、目标目录 DAG 和父目录 listing cache；按目标父目录/chunk 形成私有 batch intent。
3. 实现调用前 intent checkpoint、调用后批量核对和 per-item 收敛；覆盖 provider 成功后进程中断的重放。
4. 将 `transfer_cleanup.go` 的 115 清理改为共享 package-root/unique-parent 证明和有界 `RecycleMany`，保持 protected leftovers 与 exact identity 校验。
5. 减少每文件完整 JSON checkpoint；按 batch/时间心跳，添加安全聚合调用数和耗时指标。
6. 增加 28 文件/4 父目录 fixture、批量歧义、partial、405/429、崩溃恢复、清理 identity 变化和序列化脱敏测试。
7. 运行 focused Go tests、`go test ./...`、`go vet ./...`、`golangci-lint run`、WebUI 检查（若 DTO/阶段文案变化）、`git diff --check` 与 Windows Server gate。

## 风险文件与回滚点

- `server/pkg/cloud/client.go`
- `server/pkg/cloud/pan115/client.go`
- `server/internal/services/transfer_cloud.go`
- `server/internal/services/transfer_cleanup.go`
- 对应 fake、migration/private state 与测试文件

每完成接口、worker、cleanup 三个阶段分别运行 focused tests；任何阶段失败都可回退到 capability-missing 的 singleton 路径，不允许跳过边界验证。
