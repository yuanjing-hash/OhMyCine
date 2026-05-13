# Player mpv 高级控制

## Goal

接入单媒体播放中的 mpv 高级控制：倍速、字幕/音轨选择、全屏切换、画面比例/填充模式，并保持 TypeScript/Tauri/Rust 合同一致。

## Requirements

- 倍速控制支持常用值，并记住上次选择。
- 字幕/音轨入口可展示 mpv 当前 track 列表，支持切换和关闭字幕。
- 全屏入口可切换 Tauri 窗口全屏，并触发 mpv underlay bounds 同步。
- 画面比例/填充模式通过 mpv property 实现至少一组可用模式。
- 所有新增 command / event / state 必须在 `useMpv.ts` 中类型化。
- command 错误文案必须用户安全，不泄露 URL/token/native handle。

## Acceptance Criteria

- [ ] 倍速可切换并跨媒体沿用。
- [ ] 字幕列表、音轨列表可显示并切换；无轨道时显示空态。
- [ ] 全屏/退出全屏可用，视频 underlay 跟随正常。
- [ ] 画面比例/填充模式可切换并立即作用于 mpv。
- [ ] `npm run typecheck` / `npm run lint` / `npm run build` 通过。
- [ ] `cargo check` 和 Windows GNU target check 通过。
- [ ] Windows 宿主验证控制可用。

## Out of Scope

- 外挂字幕搜索/下载。
- 字幕/音频延迟、高级滤镜、设备选择。

## Technical Notes

- Parent task: `05-12-player-playback-controls`.
- Likely files: `useMpv.ts`, `PlayerView.vue`, `PlayerControls.vue`, `player/src-tauri/src/commands/player.rs`, `player/src-tauri/src/mpv/player.rs`.
