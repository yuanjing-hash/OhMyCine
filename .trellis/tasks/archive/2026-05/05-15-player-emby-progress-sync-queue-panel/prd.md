# Player Emby 播放进度同步与队列面板

## Goal

完善 Player 播放历史与队列体验：Emby 渠道播放进度在本机 SQLite 保存的同时同步到 Emby 服务端；播放电视剧/剧集队列时底部“队列”按钮可打开队列面板，展示当前剧集列表并支持点击切换。

## What I already know

- 用户确认：Emby 渠道的 Player 播放历史/继续观看应该同步到 Emby 服务端，而不是只保存在本机。
- 当前本机播放历史已经使用 Tauri app-data SQLite 保存，并能驱动本机继续观看。
- 当前电视剧播放时上一集/下一集按钮已经可用，说明队列上下文存在。
- 当前底部“队列”按钮仍是灰色不可点；用户期望它可打开并显示剧集队列。
- 队列面板应显示剧集条目，每项包含标题、小简介和小缩略图，并可点击切换播放。
- 不能在 UI、日志、SQLite、错误文案中暴露 tokenized stream/image URL。

## Assumptions (temporary)

- Emby 进度同步使用 Emby 官方播放进度/停止上报 API，失败不阻塞本机播放和本机历史保存。
- 队列面板先做只读播放队列，不做拖拽排序、删除、跨剧集编辑。
- 队列简介优先使用队列项已有 overview；若当前队列模型缺字段，则扩展队列项保存必要展示元数据。

## Open Questions

- 无阻塞问题；先按 MVP 方案实现本机历史必写、Emby 同步尽力而为、队列面板只读可切集。

## Requirements

- Emby DataSource 提供播放进度同步能力，用于上报播放中/暂停/停止/完成等进度状态。
- Player 保存本机进度时，对 Emby 来源额外同步到 Emby 服务端。
- Emby 同步失败不影响播放、不影响本机 SQLite 历史保存；错误必须用户安全且不泄露 tokenized URL。
- 队列按钮在当前有多项队列时可点击，不应灰色禁用。
- 队列面板从底部播放栏打开，遵守沉浸式播放器液态玻璃风格，不遮挡成完整编辑器。
- 队列面板展示当前队列条目：缩略图、标题、简介/集数信息、当前播放态。
- 点击队列条目切换到对应媒体，并更新当前队列位置、标题、播放路径和播放状态。
- 没有队列或只有单项媒体时，队列入口仍隐藏或禁用，不影响本地单文件/单片播放。

## Acceptance Criteria

- [x] Emby 播放进度可按播放中/暂停/停止/完成时机同步到 Emby 服务端。
- [x] Emby 同步失败不会阻断播放或本机历史保存，且不会暴露 tokenized URL。
- [x] 播放电视剧队列时，底部“队列”按钮可点击打开面板。
- [x] 队列面板显示多集条目，每项至少包含缩略图、标题、简介或集数信息。
- [x] 点击队列条目能切换播放并更新当前队列状态。
- [x] 无队列/单项播放时现有行为不回退。
- [x] `npm run typecheck --prefix player` / `npm run lint --prefix player` / `npm run build --prefix player` 通过。
- [x] `cargo check --manifest-path player/src-tauri/Cargo.toml` 通过（如 Rust 未改可说明）。
- [x] `RUSTC="$(rustup which rustc)" npm run tauri:build:windows --prefix player` 通过。

## Definition of Done

- Emby 服务端进度同步和本机 SQLite 历史边界清晰。
- 队列面板是用户可见、可操作的播放队列入口。
- 规格记录 DataSource/provider progress sync 与 queue panel 的关键合同。
- 用户可在 Windows 宿主验证真实 Emby 剧集播放、切集、队列面板、进度同步。

## Research References

- [`research/emby-progress-api.md`](research/emby-progress-api.md) — Emby 提供 `/Sessions/Playing...` 和 `/Users/{UserId}/PlayingItems...` 进度上报端点；当前实现需要保留 `PlaySessionId` / `MediaSourceId` 才能可靠同步。

## Technical Approach

1. 在 DataSource contract 增加可选 provider progress sync 方法，避免 PlayerView 直接依赖 Emby 类型。
2. EmbyDataSource 使用现有 `request()` 认证/redaction helper 调用 Emby 播放进度 API；优先采用 `/Sessions/Playing`、`/Sessions/Playing/Progress`、`/Sessions/Playing/Stopped` body 端点，并保留 `/Items/{id}/PlaybackInfo` 返回的 `PlaySessionId`。
3. Player 本机 SQLite 历史仍是主路径；保存本机进度成功/尝试时，对支持 provider sync 的 DataSource 尽力同步，失败不阻塞播放。
4. 扩展播放队列项展示字段（overview/backdrop/poster/season/episode/duration），确保队列面板无需再请求敏感播放 URL。
5. 在 PlayerControls 或相邻组件中实现队列面板：队列按钮有多项时可点，面板列出条目并 emit 选择事件给 PlayerView。
6. PlayerView 复用现有 `playQueueItemAt` 切换逻辑，避免新建另一套播放路径；队列切换时保留/重新获取 `mediaSourceId` 与 `playSessionId`。

## Decision (ADR-lite)

**Context**: Player 已有本机 SQLite 历史，但 Emby 用户预期继续观看/已看状态能回到 Emby 服务端；同时队列上下文已经存在却没有可点击的队列 UI。

**Decision**: 本任务采用“本机历史必写 + provider 同步尽力而为 + 只读队列面板”的 MVP。DataSource 暴露可选同步方法，Emby 实现 provider 进度上报；PlayerControls 提供队列面板但不做完整队列编辑器。

**Consequences**: 本机播放体验不依赖 Emby 网络同步，Emby 多端状态会逐步一致；后续 Jellyfin 或 Server 同步可复用可选 provider progress 方法。失败重试队列、跨设备 OhMyCine 自有同步、队列编辑器留到后续任务。

## Out of Scope

- 完整队列编辑器（排序、删除、插入、跨来源拼队列）。
- Server 同步或多设备 OhMyCine 自有云同步。
- 反向批量同步 Emby watched state 到本机历史。
- 片尾识别和自动连播策略。

## Technical Notes

- Parent task: `05-12-player-playback-controls`.
- Likely files: `player/src/services/datasource/types.ts`, `player/src/services/datasource/emby.ts`, `player/src/services/playbackHistory.ts`, `player/src/services/playbackContext.ts`, `player/src/views/PlayerView.vue`, `player/src/components/player/PlayerControls.vue`, possible new queue panel component.
- Need research reference for Emby playback progress endpoints before implementation.
