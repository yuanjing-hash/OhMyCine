# Design: Server Storage、媒体分类方案与媒体库

## Domain Model

```text
Connection (外部服务接入和凭据)
    ↓ optional
Storage (Server 可访问的物理存储根)
    ├── MediaLibrary (选择一个 Storage + 相对根，负责扫描/分类/刮削)
    └── StorageDestination (未来自动化流水线的最终写入目标)

MediaClassificationProfile (可复用逻辑分类方案)
    ↑ MediaLibrary selects one
```

`Storage` 与旧设计的 `StorageDestination` 不同：Storage 只描述物理访问能力和根边界；Destination 以后引用 Storage，并增加转移/命名/STRM 等流水线语义。

## Storage Contract

- `storages` 首版只支持 `type=local`，字段包括 ID、名称、类型、规范化根路径、启用状态、创建/更新时间。
- 本地 Storage 不需要 Connection；未来 cloud Storage 必须引用 Connection。
- Windows 根支持盘符绝对路径与 UNC。所有路径操作先规范化、校验存在/目录类型、检查 Reparse Point 边界。
- 健康探测是只读枚举与容量查询；不以创建临时文件测试可写性。真实写能力以后由明确的写任务验证。
- 删除 Storage 只删除配置。被 MediaLibrary/Destination 引用时拒绝删除。
- Storage 创建/测试时由 driver 返回 capability snapshot，而不是让用户手工声称支持：
  - `is_cloud`
  - `supports_list` / `supports_change_cursor` / `supports_watch`
  - `supports_native_offline_download`
  - `supports_direct_url`
  - `supports_signed_redirect`
  - `supports_server_side_copy`（可选 provider 优化）
- “支持 STRM”是 Server 根据稳定文件身份、目录枚举和 signed redirect 能力推导的可用投影，不是网盘原生协议字段。local Storage 默认不需要也不展示 STRM/302。

## Media Classification Profile Contract

- `MediaClassificationProfile` 是独立版本化实体，UI 名称为“规则管理”。
- 系统内置默认 Profile 只读、不可删除，但可深复制。
- 自定义 Profile 可创建、编辑、改名、复制；未被引用时可删除，被引用时拒绝删除。
- Profile 结构与 Player `ScrapeClassificationRules version: 1` 语义兼容：movie/tv 两组、有序分类、fallback、include/exclude genre/language/country/year 条件。
- Server 用 Go 独立实现 schema、校验和匹配，不依赖 Player TypeScript；默认规则应通过共享契约测试保持语义一致。
- 编辑已共享 Profile 时，API 返回/保存前 UI 展示引用媒体库数量和名称。保存后所有引用库下一次扫描/重分类使用新规则；不移动、改名或删除媒体文件。

## Media Library Contract

