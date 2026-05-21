# 复盘修复 Emby active 和播放记录同步

## Goal

修复 OhMyCine Player 播放 Emby 媒体时 Emby 后台 active/current playing 和播放记录仍然没有出现的问题。这次不能只继续猜 payload，需要先让 Emby provider sync 链路具备 token-safe 可观测性，确认 started/progress/stopped 请求是否触发、命中哪个 endpoint、返回成功还是失败，再根据真实失败点修复。

## Requirements

- 播放 Emby 媒体时，Player 必须能在诊断面板看到最近 provider sync 尝试：event、endpoint family、HTTP 成功/失败、脱敏错误、itemId/mediaSourceId/playSessionId 是否存在。
- 诊断信息不得展示或持久化 token、api_key、Authorization、tokenized stream URL、完整敏感 URL。
- Emby sync 必须明确区分三类失败：未触发、缺少源/身份/session 元数据、HTTP 请求失败或 Emby 不接受。
- active/current playing 由 `/Sessions/Playing` family 负责；legacy `/Users/{UserId}/PlayingItems...` 只能作为 provider progress fallback，不能掩盖 active session 失败。
- 本机 SQLite 播放日志仍是主记录，Emby provider sync 仍然 best-effort，不得阻塞播放。
- 修复后播放 Emby 内容时，Emby 后台应显示 active/current playing；播放一段时间后 Emby 继续观看/播放记录应更新。

## Acceptance Criteria

- [ ] 播放 Emby 内容后，诊断面板能看到 `started` 上报阶段及结果。
- [ ] 播放超过进度阈值后，诊断面板能看到 `progress` 上报阶段及结果。
- [ ] 停止/切换媒体后，诊断面板能看到 `stopped` 或 `completed` 阶段及结果。
- [ ] 若 Emby 拒绝请求，错误信息必须脱敏且足够定位 endpoint/status。
- [ ] 若 Emby 接收请求，后台 active/current playing 和播放记录应能被用户手动验证。
- [ ] `npm run typecheck --prefix player`、`npm run lint --prefix player`、`npm run build --prefix player`、Windows Tauri build 通过。

## Definition of Done

- DataSource abstraction 不被 UI 直接穿透成 Emby-specific API 调用。
- Provider sync 诊断不持久化敏感信息。
- Trellis check 完成并处理发现的问题。

## Out of Scope

- 不改 Emby 服务端配置。
- 不持久化 tokenized 图片/播放 URL。
- 不把 Emby sync 失败变成阻塞播放的错误弹窗。

## Technical Notes

- 重点检查 `player/src/views/PlayerView.vue` 是否实际触发 sync、`player/src/services/datasource/emby.ts` request 是否成功、`DataSourceManager` 是否保留播放中的 provider 实例。
- 参考 `.trellis/tasks/05-19-emby/research/emby-playback-progress-sync.md` 和 `.trellis/spec/frontend/type-safety.md`。
- 这次应优先增加安全诊断闭环，避免继续凭 Emby 后台无变化猜测。
