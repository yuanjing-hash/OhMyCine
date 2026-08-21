# Design: Server 媒体整理任务中心

## Product Boundary

“媒体整理”是下载流水线完成下载后自动生成的 TransferTask 领域视图，不是文件浏览器，也不创建手动整理任务。用户在这里查看“识别/分类 → 命名规划 → 转移入库 → 对账”的结果并处理失败或冲突；手动选择文件和发起整理留给后续“文件管理”。

全局“任务中心”继续拥有通用 Job、lane、优先级和调度控制；“媒体整理”只组合 TransferTask、对应 DownloadTask 和 transfer Job 的安全业务摘要，不复制队列事实。

## Data Flow

```text
download completed
  -> immutable DownloadTask routing/classification snapshot
  -> TransferTask + transfer Job
  -> plan safe destination-relative names
  -> transfer / wait for conflict decision
  -> library reconciliation
  -> organization list + detail projection
```

TransferTask 是列表主记录。详情按 `TransferTask.JobID` 复用现有 Job attempts、timeline、ActionRequest、retry 与 WebSocket event contract。下载页通过 `transfer_task_id` 或 transfer Job 映射跳转，不执行第二次查询拼接私有字段。

## Persistence

新增显式 v16 migration，为 `transfer_tasks` 增加 `plan_summary_json`。该字段只持久化有限、可公开的目标相对规划结果：媒体类型、目标相对目录/文件名、动作与结果状态；不得保存源绝对路径、目标绝对根、provider task ID 或原始 manifest。

约束：

- 只保留前 100 项文件摘要，并返回 `total_files` 与 `truncated` 表明是否截断。
- 单项相对路径规范化为 `/` 分隔，拒绝绝对路径、`..`、控制字符和超长片段。
- 序列化后设置总大小上限；超限时继续截断，不把原始规划 JSON 直接交给 API。
- 历史任务字段为空时仍可显示既有阶段、数量和错误，不尝试从私有 manifest 反推公开文件名。

## Authorization

新增稳定权限：

```text
transfers.read_own
transfers.read_all
```

路由与服务层都执行所有者边界；`read_all` 可见全部，只有 `read_own` 时强制 `owner_id = actor.User.ID`。Operator migration seed 获得 `transfers.read_all`，普通自定义角色保持显式授权。导航不再借用 `categories.read`。

操作不新增重复权限：失败重试沿用 `jobs.control_own/jobs.control_all`，冲突响应沿用 `jobs.respond`，并继续由 QueueService 校验 Job 所有者、状态和 ActionRequest revision。

## API Contract

新增：

```text
GET /api/v1/transfers
GET /api/v1/transfers/:id
```

列表查询参数：`status`、`library_id`、`category`、`transfer_mode`、`keyword`、`page`、`page_size`。所有参数白名单校验，`page_size` 有上限；默认按 `created_at desc, id desc` 稳定排序。

列表响应：

- `items`: transfer ID、owner ID、download task ID、job ID、显示标题、下载器/来源类型、识别状态/标题/媒体类型/分类、媒体库 ID/名称、transfer mode、conflict policy、phase、Job status、processed/total、safe error code/message、created/updated/finished time。
- `pagination`: page、page size、total。
- `stats`: 当前可见范围内的 processing、waiting_action、failed、completed_today；筛选只影响 items，不改变顶部可见范围统计。

详情在列表 DTO 基础上增加不可变规则/路由修订摘要、安全 `plan_summary`、Job attempts/timeline 和当前 ActionRequest。服务只组装明确 allowlist；不得直接序列化 DownloadTask、TransferTask、Job 或任意 JSON 字段。

重试与冲突确认继续调用现有：

```text
POST /api/v1/jobs/:id/retry
POST /api/v1/jobs/:id/actions/:version/respond
```

终态记录清理新增：

```text
DELETE /api/v1/transfers/:id
```

删除复用 `jobs.control_own/jobs.control_all`，服务层再次校验 owner 与 Job 状态。只允许 `failed`、`cancelled`、`completed`；事务内先写不含路径的 `transfer.delete` 审计，再删除 TransferTask 和对应 transfer Job，依靠 Job 外键级联清理 attempts/timeline/actions。DownloadTask、下载源文件、媒体库目标文件和做种记录均不在该操作范围内。

