# 实施计划

## 前置门

- [ ] `08-27-media-identity-search-coverage` 已完成并归档。
- [ ] 重新核对实际 `MediaIdentitySearch`、`MediaCoverage`、SiteService private result 和详情季卡契约；若与本设计实质不同，先更新本任务规划并重新走批准门。
- [ ] 确认数据库迁移 head、OpenAPI 是否存在、当前权限生成流程和 queue policy 注册方式。

## 有序步骤

1. 定义执行策略、状态机、safe DTO、filter/ranking domain types 和验证器，先写纯函数测试。
2. 增加 additive follow migration、模型、索引/唯一约束、fresh/upgrade/idempotency 测试。
3. 扩展 follows 权限目录、角色种子、生成的前端权限和权限一致性测试。
4. 实现 FollowService：defaults、create/list/get/update/delete、pause/resume、revision CAS、owner scope、配置引用验证和审计。
5. 注册 `follow-search` queue policy、worker 和 scheduler due-scan；入队时固定 run revision/策略快照，实现 manual search 幂等入队，以及暂停/删除后的安全停止。
6. 实现缺集 reconciliation，严格复用共享 MediaCoverage；覆盖 Season 0、future、unknown、completed→active 收敛。
7. 复用共享 identity resource search，实现 snapshot filters、季集严格匹配、稳定 ranking/set-cover 和安全 no_match/blocked 摘要。
8. 实现 episode claim 与现有 SiteService private resolve/DownloadService submit 交接，补 crash/retry/并发幂等测试。
9. 连接 download/transfer/media catalog 事件或安全 reconciliation，更新 claim、progress、completed 和事件投影。
10. 添加 follows handler/routes、标准 envelope、分页/action API、RBAC/CSRF/安全错误和 router 集成测试。
11. 实现详情多季选择和策略编辑对话框，替换订阅管理占位页，补 typed client/helper/component/view 测试。
12. 完成跨层、脱敏、调度恢复和真实 pipeline 集成测试；更新架构/roadmap 和存在时的 OpenAPI。

## 重点文件/模块

- `server/internal/database/migrations.go` 和新迁移测试
- `server/internal/models/`
- `server/internal/authz/catalog.json` 及生成物
- `server/internal/services/` 中 follow service/worker/scheduler 注册
- `server/internal/services/site.go` 的内部安全交接面
- `server/internal/services/download.go` 的既有内部 identity submit 面（优先复用，不放宽公开 API）
- `server/internal/handlers/`、`server/internal/httpserver/router.go`
- `server/webui/src/navigation.ts`、router、permissions
- `server/webui/src/views/DiscoveryDetailView.vue` 和新的 follow views/components/helpers

不要在 FollowWorker 中复制 TMDB 名称、media coverage、torrent 获取、下载器客户端、Transfer 或入库实现。

## 验证

```powershell
cd server
go test ./internal/database ./internal/authz ./internal/services ./internal/handlers ./internal/httpserver
go test ./...
go vet ./...
cd webui
npm test
npm run typecheck
npm run lint
npm run build
```

必要的定向回归：

- migration fresh/previous-head/idempotent/unique/FK/check。
- 快照默认预填、保存不漂移、revision CAS、运行中编辑继续使用旧 run 快照、暂停/删除使运行安全停止。
- owner/read_all/update/delete/execute 权限矩阵和事件隔离。
- due scheduler/manual execute coalescing、lease 恢复和同一订阅不并发。
- present/future/unknown/Season 0/新集出现的缺集状态机。
- include/exclude、质量、站点优先级、stable tie-break、季包 set-cover、资源上限。
- no_match/blocked/config invalid/partial site failure。
- crash-before-submit、crash-after-submit、活动下载和入库后重复运行不重复提交。
- 自动任务实际进入 DownloadService，并能沿 Transfer/import/refresh 状态收敛。
- API/DB/Job/event/log 不含 credential/torrent URL/absolute path。
- 详情订阅表单、管理页 actions、revision conflict、响应式和键盘交互。

## 回滚点与完成门

- Worker 注册和 scheduler 扫描是首要 feature-off 点；停用后不能继续创建自动下载。
- 数据 migration 不回滚删除；保留订阅/运行记录，确保之后版本可恢复。
- 删除订阅测试必须证明下载任务和媒体文件未被级联删除。
- 所有 acceptance criteria 有测试映射，全量 Server/Web UI 质量门通过，架构/roadmap 同步后才完成。
