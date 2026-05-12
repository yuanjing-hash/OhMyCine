# Player 嵌入式视频渲染 MVP

## Goal

让 libmpv 的视频画面真正绘制到 OhMyCine Player 的 WebView 窗口内，替换当前的 `vo=null` / `video=no` / 占位面板，使得选中本地或 Emby 条目播放后，用户能在 Player 页内看到视频，而不是黑屏占位或外部 mpv 窗口。

## Background

当前 Player 的后端加载/控制通路是通的：libmpv 能 `loadfile`、`pause/resume`、`seek`、`get_property`，前端 `useMpv` 有事件订阅和 UI 控制条，Emby MVP 也已经能把 stream URL 交给 `mpv_load`。但是：

- `player/src-tauri/src/mpv/render.rs` 只是一行空结构体占位。
- `player/src-tauri/src/mpv/player.rs` 为了避免弹出独立 mpv 窗口，主动设置 `vo=null` / `video=no` / `force-window=no`。
- 前端 `VideoPlayer.vue` 显示"视频内嵌渲染仍在接入中"的占位面板。
- 前端已经 `npm install` 了 `tauri-plugin-libmpv-api@0.3.2`（社区方案），但后端 `Cargo.toml` 没有引入对应的 `tauri-plugin-libmpv` Rust 插件；这是个半装状态，需要决定保留还是移除。
- 产品目标仍然是跨平台 Player（Windows、Linux、macOS、Android、iOS），但桌面嵌入式渲染要按平台后端逐步落地；本任务的第一个实际 native backend 以 Windows 为先。
- 当前首个验证平台是 Windows；构建走 WSL 的 `x86_64-pc-windows-gnu` NSIS 交叉编译，不是 MSVC。

因此这一步的核心问题是**建立跨平台 render backend 架构，并先选一条能在 Windows GNU 交叉编译环境下把视频帧绘进 WebView 窗口内的 libmpv 嵌入方案**，而不是把 libmpv 控制层重写一遍或把产品范围收窄到 Windows。

## What I already know

- `player/src-tauri/Cargo.toml` 仅用 `libmpv-sys = "3.1"` 直接 FFI，没有挂 `tauri-plugin-libmpv`。
- `player/src-tauri/src/mpv/player.rs` 当前硬性抑制视频输出，是刻意的 safety guard，不能无条件移除。
- 前端有 `tauri-plugin-libmpv-api@0.3.2` npm 包待用，但能力/维护度待评估。
- `docs/architecture/03-player-design.md` 的产品方向偏好 libmpv 嵌入（`MpvRenderContext`）而不是 mpv sidecar。
- Windows GNU 交叉编译通路已验证（`tauri:build:windows` 成功打 NSIS），Linux/WSL 的 `tauri dev` 受 WSLg EGL/Mesa 限制，渲染真实表现要 Windows 宿主确认。
- Spec `.trellis/spec/frontend/quality-guidelines.md` 明确：涉及 Tauri runtime/libmpv/windowing/rendering 的改动，WSL `tauri dev` 只能算 partial，真运行时验证留给 Windows 宿主。
- 2026-05-11 Windows 宿主实测：maximize/restore 时能看到 mpv 视频 HWND 在应用背后且跟随晚一帧，证明 underlay 存在；剩余黑屏是 Tauri/WebView2 native background 或 Player DOM 根链仍在绘制不透明黑色，而不是 mpv HWND 没创建。

## Research References

