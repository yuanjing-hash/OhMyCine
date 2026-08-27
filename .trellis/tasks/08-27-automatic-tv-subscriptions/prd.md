# 自动电视剧订阅管线

## Goal

实现 MoviePilot 风格但符合 OhMyCine 现有安全与媒体流水线的电视剧订阅：用户在统一作品详情中一次选择一个或多个季，并检查/覆盖完整执行策略；Server 持久化版本化策略快照，定期对账已播缺集，自动聚合搜索、确定性选择资源，并复用既有下载、识别、Transfer、入库和媒体服务器刷新链路。

## Dependencies and confirmed facts

- 本任务必须在 `08-27-media-identity-search-coverage` 完成后实施，并复用其 `MediaIdentitySearch`、`MediaCoverage` 和稳定 `media_type + tmdb_id` 契约。
- 当前已有持久 Job/Worker/Scheduler、下载器、SiteService、DownloadService、TransferService、MediaLibrary catalog 和事件/任务中心。
- 当前权限目录已有 `follows.read_own/read_all/create`，但模型、服务、API、Worker 和真实页面不存在。
- 当前没有一个可安全解释的“全局默认下载器”作为运行时真相；创建界面可以从可用配置中预填，但订阅必须显式绑定下载器和目标媒体库。
- MoviePilot v3 的有效产品模式是季选择对话框、可编辑 sites/downloader/path/filter 等配置、`lack_episode` 进度、立即搜索与共享 Search/Download chain；本任务不复制其 GPL 代码。

## Requirements

### R1. MP 风格订阅创建与执行策略

- 电视剧详情提供季选择对话框：季海报、名称、TMDB 总集数、已入库/缺失/未来/未知、已订阅状态，允许一次选择多个季。
- 创建流程在提交前展示完整执行策略，并用当前可用默认/首选配置预填；用户可以覆盖：
  - 目标季（特别篇必须显式选择）；
  - 启用站点及站点优先级；
  - 下载器与目标媒体库；
  - 运行周期和下一次计划；
  - 分辨率、视频编码、来源/质量、发布组等包含规则；
  - 排除关键词/发布组规则、最小做种、最大资源年龄/大小等安全过滤；
  - 单次最多选择资源数和下载优先级。
- 保存后形成版本化执行策略快照。全局默认以后变化只影响新建订阅，不改变旧订阅；编辑生成新 revision，下一次运行生效。
- 快照只保存稳定 ID 和非敏感规则，不保存站点 Cookie/passkey、下载器凭据、真实 torrent/magnet URL、临时 token 或绝对路径。

### R2. 订阅生命周期和管理页

- 当前用户可以创建、读取、编辑、暂停、恢复、删除和立即搜索自己的订阅；有细粒度 all 权限的管理员可操作他人订阅。
- 管理列表/详情显示作品和季、状态、`已入库 / 已播目标 / 缺失`、最近运行、最近命中/提交、最近安全错误、配置 revision 和下次运行时间。
- 状态至少包括 `active | paused | completed | blocked`；单次运行有 `queued | running | no_match | submitted | completed | failed | cancelled/stale` 等可观察终态。
- 同一 owner、同一 TMDB 电视剧、同一季不能同时归属两个未删除订阅；并发创建由数据库约束阻止，不能只靠前端检查。
- 多季订阅仍是一个可编辑对象，但季 claim 可独立展示和对账；删除订阅不删除已提交下载或已入库媒体。

### R3. 调度与缺集对账

- Scheduler 只扫描到期 active 订阅并入队持久 `follow-search` Job；HTTP 创建/立即执行不直接运行 TMDB/PT 搜索。
- Job 使用订阅 ID、owner、启动 revision 和该次运行的不可变策略快照，并通过队列 coalescing/resource key 阻止同一订阅跨 revision 并发运行。普通编辑不改变已开始运行的规则；暂停或删除后，旧 Job 在外部调用和下载提交前停止。
- 每次运行重新验证 TMDB 身份、当前配置引用和媒体库权限，再用共享 coverage 服务计算已播缺集。
- 只有明确 `missing` 且已播出的目标集进入搜索；`present/future/unknown` 永不触发下载。Season 0 只有快照显式包含时参与。
- 没有缺集时更新进度并标记 completed；Scheduler 仍以较低成本按计划复核 completed 订阅，未来 TMDB 出现新的已播集后自动回到 active/pending。

### R4. 自动搜索、过滤和确定性选择

