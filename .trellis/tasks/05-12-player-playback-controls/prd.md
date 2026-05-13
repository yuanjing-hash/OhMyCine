# Player 播放控制增强

## Goal

在 Windows 嵌入式视频渲染已经可用的基础上，补齐播放页的第一批高级控制能力，让用户不离开沉浸式播放页即可完成常用播放设置：倍速、字幕/音轨选择、全屏/窗口模式、画面比例/填充方式，并为后续播放列表、剧集队列、继续观看、播放历史留出清晰边界。

## What I already know

- 上一个任务已完成 Windows 透明 Tauri/WebView overlay + mpv owned top-level HWND underlay，用户已验证播放画面和控制层正常。
- 当前 `PlayerControls.vue` 已有播放/暂停、前进/后退 10 秒、进度条、音量控制。
- `PlayerControls.vue` 里上一集/下一集按钮目前是 disabled，标题写着“播放列表待接入”。
- `useMpv.ts` 已暴露 `setSubtitle(index)` / `setAudio(index)`，但当前字幕/音轨列表 `subtitleTracks` / `audioTracks` 还没有从 mpv 事件或命令填充。
- `PlayerView.vue` 已有右上角诊断入口、底部控制条自动隐藏、透明播放层、全局快捷键 `Ctrl/Cmd+Shift+D`。
- 上个任务 PRD 记录的后续事项包括：字幕/音轨入口、自定义右键菜单与播放详情、倍速控制并记住上次倍速、全屏按钮、右下角播放设置按钮与悬浮设置面板、画面比例/画面模式、播放列表、剧集队列、继续观看、播放历史。

## Assumptions (temporary)

- 本任务优先做“单个正在播放媒体”的控制增强，不先做媒体队列/播放历史数据库。
- 播放设置入口应保持 Cinema OS / liquid-glass 风格，作为播放页 overlay 的一部分，而不是跳转到设置页。
- 播放控制增强应复用现有 `useMpv` 和 Tauri command 边界，不绕开 mpv composable 直接在组件里 `invoke`。

## Open Questions

- 无。用户已确认本任务纳入完整播放体验包，播放历史/继续观看进度使用 Tauri app data 下的 SQLite 持久化。

## Requirements (evolving)

- 播放页需要新增一个可发现的播放设置入口，适合承载倍速、字幕、音轨、画面比例等设置。
- 倍速控制必须可切换常用速度，并记住用户上次选择，后续播放默认沿用。
- 字幕/音轨入口必须接入 mpv track 状态：可列出、切换、关闭字幕；没有可用轨道时显示空态。
- 播放页需要提供全屏/退出全屏入口，并保持透明 overlay + mpv underlay 的窗口跟随。
- 播放页需要支持基础画面比例/填充模式切换，优先使用 mpv property 实现。
- 播放页需要提供自定义右键菜单或等效长按/菜单入口，展示播放详情与常用操作，不使用浏览器默认菜单作为主要 UX。
- 播放页需要接入播放列表/剧集队列的第一版数据结构，让上一集/下一集按钮从 disabled 变为可用条件下可点击。
- 播放页需要记录播放历史和继续观看进度，数据存储在 Tauri app data 下的 SQLite 中，供首页/媒体库已有 continueWatching 区域后续或本任务内消费。
- 播放页控制层不能破坏当前透明 overlay + mpv underlay 的视频显示和点击命中。
- 新增控制必须保持现有播放/暂停、进度、音量、自动隐藏、诊断入口、本地/Emby 播放流程不回退。

## Acceptance Criteria (evolving)

- [ ] 播放页有一个右下角或控制条内的播放设置入口，使用 liquid-glass 浮层样式。
- [ ] 倍速控制可切换常用速度，并能记住上次选择。
- [ ] 字幕/音轨入口能展示当前可用选项；若当前后端暂未提供列表，UI 必须显示诚实的空/未检测状态。
- [ ] 画面比例/填充模式入口可切换至少一种 mpv 可执行的视频缩放/适配行为。
- [ ] 全屏入口可在播放页触发窗口全屏/退出全屏，不破坏 mpv underlay 跟随。
- [ ] 自定义右键菜单或等效播放详情菜单可展示媒体标题、来源、时长、当前时间、渲染/播放基础状态，并提供常用动作入口。
- [ ] 播放列表/剧集队列数据结构存在；当队列有前后项时，上一集/下一集按钮可切换媒体。
- [ ] 播放历史和继续观看进度会随播放更新保存到 Tauri SQLite，并能在恢复播放时用于定位或展示。
- [ ] `npm run typecheck` / `npm run lint` / `npm run build` 通过。
- [ ] `cargo check` 通过；涉及 Windows window/mpv command 时，Windows GNU target check 和 package build 通过。
- [ ] Windows 宿主手动验证播放画面、设置浮层点击、自动隐藏、全屏/还原行为。

## Definition of Done

- MVP 范围内的播放设置可以在沉浸式播放页完成。
- 新增 Tauri command / mpv property 合同在 TypeScript 类型中同步更新。
- 失败/不支持/无轨道等状态有明确 UI，不假装功能可用。
- 不把播放队列、历史、继续观看等持久化功能混进本任务，除非用户明确扩大范围。

