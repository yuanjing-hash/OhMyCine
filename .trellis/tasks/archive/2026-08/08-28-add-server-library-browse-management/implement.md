# 实施计划

1. 扩展 catalog DTO/查询和测试，加入安全 artwork/overview/recognition projection，并实现按稳定作品身份去重的跨库分页。
2. 抽取推荐与 catalog 共用的详情展示组件和 API DTO 归一化；新增媒体库列表、详情路由、导航与“媒体库管理”文案。
3. 增加作品级 action resolver，复用现有候选、TMDB 覆盖、清除覆盖、重新刮削和 reorganization preview/confirm。
4. 抽取 destructive preview/token/root/provider/checkpoint 原语，新增 catalog deletion preview/confirm 与专用 RBAC。
5. 实现本地与 115 删除 executor、逐项恢复、catalog/manifest 收敛、STRM reconcile 和 Emby/Jellyfin refresh 调度。
6. 覆盖聚合分页、空投影、权限、token replay/expiry、revision drift、路径/Reparse escape、115 ancestry drift、missing、partial success 和审计脱敏测试。
7. 更新 OpenAPI、架构/安全/spec，运行 focused Go/WebUI tests、`go test ./...`、`go vet ./...`、`golangci-lint run`、typecheck、lint、build、`git diff --check` 与 Windows Server gate。

## 风险文件与回滚点

- `server/internal/services/media_catalog.go` 及新增 catalog action/deletion service
- `server/internal/services/transfer_deletion.go` 共享安全原语抽取
- `server/internal/handlers/media_libraries.go`、`server/internal/httpserver/router.go`
- `server/internal/authz` 权限 catalog/generated 文件
- `server/webui/src/media-catalog.ts`、导航、路由、列表/详情/共享组件

先交付只读浏览与元数据动作，再接入删除 executor；删除能力在完整安全测试通过前保持不可用，不以 UI 隐藏代替后端授权。