- [`research/true-render-api-feasibility.md`](research/true-render-api-feasibility.md) — True render API 在 cross-platform architecture, Windows backend first Tauri v2 Player 中可行，推荐 app-owned OpenGL child surface + transparent Vue/WebView overlay；主要风险是 WebView2 z-order/透明度与 GL render-thread ownership。
- [`research/true-render-api-code-impact.md`](research/true-render-api-code-impact.md) — 映射当前 mpv 生命周期、后端/前端影响点、render 模块边界、回归敏感流和 Trellis jsonl 建议。
- [`research/libmpv-embedding-approaches.md`](research/libmpv-embedding-approaches.md) — libmpv 嵌入渲染手法对比（`render API` / 原生窗口 `wid` / 子窗口叠加 / offscreen texture）。
- [`research/tauri-v2-webview-video.md`](research/tauri-v2-webview-video.md) — Tauri v2 WebView 下的视频嵌入策略（子窗口、native surface、overlay、iframe 与 DOM 合成）。
- [`research/tauri-plugin-libmpv.md`](research/tauri-plugin-libmpv.md) — 社区 `tauri-plugin-libmpv` 插件能力与 `wid` 路线；本任务作为被拒绝的备选方案参考。

## Research Notes

### What the research found

- `libmpv-sys = 3.1` is the right existing foundation; expected render API exposure includes `mpv_render_context_create`, `mpv_render_context_set_update_callback`, `mpv_render_context_render`, `mpv_render_context_free`, `mpv_opengl_init_params`, `mpv_opengl_fbo`, `MPV_RENDER_API_TYPE_OPENGL`, and `MPV_RENDER_API_TYPE_SW`. Exact bindgen names must be compile-checked first.
- True render API does **not** mean letting mpv own a `wid` window. The app must own the graphics surface/context, make it current on the render thread, and call `mpv_render_context_render` with FBO params.
- For Windows MVP, the most realistic architecture is a native child OpenGL surface plus a transparent Vue/WebView overlay for Cinema OS controls.
- D3D11 should not be assumed for MVP. Research points to OpenGL render API as the feasible first backend; SW render API is only a diagnostic/prototype fallback because of CPU/IPC cost.
- The community `tauri-plugin-libmpv` route is viable for fast Windows `wid` embedding, but the user explicitly chose the higher-ceiling True render API route instead.

### Constraints from our repo/project

- `player/src-tauri/src/mpv/player.rs` currently owns `mpv_handle` and deliberately disables visible video (`vo=null`, `video=no`) to avoid external mpv windows; the True render API implementation must preserve that invariant until an app-owned render surface is ready.
- `player/src-tauri/src/mpv/render.rs` is the intended boundary for unsafe render API, GL context, callbacks, and cleanup.
- `useMpv.ts` / `PlayerView.vue` should keep the public playback API and `/player?path=...` flow stable; render status can be added without breaking local/Emby playback entry points.
- 产品保留跨平台 render backend 架构；Windows 是第一个实际落地与运行时验证的 native backend。WSL cross-build success is necessary but not sufficient; visual/runtime verification is user-owned on Windows.

### Feasible approaches here

**Approach A: True libmpv render API with Windows OpenGL child surface** (Superseded)

- How it works: create an app-owned native child render surface, create a WGL/OpenGL context, create `mpv_render_context` with `MPV_RENDER_API_TYPE_OPENGL`, wake a render thread from mpv's update callback, and render frames into the surface's default framebuffer using `MPV_RENDER_PARAM_OPENGL_FBO`.
- Pros: highest long-term product ceiling; keeps mpv from owning external windows; enables native video under Vue controls; aligns with product architecture preference for `MpvRenderContext`.
- Cons: hardest MVP route; requires native surface/GL/render-loop lifecycle, WebView2 transparent overlay validation, resize/fullscreen/DPI synchronization, and careful cleanup ordering.

**Approach B: `tauri-plugin-libmpv` / mpv `wid` route** (Rejected for this task)

- How it works: let plugin pass Tauri native window handle to mpv `wid`, with transparent WebView UI overlay.
- Pros: fastest route and Windows-tested upstream.
- Cons: not true render API; relies on a small third-party plugin/wrapper DLL; Linux/macOS incomplete; user chose not to take this shortcut for this task.

**Approach C: Software render API into CPU buffer / WebView canvas** (Fallback only)

