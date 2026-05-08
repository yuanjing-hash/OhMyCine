# Research: True libmpv render API feasibility

- **Query**: OhMyCine Player embedded video rendering MVP using the true libmpv render API instead of `tauri-plugin-libmpv`/`wid`; verify libmpv render API requirements, `libmpv-sys = 3.1` exposure, Windows-first Tauri v2 architecture, GL context options, software fallback, risks, and MVP milestones.
- **Scope**: mixed
- **Date**: 2026-05-08

## Findings

### Files Found

| File Path | Description |
|---|---|
| `/home/develop/development/Code/OhMyCine/player/src-tauri/src/mpv/player.rs` | Current raw `libmpv-sys` wrapper. `MpvPlayer::new()` creates and initializes an `mpv_handle`, disables unmanaged video with `force-window=no`, `vo=null`, `video=no`, and exposes load/pause/resume/seek/property commands. |
| `/home/develop/development/Code/OhMyCine/player/src-tauri/src/mpv/render.rs` | Placeholder only: `pub struct MpvRenderContext;` with `new() -> Self`; no libmpv render API integration yet. |
| `/home/develop/development/Code/OhMyCine/player/src-tauri/Cargo.toml` | Current Tauri backend dependencies include `tauri = "2"` and `libmpv-sys = "3.1"`; no GL/windowing helper crate is declared yet. |
| `/home/develop/development/Code/OhMyCine/player/src-tauri/Cargo.lock` | Resolves `libmpv-sys` to `3.1.0`. |
| `/home/develop/development/Code/OhMyCine/docs/architecture/03-player-design.md` | Product design requires Tauri v2 + Vue 3 + libmpv embedded rendering, Vue controls over native video, Windows desktop as practical first target, and independent Player operation. |

### Current Code Patterns

- `player/src-tauri/src/mpv/player.rs:34-52` creates and initializes `mpv_handle` directly through `libmpv-sys`.
- `player/src-tauri/src/mpv/player.rs:40-47` documents the exact current gap: no mpv render context or native window/surface handle exists, so normal GPU VO would create an unmanaged external mpv window; visible video is deliberately suppressed.
- `player/src-tauri/src/mpv/player.rs:45-47` currently sets:

```rust
player.set_option("force-window", "no")?;
player.set_option("vo", "null")?;
player.set_option("video", "no")?;
```

- For a true render API path, these suppressors must be replaced by render-context ownership, not by `wid`/external-window embedding.
- `player/src-tauri/src/mpv/render.rs:1-7` is intentionally empty and can become the isolation boundary for unsafe render API, GL context, callbacks, and cleanup.
- `docs/architecture/03-player-design.md:1823-1861` describes the intended architecture as `Vue UI -> Tauri Events/Commands -> libmpv C API -> mpv_render_context -> OpenGL/Vulkan rendering`, with video underneath Vue controls.

## libmpv Render API Requirements

### `mpv_render_context_create`

Purpose: creates a render context connected to an initialized `mpv_handle` and a selected render API. For OpenGL, libmpv does not create a window for the application; the application owns the GL context/surface and supplies callbacks/parameters that let mpv issue GL rendering commands into the app's current framebuffer.

Required shape:

- A valid, initialized `mpv_handle *`.
- A pointer to `mpv_render_context *` output storage.
- A null-terminated array of `mpv_render_param` entries.
- At minimum for OpenGL:
  - `MPV_RENDER_PARAM_API_TYPE` with value `MPV_RENDER_API_TYPE_OPENGL`.
  - `MPV_RENDER_PARAM_OPENGL_INIT_PARAMS` pointing to `mpv_opengl_init_params`.
- `mpv_opengl_init_params` must include a `get_proc_address` callback, and optionally callback context, so mpv can load OpenGL function pointers.

Important behavioral requirements from libmpv render API docs/examples:

