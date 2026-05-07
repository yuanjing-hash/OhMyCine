# 完善 Player 文件打开与返回导航交互

## Goal

继续按 `docs/architecture/06-roadmap.md` 推进 Player 当前实现，优先把“选择本地视频文件并播放”做成用户可发现、可使用的核心能力，同时补齐非首页返回导航，避免 roadmap 标记与真实 UX 不一致。

## What I already know

- 用户发现 roadmap 中一直写“拖拽视频到窗口播放”，但在当前 UI 中找不到对应能力。
- 用户希望右下角悬浮窗中的播放按钮可点击，点击后打开文件选择器，选择支持格式的视频后直接进入播放。
- 用户希望除首页之外的所有页面，包括播放页面，都有返回按钮。
- 当前 Player 路由包括 `/`、`/player`、`/source/:sourceId`、`/settings`。
- 当前 `FloatingControls.vue` 的播放按钮只是裸按钮，没有 click handler。
- 当前没有 Tauri dialog plugin，也没有 native open-file command。
- 当前拖拽播放是基础实现：`VideoPlayer.vue` 在播放页内处理 drop，读取 Tauri WebView 的 `File.path` 并通过 `PlayerView.vue` 调用 `useMpv().load(path)`。
- 当前拖拽入口只在播放页视频区域可见，不能算用户在其他页面可发现的“拖拽视频到窗口播放”。

## Assumptions (temporary)

- 本任务优先解决桌面 Player 的本地文件选择与返回导航，不扩展到移动端。
- 文件选择器使用 Tauri 原生 dialog 能力，而不是浏览器 `<input type="file">`，因为播放后端需要本地路径给 libmpv。
- “支持格式”先按常见视频扩展名过滤，播放失败时由现有 mpv load 错误路径反馈。

## Requirements (evolving)

- 右下角悬浮播放按钮点击后打开本地视频文件选择器。
- 文件选择器使用宽松视频格式过滤：`mp4`, `mkv`, `avi`, `mov`, `webm`, `m4v`, `flv`, `wmv`, `ts`, `m2ts`, `rmvb`, `mpg`, `mpeg`, `3gp`, `ogv`, `divx`, `vob`, `iso`。
- 用户选中文件后进入 `/player` 并加载该文件播放。
- 如果用户取消选择，不改变当前页面状态。
- 除首页 `/` 之外的页面都提供清晰返回按钮。
- 播放页也必须有返回按钮。
- 返回按钮行为采用：优先返回浏览历史；没有可返回历史时回到首页 `/`。
- 返回按钮、文件打开按钮和相关 hover/active 状态必须沿用当前菜单/悬浮控件的 Cinema OS / liquid-glass 视觉风格，不引入割裂样式。
- 核实 roadmap 中“拖拽视频到窗口播放”的状态：如果只在播放页局部实现，应改成准确描述，或在本任务中补齐更可发现入口。

## Acceptance Criteria (evolving)

- [ ] 右下角悬浮播放按钮可点击，不再是无效按钮。
- [ ] 点击悬浮播放按钮会打开系统文件选择器。
- [ ] 文件选择器使用宽松视频格式过滤。
- [ ] 选择支持格式视频文件后进入播放页并调用 mpv 加载。
- [ ] 取消文件选择不会跳转或报错。
- [ ] `/player`、`/source/:sourceId`、`/settings` 均显示返回按钮。
- [ ] 首页 `/` 不显示返回按钮。
- [ ] 返回按钮行为清晰：有历史时返回上一页，无历史时回首页。
- [ ] 返回按钮和文件打开入口视觉上与现有菜单/悬浮控件保持一致。
- [ ] roadmap 中拖拽/文件打开状态与实际实现一致。
- [ ] typecheck/lint/build 通过；Tauri/Rust 涉及时 cargo check 通过。

## Definition of Done

- Tests added/updated where existing test harness supports it.
- `npm run typecheck` / `npm run lint` / `npm run build` pass for Player.
- Tauri/Rust changes pass `cargo check`.
- If local desktop runtime can launch, validate file picker and playback flow manually; otherwise report partial verification.
- `docs/architecture/06-roadmap.md` reflects actual state.

## Open Questions

- 无。

## Out of Scope

- 不在本任务中新增媒体库导入、播放列表管理或历史记录持久化。
- 不在本任务中实现媒体库、Server 文件选择或云盘文件选择。
- 不在本任务中承诺移动端文件选择体验。
- 不在本任务中实现全窗口任意页面拖拽播放，除非用户明确希望本轮加入。

## Technical Notes

- Relevant files discovered:
  - `player/src/router/index.ts` — defines `/`, `/player`, `/source/:sourceId`, `/settings`.
  - `player/src/components/layout/FloatingControls.vue` — bottom-right floating play/theme controls; play button currently has no click handler.
  - `player/src/views/PlayerView.vue` — loads query `path` on mount and handles `fileDrop`.
  - `player/src/components/player/VideoPlayer.vue` — implements local drop handling inside player view.
  - `player/src/composables/useMpv.ts` — exposes `load(path)`.
  - `player/src-tauri/src/commands/player.rs` — `mpv_load(path)` invokes libmpv `loadfile`.
  - `player/src-tauri/Cargo.toml` — currently no Tauri dialog plugin dependency.
- Implementation likely needs `tauri-plugin-dialog` or equivalent Tauri v2 dialog API so frontend can select a local file path.