- How it works: use `MPV_RENDER_API_TYPE_SW`, render into CPU pixel buffers, and display via WebView/canvas/IPC.
- Pros: useful to validate render API lifecycle without native GL/WebView2 z-order.
- Cons: not performance-credible for home cinema playback; high CPU/IPC cost; not the main MVP.

**Approach D: Separate overlay mpv window** (Avoid)

- How it works: sync a borderless external/top-level video window with the Tauri window.
- Pros: emergency debugging fallback.
- Cons: violates the no-unmanaged-external-window invariant and creates fragile focus/z-order/DPI behavior.

## Open Questions

- None for this MVP. The initial True libmpv render API route was superseded by the transparent Tauri/WebView overlay + mpv `wid` video underlay path after Windows WebView2/native airspace validation. MVP still uses a cross-platform render backend boundary with Windows backend first; Linux/macOS/mobile remain planned backend targets and should show explicit unsupported/fallback states until implemented.

## Requirements (evolving)

- 建立跨平台 render backend 架构；首个实际 backend 在 Windows 上让本地/Emby 视频画面真正显示在 OhMyCine Player 页内部，不再出现独立 mpv 外部窗口，也不再出现"内嵌渲染接入中"占位。
- Windows 后端以 **mpv 视频底层 top-level HWND + 透明 Tauri/WebView 叠层** 为第一条实际落地路径：mpv 使用无焦点、无 taskbar/Alt-Tab 的 `WS_POPUP` 工具窗口作为 `wid` + `vo=gpu-next` + `hwdec=auto-safe` 视频底层；Tauri 主 WebView 窗口启用真实透明并位于其上方，Vue 控制条在透明叠层中渲染并接收点击。保留 HDR10 / Dolby Vision / gpu-next 产品上限；不引入 `tauri-plugin-libmpv` crate、不使用 mpv 侧车进程，`mpv_handle` 仍由 `MpvPlayer` 拥有。
- 当 mpv 视频底层 HWND 创建或 `mpv_initialize` 失败时，退回 `vo=null` / `video=no` 安全状态，禁止 mpv 创建任何用户可见的外部窗口，并在 UI 上展示 error/fallback 诊断。
- 用户视角必须保持"一个窗口"：mpv 视频底层窗口不进入 taskbar、不出现在 Alt-Tab、不拥有独立 close/focus；随 Tauri 主窗口的 move / resize / minimize / maximize / fullscreen / DPI-change / close 同步跟随。
- Player 独立性不变：嵌入渲染不依赖 Server、不依赖网络。
- UI 必须诚实反映渲染状态：`idle` / `initializing` / `ready` / `unsupported` / `error`，以及"尚未选择媒体"时显示可识别的状态。
- 不回退本任务之前的功能：Emby 浏览、Emby 播放、本地文件播放、Cinema OS 控制条、控制条自动隐藏、拖拽加载等行为不受影响。
- Windows MVP 视频画面默认 full-bleed 铺满 Player 主画面；不再通过 top/bottom occlusion 收缩 mpv HWND。控制条可见/可点击依赖透明 Tauri/WebView 叠层位于视频底层窗口之上；CSS 透明只有在 WebView/top-level 窗口本身透明且位于 mpv 窗口上方时才有意义。
- 非首批后端平台（Linux / macOS / Android / iOS）保留 render backend 边界与 `unsupported` 渲染状态，不崩溃，不暗示这些平台被移出产品范围。
- 后续 Player 播放控制增强需要覆盖：字幕/音轨入口、自定义右键菜单与播放详情、倍速控制并记住上次倍速、视频全屏、右下角播放设置按钮与悬浮设置面板（倍速、画面比例等）。这些是 Player 体验目标，但不在本任务范围内。

## Acceptance Criteria (completed)

