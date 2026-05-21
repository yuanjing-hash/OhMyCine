# 修复 Player Emby 播放记录与续播状态

## Goal

修复 Player 在 Emby 媒体播放后的进度同步、本机续播恢复、媒体详情页续播入口状态，让 Emby 渠道与本机 SQLite 历史形成一致体验：本机续播可靠，Emby 服务端能看到播放进度，详情页能准确提示继续播放。

## Requirements

- Emby 渠道播放时，Player 必须向 Emby 服务端发送可被 Emby Web 识别的播放 started/progress/stopped 或 completed 进度事件。
- Emby 同步必须带上可解析的 `ItemId`、`MediaSourceId`、`PlaySessionId`（当可用）和当前位置 ticks；缺失会导致同步失败时应自动补全或降级，但不得阻塞本机历史保存。
- 本机 SQLite 播放历史仍是主数据源；离开 Player、切换队列、暂停/停止时必须尽可能保存当前进度。
- 再次打开同一媒体时，Player 必须在媒体加载就绪后恢复到本机历史中可续播的位置，避免加载/seek 竞态导致 seek 丢失。
- 媒体详情页必须读取本机播放历史，对已播放但未完成的电影/剧集显示 `继续播放`，并在剧集列表中展示可续播状态。
- 对 Emby 已有 `UserData.PlaybackPositionTicks` 的条目，详情页可作为增强参考；本机历史优先。
- 不展示、不持久化 tokenized stream URL；错误消息继续使用安全文案。

## Acceptance Criteria

- [ ] 播放 Emby 媒体超过保存阈值后，离开 Player 或暂停/停止，Emby Web 端能看到该条目的继续观看/播放进度。
- [ ] 重新播放同一 Emby 媒体时，Player 会恢复到上次本机保存的位置。
- [ ] 从详情页打开已部分播放的电影/单集时，主按钮显示 `继续播放`。
- [ ] 剧集详情页的分集列表能标识已有本机续播进度的集数。
- [ ] 队列切换上一集/下一集时，旧条目保存 stopped 进度，新条目按对应身份读取续播进度。
- [ ] Provider 同步失败不会影响本机历史保存、队列切换、路由离开或播放控制。
- [ ] TypeScript 类型检查、lint、前端构建、Rust cargo check、Windows Tauri 打包通过。

## Definition of Done

- 修复代码并保持 DataSource 抽象边界。
- 覆盖 Emby 进度同步、本机续播、详情页续播入口三个问题。
- 更新必要 code-spec（若实现中发现新的跨层合同修正）。
- 运行项目要求的验证命令。
- 使用检查代理复核实现。

## Technical Approach

- 检查并修正 Emby `syncPlaybackProgress` 请求体，确保 `MediaSourceId`/`PlaySessionId` 来源稳定：优先播放上下文/路由，其次 Emby `PlaybackInfo` 缓存补全。
- 调整 PlayerView 的进度保存与 provider sync 调度：开始/暂停/停止/离开/队列切换都触发正确事件；provider sync fire-and-forget。
- 将续播 seek 延迟到加载后可执行的时机，必要时短暂等待播放时间/时长状态，避免立即 seek 被底层加载覆盖。
- 在 MediaDetailView 读取 `getPlaybackProgress`，为 detail item 与 episodes 建立本机续播 map，驱动按钮文案和分集状态。

## Out of Scope

- 不实现跨设备历史冲突合并。
- 不把 Emby 服务端历史作为本机 SQLite 的覆盖来源。
- 不新增可编辑播放历史管理页。
- 不改变队列面板交互范围。

## Technical Notes

- 相关 specs：`.trellis/spec/frontend/type-safety.md`、`.trellis/spec/frontend/component-guidelines.md`。
- 关键代码：`player/src/views/PlayerView.vue`、`player/src/views/MediaDetailView.vue`、`player/src/services/datasource/emby.ts`、`player/src/services/playbackHistory.ts`、`player/src-tauri/src/commands/history.rs`。
- 既有约束：本机 SQLite 是主历史；provider sync best-effort；tokenized URLs 不展示不持久化。