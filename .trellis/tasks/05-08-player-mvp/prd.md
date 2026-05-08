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

**Approach A: True libmpv render API with Windows OpenGL child surface** (Chosen)

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

- None for planning. User chose the True libmpv render API route. MVP uses a cross-platform render backend boundary with Windows backend first; Linux/macOS/mobile remain planned backend targets and should show explicit unsupported/fallback states until implemented.

## Requirements (evolving)

- 建立跨平台 render backend 架构；首个实际 backend 在 Windows 上通过 True libmpv render API（OpenGL）播放本地/ Emby 视频，画面显示在 OhMyCine Player 页内部，不再出现独立 mpv 窗口，也不再出现"内嵌渲染接入中"占位。
- 在 `player/src-tauri/src/mpv/render.rs` 及显式平台模块中实现 app-owned render surface / GL context / `mpv_render_context` 生命周期；Windows 后端优先落地，Linux/macOS/mobile 后端保留边界与明确 fallback，不采用 `tauri-plugin-libmpv` 或 mpv `wid` 作为主实现。
- 保留现有的 safety guard：当未绑定 app-owned 渲染目标或渲染初始化失败时，禁止 libmpv 创建外部可见窗口。
- Player 独立性不变：嵌入渲染不依赖 Server、不依赖网络。
- UI 必须诚实反映渲染状态：初始化中、ready、unsupported、error、尚未选择媒体时，显示可识别的状态。
- 不回退本任务之前的功能：Emby 浏览、Emby 播放、本地文件播放、Cinema OS 控制条、控制条自动隐藏等行为不受影响。
- 窗口缩放、最大化、全屏、拖动时视频画面应跟随 Player 容器尺寸，不错位、不遗留。
- 非首批后端平台若暂未实现 True render backend，应显示明确 unsupported/fallback 状态，不崩溃，且不暗示这些平台被移出产品范围。

## Acceptance Criteria (evolving)

- [ ] Compile proof: `libmpv-sys = 3.1` 的 render API 绑定名被确认可用，或实现了窄口径本地 FFI shim，并通过 `cargo check`。
- [ ] Windows render surface smoke test：app-owned OpenGL child surface 能在 Player 区域清屏显示，并随窗口 resize/maximize/fullscreen 对齐。
- [ ] WebView/Vue overlay smoke test：Player 背景透明区域能露出 native render surface，liquid-glass 控制条可显示在其上并接收 hover/click。
- [ ] Windows 宿主运行时：选本地文件进入 Player 页后，True render API 路径能显示视频画面而不是占位文本。
- [ ] Windows 宿主运行时：选 Emby 电影/剧集进入 Player 页后，True render API 路径能显示视频画面。
- [ ] 播放/暂停/进度/音量控制条仍然有效，控制条在播放期间能自动隐藏并 hover 重新出现。
- [ ] 窗口调整大小、最大化、全屏时视频画面正确跟随 Player 容器。
- [ ] 没有弹出独立的 mpv 外部窗口。
- [ ] 渲染初始化失败或平台不支持时，UI 显示明确的错误/降级提示，且不崩溃。
- [ ] `npm run typecheck` / `lint` / `build` 全部通过。
- [ ] `cargo check` 通过；如新增平台/GL crate，`Cargo.lock` 同步提交。
- [ ] `npm run tauri:build:windows` 成功生成 NSIS 安装包。
- [ ] 如果本轮 MVP 只完成 Windows 后端：Linux/macOS/mobile 下 Player 页对应显示"视频内嵌渲染暂未支持该平台"的明确占位，不崩溃，且文案保持这些平台仍为计划后端。

## Definition of Done

- 视频能真正嵌入 Player 窗口内部在 Windows 上播放。
- 代码沿用项目 DataSource/Player 分层，不把平台渲染代码散落到 Vue 视图层。
- 现有 Emby / 本地 / 控制条 / 沉浸式自动隐藏等功能无回归。
- 验证命令按 `.trellis/spec/frontend/quality-guidelines.md` 的渲染/runtime 条目执行，Windows 真机运行时验证明确说明留给用户。
- `docs/architecture/06-roadmap.md` 中 Player 嵌入渲染状态更新为已完成（仅限已实际完成的平台）。

## Technical Approach

