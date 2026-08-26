# Player 下载管理与完整离线播放 — 技术设计

## 1. 设计原则

1. **稳定身份持久化，传输地址瞬时化**：数据库只保存能重新解析媒体的稳定 ID；URL、Header 和凭据只存在于 Rust 单次请求内存中。
2. **Player 独立优先**：调度、离线目录、离线详情和本地播放全部属于 Player；Server 只作为一种可选 DataSource。
3. **统一能力边界**：Vue 不按 Provider 拼 URL，下载和播放共享稳定媒体身份，但分别解析各自用途的短期请求。
4. **视频与附件分离**：视频完成即可离线播放，图片/字幕/弹幕是可独立重试的附件阶段。
5. **取消即消失**：取消不是失败状态；下载任务和残缺文件被清理，失败才拥有重试语义。

## 2. 总体数据流

```text
媒体卡片 / 详情 / 播放上下文
        ↓ MediaAction + DownloadPlan（稳定 ID）
Player Download Store / Download Center
        ↓ Tauri command
Rust Download Scheduler
        ├─ Provider DownloadResolver → 瞬时 URL/Header
        ├─ Range Segment Workers → .partial
        ├─ Global Rate Limiter
        └─ Offline Package Finalizer
                 ↓
          offline_media.sqlite
          data/offline/<package-id>/assets
          用户选择的下载目录中的视频
                 ↓
OfflineDataSource + OfflineIndexStore
        ↓
统一 Playback Resolver（本地优先 → 在线回退）
```

## 3. 前端契约

### 3.1 DataSource 下载能力

在 `DataSource` 上增加可选、Provider 中立的下载规划能力，或以独立 adapter registry 包装现有 DataSource。规划结果只包含：

```ts
interface DownloadMediaDescriptor {
  sourceId: string
  sourceType: DataSourceType
  itemId: string
  mediaSourceId?: string
  variantId?: string
  libraryId?: string
  onlineIdentity?: {
    libraryId: string
    workId: string
    segmentId: string
    versionId: string
  }
  mediaType: MediaItem['type']
  displayName: string
  expectedBytes?: number
}
```

禁止在该对象中增加 URL、Header、Cookie、Token、绝对 Provider 路径或签名参数。现有 `planMediaDownload()` 改为根据用户当前选择只输出一个版本/清晰度，聚合下载则对每个具体单集输出一个 descriptor；不能再像当前实现一样自动把同一集的全部媒体版本全部加入队列。

### 3.2 下载状态

用户可见状态：

```text
queued → resolving → downloading → finalizing → completed
                   ↘ paused
                   ↘ failed → queued (manual retry)
```

`cancel_requested`、`cleanup_pending` 是 Rust 内部状态，不作为可重试下载卡片返回。取消成功后通过 `player-download:removed` 事件从 Pinia store 删除。

进度 DTO 增加：

- `speedBytesPerSecond`
- `etaSeconds`
- `activeSegments`
- `attachmentState: none | pending | syncing | complete | partial`
- `downloadedMediaCount / totalMediaCount`（聚合摘要）

### 3.3 状态管理

建立 Pinia `useDownloadStore`：

- 启动时一次加载设置、任务和离线摘要。
- 订阅 progress/removed/offline-changed 事件。
- 提供 active/complete/failed/grouped 等 computed 视图。
- `FloatingControls`、下载中心、MediaCard和详情页只消费 store，不各自监听原生事件。

建立 `useOfflineIndexStore` 或合并到下载 store 的独立只读索引：

- key 使用稳定的 `sourceId + itemId + mediaSourceId + variantId`。
- 同时维护 work/series 聚合计数。
- 首页、搜索、MediaGrid一次读取内存索引，避免每张卡调用 Tauri。

## 4. Rust 下载调度器

### 4.1 模块边界

把当前单个 `commands/downloads.rs` 拆分为：

- `downloads/commands.rs`：薄 Tauri command。
- `downloads/model.rs`：DTO、状态、设置和迁移。
- `downloads/storage.rs`：SQLite事务和查询。
- `downloads/scheduler.rs`：队列、公平调度、恢复和取消。
- `downloads/resolver.rs`：Provider resolver registry。
- `downloads/transfer.rs`：Range探测、分段、限速和原子完成。
- `downloads/offline.rs`：离线包与附件落盘。

允许保留兼容 re-export，避免一次性破坏 `lib.rs` 命令注册。

### 4.2 持久化模型

`downloads.sqlite` 继续保存执行队列，并增加 schema version/migration：

