# Research: tauri-plugin-libmpv

Date: 2026-05-08

## Sources

- npm registry API for `tauri-plugin-libmpv-api`: https://registry.npmjs.org/tauri-plugin-libmpv-api
- GitHub README raw: https://raw.githubusercontent.com/nini22P/tauri-plugin-libmpv/main/README.md
- GitHub repository API: https://api.github.com/repos/nini22P/tauri-plugin-libmpv
- GitHub source `src/desktop.rs`: https://raw.githubusercontent.com/nini22P/tauri-plugin-libmpv/main/src/desktop.rs
- GitHub source `src/utils.rs`: https://raw.githubusercontent.com/nini22P/tauri-plugin-libmpv/main/src/utils.rs
- GitHub source `Cargo.toml`: https://raw.githubusercontent.com/nini22P/tauri-plugin-libmpv/main/Cargo.toml
- Example app `Cargo.toml`: https://raw.githubusercontent.com/nini22P/tauri-plugin-libmpv/main/examples/react/src-tauri/Cargo.toml

## Package facts

- npm package: `tauri-plugin-libmpv-api`
- npm latest: `0.3.2`
- npm description: "A Tauri plugin for embedding the mpv player in your app via libmpv."
- npm repository: `git+https://github.com/nini22P/tauri-plugin-libmpv.git`
- npm package license:
  - `0.2.2`: LGPL-2.1
  - `0.3.0+`: MPL-2.0
- npm published versions visible from registry response: `0.2.2`, `0.3.0`, `0.3.1`, `0.3.2`
- JS dependency: `@tauri-apps/api = 2.8.0`
- Rust crate version in upstream `Cargo.toml`: `0.3.2`
- Rust crate dependencies include:
  - `tauri = 2.9.3`
  - `tauri-plugin = 2.5.1`
  - `raw-window-handle = 0.6.2`
  - `libloading = 0.8.9`
  - `once_cell`, `indexmap`, `serde`, `serde_json`, `thiserror`, `log`
- Repository stats from GitHub API on 2026-05-08:
  - stars: 10
  - open issues: 1
  - default branch: `main`
  - license: MPL-2.0
  - last push: 2025-11-24T04:32:11Z

## What it actually does

The upstream README describes the project as: "A Tauri plugin for embedding the mpv player in your app via libmpv."

It is not merely a property/command wrapper. The Rust implementation in `src/desktop.rs` calls `init_wid_mode`, obtains the Tauri `WebviewWindow` for the target `window_label`, reads its `raw_window_handle`, converts that to a platform window id through `get_wid`, and injects it into mpv's initial options as `wid` when the caller did not already provide one.

Relevant behavior from `desktop.rs`:

- `let window = self.app.get_webview_window(window_label)`
- `let window_handle = window.window_handle()?`
- `let raw_window_handle = window_handle.as_raw()`
- `get_wid(raw_window_handle)`
- `initial_options.insert("wid".to_string(), serde_json::json!(wid))`
- then passes the options to `libmpv-wrapper` with `mpv_wrapper_create(...)`

The JS API still exposes familiar player control functions (`init`, `command`, `setProperty`, `getProperty`, `observeProperties`, `destroy`), but initialization also binds mpv to a Tauri window via `wid` unless audio-only mode is detected or `wid` is supplied manually.

## Rendering strategy

The plugin uses mpv's native `wid` window embedding mode, not the libmpv render API.

The conversion logic in `src/utils.rs` is explicit:

- `RawWindowHandle::Win32(handle) => Ok(handle.hwnd.get() as i64)`
- `RawWindowHandle::Xlib(handle) => Ok(handle.window as i64)`
- `RawWindowHandle::Xcb(handle) => Ok(handle.window.get() as i64)`
- `RawWindowHandle::AppKit(handle) => Ok(handle.ns_view.as_ptr() as i64)`
- Wayland returns an unsupported-platform error: "Window embedding via --wid is not supported on Wayland."

That means mpv is allowed to render directly to the native window represented by Tauri's raw window handle. The README also requires window transparency so the WebView UI can sit visually above the mpv-rendered video:

- `tauri.conf.json` window: `transparent: true`
- web CSS: `html, body { background: transparent; }`

This is best understood as **mpv `wid` rendering + transparent WebView overlay**, not DOM/canvas rendering and not offscreen texture compositing.

## Platform support according to README

| Platform | Upstream status | Notes |
|---|---|---|
| Windows | Fully tested | Requires `libmpv-2.dll` and `libmpv-wrapper.dll` |
| Linux | Experimental | README explicitly says "Window embedding is not working" |
| macOS | Not tested | AppKit handle branch exists, but README says untested |