- [x] Windows 上 mpv 视频底层 HWND 创建成功并对齐 Tauri 主窗口 client area；Tauri 主窗口 move / resize / maximize / unmaximize / minimize / restore / fullscreen / DPI-change / close 时，mpv HWND 自动跟随、隐藏或销毁。（2026-05-11 Windows 宿主视觉验证通过。）
- [x] Windows 上 mpv 使用 `wid` + `vo=gpu-next` + `hwdec=auto-safe` 启动；mpv 底层窗口无独立 taskbar 图标、无 Alt-Tab 条目、无独立 focus/close，用户视角为"一个窗口"。（2026-05-11 Windows 宿主验证通过。）
- [x] Windows 宿主运行时：选本地文件进入 Player 页后，能透过透明 Tauri/WebView 叠层看到视频画面而不是占位文本、不透明黑屏或全屏黑色 DOM 面板。（2026-05-11 用户实测通过。）
- [x] Windows 宿主运行时：选 Emby 电影/剧集进入 Player 页后，能透过透明 Tauri/WebView 叠层看到视频画面。（2026-05-11 用户实测通过。）
- [x] Windows 宿主运行时：render status 为 `ready` 且已加载媒体时，`html` / `body` / `#app` / `.app-window` / route `main` / `PlayerView` / `VideoPlayer` 根链不绘制全屏不透明黑色；idle/error/unsupported/no-media 状态仍显示有意的深色占位。（2026-05-11 用户实测通过。）
- [x] 播放/暂停/进度/音量控制条仍然有效，控制条在播放期间能自动隐藏并 hover 重新出现，且点击命中 WebView/Vue 而不是 mpv 窗口。（2026-05-11 用户实测通过。）
- [x] Windows 宿主运行时：视频画面 full-bleed 铺满 Player 主画面；顶部/底部 liquid-glass 控制条在显示时仍可点击，不再依赖 bounds top/bottom occlusion 收缩视频窗口。（2026-05-11 用户实测通过。）
- [x] mpv 视频底层 HWND 创建失败或 `mpv_initialize` 失败时，Player 回退 `vo=null` / `video=no` 安全态，UI 展示 error 提示，不崩溃。
- [x] 没有弹出独立的 mpv 外部窗口、taskbar/Alt-Tab 条目或任何用户能看到的"第二个窗口"。（2026-05-11 Windows 宿主验证通过。）
- [x] 诊断面板（右上角 chip + Ctrl+Shift+D）可唤出，包含 `ownerHwndAttached` / `mpvHwndCreated` / `mpvHwndShown` / `overlayWindowTransparent` / `webviewBackgroundTransparentApplied` / `zOrderUnderlayApplied` / `geometryFollowing` / `taskbarIgnored` / `fullscreenState` / `lastSyncResult` / `mpvWidAccepted` / `mpvInitialized` 字段。
- [x] `npm run typecheck` / `lint` / `build` 全部通过。
- [x] `cargo check` 通过；如改动平台/GL crate，`Cargo.lock` 同步提交。
- [x] `npm run tauri:build:windows` 成功生成 NSIS 安装包。
- [x] 非 Windows 平台 Player 页显示"视频内嵌渲染暂未支持该平台"的明确占位，不崩溃，且文案保留这些平台仍为计划后端。

## Definition of Done

- 视频能真正嵌入 Player 窗口内部在 Windows 上播放。
- 代码沿用项目 DataSource/Player 分层，不把平台渲染代码散落到 Vue 视图层。
- 现有 Emby / 本地 / 控制条 / 沉浸式自动隐藏等功能无回归。
- 验证命令按 `.trellis/spec/frontend/quality-guidelines.md` 的渲染/runtime 条目执行，Windows GNU 交叉构建通过，Windows 真机运行时透明叠层 + mpv 视频底层窗口已由用户验证通过。
- `docs/architecture/06-roadmap.md` 中 Player 嵌入渲染状态更新为已完成（仅限已实际完成的平台；Linux/macOS/mobile 仍为后续计划后端）。

## Technical Approach