- The OpenGL context must be current on the thread that calls render-context creation when OpenGL parameters are used.
- Rendering calls must be serialized with context ownership. Do not call GL rendering concurrently from arbitrary Tauri command threads.
- The app drives redraw: mpv signals that a new frame is needed through the update callback, then the app makes the GL context current and calls `mpv_render_context_render`.
- Destruction must happen after render loop shutdown, with the GL context still valid/current enough for mpv to release GL resources.

### `mpv_render_context_set_update_callback`

Purpose: registers a C callback that mpv invokes when the render target should be redrawn.

Practical implications:

- The callback can be called from an internal mpv thread. It should not render directly, call into UI code, block, or touch non-thread-safe Tauri/WebView state.
- The callback should only signal a render loop, typically by setting an atomic flag, waking an event loop proxy, or sending a non-blocking channel message to the render thread.
- The render thread then makes the OpenGL context current and calls `mpv_render_context_render`.

### `mpv_render_context_render`

Purpose: renders the current video frame into the target described by render parameters. For OpenGL FBO rendering, the app passes an `MPV_RENDER_PARAM_OPENGL_FBO` parameter each frame.

Required shape for OpenGL:

- OpenGL context current on the render thread.
- A valid framebuffer object target described by `mpv_opengl_fbo`.
- Correct pixel dimensions, not CSS logical dimensions. On Windows this means synchronize with DPI scale factor.
- Usually include `MPV_RENDER_PARAM_FLIP_Y` depending on whether the target framebuffer coordinate system requires vertical flip.
- End parameter array with `MPV_RENDER_PARAM_INVALID`.

### `MPV_RENDER_PARAM_OPENGL_INIT_PARAMS`

The parameter value points to a `mpv_opengl_init_params` struct. For the MVP this primarily supplies:

- `get_proc_address`: function pointer loader for GL symbols.
- `get_proc_address_ctx`: optional app context pointer.

The application can get the loader from the GL context crate/platform API, for example `glutin` display `get_proc_address`, WGL `wglGetProcAddress` plus fallback to `opengl32.dll`, or an equivalent context manager.

### `MPV_RENDER_PARAM_OPENGL_FBO`

The parameter value points to `mpv_opengl_fbo`, which describes where mpv should draw for the current frame:

- `fbo`: OpenGL framebuffer object name. `0` means the default framebuffer of the current surface/context.
- `w`: framebuffer width in physical pixels.
- `h`: framebuffer height in physical pixels.
- `internal_format`: GL internal format, commonly `0` for default framebuffer or an explicit format for custom FBOs.

For the simplest native child-surface MVP, render to default framebuffer `fbo = 0`, with `w/h` set to the child surface client area in physical pixels.

## `libmpv-sys = 3.1` Render API Exposure

### What was verified from this repository

- `player/src-tauri/Cargo.toml:12` declares `libmpv-sys = "3.1"`.
- `player/src-tauri/Cargo.lock:1927-1930` resolves it to `libmpv-sys 3.1.0`.
- Current code imports many raw symbols from `libmpv_sys` successfully in `player/src-tauri/src/mpv/player.rs:8-13`, confirming the project is already using the raw generated binding style.

### Symbol/type expectation for `libmpv-sys 3.1.0`

Based on the earlier local header/binding check supplied in the task context and libmpv C API shape, the required OpenGL render API symbols should be expected under `libmpv_sys` with bindgen names close to:

- `mpv_render_context`
- `mpv_render_context_create`
- `mpv_render_context_free`
- `mpv_render_context_render`
- `mpv_render_context_set_update_callback`
- `mpv_render_param`
- `mpv_render_param_type_MPV_RENDER_PARAM_API_TYPE`
- `mpv_render_param_type_MPV_RENDER_PARAM_OPENGL_INIT_PARAMS`
- `mpv_render_param_type_MPV_RENDER_PARAM_OPENGL_FBO`
- `mpv_render_param_type_MPV_RENDER_PARAM_FLIP_Y`
- `mpv_render_param_type_MPV_RENDER_PARAM_INVALID`
- `mpv_opengl_init_params`
- `mpv_opengl_fbo`
- `MPV_RENDER_API_TYPE_OPENGL`
- `MPV_RENDER_API_TYPE_SW`