## Out of Scope (draft)

- 字幕文件下载/在线匹配/字幕搜索。
- 音频设备选择、音频延迟、字幕延迟等高级 mpv 调参。
- 跨设备同步播放历史/进度。
- Server 侧播放历史同步 API。
- Linux/macOS/mobile 渲染后端。

## Technical Approach

1. Add a playback settings surface in the existing player overlay rather than adding a separate route. It should coordinate with `controlsInteracting` so auto-hide pauses while the menu is hovered/focused.
2. Extend `useMpv` and Rust player commands for playback-rate, track-list refresh/switching, fullscreen/window mode, and video aspect/fit properties. Keep command responses typed and user-safe.
3. Add a small playback queue model for current media plus previous/next items. The first version can derive queue candidates from navigation context when available and keep local/manual queue operations minimal.
4. Add Tauri SQLite playback state storage for history and continue watching: media identity, source/library IDs when available, title, path/stream identity, last position, duration, updated time, and completed flag.
5. Wire Home/Source continue-watching sections to local playback state only where it does not conflict with existing Emby-provided continue-watching. If merging is too broad, expose the storage and restore behavior first, then leave aggregation as a follow-up.

## Decision (ADR-lite)

**Context:** The user wants the full playback experience improvements in one task: playback settings, tracks, fullscreen, aspect mode, queue, continue watching, and history. Progress/history must survive app restarts.

**Decision:** Include the full experience package in this task, but implement it as milestones behind clear typed boundaries. Persist playback history and continue-watching progress in Tauri app-data SQLite, not browser `localStorage`.

**Consequences:** The task is larger than a UI-only enhancement and touches Rust commands, SQLite schema, Vue player components, datasource/home surfaces, and Windows runtime behavior. The benefit is a durable foundation for local library, queue, and future sync features.

## Technical Notes

- Existing persistence/reference points:
  - `player/src-tauri/src/commands/credential.rs` already uses `rusqlite` under Tauri app data for credential storage.
  - `player/src/stores/datasource.ts` uses localStorage only for non-sensitive DataSource config.
  - `player/src/services/datasource/types.ts` already has `HomeSection.type = 'continueWatching'` and `MediaDetail.subtitles/audioTracks` types.
  - `player/src/services/datasource/emby.ts` already maps Emby continue-watching sections, so local history must not blindly duplicate/conflict with provider history.
- Likely frontend files:
  - `player/src/views/PlayerView.vue`
  - `player/src/components/player/PlayerControls.vue`
  - `player/src/components/player/VideoPlayer.vue`
  - `player/src/composables/useMpv.ts`
- Likely backend files:
  - `player/src-tauri/src/commands/player.rs`
  - `player/src-tauri/src/mpv/player.rs`
  - `player/src-tauri/src/main.rs` if fullscreen/window commands need wiring
  - new or existing Tauri command module for playback history SQLite storage
- Relevant specs:
  - `.trellis/spec/frontend/component-guidelines.md`
  - `.trellis/spec/frontend/type-safety.md`
  - `.trellis/spec/frontend/quality-guidelines.md`

## Subtasks

- `05-12-player-settings-panel` — 播放设置入口与 liquid-glass 浮层。
- `05-12-player-mpv-advanced-controls` — 倍速、字幕/音轨、全屏、画面比例等 mpv 高级控制。
- `05-12-player-playback-queue` — 播放队列与上一集/下一集切换。
- `05-12-player-history-continue-watching` — Tauri SQLite 播放历史与继续观看。
- `05-12-player-playback-context-menu` — 播放详情与自定义右键菜单。

## Implementation Plan

- Milestone 1: playback settings shell — floating/settings panel, interaction/autohide integration, typed UI state.
- Milestone 2: mpv controls — playback rate, subtitle/audio track refresh + switch, aspect/fit mode, fullscreen command path.
- Milestone 3: queue — current queue model, previous/next enablement, route/load integration.
- Milestone 4: history/progress — Tauri SQLite schema + commands, periodic progress saves, restore/continue behavior.
- Milestone 5: home/library integration and polish — surface local continue watching where safe, right-click/playback detail menu, validation and Windows runtime check.

## Expansion Sweep

### Future evolution

- 播放设置浮层后续可以扩展到字幕延迟、音频延迟、HDR/色调映射、硬解状态、外挂字幕加载。
- 上一集/下一集按钮后续应接入播放列表/剧集队列，而不是在本任务里临时硬编码。

### Related scenarios

- 本地文件、Emby 电影、Emby 剧集都应走同一套播放设置 UI。
- 设置浮层和现有自动隐藏逻辑需要协调：用户 hover/focus/点击浮层时控制层不能自动消失。

### Failure / edge cases

- 当前媒体没有字幕/多音轨时，UI 应显示“无可用字幕/音轨”而不是隐藏整个入口。
- mpv property 设置失败时，UI 应保留旧值并展示安全错误，不泄露 URL/token/path。
- 全屏/还原必须触发 mpv underlay bounds 重新同步。
