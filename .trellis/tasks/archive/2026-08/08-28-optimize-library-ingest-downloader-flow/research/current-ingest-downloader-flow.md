# 当前媒体库入库与下载器流程

## 配置对象

1. Connection 保存 115、媒体服务器等外部连接与凭据。
2. Storage 把连接或本地目录包装成受约束的数据源根。
3. Downloader 保存下载执行能力。qBittorrent 使用全局暂存根；115 Downloader 绑定一个 115 Storage 和其中一个下载目录，并可启用“自动监听生活事件”。
4. MediaLibrary 绑定最终 Storage/根目录、Profile、转移方式、冲突策略、命名模板、扫描周期、STRM 和 metadata artifact 配置。
5. 下载任务创建时会固化 Downloader、Profile、暂存边界和目标 MediaLibrary/Storage/连接/命名/转移/冲突快照。

## 入口

### qBittorrent / 非网盘下载

- 用户或站点提交磁力、HTTP(S) URL 或 `.torrent`。
- 磁力先以 metadata-only 模式取得文件信息，完成轻量识别后创建/校验分类目录，再设置保存位置并恢复正式下载。
- 自动入库目标当前只能是本地 MediaLibrary。
- PT 资源必须走此类支持私有种子和做种的下载器。

### 115 普通离线下载

- 只接收 magnet/HTTP(S) 等 URL，不接收 `.torrent` 文件。
- 在 Downloader 配置目录下创建唯一 `omc-<task-id>` 子目录，再向 115 提交离线任务。
- 自动入库目标必须是同一 115 Connection 下的 MediaLibrary，转移方式为云端移动或复制。

### 115 分享转存

- 校验为 115 HTTPS 分享链接并确认下载器具有 ShareReceive 能力。
- 在同一 Downloader 目录下创建 `omc-<task-id>` 子目录，将分享内容转存进去。
- provider 返回不确定时会通过任务目录内容反查是否已经提交成功。
- 转存目录随后以 completed directory task 进入普通识别和入库链。

### 115 App 手工内容

- Downloader 启用“自动监听生活事件”后，服务监听该下载目录。
- 只接管稳定至少 30 秒的普通目录，跳过 `omc-*` 保留目录；每 5 分钟有一次补偿扫描。
- provider item 会被包装成直接 completed 的 `ingest:<provider-item-id>` 下载任务，不重新下载。
- ingest_source_key 负责幂等；当前 Downloader 级 key 是 connection + downloader + provider item。

### 旧 MediaLibrary 自动摄取

- MediaLibrary 仍保留 IngestEnabled、IngestDownloaderID 和 IngestProviderRootID，并有另一套 sweep/adopt 入口。
- 该入口和 Downloader 级生活事件语义重复，是本次收敛重点。

### 自动追更

- 订阅按周期从媒体库覆盖率和 TMDB 季集信息计算 missing。
- 排除已存在 active/imported claim 的剧集，按多语言查询站点、过滤分辨率/编码/质量/关键词/发布组/做种数/大小/年龄后选取资源。
- 每个结果通过 SiteService 创建普通 DownloadTask，并绑定订阅幂等指纹和剧集 claim。
- 入库成功后 claim 变为 imported；后续运行重新依据媒体库覆盖率核对。

## 下载完成与识别

1. DownloadWorker 创建 provider 任务并持续采样状态。
2. qBittorrent 默认 2 秒轮询；115 由生活事件优先唤醒、20 秒补偿轮询、约 10 秒心跳保活。
3. provider API 的任务状态仍是完成权威，生活事件只是加速唤醒。
4. provider 完成后读取完整 manifest，持久化完成清单并过滤目标包。
5. 使用用户绑定身份或 TMDB 自动识别，固化标题、类型、分类、TMDB ID、季集与身份 revision。
6. 未可靠识别或 TV 文件集号不完整时不自动创建 TransferTask；修正身份后可复用已完成 manifest 重试。

## 整理入库

1. 每个 DownloadTask 最多创建一个 TransferTask。
2. TransferWorker 重新校验目标快照、身份快照和 manifest。
3. 根据媒体类型、季集、版本和 Profile 命名模板生成逐文件计划。
4. 本地目标执行 move/copy/symlink；115 目标执行同账号 server-side move/copy；插件输出到 115 时走 upload 分支。
5. 冲突策略为 ask/overwrite/skip/rename。所有本地路径和 provider identity 在写入前后校验。
6. 成功后记录 MediaManagedItem、完成 TransferTask、增加 MediaLibrary dirty_generation。
7. qBittorrent 的 copy/symlink 会按做种策略延迟清理；移动或无需做种的来源按安全差集清理明确的非媒体暂存项。

## 媒体库、STRM 与刷新

1. MediaLibrary supervisor 通过本地 watcher、115 事件和周期 full/incremental reconciliation 对账最终数据源。
2. reconciliation 更新 entry、recognition、baseline/dirty generation，并记录 MediaLibraryChange outbox。
3. 需要产物的库先生成或增量复用 STRM/NFO/图片；未变化的 STRM 保留原 bytes/mtime，签名临近过期才续签；云端已删除项对应的受管产物会清理。
4. change 变为 ready 后，MediaChangeService 合并通知绑定的 Emby/Jellyfin refresh target。

## 取消和删除

- CancelPipeline：先调用 provider cancel 且 deleteData=false，然后取消下载、整理、做种等未完成 Job，保留来源文件与可见的 cancelled 历史。
- Delete：只允许失败、取消或整条流水线完整收口的任务；先删除 provider 任务，再删除本地历史。deleteData=false 保留来源文件，true 才要求 provider 删除来源数据。
- Transfer/媒体库文件删除另有 preview/confirm 流程，确认令牌绑定根目录、provider identity 和当前 revision，避免误删越界。

## 已确认的问题

- MediaLibrary 级旧摄取与 Downloader 级生活事件双入口并存，概念和去重域不统一。
- 手工新建下载的“自动选择媒体库”实际是按启用库排序选择第一个兼容库，不是基于识别后的分类路由。
- UI 同时暴露 Downloader、Downloader 目录、目标媒体库和旧 MediaLibrary ingest 字段，用户容易把“下载位置”“输入收件箱”“最终媒体库”混为一谈。
- 下载、Transfer、MediaLibrary reconciliation、artifact 和 refresh 是五个持久阶段；当前页面虽然能展示多数状态，但“下载完成”和“最终可用”仍需更明确区分。

## 已确认的目标路由补充

- 每个 115 Connection 最多选择一个“自动监听默认入库媒体库”。
- 同账号下多个 115 Downloader 的生活事件手工内容共用该默认目标。
- 默认目标只影响生活事件接管，不影响用户明确选择目标的离线下载、分享转存、站点下载和订阅。
- 切换默认目标只影响新任务，已经创建的任务保留原目标快照。
