# 修复 Emby 活跃会上报和主页二次播放

## Goal

修复 OhMyCine Player 播放 Emby 媒体时 provider 同步链路失效的问题，确保 Emby 后台能看到当前活跃播放、播放开始/停止活动记录，并且本机播放日志保存后仍会 best-effort 同步到 Emby；同时修复从主页再次进入播放器时视频已加载但不会实际播放、必须手动暂停/播放才启动的问题。

## Requirements

- Emby 媒体播放开始后必须触发 provider playback sync，并向 Emby/Jellyfin playstate endpoints 上报 started/progress/stopped 或 completed。
- Emby 后台活跃状态应能识别 OhMyCine 当前播放会话，不能只写本机 SQLite。
- 本机播放日志仍是主记录；本机保存成功或进度事件发生后，Emby 同步失败不得阻塞播放或本机日志。
- Provider sync 失败时需要保留安全诊断信息，便于定位 HTTP 状态/阶段，但不得泄露 token、api_key、Authorization、完整 tokenized URL。
- 从主页或继续观看再次进入播放器时，即使 mpv 之前处于暂停状态，也应显式恢复播放并保持前端 `isPlaying` 与后端播放状态一致。
- 保留本机续播恢复、队列、字幕/音轨、播放速度和 Emby tokenized URL 不持久化规则。

## Acceptance Criteria

- [ ] 播放 Emby 影片/剧集后，Emby admin active/current playing 能看到 OhMyCine 会话。
- [ ] 播放几分钟后，Emby 继续观看/用户播放进度能更新。
- [ ] 停止/切换媒体时会发送 stopped/completed，不留下错误的活跃会话。
- [ ] 本机播放日志保存后，provider sync 仍会 best-effort 执行；失败只产生安全诊断，不影响 SQLite。
- [ ] 从主页点击已播放过的剧集/继续观看进入 Player 后，视频自动播放，不需要手动暂停再播放。
- [ ] `npm run typecheck --prefix player`、`npm run lint --prefix player`、`npm run build --prefix player`、Windows Tauri build 通过。

## Definition of Done

- 代码保持 DataSource 边界，UI 不直接耦合 Emby API。
- Provider sync contract 与 Emby endpoint 行为保持类型安全。
- 运行 Trellis check，并修复检查发现的问题。
- 完成任务状态流转。

## Out of Scope

- 不实现跨设备云同步队列。
- 不把 Emby tokenized 图片或 stream URL 存入 SQLite。
- 不把 Emby 同步失败升级为阻断播放的用户错误。

## Technical Notes

- 重点检查 `player/src/services/datasource/emby.ts`、`player/src/views/PlayerView.vue`、`player/src/composables/useMpv.ts` 和 Rust mpv commands。
- 参考既有研究：`.trellis/tasks/05-19-emby/research/emby-playback-progress-sync.md`。
- 重点确认 `syncProviderPlaybackStarted()` 是否触发、`/Sessions/Playing` 是否成功、`MediaSourceId`/`PlaySessionId` 是否来自真实 playback info，以及 mpv load 后是否显式 resume。