1. Keep `MpvPlayer` as the sole `mpv_handle` lifecycle owner. Render/windowing code may access the handle only through internal mpv-module APIs and must not leak raw pointers/HWND to Tauri commands or Vue.
2. Slim the render module boundary under `player/src-tauri/src/mpv/` for the `wid` + transparent-overlay model: `render.rs` tracks render status/backend/message/typed diagnostics; platform-specific windowing code is isolated under explicit cfg modules (Windows first as `platform/windows.rs`; Linux/macOS/mobile return `unsupported`). The True render API OpenGL scaffold (GL context, `mpv_render_context_*` FFI) remains deferred to a later deep-composition phase.
3. On Windows, create a borderless mpv video underlay HWND via `CreateWindowExW`: class with minimal paint handling, `WS_POPUP` style, extended style `WS_EX_NOACTIVATE | WS_EX_TOOLWINDOW`, explicitly without `WS_EX_APPWINDOW`, and no Win32 owner so it can sit below the Tauri window. This avoids the owned-window invariant that would otherwise force the mpv HWND above the WebView and occlude controls.
4. Before `mpv_initialize`, set `mpv_set_option_string("wid", "<HWND as integer>")`, `vo=gpu-next`, `hwdec=auto-safe`, keep `force-window=no`. mpv owns its own D3D11 swap chain inside the wid, which preserves HDR10 / Dolby Vision / gpu-next. Do not remove the `vo=null` / `video=no` fallback until the video underlay HWND is successfully created and `mpv_initialize` succeeds.
5. Synchronize mpv HWND geometry with the Tauri main window: hook Tauri `on_window_event` for `Resized`, `Moved`, `ScaleFactorChanged`, `Focused`, `CloseRequested`, and read live maximize/minimize/fullscreen state through Win32 (`IsIconic`, `IsZoomed`, `GetClientRect`, `ClientToScreen`). Frontend reports full-bleed logical bounds through `mpv_update_render_surface_bounds`; Rust sanitizes, converts CSS pixels to physical screen coordinates, and calls `SetWindowPos(mpv_hwnd, tauri_hwnd, ..., SWP_NOACTIVATE)` / `ShowWindow` so the mpv window remains immediately beneath the transparent Tauri/WebView overlay. Legacy top/bottom occlusion fields are accepted for compatibility but ignored.
6. Cleanup order on shutdown/window close: stop emitting geometry updates → hide mpv HWND → `mpv_terminate_destroy` (frees libmpv-owned D3D resources attached to the wid) → `DestroyWindow` on the video underlay HWND → unregister window class. Because the underlay is not Win32-owned by Tauri, explicit minimize/restore/close synchronization is required to preserve the single-window UX.
7. Preserve frontend render state in `useMpv` / `VideoPlayer.vue` (`idle` / `initializing` / `ready` / `unsupported` / `error`). `initializeRender` now triggers video-underlay HWND creation plus `wid` initialization rather than OpenGL context setup. Existing `load`, controls, events, `/player?path=...` route query, local file drop, and Emby `getStreamURL` flow stay intact.
8. Expose typed diagnostic fields (`ownerHwndAttached`, `mpvHwndCreated`, `mpvHwndShown`, `overlayWindowTransparent`, `webviewBackgroundTransparentApplied`, `zOrderUnderlayApplied`, `geometryFollowing`, `taskbarIgnored`, `fullscreenState`, `lastSyncResult`, `mpvWidAccepted`, `mpvInitialized`) through the existing render-status command/diagnostic panel so regressions in single-window and overlay behavior are visible without a debugger.
9. Update roadmap status only for the platform actually achieved; Linux/macOS/Android/iOS keep explicit `unsupported` rendering with no mpv window creation on those platforms.

## Implementation Plan

