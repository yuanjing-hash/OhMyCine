# Design: Downloader、媒体库与跨数据源入库收敛

## 1. Architecture

流水线不再由 downloader/provider 类型决定最终目标，而分成四个稳定边界：

```text
Source + Downloader Provider
  -> Acquired Package + SourceDataSourceIdentity
  -> Import Pipeline
       -> Manifest
       -> Route
       -> Materialize (cross-source only)
       -> Verify / Recognition
       -> Plan / Conflict
       -> Transfer Executor
       -> Library Backend reconcile
       -> Artifact / Notify
```

- `DownloaderProvider` 只提交、跟踪和取消获取任务，并返回完成包及其数据源身份。
- `MediaLibrary` 保存最终库的领域策略；`LibraryBackend` 实现扫描、监听、导入、删除和 provider identity 操作。
- `TransferRouter` 比较来源与目标的数据源身份，并按 capability 选择同源或跨源执行器。
- `ImportPipeline` 持久化阶段、快照和 checkpoint；具体 provider 不能跳过识别、冲突、所有权登记、对账和通知。
- `PluginDownloadExecutor` 保持独立，只把已经生成的本地 staging package 交给后续通用入库链。

## 2. Data-source identity

引入不可由 UI 自行构造的 `DataSourceIdentity`：

```text
kind: local | provider
provider_type: local | pan115 | future provider
connection_identity: provider 账号/连接的稳定内部 ID；local 使用 server-local
storage_scope: 可选，用于边界和 ancestry 复验，不作为同源的唯一判断
```

判定规则：

- 本地 downloader 输出与本地 MediaLibrary 均属于 `server-local`；不同卷影响 hardlink/move capability，但不需要云端下载中转。
- 同一 115 Connection 下的下载目录与媒体库目录同源，即使属于不同 Storage/root，也可在 ancestry 与 capability 复验后服务端移动/复制。
- 不同 115 Connection、local 与 115、以及未来不同 provider/账号均为跨数据源。
- 判断使用任务创建时由 Server 固化的 identity snapshot，并在执行前用当前 Connection/Storage/root 再验证；不能比较名称、展示路径或只比较 downloader 类型。

## 3. Route matrix

| Source | Target | Route | Final write |
|---|---|---|---|
| local | local | same-source local | move/copy/hardlink/symlink；跨卷时按明确能力报错或使用配置允许的 copy+cleanup |
| 115 connection A | 115 connection A | same-source provider | 现有服务端 move/copy，不下载重传 |
| local | 115 connection A | cross-source | 本地受管包校验后上传 |
| 115 connection A | local | cross-source | 115 下载到统一暂存根，校验后放置 |
| 115 connection A | 115 connection B | cross-source | A 下载到统一暂存根，再上传 B |
| future cloud A | future cloud B/local | cross-source | Reader/Exporter + staging + target Importer |

PT/BT 来源兼容仍在提交阶段单独判定：PT 不能交给 115 downloader，但 qBittorrent 下载完成的 PT 包可以通过 `local -> 115` 路由入库。

## 4. Interfaces and capabilities

目标接口使用最小必需契约加能力接口：

```text
DownloaderProvider
  Test / Submit / Get / Cancel
  optional: Pause, Resume, Manifest, Seeding, ShareReceive, NativeOffline

LibraryBackend
  Identity / ValidateRoot / Scan / Stat / ApplyImport / Delete
  optional: Watch, ServerSideMove, ServerSideCopy, Upload, DirectRead, BatchMutation

SourceExporter
  Estimate / MaterializeToManagedStaging / VerifySourceIdentity

TransferExecutor
  Supports(route) / Plan / Execute / Reconcile / Cleanup
```

- 115 `DirectURL`/读取能力封装到 `SourceExporter`，临时 URL、Cookie、pickcode 和 headers 只存在于 adapter 单次内存调用。
- 现有 115 `UploadDriver` 作为 `local -> 115` Import capability，不再硬编码只允许 `plugin_http`。
- 同源 115 executor 继续复用 batch intent、provider 对账和不确定结果恢复。
- 通用 Worker 只处理 route/capability/checkpoint，不出现 qBittorrent、pan115 等业务分支。

## 5. Durable state and migration

DownloadTask/TransferTask 新增或收敛以下私有快照：

- source data-source identity
- target data-source identity
- selected route kind: `same_source_local | same_source_provider | cross_source`
- source manifest revision and selected package
- managed staging allocation, expected/actual bytes and ownership state
- per-file materialize/upload/finalize checkpoint
- target provider identity and reconciliation generation
- cleanup/seeding references

迁移为 additive：

- 旧 local -> local 任务回填 `same_source_local`。
- 旧同账号 115 任务回填 `same_source_provider` 并保持当前 cloud state。
- 已运行或终态任务不重路由；只有新任务和用户显式重试且缺少安全执行快照的任务使用新路由器。
- 旧 MediaLibrary ingest 配置迁移为 Downloader life-event + Connection 唯一默认媒体库，partial unique index 保证同一 Connection 最多一个默认。

## 6. Managed staging and space

