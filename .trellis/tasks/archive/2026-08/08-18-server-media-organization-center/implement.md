# Implementation Plan

1. [x] 新增 v16 migration、`TransferTask.PlanSummaryJSON` 及安全 plan-summary 类型/校验，补迁移、截断、路径清洗和历史空值测试。
2. [x] 新增 `transfers.read_own/read_all` 权限目录、Operator seed 与生成的 Web UI 权限常量，补 catalog/migration/RBAC 测试。
3. [x] 实现 TransferQueryService：own/all 数据边界、稳定分页、状态/媒体库/分类/方式/关键字筛选、可见范围统计与详情 allowlist 投影。
4. [x] 扩展 Transfer worker，在确定性规划后保存有界目标相对摘要，并在执行/完成时更新安全结果；验证重试幂等且不暴露源路径或 manifest。
5. [x] 增加 `GET /api/v1/transfers` 与 `GET /api/v1/transfers/:id` handler/router，复用 QueueService 详情、重试和 ActionRequest contract，补 API 参数、权限和敏感字段回归测试。
6. [x] 新增 Web UI transfer API/types 与 OrganizationView：四项统计、服务端筛选/分页、响应式列表、详情抽屉、失败重试和冲突响应。
7. [x] 将“媒体整理”导航改为正式页面和 transfer read 权限；下载列表增加“查看整理详情”深链，同时保留现有分阶段重试。
8. [x] 接入 Job WebSocket debounce 刷新和低频轮询，覆盖路由 query 恢复、stale action、权限可见性与失败提示的前端测试。
9. [x] 更新 `docs/architecture/08-server-web-ui-design.md` 与相关 Trellis spec，明确自动整理/文件管理边界、API 安全字段及权限模型；仓库若仍无 OpenAPI 文件则记录无需更新。
10. [x] 运行 Go 定向/全量测试、Web UI test/typecheck/lint/build、embedded build 与 Windows 隔离健康检查，不连接或修改用户真实 qBittorrent 任务和媒体目录。
11. [x] 增加终态 TransferTask 记录删除：服务层 owner/control 权限与状态门、事务审计和 Job 级联清理、DELETE API、桌面/移动/详情按钮、二次确认及跨层回归测试。
12. [x] 将下载管理拆为 URL 可恢复的顶部页签，新增完整流水线 active/history 查询与成功历史记录安全删除；媒体整理新增 active/history scope 和页签，并补服务/API/前端回归测试与文档。

## Verification

- `CGO_ENABLED=0 go test ./...`, `go vet ./...`, both Server builds, and both Go module verifies passed.
- Web UI permission drift check, 59 Vitest tests, typecheck, lint, and production build passed; terminal-only delete presentation and active/history scope switching are covered.
- Transfer deletion service tests passed for failed/cancelled/completed records, active-state rejection, own/all authorization, Job-history cascade, safe audit metadata, and preservation of DownloadTask/source/library files; HTTP regression covers CSRF and viewer denial.
- `server/test.ps1` passed its isolated health check and left no Server process behind.
- Isolated browser smoke passed for the download five-tab workspace and organization active/history tabs in light/dark and desktop/390 px layouts with URL restoration and no console warnings or errors; its temporary Server process and runtime directory were removed.
