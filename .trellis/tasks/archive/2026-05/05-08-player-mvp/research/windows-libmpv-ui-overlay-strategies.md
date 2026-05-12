# Research: Windows libmpv UI overlay strategies

- **Query**: Research libmpv/mpv embedding strategies on Windows when the app needs native video plus custom clickable UI controls: True render API with app-owned OpenGL surface, mpv wid child window, offscreen rendering, DirectComposition/D3D, separate layered windows, and how other players avoid WebView overlay conflicts.
- **Scope**: mixed
- **Date**: 2026-05-08

## Findings

### Files Found

| File Path | Description |
|---|---|
| `docs/architecture/03-player-design.md` | Product-level Player design states Tauri + Vue + libmpv embedded rendering as the intended playback engine. |
| `docs/architecture/06-roadmap.md` | Current roadmap marks `MpvRenderContext` and internal libmpv rendering as unfinished/under validation, with external mpv video windows suppressed. |
| `player/src-tauri/src/mpv/render.rs` | Current true-render API wrapper around `mpv_render_context_create`, update callback, `mpv_render_context_update`, and `mpv_render_context_render` into an OpenGL FBO. |
| `player/src-tauri/src/mpv/surface.rs` | Cross-platform native render surface boundary; Windows is implemented, other platforms return unsupported state for this slice. |
| `player/src-tauri/src/mpv/platform/windows.rs` | Windows native child HWND + WGL/OpenGL surface implementation, render thread, GL symbol loading, child-window positioning, hit-test passthrough. |
| `player/src/components/player/VideoPlayer.vue` | Vue video host reports DOM bounds to Rust and keeps the native surface host `pointer-events: none`; current UI text explicitly calls out Windows native surface / transparent WebView overlay validation. |
| `.trellis/tasks/05-08-player-mvp/research/libmpv-embedding-approaches.md` | Related existing research topic for broader libmpv embedding approaches. |
| `.trellis/tasks/05-08-player-mvp/research/tauri-v2-webview-video.md` | Related existing research topic for Tauri WebView/video interaction. |
| `.trellis/tasks/05-08-player-mvp/research/true-render-api-code-impact.md` | Related existing research topic for true-render API code impact. |
| `.trellis/tasks/05-08-player-mvp/research/true-render-api-feasibility.md` | Related existing research topic for true-render API feasibility. |

### Code Patterns

#### Current project direction: true render API into app-owned Windows OpenGL child surface

- `player/src-tauri/src/mpv/render.rs:81-87` documents the intended safety boundary: create a true libmpv OpenGL render context only while an app-owned OpenGL context is current, keep symbol loading valid, and drop the mpv render context before the GL surface/context is destroyed.
- `player/src-tauri/src/mpv/render.rs:88-137` creates `mpv_render_context` with `MPV_RENDER_API_TYPE_OPENGL`, `MPV_RENDER_PARAM_OPENGL_INIT_PARAMS`, and an update callback.
- `player/src-tauri/src/mpv/render.rs:152-187` follows the render API model: call `mpv_render_context_update()`, then render into an OpenGL FBO with `MPV_RENDER_PARAM_OPENGL_FBO` and `MPV_RENDER_PARAM_FLIP_Y`.
- `player/src-tauri/src/mpv/platform/windows.rs:62-100` creates the Windows render surface by obtaining the Tauri parent HWND, registering a Win32 class, creating a child HWND, configuring pixel format, creating a WGL context, and starting a dedicated render thread.
- `player/src-tauri/src/mpv/platform/windows.rs:279-341` runs the render loop: make WGL current, set viewport/clear color, resize render context, call libmpv render API, swap buffers, then release current context.
- `player/src-tauri/src/mpv/platform/windows.rs:398-440` creates the child window as `WS_CHILD | WS_VISIBLE | WS_CLIPSIBLINGS | WS_CLIPCHILDREN` with `WS_EX_NOACTIVATE | WS_EX_TRANSPARENT`.
- `player/src-tauri/src/mpv/platform/windows.rs:531-540` returns `HTTRANSPARENT` for `WM_NCHITTEST`; the local comment says this is best-effort and that the preferred composition keeps the WebView above the full-bleed OpenGL child surface so Vue chrome receives input.
- `player/src/components/player/VideoPlayer.vue:79-97` measures the Vue host rect and sends `x/y/width/height/scaleFactor` to the Rust surface.
- `player/src/components/player/VideoPlayer.vue:138-143` keeps the DOM host as `pointer-events-none` and `aria-hidden`, so intended pointer handling remains in Vue, not the native surface.
- `player/src/components/player/VideoPlayer.vue:64-76` status copy already records the unresolved validation point: Windows app-owned OpenGL child surface and libmpv render context exist, but actual video, transparent WebView layering, and resize behavior need Windows-host validation.

#### Strategy A: true render API with app-owned OpenGL surface

External mpv docs describe this as the recommended embedding path:

- `include/mpv/client.h` says using the render API is recommended; it requires the app to create/maintain an OpenGL context and render video with a specific API call. It also states the render API does not include keyboard or mouse input directly.
- `include/mpv/render.h` says create `mpv_render_context` before playback/VO creation, call `mpv_render_context_render()` explicitly, and use `mpv_render_context_set_update_callback()` to know when there is a new frame.
- `include/mpv/render.h` recommends rendering on a separate thread and requires that OpenGL render API calls use the same current OpenGL context that created the render context.
- `mpv-examples/libmpv/README.md` says render API gives more flexibility than raw window embedding; specifically, the app can render its own OSD on top of video, which is not possible with raw window embedding.
- `mpv-examples/libmpv/qt_opengl/mpvwidget.cpp` shows the common pattern used by other GUI players/toolkits: create the mpv GL context inside a toolkit OpenGL widget (`QOpenGLWidget`), render into the widget's default framebuffer in `paintGL()`, then draw controls/OSD in the same toolkit scene rather than as a separate foreign child window layered over mpv.

Windows implications for custom clickable UI:

- If the video is rendered into the same composited scene as the UI, normal UI hit testing works because controls are not trying to float above a separate mpv-owned window.
- In a Tauri/WebView app, the current implementation still renders into a separate native child HWND behind/near a WebView, so it can still encounter Win32/WebView “airspace” ordering issues unless the WebView is genuinely transparent and compositor ordering is validated on Windows.
- The true-render API remains the best-aligned strategy for custom clickable chrome because mpv does not own input or the window; the app owns controls, pointer handling, focus, and sizing.

#### Strategy B: mpv `wid` child window embedding

External mpv docs describe `wid` as the older native-window embedding path:

- `include/mpv/client.h` says the older method is to get a raw native window handle and set it as the `wid` option; it works on X11, win32, and macOS, is easier than render API, but has various problems.
- `DOCS/man/options.rst` defines `--wid=<ID|-1>` as attaching mpv to an existing window. On Win32, the ID is interpreted as an `HWND`; mpv creates its own window and sets the `wid` window as parent.
- `mpv-examples/libmpv/README.md` says native window embedding is OS-dependent; on X11 and Win32, mpv fills the referenced parent window and letterboxes video. It also notes `input-vo-keyboard` may be needed for embedded-window keyboard behavior.
- `include/mpv/render.h` explicitly says render API is generally recommended because `wid` embedding can cause issues with GUI toolkits and some platforms.

Windows implications for custom clickable UI:

- `wid` gives mpv its own child HWND. That is simple for showing video, but makes custom WebView/Vue controls a separate windowing/composition problem.
- HTML/CSS `z-index` cannot reliably place DOM controls above a foreign native child HWND because the native child is not part of the DOM paint tree.
- Hit testing and focus also tend to belong to the mpv child window unless explicitly disabled/passed through, so clickable Vue controls over the video area are hard to make reliable.
- `wid` is therefore useful as a spike or fallback when video-only embedding is acceptable, but it does not match a Cinema OS player that needs liquid-glass clickable controls over native video.

#### Strategy C: offscreen rendering / software frames

mpv render API supports a software backend (`MPV_RENDER_API_TYPE_SW`) in addition to OpenGL, and offscreen OpenGL rendering to an FBO/texture is possible when the app controls the GL target.

Windows implications:

- Software/offscreen CPU frame paths avoid foreign child-window overlay conflicts because frames can be copied into a WebView canvas/bitmap or toolkit texture.
- This is generally the least attractive path for high-quality playback because it tends to add CPU copies, loses or complicates hardware decode/HDR/color-management benefits, and can increase latency.
- Offscreen GPU rendering to a texture can be architecturally strong if the app has a native compositor scene that can combine video texture + UI. In a standard Tauri WebView app, there is no simple, stable path for the Rust/native GL texture to become a DOM layer without copies or custom WebView/native composition work.

#### Strategy D: DirectComposition / D3D composition

mpv's public libmpv render API documented backend is OpenGL plus software. There is no general public libmpv D3D11/DirectComposition render API equivalent to `MPV_RENDER_API_TYPE_OPENGL` in the documented headers inspected here.

Windows implications:

- A DirectComposition/D3D design would be a Windows-native compositor strategy owned by the app: mpv would still need to deliver frames via supported render API or a VO-specific path, then the app would compose video and UI surfaces.
- This can theoretically solve WebView/native-airspace conflicts if WebView2 and video are both put into a DirectComposition visual tree, but it is substantially more Windows-specific than the current WGL child-surface implementation.
- Because libmpv's stable API is OpenGL/software, D3D/DirectComposition should be treated as a platform-specific advanced path rather than the current lowest-risk MVP path.

#### Strategy E: separate layered windows

Another Windows workaround is to place controls in a separate transparent/layered top-level window above the native video child/top-level window.

Windows implications:

- This can avoid DOM-vs-child-HWND z-order constraints because the overlay is not inside the WebView's DOM; it is another native window.
- It creates separate problems: synchronizing move/resize/fullscreen/minimize, DPI scaling, focus/activation, click-through regions, taskbar/Alt-Tab behavior, IME/accessibility, screen capture, and multi-monitor behavior.
- It is usually a workaround when the renderer cannot be integrated into the same scene as the UI, not the cleanest embedded-player architecture.