- `download_tasks`：稳定 descriptor、目标 root、相对文件名、状态、大小、重试次数、实体指纹、聚合 ID、创建/更新时间。
- `download_segments`：task、range start/end、completed bytes、segment状态。只存字节区间，不存请求信息。
- `download_settings` 可继续复用 `settings.sqlite`，字段为目标目录引用、并发任务数、分段数、全局 bytes/s 上限。
- `download_cleanup`：取消后残留分片的内部重试事实，不返回下载列表。

`offline_media.sqlite` 保存：

- `offline_packages`：作品级来源身份、快照版本、附件状态。
- `offline_items`：电影/季/集层级、精确媒体身份、本地视频位置、大小/指纹、完成时间。
- `offline_assets`：package/item、poster/backdrop/still/subtitle/danmaku kind、受控相对路径、状态和安全错误码。

详情快照使用版本化、大小有界的 JSON，只允许 MediaDetail 展示字段；不得包含 path、URL、Header或凭据。视频定位由受校验的 download root 引用加相对文件名组成，不能进入 Vue Router。

### 4.3 调度与限速

- scheduler 使用全局 semaphore 控制同时媒体任务数。
- 单任务只有在 Range 可验证、总大小已知且桌面目标支持随机写时才启用多分段；否则安全退化为单流。
- 分段 worker 使用随机访问写入预分配 `.partial`，每个区间只写自己的范围。
- 全局 token bucket 在所有任务/分段之间共享，统计滑动窗口速度并计算 ETA。
- 用户暂停保留分段与 partial；应用崩溃/退出时运行中任务标记为 `interrupted`，下次启动自动回到队列。用户暂停状态不自动恢复。
- 最终校验通过后原子重命名，再事务写入 offline item；SQLite状态不能先于文件完成。

### 4.4 取消

1. command 写入 cancel intent 并通知 cancellation token。
2. scheduler 等待当前写入点退出并释放句柄。
3. 删除该任务拥有的 `.partial`、segment row和task row。
4. 若文件系统删除失败，将精确受控路径写入内部 `download_cleanup`，任务仍向 UI 发送 removed；启动/空闲时重试清理。
5. 已完成 offline item不因取消同组剩余项目而删除。

## 5. Provider 解析与302恢复

统一接口：

```rust
trait DownloadResolver {
    async fn resolve(&self, descriptor: &StableDescriptor) -> Result<ResolvedTransfer>;
}

struct ResolvedTransfer {
    url: Url,
    headers: HeaderMap,
    expected_identity: Option<EntityIdentity>,
}
```

`ResolvedTransfer` 不实现 Serialize，不进入任务数据库、事件或日志。

### 5.1 Provider 行为

| 来源 | 稳定身份 | 每次 resolve 行为 |
|---|---|---|
| local | source + item | 重新校验配置根并安全打开文件 |
| OpenList/Alist | source + provider path | 重新调用 `/api/fs/get` 并获取新 sign |
| CloudDrive2/123/夸克 | source + provider item | 使用安全凭据重新请求临时下载地址 |
| WebDAV | source + provider path | 重新构造同源地址和瞬时 Basic Auth |
| Emby/Jellyfin | source + item + mediaSource | 调 PlaybackInfo确认同一媒体源，优先官方 Download/Static stream入口，再取得新重定向 |
| Server物理媒体 | source + entry ID | 重复请求 `/api/v1/player/media-entries/{id}/stream`；本地返回 Range流，115返回新302 |
| Server在线插件 | source + online work/segment/version/variant | 使用device credential重新请求现有播放方案或新增用途受限的 download resolve API |

Server 端只复用现有 Player stream/playback API，本任务不修改 `server/`。如果在线插件的现有 playback plan 不能安全支持下载续传，则 Player 对该能力显示明确不可用原因，并把最小 download-purpose resolve 契约记录为后续 Server 子任务；不得为了完成 Player 任务绕过device-token、媒体权限或临时地址边界。

### 5.2 恢复策略

- 401/403：重新解析一次；若同一凭据再次失败，分类为 credential/permission failure。
- 404/410：视为短期地址过期时重新解析；稳定媒体本身不存在则失败为 source missing。
- 429/5xx/网络中断/提前 EOF：指数退避并重新解析，次数有界。
- 跨 origin redirect清空所有 Provider私有 Header；HTTPS禁止降级。
- 续传必须同时满足 `206 + Content-Range起点正确 + 实体指纹一致`。实体指纹优先强 ETag，其次 Last-Modified+总大小；完全没有可靠身份时单流中断从头重启。

## 6. 离线媒体包

