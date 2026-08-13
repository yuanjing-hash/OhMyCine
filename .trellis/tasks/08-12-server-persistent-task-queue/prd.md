# Server 持久化任务队列、调度与任务中心

## Goal

为下载、上传/传输、刮削、刷新等离散重任务提供统一持久队列，并将“媒体自动化 → 任务中心”实现为全局队列控制台。用户可以查看所有有权访问的任务、处理等待操作、控制任务，并在同一任务类型与优先级 lane 内调整尚未开始任务的顺序。

媒体库 watcher、文件树 reconciliation 和 STRM/伴随文件 projection 继续由各媒体库 supervisor 独立并行运行，不进入该队列。

## Background

- Server 已有 SQLite、认证/RBAC、审计、结构化日志和媒体库 supervisor，可作为队列持久化、安全与边界基础。
- 下载器、DownloadRule、跨存储传输和网络 metadata 尚未实现；本任务提供可测试的 typed worker 与 fake worker，不伪造具体业务 adapter。
- 现有 `/automation/tasks` 是规划占位入口；本任务直接实现该入口，不新增语义重复页面。
- 不同 job type 拥有独立并发槽，因此不存在可严格兑现的跨类型全局绝对顺序。

## Requirements

1. 持久化 `Job`、`JobAttempt`、lease/heartbeat、checkpoint、resource keys、progress/telemetry、`ActionRequest` 与用户响应；SQLite 是事实源，内存仅保存运行时协调状态。
2. 状态机包含 `queued/running/waiting_user_action/retry_wait/paused/completed/failed/cancelled`。所有转换由 service 验证；关键控制、调序和响应操作写入审计。
3. 任务在用户确认开始时入队；草稿/预览不入队。每个 job type + priority 组成一个调度 lane，lane 内按人工顺序键与创建顺序执行。
4. 仅 `queued` 任务可以在相同 job type 与 priority lane 内重排。不得跨 lane 拖动；`running/waiting_user_action/retry_wait/paused/completed/failed/cancelled` 不可调序。
5. 调序使用 revision/expected-version 乐观并发控制和事务化稀疏顺序键。并发变化或任务已被 claim 时拒绝并要求刷新；不能静默覆盖其他管理员的排序。
6. job type 分别配置全局并发：至少覆盖 `download`、`transfer`、`upload`、`scrape`、`refresh` 和测试 worker；下载任务必须限制同时运行数量。
7. 额外按 downloader/provider/Storage/MediaLibrary resource key 控制并发和速率；受限 provider 不得占满其他 provider 的可运行槽。
8. Scheduler 用短事务 claim 一条满足 `queued + due + lane order + capacity` 的 Job，写入随机 lease token/expiry 后再交给 typed worker；不得在数据库事务中等待网络或限速 token。
9. worker heartbeat 延长 lease。Server 重启或 worker 失联后回收过期 lease，并从持久 checkpoint 幂等恢复；旧 lease token 不能提交新状态。
10. 需要用户选择时创建版本化、结构化 `ActionRequest`，Job 进入 `waiting_user_action`，提交 checkpoint 并释放 lease/worker slot。响应后重新入队，在执行前重验外部状态；旧版本响应不能覆盖新冲突。
11. `retry_wait` 使用持久 `next_attempt_at` 并释放 worker；429/风控按 provider 策略退避，不占槽 sleep。到期后回到原 job type/priority lane。
12. 支持暂停、恢复、取消和失败重试。取消排队、运行或等待任务默认不删除任何本地或云端文件；业务数据删除必须走独立高风险确认流程。
13. 任务中心统一展示有权访问的全部 Job，可按状态、job type、priority、resource/provider、创建人和时间筛选，并提供分页、详情、attempt/状态时间线和等待用户操作视图。
14. 列表展示真实 lane 位置、优先级、状态、进度、已处理/总量、速度、ETA、重试时间、最近错误与更新时间。未知值使用 `null/unknown`，不伪造为 0。
15. 页面允许有权用户在当前 lane 过滤视图中拖拽或使用键盘上移/下移 `queued` 任务；保存后以服务端返回顺序为准，并明确显示冲突刷新提示。
16. REST 是任务事实与控制的恢复入口；WebSocket 只推送权限过滤后的实时变化并进行进度事件节流，断线后通过 REST 恢复。
17. 权限区分读取、控制、响应等待操作和调序；管理员拥有全部能力，operator 默认可查看/控制/响应/调序，viewer 默认不可访问全局任务中心。service 仍执行 actor 与 owner 范围校验，不能依赖页面隐藏。
18. 日志、API、WebSocket、checkpoint 摘要和审计不得泄露凭据、绝对路径、Authorization/Cookie、上游 token URL 或任意 worker 私有 payload；任务详情返回显式安全字段。
19. 可合并 Job 使用 `(job_type, resource_key, coalescing_key)` 活跃约束与单调 dirty generation；运行期间出现的新 generation 不得丢失，完成时保留或创建 follow-up Job。
20. MediaLibrary watcher、定时增量/全量、文件树 reconciliation 与 STRM projection 不注册 typed worker、不创建 Job、不消耗 queue slot，也不受 lane 排序影响。