- PR1 / milestone 0: remove the app-owned OpenGL child HWND scaffold and slim `mpv/render.rs` / `mpv/surface.rs` to a `wid`-oriented status boundary; ensure `cargo check` for host and `x86_64-pc-windows-gnu` still pass.
- PR2 / milestone 1: Windows platform module creates the mpv video underlay HWND (`WS_POPUP` + `WS_EX_NOACTIVATE | WS_EX_TOOLWINDOW`, no `WS_EX_APPWINDOW`, no Win32 owner), registers a dedicated window class, and proves taskbar/Alt-Tab remain single-window with a color-fill paint smoke test (no mpv yet).
- PR3 / milestone 2: wire `wid` + `vo=gpu-next` + `hwdec=auto-safe` into `MpvPlayer::new`, retain the `vo=null` / `video=no` fallback on failure, and surface render status through existing `mpv_render_status` command. Local file playback must not open a second visible window.
- PR4 / milestone 3: geometry sync via Tauri `on_window_event` (`Resized` / `Moved` / `ScaleFactorChanged` / `Focused` / `CloseRequested`) plus frontend `mpv_update_render_surface_bounds` reporting full-bleed logical bounds; add Win32 subclass if drift is observed. Validate maximize/unmaximize/minimize/restore/fullscreen/DPI-change follow.
- PR5 / milestone 4: Cinema OS chrome regression pass — liquid-glass top/bottom bars stay clickable in the transparent WebView overlay above the video underlay, Emby and local playback regressions covered, diagnostics panel exposes the required fields.
- PR6 / milestone 5: lifecycle hardening (cleanup order, close-during-playback, multi-monitor moves), unsupported-state placeholders on non-Windows builds, roadmap update, and Windows NSIS package build.

## Out of Scope

- 本轮切片不实现 Linux/macOS 的具体 native surface/render loop；但架构与文案必须保留后续后端扩展空间。
- 本轮切片不实现 Android / iOS 移动端渲染后端；移动端仍属于产品规划。
- WebView2 Composition Hosting（让 mpv 的 D3D 内容与 WebView 通过 DCompositionVisual 真合成在同一表面）属于 Phase 2 Windows 深度整合，不在本轮范围。
- True libmpv render API（app-owned GL context + `mpv_render_context_create`）作为 Phase 2 候选保留；本轮因 WebView2 DirectComposition 覆盖任何 Tauri 内部 sibling HWND 而不可行，相关 FFI scaffold 不在本轮新增。
- 字幕/音轨切换 UI、播放列表、剧集队列、继续观看、播放历史——由后续任务承担。
- 播放页高级控制增强由后续任务承担：字幕/音轨入口、自定义右键菜单、播放详情面板、倍速控制与倍速记忆、全屏按钮、右下角播放设置按钮、悬浮设置面板、视频比例/画面模式等。本任务只需确保当前基础控制条在全屏沉浸式视频背景上仍可操作。
- 嵌入渲染性能调优（HDR、10-bit、hwdec 全面适配）超出 MVP 范围；`vo=gpu-next` + `hwdec=auto-safe` 默认开启，但不做逐 codec / 逐 HDR pipeline 的 certification。
- 抛弃/替换当前的 `libmpv-sys` FFI 控制层；除非研究结论明确要求。
- 引入 `tauri-plugin-libmpv` crate 或 sidecar mpv 进程。

## Technical Notes

- Spec：
  - `.trellis/spec/frontend/directory-structure.md` — Player 服务/Tauri 模块边界。
  - `.trellis/spec/frontend/component-guidelines.md` — VideoPlayer 组件与 Cinema OS 风格。
  - `.trellis/spec/frontend/quality-guidelines.md` — libmpv/windowing/rendering 改动的验证契约。
  - `.trellis/spec/frontend/type-safety.md` — Tauri invoke 返回值类型安全。
