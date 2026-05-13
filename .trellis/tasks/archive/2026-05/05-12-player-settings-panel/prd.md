# Player 播放设置面板

## Goal

在播放页 overlay 中新增符合常见视频播放器习惯的播放控制布局：底部播放控制条承载播放动作，播放设置按钮仅打开 picture/aspect ratio 画面设置 liquid-glass 浮层，并与现有控制条自动隐藏逻辑协同。

## Requirements

- 在播放页提供可发现的播放设置按钮，优先放在底部控制条或右下角播放相关区域。
- 产品规则：底部播放控制条拥有播放动作入口，包括倍速、字幕、音轨、播放队列/列表、全屏；这些入口不应埋在设置浮层中。
- 设置浮层当前仅承载画面/画面比例相关控制或占位，不展示倍速、字幕、音轨、队列、全屏等播放动作。
- 设置浮层不得展示最近播放/历史内容，不得包含窗口置顶/Always-on-top/pin 控件。
- 底部控制条右侧必须有视觉可发现的全屏按钮，优先切换整个播放器窗口全屏；若后端能力不可用，应安全降级而不影响播放。
- 设置浮层必须使用 Cinema OS / liquid-glass 风格，不跳转设置页。
- 浮层打开、hover、focus、拖动/点击时，Player chrome 不应自动隐藏。
- 浮层必须适配透明 Tauri/WebView overlay，不绘制全屏不透明背景遮挡视频。
- 播放页 chrome 可见时保留顶部左侧返回/回首页入口，避免沉浸模式下丢失导航。
- 渲染诊断不应作为常驻 chip 出现在正常播放 UI；仅通过快捷键或显式调试入口打开。

## Acceptance Criteria

- [x] 播放页有播放设置入口。
- [x] 底部控制条可见展示倍速、字幕、音轨、播放队列/列表和全屏入口。
- [x] 全屏按钮位于播放控制条最右侧，并可安全切换整个播放器窗口全屏或安全降级。
- [x] 设置浮层可打开/关闭，支持鼠标和键盘 focus。
- [x] 设置浮层打开期间底部控制条保持可见。
- [x] 设置浮层只展示画面/画面比例相关分区，不包含最近播放/历史、窗口置顶、倍速、字幕、音轨、队列或全屏分区。
- [x] 浮层不会破坏视频透明 overlay 显示。
- [x] 播放页 chrome 可见时有顶部左侧返回入口。
- [x] 正常播放 UI 不显示常驻渲染诊断 chip。
- [x] `npm run typecheck` / `npm run lint` / `npm run build` 通过。
- [x] `RUSTC="$(rustup which rustc)" npm run tauri:build:windows --prefix player` 通过。

## Out of Scope

- 实际 mpv 倍速/字幕/音轨/队列/画面比例命令接入。
- 播放历史和队列持久化。

## Technical Notes

- Parent task: `05-12-player-playback-controls`.
- Likely files: `PlayerView.vue`, `PlayerControls.vue`, `PlayerSettingsPanel.vue`, Tauri capability config if fullscreen window API requires permission.
