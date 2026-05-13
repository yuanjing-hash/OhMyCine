# Player 播放队列与剧集切换

## Goal

为播放页建立第一版播放队列/剧集队列模型，让上一集/下一集按钮在有队列上下文时可用，并为后续连续播放打基础。

## Requirements

- 定义播放队列项结构，至少包含 sourceId/libraryId/id/title/path/type/poster/backdrop/season/episode 等必要字段。
- 播放页可接收或恢复当前队列上下文。
- PlayerControls 的上一集/下一集按钮在有前后项时启用，否则保持 disabled。
- 点击上一集/下一集会加载对应媒体并更新当前队列位置。
- 本地文件单媒体播放没有队列时行为不回退。

## Acceptance Criteria

- [ ] 队列类型和状态管理存在且类型安全。
- [ ] 上一集/下一集按钮按队列状态启用/禁用。
- [ ] 切换前后项能更新媒体标题、路径和播放状态。
- [ ] 没有队列时本地/Emby 单片播放不受影响。
- [ ] `npm run typecheck` / `npm run lint` / `npm run build` 通过。

## Out of Scope

- 完整队列编辑器。
- 跨设备队列同步。
- 自动连播策略和片尾识别。

## Technical Notes

- Parent task: `05-12-player-playback-controls`.
- Likely files: `PlayerView.vue`, `PlayerControls.vue`, datasource detail/list play entry points, possible new composable/store.
