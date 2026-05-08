# Research: libmpv embedding approaches

Date: 2026-05-08

## Sources

- mpv manual: https://mpv.io/manual/stable/
- `tauri-plugin-libmpv` README: https://raw.githubusercontent.com/nini22P/tauri-plugin-libmpv/main/README.md
- `tauri-plugin-libmpv` `src/desktop.rs`: https://raw.githubusercontent.com/nini22P/tauri-plugin-libmpv/main/src/desktop.rs
- `tauri-plugin-libmpv` `src/utils.rs`: https://raw.githubusercontent.com/nini22P/tauri-plugin-libmpv/main/src/utils.rs
- OhMyCine current code:
  - `player/src-tauri/src/mpv/player.rs`
  - `player/src-tauri/src/mpv/render.rs`
  - `player/src/components/player/VideoPlayer.vue`

## Current OhMyCine baseline

OhMyCine currently uses raw `libmpv-sys` in Rust:

- `MpvPlayer::new()` creates `mpv_handle`, sets options, initializes mpv.
- To avoid unmanaged external mpv windows, current options include:
  - `force-window=no`
  - `vo=null`
  - `video=no`
- `render.rs` is only a placeholder: `pub struct MpvRenderContext;`.
- Frontend `VideoPlayer.vue` displays a truthful placeholder saying embedded rendering is still being integrated.

This means the control path is already valid, but visible video is deliberately disabled.

## Approach A: mpv `wid` mode against Tauri native window (plugin-managed) — Recommended for Windows MVP

### How it works

mpv supports a `wid` option: pass an existing native window id/handle and mpv draws video into that window. The `tauri-plugin-libmpv` project implements this pattern for Tauri v2:

1. Get Tauri `WebviewWindow` by label.
2. Call `window.window_handle()?.as_raw()`.
3. Convert raw handle to an integer window id:
   - Win32: `handle.hwnd.get() as i64`
   - Xlib/Xcb: native X window id
   - AppKit: `ns_view.as_ptr() as i64`
   - Wayland: unsupported
4. Insert `wid` into mpv initial options.
5. Let mpv render with a normal VO (`gpu-next` in README example).
6. Make the Tauri window/WebView transparent so UI chrome can be drawn visually above the video.

### Pros

- Fastest route to a real Windows MVP.
- Existing plugin already targets Tauri v2.
- Existing JS API provides `init`, `command`, `setProperty`, `getProperty`, `observeProperties`, `destroy`.
- Windows status is documented upstream as fully tested.
- Rust side avoids MSVC-only bindings; dependency surface is `tauri`, `raw-window-handle`, `libloading`, etc.
- Aligns with current product priority: Windows desktop first.

### Cons / risks

- It is not true WebView DOM/canvas rendering; it depends on native window/video composition and transparent WebView overlay.
- Linux embedding is explicitly not working in the plugin README; macOS is untested.
- Window transparency can interact badly with custom window chrome, hit testing, shadows, fullscreen, and DPI.
- The project is small (10 GitHub stars when checked).
- We should avoid two active mpv backends; adopting the plugin likely means migrating or disabling the current raw `libmpv-sys` Tauri commands.

### Windows GNU feasibility

Verdict: **viable for MVP on windows-gnu, but must validate build + runtime**.

Reasoning:

- Plugin Rust crate uses Tauri v2 and raw-window-handle, not MSVC-only Windows crates.
- Native mpv and wrapper are dynamically loaded runtime DLLs.
- OhMyCine already cross-builds Windows GNU NSIS packages.
- Remaining unknown is runtime compatibility and composition behavior on the user's Windows host.

## Approach B: Self-built raw `wid` mode in existing `libmpv-sys` backend

### How it works

Keep OhMyCine's current `MpvPlayer` and add an API to pass a native window id into `mpv_set_option_string(ctx, "wid", ...)` before `mpv_initialize`, then enable a real VO (`gpu` or `gpu-next`) instead of `vo=null` / `video=no`.

Potential code-level sketch:

```rust
let wid = obtain_window_handle_somehow(app_handle, window_label)?;
player.set_option("wid", &wid.to_string())?;
player.set_option("vo", "gpu-next")?;
player.set_option("video", "yes")?;
player.set_option("force-window", "yes")?;
```

### Pros

- Keeps our existing Rust command/event layer.
- Avoids adopting a small third-party plugin and wrapper library.
- Gives full control over lifecycle, errors, logging, and future platform work.