- 复用现有全局“下载暂存目录”作为 Server 统一工作根，不增加另一个中转目录概念。
- qBittorrent 使用现有任务/分类目录；跨源物化使用专用内部命名空间和 task-owned 子目录，二者均受 canonical path、Reparse Point/symlink escape 和所有权约束。
- `Estimate` 可得时，在云端拉取前预留 `selected bytes + configured safety margin`；不可得时禁止自动跨源，等待取得可信 manifest/大小，而不是无限制下载。
- 空间预留是持久事实；调度器按 staging resource key 控制并发，避免多个任务各自通过瞬时 free-space 检查后共同写爆磁盘。
- 下载写入 `.partial`/任务私有路径；完整校验和原子 finalize 后才成为可供识别/入库的 managed package。
- 云端跨源成功并完成目标对账后清理 task-owned staging；失败、等待人工识别或上传结果不确定时保留并复用。
- qBittorrent 做种源由 SeedingTask 持有引用，未完成做种前清理器不得删除；上传成功不等于可以删除做种数据。

## 7. Import and cleanup semantics

- `move` 表示目标成功并对账后允许清理来源；`copy` 表示保留来源。qBittorrent 做种策略可延后或禁止来源清理。
- 115 跨源来源清理仅在目标已验证、任务策略要求 move 且 provider identity 复验成功后送入回收站；不清空回收站。
- Cancel 先真实取消 provider 任务并保留文件，再停止 materialize/import；仅任务拥有的未完成 partial 可安全删除。
- Delete 默认只删 provider 任务和本地历史、保留来源与最终文件；完全删除必须显式选择并继续走 preview/confirm/ownership 边界。
- 上传返回不确定时先查询目标父目录并按计划 identity/size/SHA1 对账，不盲目重传。

## 8. 115 life-event default target

- 每个 115 Connection 由数据库唯一约束保证最多一个启用的自动监听默认 MediaLibrary。
- 没有有效默认目标时禁止开启生活事件监听；删除/停用默认库前必须替换默认或关闭相关监听器。
- 默认只路由 115 App 手工内容；显式离线、分享、站点下载和追更继续使用任务目标快照。
- 生活事件、provider 完成和补偿扫描共享 provider-item 幂等域，不会重复建立流水线。

## 9. API and Web UI

- 下载创建/订阅编辑的目标媒体库列表改为 Server 返回的权威 route preview，不再由前端复制 provider-type 条件。
- 每个目标显示 `同源云端整理`、`本地整理` 或 `跨数据源（需要 Server 暂存）`，并在已知 manifest 时显示预计空间。
- 空间不足、缺少统一暂存根、来源不可导出或目标不可写时禁用该组合并显示稳定原因码。
- 任务详情分开展示获取、跨源拉取、校验、识别、上传/放置、对账、产物与刷新阶段；下载完成不得冒充最终入库完成。
- 媒体库设置提供同一 115 Connection 内唯一“自动监听默认入库库”控制；Downloader 设置只展示当前默认目标和跳转入口。
- API/OpenAPI DTO 不暴露绝对路径、provider item ID、临时 URL、Cookie 或私有 checkpoint。

## 10. Compatibility, rollout and rollback

- 先引入 identity/router 和只读 route preview，再接入 executors，最后移除旧硬编码限制，避免中间版本放开不可执行组合。
- feature rollout 前保留旧同源 executor；跨源 capability 未就绪时 route preview 仍明确禁用。
- 数据库迁移只增列/表/索引；回滚版本可忽略新字段，但不得让新建跨源任务被旧 worker claim。通过 job policy/version gate 阻止旧 worker 执行未知 route。
- OpenList/CloudDrive2 的具体 Server 写入驱动不在本次新增范围；接口和禁用原因必须完整，未来实现 capability 即可解锁。

## 11. Security and observability

- 所有本地读写限定在统一暂存根或目标 Library root，逐阶段重新 canonicalize，并拒绝 traversal、symlink、junction 和 Reparse Point escape。
- 云端读取/写入逐项复验 Connection、Storage root ancestry 与稳定 provider identity。
- 日志、Job 公共 DTO、WebSocket 和审计只记录 task ID、route kind、阶段、受控相对摘要、字节进度与稳定错误码。
- 空间不足、来源变化、校验失败、目标冲突、上传不确定和 cleanup 失败分别持久化，允许只重试失败阶段。

## 12. MoviePilot-like media-type-first organization

MoviePilot 的分类配置先把规则分成 `movie` 与 `tv` 两个域，整理路径再按 `library_type_folder`、`library_category_folder` 的顺序依次追加媒体类型和类型内分类。OhMyCine 保留自己的 Profile、模板快照和安全执行器，不复制上游代码，但采用相同的层级原则：

```text
MediaLibrary root
  -> fixed type root: 电影 | 电视剧
  -> Profile category from the matching movie/tv group
  -> title / season structure from the type-specific template
```

- 固定类型根是 Server organization domain 的不变量，由共享模板规范化器负责；不能在 local、115 或 upload executor 内各自拼接。
- Profile/MediaLibrary 持久模板在写入和迁移时规范化为正确类型根。模板原本已以正确固定根开头时保持一次；否则在最前面增加固定根，原自定义结构完整保留在其后。
- DownloadTask 创建时继续冻结规范化后的完整模板，因此升级前已排队任务不变，升级后所有入口自动获得新层级。
- corrective reorganization 使用当前规范化模板；普通 Transfer planner 使用任务快照。二者共用同一个安全目录渲染和路径边界校验。
- 分类 matcher 仍只返回类型内的叶子分类，不能把 `电影`/`电视剧` 伪装成 Profile category；扫描与 catalog projection 不因物理目录规范化改变权限或写入边界。