- 一个 MediaLibrary 严格引用一个 Storage、一个存储内相对根和一个 MediaClassificationProfile。
- `relative_root=\`（API 内规范为 `/`）表示 Storage 根。绝对物理路径只在 Server 路径边界内拼接。
- 一个库可以包含电影与剧集混合内容。扫描器先识别 media type，再进入 Profile 对应规则组。
- 每个媒体库独立持有递归、扫描模式、全量计划、增量策略、扩展名/忽略规则、TMDB 语言地区与匹配设置。
- 扫描、元数据、海报和分类结果保存在 Server data/cache，不写回 Storage。
- 跨 Storage 展示由聚合视图组合多个 MediaLibrary，不允许一个库持有多个 Storage。
- 用户先选择来源 Storage + source relative root 创建 MediaLibrary。云端 Storage 只有在 capability 允许时，MediaLibrary 才能启用 `signed_proxy_enabled` 和 `strm_enabled`。`strm_enabled=true` 时额外要求用户通过本地目录选择器设置 `strm_local_root`；服务端规范化并把它作为该 Library 的 managed output boundary 持久化到本机 SQLite。它不是第二个 source Storage，也不要求预注册 local Storage。启用后，该库扫描到的远端媒体生成受控 STRM 投影；播放请求先验证签名，再由 driver 获取/刷新临时直链并返回 HTTP 302。
- local source MediaLibrary 不显示或接受 STRM 配置，不保存本地输出根。cloud Library 未启用 STRM 时，`strm_local_root` 为空且无任何投影落地；服务端必须拒绝“STRM 已开启但未选择有效本地目录”和“local source 开启 STRM”两类组合。该根必须是绝对目录、存在、可写、不是文件/Reparse Point，所有同步/清理操作均重新做边界与逃逸校验。
- 每库分别配置三种流量约束，避免把不同风控混成一个“速度”：
  - provider scan/list request rate + concurrency
  - metadata scrape request rate + concurrency
  - direct URL refresh/proxy resolution rate + cache TTL ceiling
- 本地 Storage 使用 filesystem watcher 作为低延迟信号；所有 Storage 都保留定时增量 reconciliation 和周期全量 reconciliation。watch/event 只是提示，不能成为唯一一致性来源。
- 监听由每个 MediaLibrary 的常驻 supervisor 承担，不进入全局 Job 队列。所有已启用库可同时监听；单库内部以 generation/single-flight 合并事件，避免同一目录并发重放。Storage driver 自动选择 `filesystem-watch`、provider event、change cursor 或 polling；115 driver 优先把“生活事件”归一化为 create/move/rename/delete 事件。
- supervisor 持久化 provider-relative `FileTreeSnapshot`、driver cursor、generation 和最后 reconciliation 状态。全量枚举重建树；可靠事件直接修改树；游标失效或 polling 模式通过新旧树 diff 收敛。定时增量和周期全量属于每库独立 reconciler，不占下载、上传或刮削 worker slot。
- cloud Library 开启 STRM 时，projection mirror 明确落在用户为该库选择的 `strm_local_root`，使用远端相对目录结构：视频 `mp4/mkv/ts/iso/rmvb/avi/mov/mpeg/mpg/wmv/3gp/asf/m4v/flv/m2ts/tp/f4v` 映射为 `.strm`，`srt/ssa/ass/jpg` 作为伴随文件下载到同一相对位置。全量从文件树 materialize，增量只消费 snapshot diff；写入采用临时文件 + 原子替换，删除只约束在该 Library 的 STRM 目录。关闭 STRM 时不要求目录、不创建或保留投影产物，远端索引/操作不受影响。
- cloud mount 是 Storage/Connection driver 可选的本地访问路径，用于刮削产物上传、transfer/import 等明确写入路线；它不是 MediaLibrary STRM projection。除非用户单独开启 STRM 并选择本地 STRM 目录，否则 mount 的存在不会生成任何 `.strm`、字幕或图片镜像。

## Downloader and Transfer Contract

- Downloader 是独立可管理 provider：qBittorrent/Transmission 等本地下载器，以及 115 等网盘原生离线下载器。
- 每个 downloader 声明输出约束和 telemetry capability。网盘原生离线下载器只允许直接写入所属 cloud Storage；本地下载器只写入配置的本地 staging Storage/root。
- `DownloadRule` 是独立可复用编排实体，引用 Downloader、目标 MediaLibrary、可选 staging Storage、跨 Storage route preference、冲突行为、带宽/并发策略和入库设置。系统允许多条规则并且恰有一条有效默认规则。
- 用户提交资源时默认选择默认 DownloadRule，也可切换任意有权使用且当前有效的规则。Server 根据规则中的 downloader output 与 library Storage 生成可预览 route：
  - direct-to-target：下载器可直接写目标 Storage
  - local-to-local：move/hardlink/copy
  - cloud-to-local：网盘下载完成后拉取到本地
  - local-to-cloud：本地下载完成后上传目标网盘
  - cloud-to-cloud：优先 provider server-side copy；否则显式经过受控本地 staging 下载再上传
- 跨 Storage 默认关闭；用户启用并确认 staging、空间和传输策略后才允许。不得静默把 hardlink 降级为 copy。冲突策略未设置时为 `ask`；用户显式设置 `overwrite` 后直接替换目标，不产生 ActionRequest。
- DownloadTask 创建时保存 DownloadRule 的不可变版本快照以及实际解析后的 DownloadRoute；后续编辑/停用规则只影响新任务，不能改变运行中或待恢复任务的 downloader、目标库或传输路径。
- DownloadTask 持久化 provider task id、阶段、状态、bytes、progress、down/up speed、ETA 和最后采样时间。WebSocket 推送实时增量，REST 保留可恢复任务事实；不支持某项 telemetry 时显示 unknown，不能伪造 0 或百分比。

## Destructive Deletion Contract

- DownloadRule 不得包含“完成后自动删除本地源/云端源”的普通布尔开关。清理属于独立高风险动作，不是下载编排的隐式尾步骤。
- 必须拆分确认范围：
  - 仅删除 Server task record（不删任何文件）
  - 删除 downloader task（保留数据）
  - 删除本地 staging/source data
  - 删除目标 MediaLibrary 文件
  - 删除 cloud provider source/target object
- 每一种真实数据删除都展示 provider/Storage、受控相对路径、文件数量与估算大小，要求再次输入/确认；云端删除明确提示回收站/不可恢复语义并由 driver 返回 capability。
- 批量、递归或 cloud permanent delete 使用更高风险 permission 和审计；默认先支持 dry-run/preview。不得把“取消下载”解释为“删除数据”。
- 冲突覆盖不是上述手工删除流程：任务 snapshot 为 `overwrite` 时允许自动移除旧目标并替换。本地旧目标直接永久删除，不创建 Server 管理的隔离回收区；cloud driver 支持原生回收站时默认把旧目标送入云端回收站，不支持时直接永久替换。任何路径都不暂停请求二次确认，并校验受控目标路径、审计旧/新对象身份，不扩展到无关文件或递归目录。

## Persistent Queue Contract

- 下载、上传/跨 Storage transfer、metadata scrape 和 refresh 以持久化 Job 入队，不在 HTTP handler 或一个不可恢复 goroutine 中长时间执行。MediaLibrary 监听、文件树 reconciliation 与对应 STRM/伴随文件同步由每库常驻 supervisor 执行，不进入此队列。
- 用户确认创建/启动任务后才入队；草稿、路线预览和冲突预览不是已入队执行任务。
- 调度器按 job type 使用独立全局并发，并额外执行 provider/downloader/Storage/MediaLibrary resource key 限流，防止一个网盘或下载器占满所有 worker。
- 下载任务必须有可配置最大并发；scrape/upload 也分别有限制。Storage 扫描/监听不设置跨库全局并发槽，但每个 provider 仍执行自己的请求速率保护。
- Job 状态至少包括 `queued/running/waiting_user_action/retry_wait/paused/completed/failed/cancelled`。`waiting_user_action` 与 `retry_wait` 不占 worker slot。
- `ask` 冲突、匹配不确定、手工删除确认、凭据恢复等需要用户决策时：保存 structured ActionRequest，提交当前 checkpoint，释放 lease/slot，继续调度后续任务。用户响应后重新入队原阶段，执行前再次校验外部状态。规则 snapshot 为 `overwrite` 时不得创建 ActionRequest。
- Worker 使用 lease + heartbeat + attempt/checkpoint；Server 重启或 worker 失联后，过期 lease 可安全回收，副作用阶段依赖 idempotency key 防重。
- 队列默认 FIFO，但任务类型可配置优先级；同一资源锁冲突不能阻塞其它无关资源任务。WebSocket 推送变化，REST/DB 是恢复事实。
- MediaLibrary watcher、定时增量和 transfer completion 产生的是 supervisor dirty signal，不是 Queue Job：同一 library 同一 scope 同时最多一个 reconciliation；运行期间新事件只推进 dirty generation，结束后立即追赶到最新 generation。不同媒体库互不等待。

## Import Pipeline Contract

```text
Discover/manual input
  -> choose Downloader + target MediaLibrary
  -> resolve/confirm DownloadRoute
  -> Download (live progress/speed)
  -> Identify + metadata match sufficient for path plan
  -> build previewable ImportPlan
  -> Transfer (separate progress/speed)
  -> target MediaLibrary incremental reconciliation
  -> complete metadata/classification/artwork
  -> generate/update STRM projection when enabled
  -> refresh Emby/Jellyfin + notify Player
