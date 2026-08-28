# 下载器、媒体库与入库抽象审计

## 结论

- Downloader：已经采用 Go interface + registry + provider implementation，方向正确，但基础接口过宽，插件下载绕开了注册表和通用 Worker。
- MediaLibrary：目前是 GORM 数据模型加一个大型 MediaLibraryService，不是严格的可替换媒体库领域接口；数据源差异只有一部分被 cloud Driver 隔离。
- Transfer/Import：已经具备持久任务、阶段状态、checkpoint 和拆分方法，但执行策略仍由 `TargetStorageType`、`ProviderType` 分支选择，不是完整的 pipeline stage/strategy 契约。

## Downloader 当前结构

`pkg/downloader.Client` 定义：

- Test
- Submit
- Get
- Pause
- Resume
- Cancel

`Registry` 以 provider type 注册 capabilities 和 Builder。当前 qBittorrent、115 Offline、Fake 都通过这个入口构建，实现可以互换。额外能力已有小接口：

- MetadataClient：下载前 manifest、分类和路径路由。
- ManifestClient：下载完成后读取文件清单。
- Capabilities：Pause、Resume、DeleteData、Seeding、NativeOffline、ShareReceive、OutputConstraint 等。

### 正确之处

- DownloadService/Worker 面向 `downloader.Client` 调用提交、查询、取消。
- qBittorrent 和 115 的 API 差异被封装在各自 client 内。
- ProviderError 统一错误码和 retryable 语义。
- Registry 避免在配置服务中直接实例化具体 client。

### 不严格之处

- `Client` 强制每个 provider 实现 Pause/Resume；115 实际只能返回 unsupported。这违反接口隔离，更合适的是 Required Client + 可选 Pauser/Resumer。
- Source 使用 `url/torrent/115_share/provider_item` 字符串联合体，来源兼容主要由 service 和具体 client 双重判断，缺少 provider 自己可声明的 SourceCapabilities。
- DownloadWorker 仍检查 qBittorrent/115 类型来决定 metadata-only、事件轮询和特殊恢复。
- `PluginDownloadExecutor` 没有注册为 downloader.Client：Submit、Run、Interrupt、清理、状态保存都走独立旁路，DownloadWorker 先查 `ProviderType == plugin_http` 再整体转交。产品决定是保留该独立执行器，不纳入本次传统下载器重构。

## MediaLibrary 当前结构

`models.MediaLibrary` 是持久化配置/状态记录，包含：

- Storage/Profile/root
- 扫描、识别、命名、转移、冲突策略
- STRM/metadata artifact
- generation/revision/status
- legacy ingest 配置

`MediaLibraryService` 同时负责：

- CRUD 和配置校验
- supervisor/watcher/life event
- 本地/115 扫描
- recognition/catalog
- ingest adoption
- reconciliation 和 entry persistence
- 删除、覆盖识别和 artifact 调度

### 已有的数据源抽象

`pkg/cloud.Driver` 统一了云 provider 的 Probe/List/Stat/DirectURL，并用小接口扩展：

- BulkTreeDriver
- ChangeSource
- NativeOfflineDriver
- ShareReceiveDriver
- MutationDriver / BatchMutationDriver
- UploadDriver / SmallFileUploadDriver

115 正确实现了这些 driver capability，云端移动、复制、重命名、回收站等操作已经大部分被隔离。

### 核心缺口

- 本地文件不实现与 cloud Driver 对等的 Library Backend；扫描直接调用 `ScanLocal`，云端直接调用 `ScanProvider`。
- `MediaLibraryService.reconcile` 明确 `switch storage.Type` 选择 local 或 pan115。
- artifact、catalog deletion、transfer deletion、listener 等仍重复判断 StorageType。
- `ScanLocal` 和 `ScanProvider` 返回相同 Result，但不是 Scanner interface 的两个实现。
- MediaLibrary 本身没有 Scan/Watch/Import/Delete/Reconcile 等严格领域方法；这些行为分散在多个 service 文件。

因此它目前不是“一个严格 MediaLibrary 类，由不同数据源重写方法”，而是“同一服务根据 StorageType 走不同函数，云盘部分再调用 Driver”。

## Transfer/Import 当前结构

当前持久化阶段已经较完整：

1. DownloadWorker 等待 provider 完成并保存 manifest。
2. verifyCompleted/recognition 生成可信身份和 package selection。
3. TransferService.EnqueuePackage 固化入库任务。
4. TransferWorker 验证 route、identity、manifest，生成命名计划。
5. 根据目标执行 local transfer、cloud transfer 或 plugin-to-cloud upload。
6. 处理冲突并持久化 checkpoint/managed items。
7. dirty generation 推动媒体库 reconciliation。
8. artifact/outbox/media-server refresh 收口。

### 正确之处

- DownloadTask 和 TransferTask 分离，下载完成不等于入库完成。
- 每个任务保存目标、规则和 manifest 快照，支持重启恢复。
- 115 有 batch intent、provider 对账和不确定结果恢复。
- 删除使用受管所有权和 preview/confirm，而不是按路径猜测。

### 不严格之处

- TransferWorker 通过 `TargetStorageType == pan115` 决定 runCloudTransfer/runCloudUpload，否则执行本地流程。
- cloud transfer 又要求 `ProviderType == pan115_offline`，upload 要求 `ProviderType == plugin_http`。
- cleanup、managed item、deletion 继续按 ProviderType/StorageType 分支。
- 没有正式的 ImportPipelineStage、TransferExecutor 或 LibraryBackend 接口；当前“步骤”主要是大 Worker 中的函数和文件拆分。

## 推荐目标边界

Go 不需要基类继承，推荐用严格接口和组合：

```text
DownloadOrchestrator
  -> DownloadExecutor (required contract)
       -> qBittorrentExecutor
       -> Pan115OfflineExecutor

PluginDownloadExecutor (本次保持独立)

MediaLibrary aggregate
  -> LibraryBackend (required contract)
       -> LocalBackend
       -> Pan115Backend
       -> future OpenList/CloudDrive2 backend

ImportPipeline
  -> ManifestStage
  -> RecognitionStage
  -> RouteStage
  -> PlanStage
  -> ConflictStage
  -> LibraryBackend.ApplyImport
  -> ReconcileStage
  -> ArtifactStage
  -> NotifyStage
```

基础接口只承诺所有实现都能完成的能力；监听、批量操作、上传、做种、分享转存等使用 capability interfaces。这样新增 provider 只需注册实现，不修改通用 orchestration。