#### How other players/toolkits avoid WebView overlay conflicts

Observed/common patterns from mpv docs and examples:

1. **Toolkit OpenGL widget + render API**: Qt example uses `QOpenGLWidget`, creates `mpv_render_context` in `initializeGL()`, calls `mpv_render_context_render()` in `paintGL()`, and lets the toolkit own the whole widget/UI scene. This avoids a WebView DOM overlay fighting a separate mpv child HWND.
2. **Native toolkit UI, not WebView UI**: Many mpv frontends use Qt/GTK/WPF/WinUI/native controls around or above the video in the same toolkit/compositor model, rather than relying on CSS z-index over an external video window.
3. **Raw `wid` only where overlay requirements are limited**: `wid` embedding is acceptable for simple video panes or controls outside the video rectangle, but mpv's own docs say render API is recommended for flexibility and GUI-toolkit reliability.
4. **OSD inside the render scene**: The render API allows app-owned OSD/UI drawing on top of video in the same GL scene. In a Vue/Tauri app, equivalent behavior would require either native-rendered controls/OSD or a proven transparent WebView-over-native-video composition.

### External References

- [mpv `include/mpv/client.h`](https://github.com/mpv-player/mpv/blob/master/include/mpv/client.h) — documents “Embedding the video window”; recommends render API; describes older `wid` native-window embedding on X11/win32/macOS.
- [mpv `include/mpv/render.h`](https://github.com/mpv-player/mpv/blob/master/include/mpv/render.h) — documents render API lifecycle, threading rules, OpenGL-current-context requirement, `MPV_RENDER_API_TYPE_OPENGL`, and software backend.
- [mpv `include/mpv/render_gl.h`](https://github.com/mpv-player/mpv/blob/master/include/mpv/render_gl.h) — defines OpenGL render target structures such as `mpv_opengl_fbo`.
- [mpv manual `--wid`](https://mpv.io/manual/master/#options-wid) — documents `--wid=<ID|-1>` and Win32 `HWND` semantics; GitHub raw `DOCS/man/options.rst` was used because mpv.io returned HTTP 403 from this environment.
- [mpv-examples libmpv README](https://github.com/mpv-player/mpv-examples/tree/master/libmpv#methods-of-embedding-the-video-window) — compares native window embedding, render API, and deprecated `opengl-cb`; states render API allows app OSD over video and is generally recommended over window embedding.
- [mpv-examples Qt OpenGL widget](https://github.com/mpv-player/mpv-examples/blob/master/libmpv/qt_opengl/mpvwidget.cpp) — concrete pattern for `QOpenGLWidget` + `mpv_render_context_create` + `mpv_render_context_render`.
- [mpv-examples SDL render API](https://github.com/mpv-player/mpv-examples/blob/master/libmpv/sdl/main.c) — concrete render-loop example using `mpv_render_context_set_update_callback`, update events, FBO rendering, and `SDL_GL_SwapWindow`.
- [Microsoft WPF/Win32 interoperation: technology regions overview](https://learn.microsoft.com/en-us/dotnet/desktop/wpf/advanced/technology-regions-overview) — describes the Windows “airspace” class of problems when different rendering technologies / child HWNDs are composed together; relevant analogy for WebView + native video child HWND layering.

### Related Specs

- `.trellis/spec/` — no specific spec file was found in this pass that codifies Windows libmpv/WebView overlay strategy.
- `docs/architecture/03-player-design.md` — design-level requirement for embedded libmpv playback and Cinema OS UI.
- `docs/architecture/06-roadmap.md` — current milestone state for libmpv embedded rendering and UI controls.

## Caveats / Not Found

- No external search tool was available in this agent environment; external references were fetched from public GitHub raw sources where possible, and mpv.io manual pages were cross-referenced but returned HTTP 403 when fetched directly.
- No stable public libmpv Direct3D/DirectComposition render backend was found in the documented `include/mpv/render.h` / `render_gl.h` API surface inspected here.
- The current OhMyCine Windows implementation already follows the true-render/OpenGL route, but because it still uses a native child HWND alongside a Tauri WebView, the exact WebView transparency/z-order/input behavior remains a Windows-native runtime validation item.

## Concise Recommendation Summary

For OhMyCine's Windows MVP with native video plus custom clickable Vue controls, keep the primary direction as **libmpv render API + app-owned OpenGL/WGL surface**, not `wid`. The render API is the only documented mpv path that gives the app ownership of rendering, sizing, input, and OSD/control composition. Avoid `wid` for the main UX because it creates an mpv-owned child HWND and makes WebView overlays unreliable. Treat software/offscreen frames as a diagnostic/fallback due to copy/performance/HDR costs, DirectComposition/D3D as a later Windows-specific compositor investigation, and separate layered overlay windows as a last-resort workaround. The immediate MVP risk is not libmpv render API itself; it is validating Tauri/WebView2 transparency, child-HWND z-order, DPI resize, and pointer pass-through on native Windows.