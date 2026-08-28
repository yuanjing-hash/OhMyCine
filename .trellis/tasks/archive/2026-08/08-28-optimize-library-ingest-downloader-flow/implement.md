# Implementation Plan

## Phase 1: Contracts and migrations

1. 更新 backend specs，固化 Downloader Provider、Library Backend、DataSourceIdentity、TransferRoute 和 staging ownership 契约。
2. 添加 additive migrations、模型字段、route/version enum、同一 115 Connection 唯一默认媒体库约束和旧配置幂等回填。
3. 将 downloader 基础接口缩到 required contract，把 pause/resume/manifest/seeding/share 等拆为 capability interfaces；保持插件下载执行器独立。

## Phase 2: Source/target identity and route selection

4. 实现 Server-side `DataSourceIdentity` 构造与复验：local、同 115 Connection、不同 Connection 和未来 provider。
5. 实现单一 `TransferRouter`/route preview service，替换 download 创建、站点提交、115 分享、生活事件与自动追更中的目标类型硬编码及“排序第一”兜底。
6. 扩展任务快照，确保创建时固化 source/target identity、route、目标库策略和执行版本。

## Phase 3: Library Backend and executors

7. 从 MediaLibraryService 提取 `LibraryBackend` registry，先实现 LocalBackend 和 Pan115Backend，并把扫描、监听、导入、删除的 provider 分支逐步移入 backend/capability。
8. 抽取现有 local -> local 与同账号 115 -> 115 为两个同源 Transfer Executor，保持做种、batch intent、冲突和 provider 对账行为。
9. 将现有 115 UploadDriver 从 plugin-only 分支推广为受管 local -> 115 executor，并复用不确定上传结果对账。
10. 为 115 实现安全 SourceExporter/reader，完成 115 -> local staging 的分段下载、checkpoint、大小/SHA1 校验和临时 URL 内存边界。
11. 组合跨源 executor：115 -> local、未来 115 A -> 115 B；目标 B 未实现写入 capability 时保持不可选。

## Phase 4: Managed staging and pipeline

12. 把现有下载暂存目录升级为统一 managed working root，增加任务隔离分配、持久空间预留、并发 resource key、partial finalize 和 ownership 引用。
13. 将 ImportPipeline 明确拆为 Manifest、Route、Materialize、Verify/Recognition、Plan/Conflict、Execute、Reconcile、Artifact、Notify checkpoints。
14. 收敛取消、删除、完全删除、来源 move/copy 与 qBittorrent seeding cleanup，确保跨源任务不会扩大删除边界。
15. 统一 115 provider completion、生活事件和补偿扫描幂等域；迁移并移除 MediaLibrary 旧 ingest 入口。

## Phase 5: API and Web UI

16. 增加 route preview、空间状态、staging/materialize 阶段和 115 默认入库库 API/OpenAPI；保持安全响应 envelope 和 RBAC。
17. 更新新建下载、海报详情资源下载、直接搜索、自动追更与任务详情 UI：目标库由后端矩阵返回，支持快速理解同源/跨源及禁用原因。
18. 更新媒体库管理和下载器设置 UI，完成 Connection 唯一默认库控制、无默认禁止监听和切换/删除保护。

## Phase 6: Verification and docs

19. 单元测试：identity 等价、route matrix、capability selection、space reservation、checkpoint replay、provider ambiguity、cleanup/seeding ownership。
20. 集成测试：local -> local、local/qB PT -> 115、115 -> 同账号 115、115 -> local；覆盖重启、断网、空间不足、取消、删除、完全删除和上传不确定。
21. WebUI 测试：所有创建入口使用同一目标矩阵，订阅快照不回归，阶段与错误显示真实。
22. 更新 `docs/architecture/02-server-design.md`、`06-roadmap.md`、OpenAPI 和相关 Trellis specs。

## Phase 7: Media-type-first classification paths

23. 根据 MoviePilot 的 `type folder -> category folder` 结构，增加 Server 共享模板规范化器：电影固定根为 `电影`，剧集固定根为 `电视剧`；已存在正确根时不重复追加。
24. 把规范化接入 Profile 创建/复制/更新/默认值、MediaLibrary 策略同步、所有 DownloadTask 快照入口和 corrective reorganization，确保 provider executor 只执行统一计划。
25. 增加 additive migration，规范化 Profile 与 MediaLibrary 的未来任务模板，但不改写 DownloadTask/TransferTask 的历史快照和已入库文件。
26. 更新 Server WebUI 默认值与模板说明，明确固定类型根不可编辑、模板描述类型根内结构。
27. 补齐 movie/tv 同名分类、本地/115/跨源/插件/追更共享计划、季目录、已有根去重、历史任务快照和重复迁移回归。

## Validation commands

```powershell
cd server
go test ./...
go vet ./...
go mod verify
golangci-lint run

cd webui
npm run typecheck
npm run lint
npm run test -- --run
npm run build

cd ..
git diff --check
```

另运行仓库已有 Windows Server gate（若脚本存在）和针对 Transfer/Download/MediaLibrary 的 focused tests。不得通过删除或重置用户的运行配置来获得干净测试环境。

## Risky areas and rollback points

- Migration/route version：先验证旧任务只走旧同源路径，再启用新跨源 route。
- 115 SourceExporter：任何 DirectURL/header 泄漏、SHA1/size 不一致或 ancestry 变化都必须 fail closed。
- Space reservation：崩溃恢复必须回收陈旧 reservation，但不能删除仍有 owner reference 的文件。
- qB seeding：上传/本地入库成功不得提前清理做种源。
- Provider ambiguity：move/upload/delete 返回不确定时先对账，不盲目重试。
- WebUI rollout：后端 capability 未上线前不得解除前端限制。

## Pre-start checks

- `prd.md`、`design.md`、`implement.md` 与 specs 对同源/跨源定义一致。
- `implement.jsonl`、`check.jsonl` 均包含真实 spec/research 条目。
- 用户在看到最终规划摘要后的下一条消息明确批准实施。
