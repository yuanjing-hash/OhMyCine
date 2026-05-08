# Research: Tauri v2 WebView video embedding patterns

Date: 2026-05-08

## Sources

- `tauri-plugin-libmpv` README: https://raw.githubusercontent.com/nini22P/tauri-plugin-libmpv/main/README.md
- `tauri-plugin-libmpv` `src/desktop.rs`: https://raw.githubusercontent.com/nini22P/tauri-plugin-libmpv/main/src/desktop.rs
- `tauri-plugin-libmpv` `src/utils.rs`: https://raw.githubusercontent.com/nini22P/tauri-plugin-libmpv/main/src/utils.rs
- `tauri-plugin-libmpv` example Cargo config: https://raw.githubusercontent.com/nini22P/tauri-plugin-libmpv/main/examples/react/src-tauri/Cargo.toml
- OhMyCine current frontend/runtime files:
  - `player/src/components/player/VideoPlayer.vue`
  - `player/src/views/PlayerView.vue`
  - `player/src-tauri/src/mpv/player.rs`
  - `player/src-tauri/tauri.conf.json`

## Pattern 1: Single Tauri WebviewWindow + native mpv `wid` + transparent WebView — Recommended MVP shape

### How it works

The application uses one Tauri `WebviewWindow`. Rust obtains the native window handle through Tauri/raw-window-handle. mpv receives that native handle as its `wid` option. The WebView background is made transparent so the video rendered by mpv to the native window can show through, while Vue renders controls, chrome, and overlays above it.

This is exactly what `tauri-plugin-libmpv` implements:

```rust
let window = app.get_webview_window(window_label)?;
let window_handle = window.window_handle()?;
let raw_window_handle = window_handle.as_raw();
let wid = get_wid(raw_window_handle)?;
initial_options.insert("wid".to_string(), serde_json::json!(wid));
```

The plugin's `get_wid` supports:

- Win32: `HWND`
- Xlib: X window id
- Xcb: X window id
- AppKit: `ns_view`
- Wayland: explicitly unsupported

The README then requires:

- Tauri window config: `transparent: true`
- CSS: `html, body { background: transparent; }`

### Pros

- Uses a proven Tauri v2 plugin rather than inventing native window glue.
- Works with Vue UI: controls remain DOM/CSS.
- No per-frame copy to WebView canvas.
- Windows is documented as fully tested upstream.
- Good fit for OhMyCine's current Player layout: the whole Player page can be transparent behind glass controls.

### Cons / gotchas

- This is not a native child `<div>` surface. It is whole-window composition. We cannot trivially clip mpv to a DOM element unless the entire page layout is designed around a video-underlay.
- `html/body/#app` backgrounds and route backgrounds must be audited. Any opaque full-screen background will hide the video.
- Window transparency may change native window shadows, drag regions, hit testing, and platform appearance.
- Multi-window / modal overlays must be checked.
- Linux/Wayland is unsupported by this plugin path; Linux X11 is theoretically possible but README says window embedding is not working.

### MVP fit

Verdict: **best Windows-first MVP shape**.

OhMyCine already uses an immersive Player page with hover-revealed controls. A full-window video underlay with Vue chrome above fits the product direction.

## Pattern 2: Native child video window inside the Tauri window

### How it works

Create a platform child window/control as a child of the Tauri native window and give that child HWND/XID/etc. to mpv. Synchronize its bounds with a DOM video container via frontend measurements and Tauri commands.

### Pros

- Video can be clipped to a specific rectangle instead of occupying the whole window.
- Does not require making the entire WebView transparent if the child window is stacked correctly.

### Cons / gotchas

- WebView2 itself is a native child/composited surface. Z-order between WebView2 and an additional sibling/child HWND can be difficult.
- DOM coordinates must be measured and converted from CSS pixels to physical pixels with DPI scaling.
- Resize, route transitions, fullscreen, window movement, drag region, and focus all need synchronization.
- Requires custom Win32/X11/AppKit code or a dedicated crate/plugin.

### MVP fit

Verdict: **viable but too much native surface work for this MVP**.

This may become relevant later if full-window transparent video underlay conflicts with the final UI design.

## Pattern 3: Separate borderless overlay window synchronized with the Tauri window

### How it works

Create a separate top-level window for video and place it behind or above the Tauri window. Continuously synchronize position/size/z-order. UI remains in the original Tauri WebView window.

### Pros

- Avoids embedding into WebView/native child hierarchy.
- Can work even if direct child window composition fails.

### Cons / gotchas

- Focus and z-order are fragile.
- Separate windows can appear in taskbar/Alt-Tab unless carefully configured.
- Multi-monitor, DPI, fullscreen, minimize/restore, and dragging require lots of event glue.
- This conflicts with the previous safety goal: no unmanaged external mpv windows.

### MVP fit

Verdict: **not recommended** except as a debugging fallback.

## Pattern 4: Render API into native renderer, then composite UI separately

### How it works

Use libmpv render API and draw frames into a native rendering surface controlled by the app. Then either:

- Put a transparent WebView over that native surface, or
- Move UI into a native renderer too.

### Pros

- Highest product ceiling.
- Better long-term control over compositing, colors, HDR, GPU interop.

### Cons / gotchas

- Requires owning a native render loop and graphics context.
- Tauri's main UI is WebView; mixing a custom renderer and WebView still hits composition/z-order issues unless the architecture is designed around two surfaces.
- More platform-specific code.

### MVP fit

Verdict: **long-term candidate, not MVP**.

## Event and resize synchronization

For the recommended single-window transparent `wid` pattern, resize handling is mostly delegated to the native window/mpv VO because mpv renders to the same main window handle.

Still, OhMyCine must validate:

- window resize
- maximize/unmaximize
- fullscreen
- DPI scaling across monitors
- custom titlebar drag regions
- hover-revealed controls and pointer events
- route transitions in and out of PlayerView

If later using a child/sibling window, the frontend must report container bounds to Rust, likely through a `ResizeObserver` in `VideoPlayer.vue` and a Tauri command to move/resize the native surface.

## Recommendation for OhMyCine

Use **single Tauri window + plugin-managed `wid` + transparent WebView** for the Windows MVP.

Implementation shape:

1. Register `tauri-plugin-libmpv` in Rust.
2. Configure bundle resources for plugin DLLs.
3. Enable Tauri window transparency for the Player window/app window.
4. Make the global app/page backgrounds compatible with transparent video underlay, while preserving liquid-glass panels.
5. Replace `useMpv` internals with `tauri-plugin-libmpv-api` calls, keeping the composable's public API stable for `PlayerView`.
6. Keep non-Windows / failed-init placeholder states.

This gives the shortest path to visible playback while preserving the option to build a true render API backend later if product needs outgrow `wid` mode.