- 关键代码位置：
  - `player/src-tauri/src/mpv/player.rs`（`wid` + `vo=gpu-next` + `hwdec=auto-safe` 初始化；失败回退 `vo=null` / `video=no`）
  - `player/src-tauri/src/mpv/render.rs`（渲染状态/后端/诊断字段，去掉 OpenGL 渲染上下文所有权）
  - `player/src-tauri/src/mpv/surface.rs`（`wid` 场景下的平台中立 bounds/sanitize 类型）
  - `player/src-tauri/src/mpv/platform/windows.rs`（新增/重写：mpv 视频底层 HWND 创建、销毁、几何同步、可选 Win32 subclass）
  - `player/src-tauri/src/mpv/surface.rs` 的非 Windows cfg fallback（返回 `unsupported` 的占位，不创建任何 mpv 窗口；后续 Linux/macOS 后端可再拆分平台文件）
  - `player/src-tauri/src/mpv/mod.rs` / `events.rs`
  - `player/src-tauri/src/main.rs`（`on_window_event` 钩子 `Resized` / `Moved` / `ScaleFactorChanged` / `Focused` / `CloseRequested`；启动时显式将 Tauri WebView/window background color 设置为透明并记录诊断）
  - `player/src-tauri/Cargo.toml`（Windows-scoped `windows-sys` features for `Win32_UI_WindowsAndMessaging` / `Win32_Graphics_Gdi` / `Win32_UI_HiDpi`；不再需要 OpenGL / WGL crate；不引入 `tauri-plugin-libmpv`）
  - `player/src/components/player/VideoPlayer.vue`（保持 render 状态 host，bounds 报告 full-bleed 视频区域，不再依赖 top/bottom occlusion）
  - `player/src/composables/useMpv.ts`（保留 render status / `initializeRender` / `updateRenderSurfaceBounds`，语义改为触发视频底层 HWND + `wid` 初始化并暴露 typed diagnostics）
  - `player/src-tauri/tauri.conf.json`（主窗口 `transparent: true` 且显式 `backgroundColor: [0, 0, 0, 0]`，避免 WebView2 默认背景继续不透明绘制）
- 构建约束：
  - Windows GNU 交叉编译，mpv 视频底层 HWND 路径依赖 `windows-sys` Win32 API，须在 `x86_64-pc-windows-gnu` 下链接通过。
  - 非 Windows 平台不得创建 mpv 窗口；`cargo check` 在 host Linux 上必须保持通过。
  - Cross-build 产物只证明生成；单窗口观感、几何跟随、HDR/DV/gpu-next 真实效果由用户 Windows 宿主验证。
- 已拒绝的备选方案：
  - `tauri-plugin-libmpv` crate：依赖三方小插件与 wrapper DLL；保留为研究参考，不纳入实现。
  - mpv sidecar 进程：与 `mpv_handle` 由 `MpvPlayer` 拥有的单一所有权契约冲突；不采用。
  - 回到 app-owned OpenGL child HWND + True libmpv render API：受 WebView2 DirectComposition airspace 限制导致 sibling HWND 不可见，延后至 Phase 2 Windows 深度整合与 WebView2 Composition Hosting。

## Decision (ADR-lite, superseded): True libmpv render API first attempt

**Context:** Current Player playback control path works but visible video is intentionally disabled to avoid unmanaged external mpv windows. Research found two plausible first-backend paths: a faster `tauri-plugin-libmpv` / mpv `wid` route, and a harder but higher-ceiling True libmpv render API route using an app-owned OpenGL surface. The product direction favors deep libmpv embedding through `MpvRenderContext` rather than sidecar/window ownership shortcuts, and the product scope remains cross-platform.

**Superseded decision:** The initial attempt was to implement the MVP through the True libmpv render API route: establish a cross-platform render backend boundary, then land Windows OpenGL rendering (`MPV_RENDER_API_TYPE_OPENGL`) as the first concrete backend into an app-owned native render surface, with Vue/WebView controls as transparent overlay.