### D3D11 exposure

The task context said a quick local check found `MPV_RENDER_API_TYPE_OPENGL` and `MPV_RENDER_API_TYPE_SW`, but not D3D11. That matches upstream libmpv render API expectations for portable public render APIs: OpenGL and software are exposed broadly; D3D11 is not a stable portable render API type in the same way for libmpv render API consumers.

Caveat: direct reading of the local cargo registry binding file was denied by the harness during this research session, so the binding-file verification could not be completed line-by-line. Before implementation, run a small compile-only import check or inspect the generated binding to confirm exact bindgen symbol names. The architecture below does not depend on D3D11 being available.

## Windows-first True Render API Architecture for Tauri v2

### Minimal architecture: native child OpenGL surface + transparent WebView overlay

The practical Windows-first shape is:

```text
Tauri top-level HWND
├─ Native child HWND / render panel
│  └─ WGL/OpenGL context current on render thread
│     └─ mpv_render_context_render(... OPENGL_FBO default framebuffer ...)
└─ WebView2 Vue UI layer
   ├─ transparent background where video should show through
   ├─ hover-revealed controls, subtitles/danmaku canvas, OSD
   └─ pointer hit-testing for controls
```

Key points:

- Do not use `wid` and do not let mpv create/manage its own native window.
- Create an application-owned native child window or overlay panel with its own OpenGL context.
- Create `mpv_render_context` while that GL context is current.
- Register an update callback that wakes a render thread/event loop.
- Render frames into the child surface's default framebuffer or an app-managed FBO.
- Keep Vue controls in WebView because OhMyCine's Cinema OS UI is Vue-first and already designed for hover-revealed liquid-glass overlays.

### Creating the OpenGL context/surface/window

Windows MVP options:

1. Create a child `HWND` using Win32 APIs, parented to the Tauri window's `HWND`; create WGL context manually with `windows-sys` and `opengl32`.
2. Use `glutin`/`winit` to create a GL context and a window/surface, then parent or position it with native handles.
3. Use Tauri/TAO raw window handles for the parent top-level window, then manage the child surface directly.

For MVP feasibility, the lowest-abstraction path is Win32 child `HWND` + WGL through `windows-sys`, because it avoids running a second independent winit/tao event loop inside Tauri. However, it means more unsafe Win32/WGL boilerplate.

`glutin` can reduce GL config/context boilerplate, but using it inside an already-running Tauri/TAO app is mainly suitable if it can create a context from an existing window/surface or if the app can carefully integrate handle ownership. It is less straightforward if it wants its own event loop/window lifecycle.

### Composition with Vue WebView UI

There are two composition patterns on Windows:

1. Render child HWND behind a transparent WebView2.
2. Render sibling/overlay child HWND and move/resize WebView2 or native surface to maintain z-order.

For OhMyCine's intended UI, a transparent WebView overlay is still needed if Vue controls, danmaku canvas, subtitles, hover UI, and glass chrome must appear over the video.

Tauri/WebView2 must be configured so the WebView background is transparent. The Vue root/player view also needs transparent regions where the video is visible. Controls can remain normal HTML/CSS layers above the transparent video area.

### Resize/fullscreen/DPI synchronization

The render surface must track:

- Tauri window size changes.
- Player container rectangle inside the WebView layout.
- Fullscreen enter/exit and borderless titlebar changes.
- DPI scale factor changes when the window moves between monitors.
- Minimize/restore and visibility changes.

A practical MVP protocol:

1. Vue observes the player container with `ResizeObserver` and reports CSS-pixel rect to Rust via Tauri command/event.
2. Rust multiplies by the Tauri/window scale factor to compute physical framebuffer size.
3. Rust calls `SetWindowPos` on the child `HWND` to match the player rect in parent-window coordinates.
4. Render loop stores the latest physical width/height and passes it through `mpv_opengl_fbo { fbo: 0, w, h, internal_format: 0 }`.
5. Fullscreen changes send one explicit sync event after transition.

## Rust Crates / Options

| Option | Role | Windows GNU compatibility | Fit for MVP |
|---|---|---|---|
| `windows-sys` | Raw Win32 APIs: `HWND`, `CreateWindowExW`, `SetParent`, `SetWindowPos`, WGL/OpenGL loader interop, DPI calls. | Good. Pure Rust bindings over Windows import libs; widely used with `x86_64-pc-windows-gnu`. Feature selection is verbose. | Strong fit for child HWND and explicit lifecycle. More unsafe code. |
| `raw-window-handle` | Common handle types for extracting/passing native handles. | Good; data-only crate. | Useful for retrieving/transporting Tauri/TAO window handles, not sufficient alone. |
| `tao` | Window/event loop library used under Tauri/wry ecosystem. | Generally compatible through Tauri dependency stack. | Avoid adding a second independent app event loop; useful mainly because Tauri exposes tao-backed windows/handles. |
| Tauri window handles | Source of parent top-level native window handle; Tauri v2 exposes raw-window-handle traits/APIs depending on features/platform. | Compatible as part of existing build. | Necessary to parent the native render surface to the Tauri window. |
| `glutin` | Cross-platform OpenGL config/context/surface creation. | Generally supports Windows GNU through `glutin-wgl`/`windows-sys` stack, but exact versions should be compile-checked with project target. | Helpful if it can manage a context for an existing child window; may be awkward with Tauri lifecycle. |
| `winit` | Cross-platform windows and events; often paired with glutin. | Generally supports Windows GNU. | Not ideal to create/own another event loop inside Tauri. Could be used only indirectly or in a separate render-window spike. |
| Manual WGL + `gl`/loader | Create context and load GL functions manually. | Compatible if linked against `opengl32`; more platform code. | Most predictable for Windows child HWND MVP. |

Notes for `x86_64-pc-windows-gnu`:

- `windows-sys`, `raw-window-handle`, and Win32 import libs are normally compatible with GNU target.
- GL context creation via WGL links to `opengl32`, which is available in Windows SDK/MinGW environments.
- The harder dependency is not Rust crate compatibility; it is shipping/locating `libmpv-2.dll` and its dependent DLLs built for the same Windows ABI/runtime expectations.

## Implementation Shape Comparison

### A. Native child/overlay render surface using OpenGL + mpv render API

What it provides:

- True embedded libmpv render API path.
- Hardware decode/render path remains possible.
- Avoids unmanaged mpv external window.
- Fits the product goal of native video with Vue UI overlays.

Minimum moving parts:

- Parent Tauri `HWND` acquisition.
- Child render `HWND` creation and placement.
- WGL/GL context creation and function loader.
- `mpv_render_context_create` with OpenGL init params.
- Update callback -> render thread wakeup.
- Render loop with FBO params and buffer swap.
- Transparent WebView/Vue overlay configuration.
- Resize/fullscreen/DPI synchronization.
- Cleanup ordering.

Expected MVP result:

- A Windows-native visual test build where video appears inside the Tauri player area, Vue controls overlay it, and no external mpv window appears.

### B. Software render API (`MPV_RENDER_API_TYPE_SW`) into CPU buffer, displayed in WebView/canvas

What it provides:

- Avoids native GL context and child-window z-order problems.
- Useful as a diagnostic/prototype to validate mpv render API lifecycle, frame callbacks, and Vue display plumbing.
- Can work even where OpenGL/window composition is blocked.

Minimum moving parts:

- `mpv_render_context_create` with API type `MPV_RENDER_API_TYPE_SW`.
- CPU pixel buffer allocation sized to video/player rect.
- Render params for software buffer, format, size, and stride.
- Transfer frames to WebView: image bitmap, canvas, shared memory, custom protocol, or IPC.

Major limitations:

- High CPU and memory bandwidth cost, especially 1080p/4K.
- Tauri IPC/WebView transfer can become the bottleneck.
- Color/HDR/hardware decode behavior will not represent final player quality.
- Latency and frame pacing will be poor compared with native GL.

Best use:

- Short-lived fallback/prototype for render API integration and CI-like smoke tests, not the main MVP for a home cinema player.

## Biggest Risks

| Risk | Why it matters | Practical guardrail |
|---|---|---|
| WebView2 z-order and transparency | Native child HWND and WebView2 are both native surfaces; sibling z-order and transparency may not behave like normal DOM layers. | Validate first with a colored GL child surface behind transparent Vue before adding mpv. |
| OpenGL context ownership/threading | `mpv_render_context_create` and `mpv_render_context_render` require the correct GL context current on the correct thread. | Use one dedicated render thread; route callbacks/events to it; avoid rendering from Tauri command threads. |
| Update callback threading | mpv callback may run on mpv internal threads. | Callback only wakes render loop via atomic/channel; no GL, no locks that can deadlock with mpv control calls. |
| Render loop/frame pacing | Rendering too often wastes CPU/GPU; rendering too late stutters. | Render on mpv update callback plus resize/expose events; optionally use vsync/swap interval. |
| `mpv_render_context_create` timing | OpenGL init params need a current context and valid loader. | Create child surface and make GL current before creating mpv render context. |
| Windows GNU packaging | `libmpv-2.dll` and dependency DLLs must match target and be discoverable at runtime. | Keep existing `npm run tauri:build:windows` packaging checks; include a runtime smoke checklist for user visual testing. |
| Cleanup ordering | Destroying window/GL before mpv render context can crash or leak. | Stop render loop -> free mpv render context while GL context is valid -> destroy GL context -> destroy child HWND -> terminate mpv. |
| DPI/resize mismatch | CSS pixels differ from framebuffer pixels; video can blur, crop, or render black. | Use physical pixel dimensions in FBO params and update on scale-factor changes. |
| WebView overlay hit testing | Transparent regions may still intercept mouse events or block native surface. | Vue player root uses intentional `pointer-events` layering: controls receive events; transparent video zones can pass/ignore as designed. |
| Cross-platform expansion | Windows child HWND/WGL path does not solve macOS/Linux/mobile. | Keep render backend in `mpv/render.rs` with platform-specific modules; MVP explicitly Windows-first. |

## Recommended MVP Sequence

### Milestone 0: Binding and build proof

Goal: prove exact `libmpv-sys` render symbol names compile for the existing target.

Validation commands:

```bash
cd /home/develop/development/Code/OhMyCine/player/src-tauri
cargo check
```

Add only a compile-only import/use in the render module during implementation, then remove or expand it into real wrapper code. Verify these symbols/types exactly: `mpv_render_context_create`, `mpv_render_context_set_update_callback`, `mpv_render_context_render`, `mpv_render_context_free`, `mpv_render_param`, `mpv_opengl_init_params`, `mpv_opengl_fbo`, `MPV_RENDER_API_TYPE_OPENGL`, `MPV_RENDER_API_TYPE_SW`.

### Milestone 1: Native child surface smoke test, no mpv

Goal: prove Tauri parent HWND -> child HWND -> GL context -> clear color -> swap buffers.

Validation:

- Windows build starts without external mpv window.
- A colored rectangle appears exactly in the intended player area.
- Resize/fullscreen/DPI changes keep the rectangle aligned.

Commands:

```bash
cd /home/develop/development/Code/OhMyCine/player
npm run tauri:build:windows
```

Runtime visual validation is user-owned on Windows per project preference.

### Milestone 2: Transparent Vue/WebView overlay proof

