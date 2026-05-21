# 修复 Emby 云端播放进度同步

## Goal

让 OhMyCine Player 播放 Emby 视频时像官方客户端一样向 Emby 上报播放开始、进度、暂停/恢复和停止事件，使 Emby 后台活动记录出现“开始播放/停止播放”，并让 Emby continue-watching/Resume 能看到对应进度。

## Requirements

* 播放 Emby 媒体时必须向 Emby 上报有效 playstate，不只是本机 SQLite 历史。
* 上报开始播放时，Emby 后台活动记录应能出现类似“用户在设备上开始播放 xxx”的记录。
* 停止/切换/退出播放时，Emby 后台活动记录应能出现停止播放记录，并携带当前进度。
* 进度同步必须包含稳定 `ItemId`、`MediaSourceId`、`PlaySessionId`（可取到时）、`PositionTicks`、`PlayMethod`、`CanSeek`、`IsPaused` 等关键字段。
* `PlaybackInfo` 请求应按 Emby Web/类似客户端模式携带 `StartTimeTicks`、`CurrentPlaySessionId`、`IsPlayback=true` 等上下文，复用/刷新 `PlaySessionId`。
* `/Sessions/Playing...` 是主同步路径；旧 `/Users/{UserId}/PlayingItems...` 作为兼容 fallback，并按 Emby 文档补齐 `MediaSourceId`、`NextMediaType`、进度 query/body 字段。
* Provider 同步仍是 best-effort，不能阻塞本机 SQLite 历史、播放、切换、退出。
* 同步失败不得泄漏 tokenized URL、Emby token、stream URL 或本地路径。

## Acceptance Criteria

* [ ] 播放 Emby 视频后，Emby 后台活动记录能看到 OhMyCine 设备的开始播放记录。
* [ ] 暂停/恢复/播放过程中，Emby 能收到进度更新，Resume/Continue Watching 能反映进度（受 Emby 自身最小/最大续播阈值约束）。
* [ ] 停止、切换队列、离开播放器或关闭窗口时，Emby 能收到 stopped/delete 上报，且不会误清掉有效中途进度。
* [ ] `MediaSourceId` / `PlaySessionId` 缺失时会重新 `PlaybackInfo` 获取；仍取不到时 session endpoint 继续 best-effort，legacy endpoint 不发送缺少 required `MediaSourceId` 的请求。
* [ ] 本机 SQLite 历史保存和续播恢复仍然优先且不受 Emby 同步失败影响。
* [ ] `npm run typecheck --prefix player`、`npm run lint --prefix player`、`npm run build --prefix player`、Windows Tauri package 通过。

## Definition of Done

* 研究结果已持久化到 `research/emby-playback-progress-sync.md`。
* Emby progress sync 按研究结论修复并通过 Trellis check。
* Code-spec 同步记录 Emby 云端 playstate 上报约束。

## Research References

* [`research/emby-playback-progress-sync.md`](research/emby-playback-progress-sync.md) — Emby/Jellyfin 客户端通过 PlaybackInfo + Sessions/Playing/Progress/Stopped + legacy PlayingItems 携带 ItemId/MediaSourceId/PlaySessionId/PositionTicks 来产生后台活动和 Resume 进度。

## Technical Approach

* 扩展 `ProviderPlaybackProgressInput` 以传递 resume/start position、duration、必要播放状态字段；在 PlayerView 启动同步前读取 route/local resume 作为 `StartTimeTicks`。
* EmbyDataSource 保留当前 item 的 `MediaSourceId` / `PlaySessionId`，并在 `PlaybackInfo` 请求中传入 `StartTimeTicks` 和 `CurrentPlaySessionId`。
* Session body payload 补齐 Emby Web 常见字段：`ItemId`、`MediaSourceId`、`PlaySessionId`、`PositionTicks`、`RunTimeTicks`、`CanSeek`、`IsPaused`、`PlayMethod`、`EventName`、`PlaybackStartTimeTicks`、`PlaybackRate`。
* Legacy user/item fallback 补齐 `NextMediaType`，progress body 也带 `EventName`，避免 required query 缺失导致完全无效。
* 在成功或尝试上报后清理/刷新 Emby home cache 中 Resume 相关数据，避免首页继续观看使用旧状态。

## Decision (ADR-lite)

**Context**: 用户在 Emby 后台看不到 OhMyCine 的任何开始/停止播放活动，说明当前同步请求没有被 Emby 当作有效 playstate report；只修首页合并和本机历史不足以解决云端记录问题。

**Decision**: 按 Emby/Jellyfin Web 客户端模式实现完整 playback report：先 PlaybackInfo 获取/刷新 session，再以 `/Sessions/Playing...` 为主、legacy `/Users/{UserId}/PlayingItems...` 为 fallback 上报开始、进度和停止。

**Consequences**: 会增加播放开始/同步时的 Emby API 请求，但保持 best-effort，不影响播放。Resume 可见性仍受 Emby 自身阈值影响：太靠前、太接近结尾或已播放状态可能不会出现在 Continue Watching。

## Out of Scope

* 不实现 Jellyfin DataSource，只参考 Jellyfin/Emby 客户端模式修 Emby。
* 不绕过 Emby 服务器的 MinResume/MaxResume/已播放策略。
* 不在 UI 中展示 tokenized stream/artwork/progress URL。

## Technical Notes

* `player/src/views/PlayerView.vue` 当前负责本机历史保存和 provider sync 事件触发。
* `player/src/services/datasource/emby.ts` 当前已有 `/Sessions/Playing...` 与 legacy `/Users/{UserId}/PlayingItems...`，但字段和会话刷新需要补强。
* Emby 后台“开始播放/停止播放”活动记录来自 playstate report，不是 `/Items/{id}/PlaybackInfo` 本身。