**Superseding reason:** Windows WebView2/native airspace behavior made the child/sibling OpenGL-surface approach unsuitable for this MVP because the native surface could not reliably appear beneath interactive Vue controls. The product ceiling is preserved by deferring True render API / WebView2 Composition Hosting to a later deep-integration phase; the completed MVP uses the transparent Tauri/WebView overlay + mpv `wid` video underlay decision below.

## Decision (ADR-lite): Transparent Tauri/WebView overlay above mpv video underlay

**Context:** The previous "owned top-level mpv HWND above Tauri" pivot made video visible, but because the mpv window was composed above the WebView it occluded Vue controls: CSS `z-index`, `WS_EX_TRANSPARENT`, and hit-test pass-through could not make WebView controls draw above a higher native top-level window. This is the same WebView2/native airspace family in the opposite direction. CSS transparency only helps if the Tauri/WebView top-level window itself is transparent and is actually above the video window. Product direction still requires `wid` + `vo=gpu-next` capability, a single-window user experience, non-Windows unsupported states, and no dependency on `tauri-plugin-libmpv` or sidecar mpv.

**Decision:** Change the Windows backend from "mpv top-level HWND above/occluding Tauri WebView with top/bottom bounds occlusion" to "mpv video top-level underlay below a transparent Tauri/WebView overlay". Concretely on Windows:

1. Enable transparency on the Tauri main WebView window and make the Player route/background chain transparent only where video should show through. Non-player pages continue painting the Cinema OS dark/liquid background.
2. Create a borderless `WS_POPUP` mpv video underlay with `WS_EX_NOACTIVATE | WS_EX_TOOLWINDOW`, no `WS_EX_APPWINDOW`, and no Win32 owner. It is not a Win32 owned window because owned windows are always above their owner and would recreate the occlusion bug.
3. Place the mpv underlay immediately below the Tauri HWND via `SetWindowPos(mpv_hwnd, tauri_hwnd, ..., SWP_NOACTIVATE)` and keep it aligned to the full Player surface. The previous top/bottom occlusion model is neutralized; video remains full-bleed behind the overlay.
4. Pass the underlay HWND to mpv via `mpv_set_option_string("wid", "<integer>")` before `mpv_initialize`, set `vo=gpu-next`, `hwdec=auto-safe`, keep `force-window=no`. mpv owns its D3D11 swap chain inside the wid.
5. Preserve single-window UX explicitly: the underlay is hidden from taskbar/Alt-Tab by tool-window style, never activates/focuses, and follows Tauri move/resize/minimize/restore/close through Tauri window events plus live Win32 owner-state reads. If underlay creation or `mpv_initialize` fails, fall back to `vo=null` / `video=no` and surface a safe error.

**Consequences:**

- **Vue controls should be visible/clickable.** The controls are in the Tauri/WebView overlay above the video underlay rather than behind a topmost mpv window.
- **Transparency must be real at the native window level.** CSS `background: transparent` is insufficient unless Tauri/WebView transparency is enabled and the DOM/background chain avoids opaque panels on the Player route.
- **The mpv underlay is not Win32-owned.** That is an intentional tradeoff to allow it to sit below Tauri. Single-window behavior is preserved by `WS_EX_TOOLWINDOW`, `WS_EX_NOACTIVATE`, no `WS_EX_APPWINDOW`, and explicit follow/hide/destroy logic.
- **HDR10 / Dolby Vision / gpu-next capability is preserved.** mpv still runs the real `gpu-next` VO in its own D3D11 swap chain through `wid`.
- **True render API and WebView2 Composition Hosting remain future deep-integration options.** They are not removed from the product ceiling, but are deferred beyond this MVP.
- **Linux / macOS / Android / iOS stay on the `unsupported` render state.** They remain planned, not removed. No mpv window is ever created there.
- **Runtime proof is Windows-host owned and completed for this MVP.** WSL cross-build proves compilation/package generation only; actual transparent overlay composition, click hit-testing, and geometry follow were manually verified by the user on Windows on 2026-05-11.