### 6.1 完成边界

1. 视频原子完成。
2. 写入有界 MediaDetail及作品层级快照。
3. 复制必要海报/背景/单集图到 `data/offline/<package-id>/assets`，不进入普通 LRU cache。
4. 下载可安全访问的外置字幕到包目录；内嵌字幕只记录轨道信息。
5. 通过 DataSource provider API获取弹幕评论并保存规范化、版本化本地数据，不保存弹幕源 URL。
6. 视频+详情成功后发布 offline-changed；附件失败标记 partial并允许单独重试。

同一剧集的多个单集共享作品快照与海报资产，单集维护自己的still/subtitle/danmaku。删除最后一个单集时才清除不再被引用的作品资产。

### 6.2 OfflineDataSource

实现只读 DataSource：

- `list/listLibraries/search/getDetail` 从 `offline_media.sqlite` 读取。
- `getStreamRequest` 返回受控本地 locator，不通过路由暴露绝对路径。
- 不实现远程修改、Provider收藏或删除源媒体；删除离线包走下载中心的本地专用动作。
- 作为内置来源在没有任何远程连接时也可初始化。

## 7. 本地优先播放

在 `PlayerView` 调用 DataSource stream resolver 前增加统一 `resolvePlaybackMedia()`：

1. 根据当前 source/item/mediaSource/variant查询 offline index。
2. Rust重新校验本地文件存在、大小/指纹匹配且仍位于登记root内。
3. 有效则创建进程内 local playback context并返回本地流，同时加载离线字幕/弹幕。
4. 无效则事务标记offline item missing，刷新徽标，再调用原 DataSource在线解析。
5. 原来源详情失败时，详情服务按相同稳定身份读取离线快照；冷启动可直接从 OfflineDataSource导航。

在线与离线播放历史仍使用原作品身份，从而不会因为本地优先而产生重复观看记录。

## 8. UI 设计

- `FloatingControls` 增加下载图标及活动数徽标，点击进入 `/downloads`。
- `App.vue` 移除全局 `DownloadQueue`圆钮；下载状态由 Pinia store常驻。
- `DownloadsView` 使用顶部分页：进行中、已完成、失败、设置。
- 桌面任务卡展示进度、速度、ETA、大小、分段和聚合摘要；移动端使用同一信息层级的紧凑卡片。
- 设置页使用共享表单token，分别解释“同时下载数”和“单任务线程数”；限速以 MB/s输入并存为 bytes/s。
- `MediaCard` 左上角使用下载箭头/勾和文字tooltip；现有已播放勾保持右下角。剧集部分状态显示 `3/12`。
- 详情页和单集列表复用同一 offline store selector，不各自请求数据库。

## 9. 迁移与兼容

- schema migration按当前列探测增量添加，不删除用户数据库。
- 旧 cancelled记录只清理可由旧任务目标精确推导的 `.partial`；不能证明归属的文件不删除。
- 旧 queued/running进入interrupted并由scheduler恢复；旧 paused保持paused。
- 旧 completed记录在最终文件存在时创建 minimal offline item；详情补齐失败不影响本地视频播放，但显示“离线详情待补全”。
- 当前默认下载目录设置迁移到新下载设置键；以后切换默认目录不改旧offline item的root引用。

## 10. 风险与回滚

- **分段写入损坏风险**：仅在Range和实体身份可验证时启用；最终大小/区间覆盖校验失败则不完成。
- **Provider行为差异**：每个resolver有独立fixture和真实端到端测试；失败可退回单流而不是修改稳定身份。
- **Android SAF随机写限制**：Android首版保持单流，避免伪并发破坏DocumentProvider文件。
- **离线资产膨胀**：资产按package引用计数，删除包时精确回收；不做自动淘汰。
- **大范围改造风险**：保留现有Tauri命令名兼容Vue调用，先迁移核心，再切换UI；旧schema可读是回滚前提。

## 11. 验证矩阵

- Rust单元：迁移、调度槽、暂停/取消、segment覆盖、token bucket、实体变化、跨源Header剥离、敏感字段schema。
- 本地HTTP集成：正常Range、忽略Range、302过期刷新、401/403、提前EOF、ETag变化、HTTPS降级拒绝。
- Provider契约：Server本地Range、Server 115多次302、Emby静态流/302刷新、Server在线插件稳定版本。
- Vue回归：版本选择、右键/长按、下载中心分页、设置迁移、取消移除、徽标聚合和离线回退。
- 平台：Windows原生真实下载与断网播放；Android SAF授权、前台通知、暂停/取消、离线播放。