## Acceptance Criteria

- [ ] download lane 并发设置为 2 时最多两个下载 Job 同时 `running`，同 lane 其余任务按 priority + position 保持 `queued`；transfer lane 可独立运行。
- [ ] 三个相同 type/priority 的 queued Job 可通过鼠标拖拽和键盘重排，刷新与 Server 重启后顺序保持；不得拖入其他 type/priority lane。
- [ ] 调序过程中某 Job 被 claim 或 revision 改变时返回稳定冲突，页面刷新真实顺序且不覆盖其他管理员操作。
- [ ] Job A 进入 `waiting_user_action` 后释放槽，Job B 立即运行；响应 A 后从 checkpoint 重新入队，旧 action version 的重复响应被拒绝。
- [ ] `retry_wait` 不占槽且到期恢复；Server 重启可回收过期 lease并恢复未完成任务，旧 lease 无法提交。
- [ ] provider A 的并发/限速不阻塞 provider B 的可运行 Job。
- [ ] 任务中心可筛选全部有权任务，查看真实 lane 位置、进度/速度/ETA、attempt 时间线、错误和等待操作；未知 telemetry 显示“未知”。
- [ ] 暂停、恢复、取消、失败重试、ActionRequest 响应和调序均经过 API/service 权限与审计；取消不删除真实数据。
- [ ] WebSocket 与 REST/DB 最终一致，断线重连后页面通过 REST 恢复，没有重复或伪造状态。
- [ ] 同一媒体库突发 100 个文件事件不在 Job 表创建扫描任务；多个 LibrarySupervisor 仍可并行且不消耗队列槽。
- [ ] Windows `server/test.ps1`、Go race-relevant stress tests、Web UI test/typecheck/lint/build、嵌入式构建与隔离进程验收全部通过。

## Key Decisions

- 任务中心是统一观察和控制面，但不是跨类型串行执行器。
- 人工顺序仅在 `job_type + priority` lane 内生效；不同 lane 按各自容量并行运行。
- 仅 `queued` 可重排；暂停和 retry/waiting 状态不通过拖拽偷偷改变调度语义。
- SQLite/REST 是事实源，WebSocket 只做实时加速。
- 本任务实现 fake typed worker 验证队列，不提前实现 downloader/cloud/TMDB adapter。

## Out of Scope

- qBittorrent、Transmission、115 离线下载或其他真实 downloader/provider adapter。
- 真实跨存储字节传输、metadata 网络刮削、STRM/302 和媒体服务器刷新实现。
- 分布式多节点调度；首版为单 Server 进程，但 schema 不依赖纯内存队列。
- 跨 job type 的全局绝对执行顺序。
- MediaLibrary watcher、文件树 reconciliation、定时扫描和 STRM projection。
