# Player mpv 高级控制

## Goal

接入单媒体播放中的 mpv 高级控制：倍速、字幕/音轨选择、全屏切换、画面比例/填充模式，并保持 TypeScript/Tauri/Rust 合同一致。

## Requirements

- 倍速控制提供 0.5x、0.75x、1.0x、1.25x、1.5x、2.0x 常用值，通过 mpv `speed` property 应用，并通过 Tauri app data 下的 SQLite Player 偏好持久化后跨媒体沿用（不使用浏览器 localStorage）。
- 字幕入口从 mpv `track-list` 读取当前字幕轨道，并合并媒体详情/DataSource 已知字幕元数据；支持关闭字幕、按 mpv track id 切换、可加载外部字幕通过 `sub-add` 加载，暂不可加载的详情字幕必须显示为禁用项并说明原因。
- 音轨入口从 mpv `track-list` 读取当前音轨，支持按 track id 切换；底部栏音轨控制在 0/1 条可选音轨时隐藏，仅多音轨时显示；读取失败时使用安全错误文案，不泄露内部细节。
- 播放队列入口在队列为空或只有 1 项时隐藏，仅多项队列时显示。
- 全屏入口切换 Tauri Player 窗口全屏/退出全屏，失败时回退浏览器 fullscreen，并在状态变化后触发 mpv underlay bounds 重新同步。
- 底部栏设置入口统一显示为“设置”，当前设置面板只包含画面比例与填充模式：通过 mpv `video-aspect-override` 和 `panscan` 应用基础模式；不安全或未实现的拉伸模式保持禁用占位。
- 非全屏状态切换显式画面比例时，在应用 mpv 画面比例后尝试同步调整 Player 应用窗口比例；全屏状态只调整 mpv 行为，不调整窗口，失败时显示用户安全提示并保持已选 mpv 设置。
- Player UI 不展示“MVP”字样。
- 用户点击 Player 返回按钮、浏览器/路由导航离开 `/player`、或 Player 视图卸载时，必须停止/暂停当前播放并清理透明覆盖层状态，避免隐藏视频继续在后台播放。
- 普通 Player 设置面板与播放控制 UI 不展示 `mpv` / `MPV`、`video-aspect`、`panscan` 等内部实现标签或属性名；诊断面板可保留必要的后端细节。
- 所有新增 command / event / state 必须在 `useMpv.ts` 中类型化。
- command 错误文案必须用户安全，不泄露 URL/token/native handle。

## Acceptance Criteria

- [x] 倍速可切换并跨媒体沿用。
- [x] 字幕列表合并 mpv 内嵌轨道与媒体详情/DataSource 已知字幕；可加载外部字幕可选择，暂不可加载字幕禁用并显示原因；无轨道时显示空态。
- [x] 多音轨时音轨列表可显示并切换；0/1 条音轨时底部栏音轨控制隐藏。
- [x] 播放队列入口仅在队列多于 1 项时显示。
- [x] 离开 `/player`（顶部返回、浏览器/路由导航、视图卸载）会停止/暂停播放并移除透明播放层状态，避免后台继续播放。
- [x] 全屏/退出全屏可用，视频 underlay 跟随正常，且全屏按钮保持在底部栏最右侧。
- [x] 设置入口显示为“设置”，面板当前只包含画面比例/填充模式。
- [x] 画面比例/填充模式可切换并立即作用于 mpv；非全屏显式比例切换会尝试同步调整窗口比例。
- [x] Player UI 不显示“MVP”文案。
- [x] 普通 Player 设置面板与播放控制 UI 不暴露 `mpv` / `MPV`、`video-aspect`、`panscan` 等内部实现标签或属性名。
- [x] `npm run typecheck` / `npm run lint` / `npm run build` 通过。
- [x] `cargo check` 和 Windows GNU target check 通过。
- [ ] Windows 宿主验证控制可用（由用户在 Windows 宿主手动打开打包产物验证；当前实现保持此项未勾选，除非用户确认运行结果）。

## Out of Scope

- 外挂字幕搜索/下载。
- 字幕/音频延迟、高级滤镜、设备选择。

## Technical Notes

- Parent task: `05-12-player-playback-controls`.
- Likely files: `useMpv.ts`, `PlayerView.vue`, `PlayerControls.vue`, `PlayerSettingsPanel.vue`, `VideoPlayer.vue`, `player/src-tauri/src/commands/player.rs`, `player/src-tauri/src/mpv/player.rs`.
- 普通用户界面使用“画面比例”“画面适配”等产品化文案；`mpv` 属性名只保留在代码、开发文档或显式诊断信息中。
