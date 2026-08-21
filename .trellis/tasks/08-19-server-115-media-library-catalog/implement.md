# 实施计划

## 1. Storage-scoped 115 directory picker

- [x] 扩展 provider directory token claims 与 service，支持从 Storage 根浏览、分页和选择。
- [x] 让 `/storages/{id}/directory` provider-neutral 地分派 local/pan115，并保持 no-store/RBAC。
- [x] 修复 `DirectoryPickerDialog` 后续导航 endpoint，使浏览始终限制在当前 Storage。
- [x] 创建/编辑 MediaLibrary 对 pan115 开放目录选择，提交 provider selection token。
- [x] 添加跨 actor/Connection/Storage、过期、越界和 local 回归测试。

## 2. Schema and scan root

- [x] 新增 v21 migration、model 字段、索引与旧 115 library/Entry 兼容回填。
- [x] handler/service 解析 selection token 并持久化 provider root ID；DTO/日志不暴露它。
- [x] 115 reconciliation 从 MediaLibrary provider root ID 起步，保留空值回退。
- [x] 添加子目录 scan root、旧库升级、partial preserve 和 local scan 回归测试。

## 3. Shared path-aware media parsing

- [x] 将 Player 已验证的季集格式和父目录上下文行为移植为 Go parser contract。
- [x] 扫描与本地文件事件统一写入 `work_key`、`series_title`、season/episode。
- [x] 覆盖中英季集命名、技术噪声、季度目录、episode-only 文件和未识别内容测试。

## 4. Paginated APIs and catalog read model

- [x] 实现统一 pagination parser，限制 page/page_size/media_type/query。
- [x] 将 raw entries 改为数据库 COUNT + LIMIT/OFFSET，并兼容 legacy limit。
- [x] 新增 catalog list/detail service、DTO、handler、router 和 RBAC tests。
- [x] 验证作品分页不会拆散 Series，过滤/搜索 total 正确，越界与非法参数行为稳定。

## 5. Server Web UI

- [x] 更新 API types 和媒体库 activity loader，分离 runs、catalog page 与展开详情。
- [x] 增加作品级表格、Series 展开、搜索、类型筛选、页大小和翻页控件。
- [x] 处理 abort/stale response、扫描轮询、库切换、空状态和错误状态。
- [x] 添加 Vitest 契约/纯函数测试，并检查窄屏文本、按钮和表格不重叠。

## 6. Documentation and verification

- [x] 更新 backend media-library spec、Server architecture/roadmap/Web UI 文档中的已实现契约。
- [x] 运行针对性 Go tests、Web UI tests/typecheck/lint/build。
- [x] 运行 `go test ./...`、`go vet ./...`、Server build 和仓库要求的 Windows `server/test.ps1` 可行子集。
- [x] 检查 git diff，只保留本任务改动并与现有 125 项并行变化协作，不回滚用户代码。

## Risky Files / Rollback Points

- `server/internal/database/migrations.go`: 只允许可重复验证的加法迁移；先跑 migration tests。
- `server/internal/services/provider_directory.go`: token scope 是安全边界；先完成 service tests 再接 handler/UI。
- `server/internal/services/media_library.go`: 扫描事务不得因 catalog 字段导致删除语义变化。
- `server/webui/src/components/DirectoryPickerDialog.vue`: local picker 是共享组件，必须保留 local 回归测试。
- 每个阶段完成后先跑对应测试；若某阶段失败，回退该阶段新增调用，不删除数据库列或用户数据。