```

- 自动入库必须先产生可审计的 ImportPlan。匹配不确定时进入待人工确认，不把文件自动放入猜测目录。
- 入库后的 MediaLibrary scan 是最终事实确认；下载器 save path 不是媒体库记录的长期身份。
- 每阶段可重试且幂等，失败不回滚已经成功的外部下载，但必须保留可恢复状态和错误边界。

## Task Ordering

1. Storage 基础与路径安全。
2. MediaClassificationProfile 规则管理。
3. 持久化任务队列与调度基础。
4. MediaLibrary 配置、规则选择、并行监听与文件树基础。
5. Downloader 管理与实时任务 telemetry。
6. DownloadRule 管理和默认规则选择。
7. 跨 Storage transfer/import orchestration。
8. Storage playback projection：STRM + signed 302；先用 fake cloud capability/file tree 验证，首个真实 cloud driver 后补 live test。

## Compatibility and Roadmap

- 将 Server MVP 首个切片从 OpenList 优先调整为本地 Storage/MediaLibrary 优先；OpenList/115/CloudDrive2 范围不删除。
- 后续网络 Storage 只需新增 Connection/driver，并继续复用 Storage、MediaLibrary、Profile 接口。
- 流水线 CategoryRule 保持独立，后续引用 StorageDestination，不复用 MediaClassificationProfile。

## Operational Safety

- 用户指定的绝对媒体根只作为本机运行时验收输入，不写进 task doc、源码、migration、seed、fixture 或日志快照。
- 自动化测试仅操作 `t.TempDir()` 或 `.runtime/windows/tests`。
- 真实目录初期只读；任何 copy/move/hardlink/symlink/delete 都属于未来独立任务。