### Cons / risks

- We would need to reproduce much of `tauri-plugin-libmpv`:
  - raw window handle extraction
  - platform handle conversion
  - runtime library loading/package layout if needed
  - event handling and observed properties
  - transparency/window config guidance
- Higher implementation risk for little MVP benefit.
- Existing raw `libmpv-sys` links directly to libmpv; Windows package currently has custom libmpv setup. Any mismatch between link-time and runtime libmpv can become packaging debt.

### Windows GNU feasibility

Verdict: **viable but more work than plugin-managed `wid`**.

This is reasonable if the project refuses third-party plugin dependency or wants strict long-term ownership, but it is a slower MVP path.

## Approach C: libmpv render API (`mpv_render_context_create`) with OpenGL/D3D11 texture composition

### How it works

The host creates a rendering context (OpenGL/D3D11/software), creates an mpv render context from the mpv handle, receives update callbacks, and calls render functions into host-controlled framebuffer/surface.

This is the architecture with the highest ceiling because the app owns composition. It can support precise UI/video layering and eventually better cross-platform rendering if paired with a proper renderer abstraction.

### Pros

- Best long-term architecture for deeply integrated immersive UI.
- Avoids z-order hacks and transparent WebView limitations if the host renderer can composite everything.
- Can evolve toward custom shaders, HDR/tonemapping control, offscreen texture workflows, and platform-specific optimized backends.

### Cons / risks

- Tauri WebView apps do not already expose a simple native render loop for arbitrary OpenGL/D3D11 composition behind DOM content.
- We would need to introduce a native rendering surface/window and synchronize it with the WebView UI.
- D3D11/DXGI plumbing on Windows may require more platform-specific bindings and runtime debugging.
- Cross-platform support requires separate backend work: OpenGL/X11/Wayland/macOS/Windows differences.
- This is too large for a first MVP unless we narrow it to a small native child window renderer.

### Windows GNU feasibility

Verdict: **viable but complex; not recommended for this MVP**.

It is the likely long-term ideal, but starting here risks spending the task on graphics infrastructure instead of shipping visible playback.

## Approach D: Offscreen frames into WebView canvas / image stream

### How it works

Render mpv frames offscreen, copy them to CPU memory or shared GPU textures, and display them inside the WebView via canvas/WebGL/WebGPU or a stream.

### Pros

- Conceptually fits DOM layout: video becomes a normal canvas element.
- UI layering becomes easy inside the WebView.

### Cons / risks

- High CPU/GPU copy cost if not using zero-copy shared textures.
- WebView2/WebKit shared texture interop is non-trivial.
- Latency, color, HDR, frame pacing, and synchronization are hard.
- Overkill for an MVP.

### Windows GNU feasibility

Verdict: **not viable for MVP**.

## Approach E: Separate borderless overlay/top-level mpv window synchronized with Tauri

### How it works

Create or let mpv create its own top-level borderless window and continuously move/resize it to sit behind or above the Tauri window/video area.

### Pros

- Avoids some child-window handle constraints.
- Can be debugged incrementally.

### Cons / risks

- Focus, z-order, drag, fullscreen, multi-monitor, DPI, and window movement are fragile.
- Violates current safety goal of preventing unmanaged external mpv windows unless tightly controlled.
- User may see flicker or separate taskbar/focus behavior if not perfect.

### Windows GNU feasibility

Verdict: **avoid unless plugin `wid` fails**.

## Ranked recommendation for OhMyCine MVP

1. **Approach A: adopt `tauri-plugin-libmpv` Windows-first** — fastest real embedded rendering path, already matches Tauri v2 and uses `wid` mode.
2. **Approach B: self-built `wid` mode in current `libmpv-sys` layer** — backup if plugin packaging/runtime fails or dependency risk is unacceptable.
3. **Approach C: true render API** — long-term architecture candidate, not MVP.
4. **Approach E: synced overlay window** — emergency fallback only.
5. **Approach D: WebView canvas/offscreen** — not MVP.

## Biggest unknowns

- Plugin route: whether OhMyCine's transparent window, custom chrome, resize/fullscreen behavior, and Windows GNU package runtime all work together.
- Self-built `wid`: whether we can get identical behavior without pulling in the plugin's wrapper/lifecycle work.
- Render API: how to introduce a native render loop/surface without fighting Tauri WebView composition.
