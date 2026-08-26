# Player 下载与离线播放现状研究

## 已有能力

- `player/src/services/downloads.ts` 已提供默认目录、任务列表、入队、聚合入队、取消、重试和进度事件的 Vue/Tauri 边界；现有持久化 DTO 已避免 URL/Header 字段。
- `player/src/services/downloadPlanning.ts` 能递归展开 series/season/folder，并限制最大深度与最多 2000 个文件。
- `player/src/services/mediaActions/downloadAdapter.ts` 已复用统一媒体操作系统，桌面和 Android 均可触发 download/downloadTo。
- `player/src-tauri/src/commands/downloads.rs` 已有 SQLite、桌面 partial 文件、Android SAF、local copy、Provider下载、Range续传实体检查和原子 rename 基础。
- `player/src-tauri/src/commands/provider_file.rs:85` 的原生 resolver 已覆盖 CloudDrive2、WebDAV、123、夸克与 Emby/Jellyfin；Alist resolver 仍在 downloads模块中。
- `server/internal/handlers/player.go:205` 与 `server/internal/services/player_media.go:627` 已提供device-token保护的稳定媒体 entry stream。Server本地文件走 `http.ServeContent`，115投影每次调用重新解析302。
- `player/src/services/datasource/server.ts:513` 已有Server物理媒体和在线插件播放方案解析；在线插件稳定身份包含 library/work/segment/version/variant。
- `player/src/services/datasource/emby.ts:599` 已有成熟PlaybackInfo、MediaSource选择、静态流fallback、RequiredHttpHeaders和字幕轨道解析，可作为原生下载resolver的行为参考。
- `player/src-tauri/src/commands/image_cache.rs`、字幕缓存命令和danmaku service已有受控HTTP/资产处理基础，但普通图片缓存为LRU，不满足“离线包必须保留”。

## 已确认缺陷

1. `player/src-tauri/src/commands/downloads.rs:191` 入队后直接调用 `start_task`，没有公平队列、并发上限或全局限速。
2. `player/src-tauri/src/commands/downloads.rs:282` 取消只写 `cancelling/cancelled`；`start_task` 完成分支仍保留数据库记录与partial。
3. `player/src/components/media/DownloadQueue.vue:112` 把 `cancelled` 与 `failed/paused` 一起显示“重试”，直接违反用户的新取消语义。
4. `player/src/services/downloadPlanning.ts:65` 遍历所有 `mediaSources`，导致单集/电影默认下载全部版本，而不是当前选中版本/清晰度。
5. `player/src/services/mediaActions/downloadAdapter.ts:9` 的来源白名单不含 `server`，Server物理与在线媒体不能进入Player本地下载。
6. `player/src/App.vue:81` 全局挂载独立 `DownloadQueue`圆钮；`player/src/components/layout/FloatingControls.vue` 没有下载入口。
7. `player/src/views/SettingsView.vue:2534` 仍承载默认下载目录，尚无并发、分段和限速设置。
8. `player/src/services/datasource/cache.ts` 的 `SourceMetadataCache` 只是内存Map，重启/断网无法进入完整详情。
9. `player/src-tauri/src/commands/provider_file.rs:180` 的Emby下载只构造 `Videos/{id}/stream?Static=true`；传输中断后当前execute只重新解析一次，不具备有界地址刷新循环与PlaybackInfo同版本复验。
10. `player/src/services/datasource/server.ts:513` 的Server播放请求在Vue内瞬时取得device credential；当前Rust下载resolver不能读取/处理Server稳定entry或在线插件descriptor。
11. `player/src/components/media/MediaCard.vue:264` 仅有右下角已播放徽标；没有统一离线索引和左上角下载状态。

## 必须复用的现有边界

- 媒体操作：继续使用 `services/mediaActions`，不要在各详情页复制下载逻辑。
- 数据源与版本：复用 `DataSource.getDetail/getStreamRequest`、`MediaSourceOption`、`StreamVariant` 的身份语义；下载descriptor只增加稳定字段。
- 凭据：Rust从现有credential envelope读取，Vue和SQLite不接触明文凭据。
- Server：优先重复请求现有 `/api/v1/player/media-entries/{id}/stream` 和在线playback plan；只有在线下载续传无法安全表达时才新增最小API。
- Emby：复用TS实现的PlaybackInfo选择规则和同一 `MediaSourceId` 语义，原生实现不能另选其它版本。
- 本地播放：复用进程内 playback context/locator，绝对路径不能进入Vue Router。
- 图片/字幕/弹幕：复用受控网络、大小、格式和重定向校验；离线资产另设持久目录，不引用可能被LRU清理的普通cache。
- 观看状态：离线播放继续使用原 `sourceId + mediaIdentity`，不能因为OfflineDataSource产生第二份历史。

## 关键安全不变量

- 下载任务、segment、offline snapshot、Vue事件、日志和错误均不得含URL、Header、Cookie、API Key、device token或签名参数。
- 302每次重新解析，跨origin删除Provider Header，HTTPS禁止降级。
- Range续传必须验证206、Content-Range起点和实体身份；不能验证时从头重启。
- 取消只清理任务精确拥有的partial/segment，不猜测删除用户目录中的其它文件。
- 下载root切换只影响未来任务；旧offline item继续引用创建时的受控root。
- 当前有独立任务修改 `server/`；本任务所有写入必须限制在 `player/` 与自己的 Trellis 任务目录，禁止修改或回滚任何 `server/` 文件。

## 现有验证入口

- `npm run verify:download-planning`
- `npm run verify:android-downloads`
- `npm run verify:server-datasource`
- `npm run verify:server-online-library`
- `npm run verify:media-actions`
- `npm run verify:danmaku`
- `npm run verify:secure-playback-routing`
- `npm run typecheck`
- `npm run lint`
- `npm run build`
- `cargo fmt --check`
- `cargo check`
- `cargo clippy --all-targets --all-features -- -D warnings`
- `cargo test`
