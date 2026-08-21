# Server 媒体整理任务中心

## Goal

将已经落地的“下载完成 → 复核识别/刮削 → 重命名/目录规划 → 转移入库 → 做种衔接/媒体库对账”从下载页的附属状态提升为独立的“媒体整理”工作区。用户能看懂每个整理任务处理了什么、当前在哪个阶段、为什么失败，并能对失败阶段执行安全重试。

## Confirmed Facts

- 侧栏已有 `/automation/organization` 占位页，当前由 `categories.read` 临时控制并标记 `planned`。
- 产品文档已定义媒体整理负责“分类规则应用结果、元数据匹配、命名/转移记录、失败重试和待人工处理项”；规则定义与存储目标配置不属于本页。
- `DownloadTask` 已持久分类/刮削摘要，`TransferTask` 已持久目标媒体库、manifest 文件数、阶段、处理数和错误码，并关联独立 transfer Job。
- 全局任务中心已提供 Job 详情、attempts、timeline、失败重试和 WebSocket 事件；媒体整理应复用这些事实，不再建第二套通用队列。
- 公开 DTO 不得暴露 staging/Storage 绝对路径、provider task ID、manifest 原始文件名或任意内部 JSON。

## Requirements

1. 打开侧栏“媒体整理”，使用真实页面替换 PlannedView，并使用与 download/transfer 所有者可见性一致的 read-own/read-all 权限，不再借用 `categories.read`。
2. 列表以一条 TransferTask 为一条媒体整理记录，展示安全标题、来源下载器、识别结果、分类、目标媒体库、move/copy/symlink、冲突策略、当前阶段、文件进度、安全错误和时间。
3. 提供状态、分类、目标媒体库和入库方式筛选，以及标题关键字搜索；分页和总数由 Server 处理。
4. 点击记录打开详情抽屉，展示不可变任务快照、分类/元数据匹配摘要、命名模板结果的安全相对摘要、Job attempts/timeline、冲突等待选项以及错误原因。
5. 失败 transfer 可在列表和详情中重试，仅重试原 transfer Job；待冲突确认可在详情内响应，两者均复用 QueueService 的权限、revision 和 action contract。
6. 顶部展示处理中、待处理、失败、今日完成统计；通过现有 Job WebSocket 事件触发刷新，并保留低频轮询收敛。
7. 下载页仅保留整理阶段摘要、“重试入库”快捷入口和“查看整理详情”跳转；完整 attempts/timeline 不在下载页重复实现。
8. 媒体库 watcher/reconciliation 仍不进持久 Job 队列；整理页可展示目标库 dirty/reconciliation 摘要，但不伪造一条扫描 Job。
9. 失败、已取消或已完成的整理记录可以在列表和详情中删除；删除仅清理 TransferTask、对应 transfer Job 及其 attempts/timeline/actions，不删除下载记录、下载源文件或媒体库文件。运行中、等待操作、暂停和等待重试的任务必须先进入终态。
10. 下载管理不再把新建、实时任务、历史、做种和下载器配置纵向平铺。顶部使用可恢复的管理页签切换；下载终态及完整后续流水线已收口的记录进入历史，主列表只保留仍在执行或需要处理的任务。媒体整理同样拆分“进行中/历史记录”，历史保留重试与安全删除操作。

## Acceptance Criteria

- [x] `/automation/organization` 不再显示占位页，获权用户可查看自己/全部媒体整理记录。
- [x] 至少能按状态、目标库、分类和入库方式筛选，并显示分页总数与四项顶部统计。
- [x] 任务详情包含路由/识别/转移摘要、attempts/timeline 和安全错误，不包含绝对路径、provider ID、凭据或原始 manifest。
- [x] 失败入库从列表或详情重试后只重排 transfer Job；已完成的 download Job 不重新执行。
- [x] 冲突等待任务能在详情中响应当前有效选项，过期 revision 被拒绝并刷新。
- [x] 下载页可跳转到对应媒体整理记录，并保留现有分阶段重试。
- [x] 终态整理记录提供明确的删除按钮和二次确认，删除后立即从列表消失；非终态删除被服务端拒绝，且任何真实媒体文件都不受影响。
- [x] Go 服务/API/RBAC 测试、Web UI 测试/typecheck/lint/build、embedded build 和 Windows 隔离健康检查通过。
- [x] 下载管理提供进行中、历史记录、新建下载、做种管理和下载器管理页签，默认不再显示已完整收口的任务。
- [x] 下载历史支持失败/取消任务原有的数据删除，以及成功收口任务的“仅删除历史记录”；后者不得操作 qBittorrent 或真实文件。
- [x] 媒体整理提供进行中/历史记录页签，终态任务进入历史并继续提供详情、重试和删除记录。

## Out of Scope

- 本任务不实现 STRM 投影、302 代理、Emby/Jellyfin 刷新或网盘转移驱动；未来以后续 stage 接入同一详情模型。
- 不在本页编辑分类 Profile、MediaLibrary 或 Storage 配置。
- 不替代全局任务中心的所有 Job 类型、lane 排序和管理员队列策略。
- 不提供“手动新建整理任务”入口；手动选择文件、发起整理及文件操作统一归属后续“文件管理”页面。