For OhMyCine's current MVP, this makes the plugin attractive only if the MVP is Windows-first. It should not be presented as complete Linux/macOS embedded rendering.

## Dynamic library setup

The plugin requires two runtime libraries:

1. `libmpv-wrapper` — a wrapper library maintained by the plugin author.
2. `libmpv` — the real mpv core.

README describes automatic setup:

```bash
npx tauri-plugin-libmpv-api setup-lib
```

On Windows it downloads:

- `libmpv-wrapper` from `nini22P/libmpv-wrapper` releases.
- `libmpv` from `zhongfly/mpv-winbuild` releases.

Manual Windows setup expects:

- `src-tauri/lib/libmpv-wrapper.dll`
- `src-tauri/lib/libmpv-2.dll`
- `tauri.conf.json` bundle resources include `lib/**/*`

OhMyCine already has its own `scripts/setup-libmpv.mjs` and Windows GNU packaging setup. We should decide whether to keep our script, replace it, or make it call/align with the plugin setup CLI.

## JS API shape

README quick-start imports:

```ts
import {
  MpvObservableProperty,
  MpvConfig,
  init,
  observeProperties,
  command,
  setProperty,
  getProperty,
  destroy,
} from 'tauri-plugin-libmpv-api'
```

Example config:

```ts
const mpvConfig: MpvConfig = {
  initialOptions: {
    vo: 'gpu-next',
    hwdec: 'auto-safe',
    keep-open: 'yes',
    force-window: 'yes',
  },
  observedProperties: [
    ['pause', 'flag'],
    ['time-pos', 'double', 'none'],
    ['duration', 'double', 'none'],
    ['filename', 'string', 'none'],
  ],
}

await init(mpvConfig)
await command('loadfile', ['/path/to/video.mp4'])
await setProperty('volume', 75)
const volume = await getProperty('volume', 'int64')
const unlisten = await observeProperties(...)
await destroy()
```

This API overlaps almost entirely with OhMyCine's current custom `useMpv` + Tauri command bridge. Adopting the plugin likely means migrating the control layer to plugin APIs, not using both mpv instances side by side.

## Windows GNU cross-compile assessment

No direct upstream statement about `x86_64-pc-windows-gnu` was found.

However, the Rust crate's dependency surface is favorable:

- It uses `raw-window-handle` to obtain HWND.
- It uses `libloading` to load the wrapper/library dynamically.
- It does not declare `windows`, `windows-sys`, or `winapi` dependencies in `Cargo.toml`.
- The platform-specific work appears to be hidden in `libmpv-wrapper.dll`, which is shipped as a runtime DLL.

This suggests Windows GNU cross-compile should be realistic, because the Rust side should not require MSVC-only headers/libraries. The remaining risk is runtime compatibility of the downloaded `libmpv-wrapper.dll` and `libmpv-2.dll` with the MinGW-built Tauri executable.

## Maintenance and risk

Positive signals:

- Last push on GitHub: 2025-11-24.
- Latest npm package: 0.3.2.
- Tauri v2 dependencies: `tauri = 2.9.3`, `@tauri-apps/api = 2.8.0`.
- Small open issue count (1 open issue at time checked).
- MPL-2.0 license is compatible with normal app use as long as modifications to MPL-covered files are handled correctly.

Risks:

- Very small ecosystem footprint (10 stars).
- Linux embedding explicitly does not work; macOS untested.
- The approach relies on window transparency and mpv `wid` behavior, which may have z-order, resize, DPI, and input edge cases.
- Adopting plugin means adding a second mpv integration path unless we remove/migrate the existing `libmpv-sys` wrapper.

## Integration implications for OhMyCine

Recommended if choosing this route:

- Treat it as the MVP rendering backend for Windows only.
- Replace the custom Tauri mpv commands in `useMpv` with `tauri-plugin-libmpv-api` calls, or wrap plugin calls behind the same `useMpv` public API.
- Remove or stop initializing the current `MpvPlayer` raw `libmpv-sys` state to avoid two mpv instances.
- Configure Tauri window transparency and CSS transparent background only where required; preserve visual glass panels and app chrome.
- Bundle `src-tauri/lib/libmpv-wrapper.dll` and `libmpv-2.dll` through Tauri resources.
- Keep fallback UI for non-Windows / failed initialization.

## Bottom line

- It **does provide real embedded rendering on Windows**, using mpv `wid` against the Tauri native window handle plus transparent WebView overlay.
- It is a **realistic MVP option for Windows GNU cross-build**, though this needs actual `npm run tauri:build:windows` validation in OhMyCine.
- Biggest unknown: runtime behavior under OhMyCine's current custom window chrome / transparency / resize / fullscreen stack, plus dependency on a small third-party plugin and wrapper DLL.