Goal: prove Vue controls can sit visually above the native GL surface.

Validation:

- WebView background is transparent in the player region.
- Liquid-glass controls render above the colored GL/video area.
- Hover-revealed controls still receive mouse events.
- Window dragging/chrome is not blocked by invisible overlay regions.

### Milestone 3: mpv OpenGL render context creation

Goal: create `mpv_render_context` with OpenGL init params while the child GL context is current.

Validation:

- `mpv_render_context_create` returns success.
- No unmanaged external mpv window is created.
- Existing audio/control commands still work.
- Remove `vo=null` and `video=no` only when render context path is active.

### Milestone 4: Render callback and frame display

Goal: update callback wakes render thread; render thread calls `mpv_render_context_render` with default FBO and swaps buffers.

Validation:

- Local file or known stream displays video in the child surface.
- Pause/resume/seek still works through existing Tauri commands.
- Resize does not crash and FBO dimensions update.

### Milestone 5: Lifecycle hardening

Goal: robust shutdown and state transitions.

Validation:

```bash
cd /home/develop/development/Code/OhMyCine/player/src-tauri
cargo check
cargo clippy
cargo test
```

```bash
cd /home/develop/development/Code/OhMyCine/player
npm run typecheck
npm run lint
npm run build
npm run tauri:build:windows
```

Runtime checklist:

- Start playback.
- Pause/resume.
- Seek repeatedly.
- Resize window.
- Enter/exit fullscreen.
- Switch DPI monitor if available.
- Close during playback.
- Reopen and play another file.

### Milestone 6: Optional software-render spike

Only if native GL composition blocks progress. Keep it as a diagnostic branch/prototype and do not treat it as the main product renderer.

## External References

- libmpv C API documentation, `render.h` / render API section — defines `mpv_render_context_create`, `mpv_render_context_set_update_callback`, `mpv_render_context_render`, `mpv_render_param`, `mpv_opengl_init_params`, `mpv_opengl_fbo`, OpenGL API type, and software API type.
- mpv examples using `libmpv/render_gl.h` / OpenGL embedding — demonstrate app-owned GL context, `get_proc_address`, update callback wakeup, FBO render params, and render-loop ownership.
- Tauri v2 window/WebView documentation — relevant for retrieving native window handles, configuring transparent windows/WebViews, fullscreen/resize events, and platform-specific WebView behavior.
- `windows-sys` crate documentation — relevant for Win32 child `HWND`, WGL/OpenGL, DPI, and window positioning APIs.
- `glutin` / `raw-window-handle` documentation — relevant for cross-platform GL context creation and native handle interop.

## Related Specs

| Spec | Relevance |
|---|---|
| `/home/develop/development/Code/OhMyCine/docs/architecture/03-player-design.md` | Defines Player as Tauri v2 + Vue + embedded libmpv, with native video under Vue UI controls. |
| `/home/develop/development/Code/OhMyCine/docs/architecture/01-overview.md` | Product-level context for Player as independent user-facing app. Not read in this session. |
| `/home/develop/development/Code/OhMyCine/docs/architecture/07-security-design.md` | Relevant for future playback URLs/credentials/logging, but not directly needed for GL render feasibility. Not read in this session. |

## Caveats / Not Found

- Direct local inspection of the cargo registry binding file for `libmpv-sys-3.1.0` was denied by the harness during this research session. The report therefore verifies the crate version from repo files and records expected render API symbol names from libmpv API shape plus the task-provided earlier local check, but exact bindgen identifiers should be compile-checked before implementation.
- No D3D11 render API path should be assumed for MVP. The feasible path is OpenGL render API on Windows, not D3D11.
- WebView2 transparency plus native child HWND z-order is the highest-risk visual integration area and needs a smoke test before mpv-specific work.
- The software render API is feasible as a short-lived prototype/fallback, but it is not a performance-credible MVP renderer for 1080p/4K home cinema playback.