- Worker 复用子任务 1 的身份名称搜索，带入目标季集和快照站点顺序；不另写订阅专用标题搜索。
- 先验证作品/年份/季集覆盖，再执行快照的 include/exclude、质量、发布组、大小、做种、年龄等过滤。
- 排名必须稳定、可解释，建议顺序为：精确覆盖缺集且少重复 → 用户站点优先级 → 质量规则匹配 → seeders/健康度 → 发布时间 → 稳定资源键。
- 可以用一个全集/季包覆盖多个缺集，但不得因为包内包含已入库集而创建重复逻辑 claim；单集和多集资源选择使用集合覆盖，受单次资源上限约束。
- 普通排序并列由稳定资源键确定性裁决；只有身份/季集不确定、配置无效、覆盖率未知或候选无法证明覆盖目标集时才 blocked/failed，不静默随机下载。
- `no_match` 不是永久失败：记录过滤摘要并按计划重试，不在日志保存原始敏感查询或下载地址。

### R5. 下载交接和幂等

- 每个待覆盖集建立持久幂等关联；已有入库事实、活动 follow claim 或匹配的活动下载任务时不重复提交。
- 资源来源必须通过 SiteService 的服务端私有结果/claim 解析，随后调用现有 `DownloadService`；订阅 Worker 不直接请求 torrent、调用下载器或移动文件。
- 下载提交携带已由 TMDB `GetByID` 复验的内部 `RecognitionOverride`、owner、下载器、目标媒体库和优先级，使后续仍走 recognition、Transfer、import、STRM/refresh/notify。
- 下载/Transfer/媒体库事件和周期性 reconciliation 更新 episode claim 与订阅进度；任务失败可在下一次安全重试，但不能制造同集并发任务。
- 已提交任务不因订阅暂停/删除而自动取消；用户在下载管理中继续显式控制它。

### R6. 权限、安全、审计和可观察性

- 补充 follows 更新、删除、执行的 own/all 权限；所有列表、详情、mutation、Job 和事件都按 owner/permission 过滤。
- 创建/编辑时以及每次运行前验证站点、下载器、目标媒体库、摄取和权限可用；失效后 blocked 并给出不含敏感值的可操作错误。
- 审计记录创建、编辑、暂停、恢复、删除和立即执行，仅含资源 ID、owner、安全配置摘要/revision 和结果。
- 事件至少覆盖 `follow.running`, `follow.missing_found`, `follow.download_submitted`, `follow.no_match`, `follow.blocked`, `follow.completed`，并限制高频/按权限过滤。
- API/Job/日志/事件不暴露 Cookie、passkey、真实 torrent URL、下载器凭据、绝对路径、临时地址或上游响应体。

## Acceptance Criteria

- [ ] 详情页能选择一个或多个季，并在创建前配置站点、下载器、目标库、周期、质量/包含/排除、资源上限和优先级。
- [ ] 新订阅保存完整可编辑 revision 快照；修改全局默认不改变旧订阅，编辑后的 revision 只影响后续运行。
- [ ] 同一 owner/剧/季的并发重复创建被数据库级约束阻止。
- [ ] 管理页可查看进度和安全运行摘要，并可编辑、暂停、恢复、删除和立即搜索。
- [ ] Scheduler/立即搜索只入队 Job，不在 HTTP 请求中执行长耗时搜索；同一订阅没有并发运行。
- [ ] Worker 只把明确已播缺集作为目标，忽略 present/future/unknown；Season 0 只在显式选择时处理。
- [ ] 资源按快照过滤并确定性排序；全集/季包能覆盖多个缺集，重复运行不会重复下载同一集。
- [ ] 没有合格结果时保持活动并显示 no_match；身份歧义、未知覆盖或配置失效时 blocked，不下载错误资源。
- [ ] 自动提交复用现有 SiteService/DownloadService/Transfer/import/refresh 链路，下载与任务中心可追踪。
- [ ] 下载完成并重新扫描后订阅进度更新；季补齐后 completed，新已播集出现时可恢复待搜索。
- [ ] 普通用户不能读写他人订阅；API、日志、Job 和事件无凭据、真实下载 URL 或绝对路径泄漏。
- [ ] 新迁移 fresh/upgrade/idempotency、Go 服务/Worker/RBAC、Vue 页面和全量质量门通过，架构/roadmap 同步。

## Out of Scope

- 不实现洗版、最佳版本升级、分集质量升级、订阅分享、热门订阅、音乐或字幕订阅。
- 不自动取消用户已经提交的下载，不自动删除文件或已入库媒体。
- 不新增站点、下载器协议或全局规则编辑器；本期使用现有配置并保存订阅执行快照。
- 不支持标题模糊命中作为自动下载唯一授权，不处理 episode group/自定义季序。

## Key Decisions

- 全局默认只预填新订阅，保存后固化可编辑执行快照。
- 运行绑定启动 revision；配置编辑从下一次运行生效。
- 自动化 fail closed，安全歧义不随机下载。
- 自动下载只编排现有流水线，不建立旁路。