1. Verify exact `libmpv-sys` render API symbol names in this repo and add a compile-safe wrapper boundary if needed.
2. Introduce a render module boundary under `player/src-tauri/src/mpv/`: `render.rs` owns `mpv_render_context`; platform-specific code is isolated under explicit cfg modules (Windows first; Linux/macOS unsupported placeholders if needed).
3. Keep `MpvPlayer` as the sole `mpv_handle` lifecycle owner. Render code may access the handle only through internal mpv-module APIs and must not leak raw pointers to Tauri commands or Vue.
4. Implement a Windows OpenGL child surface smoke test first, before mpv frames: create app-owned render surface, clear color, align with Player container, validate transparent Vue overlay.
5. Add True render API lifecycle: create `mpv_render_context` with `MPV_RENDER_API_TYPE_OPENGL`, register update callback, wake a render thread, call `mpv_render_context_render` with `MPV_RENDER_PARAM_OPENGL_FBO`, and clean up in strict order.
6. Add frontend render state to `useMpv` / `VideoPlayer.vue` (`idle` / `initializing` / `ready` / `unsupported` / `error`) while preserving existing `load`, controls, events, route query, local file drop, and Emby `getStreamURL` flow.
7. Keep no-external-window safety: do not remove `vo=null` / `video=no` fallback unless the app-owned render context is active.
8. Update roadmap status only for the platform actually achieved.

## Implementation Plan

- PR1 / milestone 0: compile proof for render API bindings and render module scaffolding.
- PR2 / milestone 1: Windows native OpenGL child surface clear-color smoke test with resize/fullscreen/DPI sync.
- PR3 / milestone 2: transparent Vue/WebView overlay and render state UI.
- PR4 / milestone 3: `mpv_render_context_create` + update callback + frame render loop.
- PR5 / milestone 4: lifecycle hardening, local/Emby regression checks, roadmap update, package build.

## Out of Scope

- 本轮切片不实现 Linux/macOS 的具体 native surface/render loop；但架构与文案必须保留后续后端扩展空间。
- 本轮切片不实现 Android / iOS 移动端渲染后端；移动端仍属于产品规划。
- 字幕/音轨切换 UI、播放列表、剧集队列、继续观看、播放历史——这些由后续任务承担。
- 嵌入渲染性能调优（HDR、10-bit、hwdec 全面适配）超出 MVP 范围。
- 抛弃/替换当前的 `libmpv-sys` FFI 控制层；除非研究结论明确要求。

## Technical Notes

- Spec：
  - `.trellis/spec/frontend/directory-structure.md` — Player 服务/Tauri 模块边界。
  - `.trellis/spec/frontend/component-guidelines.md` — VideoPlayer 组件与 Cinema OS 风格。
  - `.trellis/spec/frontend/quality-guidelines.md` — libmpv/windowing/rendering 改动的验证契约。
  - `.trellis/spec/frontend/type-safety.md` — Tauri invoke 返回值类型安全。
- 关键代码位置：
  - `player/src-tauri/src/mpv/render.rs`（占位）
  - `player/src-tauri/src/mpv/player.rs`（主动抑制视频输出的位置）
  - `player/src-tauri/src/mpv/mod.rs` / `events.rs`
  - `player/src-tauri/Cargo.toml`（可能新增 Windows/OpenGL/surface 相关 crate；不以引入 `tauri-plugin-libmpv` 为主线）
  - `player/src-tauri/src/mpv/surface.rs` / `platform/windows.rs`（如需要新增，隔离平台 surface/GL 代码）
  - `player/src/components/player/VideoPlayer.vue`（替换占位为 render 状态与 native surface host/status 边界）
  - `player/src/composables/useMpv.ts`（新增 render status / initializeRender，保留现有控制 API）
- 构建约束：
  - Windows GNU 交叉编译，MVP 需要确认所选渲染方案在 x86_64-pc-windows-gnu 下能链接。
  - Cross-build 产物只证明生成，运行时由用户 Windows 宿主验证。

## Decision (ADR-lite)

**Context:** Current Player playback control path works but visible video is intentionally disabled to avoid unmanaged external mpv windows. Research found two plausible first-backend paths: a faster `tauri-plugin-libmpv` / mpv `wid` route, and a harder but higher-ceiling True libmpv render API route using an app-owned OpenGL surface. The product direction favors deep libmpv embedding through `MpvRenderContext` rather than sidecar/window ownership shortcuts, and the product scope remains cross-platform.

**Decision:** Implement the MVP through the True libmpv render API route. Establish a cross-platform render backend boundary, then land Windows OpenGL rendering (`MPV_RENDER_API_TYPE_OPENGL`) as the first concrete backend into an app-owned native render surface, with Vue/WebView controls as transparent overlay. Keep `tauri-plugin-libmpv` / `wid` as rejected fallback research, not the primary implementation.

**Consequences:** This increases implementation complexity and requires native surface, GL context, render-thread, resize/fullscreen/DPI, and cleanup work. It preserves the highest product ceiling and avoids delegating video ownership to mpv or a small third-party plugin. Linux/macOS/mobile backends remain planned rather than removed; until implemented they must show clear fallback UI. Windows runtime visual verification remains user-owned.