## Service and Query Design

新增 TransferQueryService，职责仅为授权后的领域投影与筛选：

- 用受控 join/batched lookup 获取 TransferTask、DownloadTask 与 Job 摘要，避免每行 N+1。
- phase 和 Job status 联合映射为 UI 状态组：processing、waiting_action、failed、completed。
- keyword 只查询允许公开的显示标题/刮削标题，不查询路径或 provider ID。
- `completed_today` 以 Server 本地配置时区的当日起点转换到 UTC 查询；若当前没有时区设置，则显式使用 Server 运行时本地时区并记录测试边界。
- safe error message 使用现有公开错误字段/映射，不返回 raw OS error。

Transfer worker 在计划确定后、实际写入前保存安全 plan summary；重试应覆盖同一任务的摘要并保持结果幂等。完成时标记每项结果，失败时至少保留已规划摘要和安全阶段错误。

## Web UI

新增 `OrganizationView.vue` 与对应 API/types：

- 顶部四张简洁统计卡：处理中、待处理、失败、今日完成。
- 工具栏提供状态、媒体库、分类、入库方式和标题搜索，筛选/分页写入 URL query，刷新和返回时可恢复。
- 主列表以桌面表格呈现关键信息，小屏降级为卡片；失败行可直接“重试入库”。
- 点击行或“查看详情”打开侧边抽屉，分为概览、识别与命名、执行记录、待处理操作四区。
- 失败、已取消和已完成记录在列表、移动卡片和详情中显示“删除记录”；确认文案明确不会删除下载内容或媒体库文件。
- 冲突操作在抽屉内展示服务端返回的有效选项；提交 stale revision 后提示并重新加载详情。
- 复用全局 toast；Job WebSocket 事件若命中当前列表中的 job ID 或类型为 transfer，则 debounce 刷新，另以低频轮询保证最终收敛。

下载页保留阶段摘要和“重试入库”，并在存在 TransferTask 时增加“查看整理详情”链接到 `/automation/organization?task=<id>`。页面打开后自动定位并打开详情抽屉。

### Lifecycle tabs and history

下载管理顶部使用现有 `management-tabs` 视觉/可访问性契约，将 `进行中`、`历史记录`、`新建下载`、`做种管理`、`下载器管理` 作为互斥 panel；当前 panel 写入 URL query，刷新后可恢复。API 的 `scope=active|history|all` 在 owner/all 权限范围内先按完整流水线状态筛选、再应用 limit，不能只在浏览器隐藏前 100 条中的终态记录。

下载历史仅包含已经收口的事实：取消任务，或 download Job 完成且 transfer/seeding 后续不存在或均成功完成。下载失败、入库失败、做种失败以及仍排队/等待/暂停的后续任务继续留在“进行中”，避免错误被历史页掩盖。成功历史的删除是记录级清理：事务内删除 DownloadTask、关联 TransferTask/SeedingTask 及三类 Job 执行历史，保留 provider、暂存和媒体库文件；失败/取消下载继续使用既有 provider-first `deleteData=true` 破坏性删除。

媒体整理列表增加 `scope=active|history|all`。active 包含非终态 Job，history 包含 `failed|cancelled|completed`；已有状态筛选继续在 scope 内叠加。历史表格/移动卡片/详情保留原有安全删除和失败重试。

## Security and Observability

- API DTO、日志、审计和前端状态均不包含 staging/storage 绝对路径、provider ID、manifest、凭据、任意私有 payload/checkpoint 或未清洗错误。
- plan summary 路径在写入和读取两端校验，防止历史脏数据直出。
- 列表、详情、重试和冲突响应覆盖 own/all/forbidden 测试。
- 记录详情查看不新增高噪声审计；重试和冲突响应继续由 QueueService 写已有审计/事件。

## Compatibility and Rollback

v16 为可空/默认空的增量列，历史数据无需回填。先落 migration/service/API，再切换前端路由；任一阶段失败时旧 transfer pipeline 仍能工作。前端回滚只需恢复 PlannedView，新增只读 API 和空摘要列可安全保留。
