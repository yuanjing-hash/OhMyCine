# Research: Player diagnostics panel scroll on Windows transparent overlay

- **Query**: Research and inspect the OhMyCine Player diagnostics panel scrolling issue on Windows Tauri transparent overlay; determine likely CSS/DOM reason and propose robust fix.
- **Scope**: mixed, primarily local code inspection with platform-pattern notes
- **Date**: 2026-05-21

## Findings

### Files Found

| File Path | Description |
|---|---|
| `player/src/components/player/VideoPlayer.vue` | Diagnostics panel implementation and native surface bounds reporter. |
| `player/src/views/PlayerView.vue` | Player route shell, chrome visibility handlers, transparent-root classes, parent clipping, top/bottom hover strips, and render-bounds forwarding. |
| `player/src/components/layout/AppLayout.vue` | Global app shell with `main.cinema-scrollbar absolute inset-0 overflow-auto`. |
| `player/src/styles/global.css` | Global `html/body` overflow and `.cinema-scrollbar` styling. |
| `player/src/styles/glass.css` | `.app-window` fixed viewport sizing and overflow clipping. |
| `player/src/components/layout/WindowChrome.vue` | Fixed frameless drag/window-control overlay above route content. |
| `player/src-tauri/tauri.conf.json` | Transparent frameless Tauri window configuration. |
| `player/src-tauri/src/main.rs` | Runtime WebView background transparency application. |
| `player/src-tauri/src/mpv/platform/windows.rs` | Windows mpv underlay / transparent WebView overlay implementation. |
| `.trellis/spec/frontend/type-safety.md` | Contract for Windows transparent overlay and render diagnostics. |
| `.trellis/spec/frontend/component-guidelines.md` | Player chrome, pointer interception, and diagnostics UI constraints. |

### Code Patterns

#### Current diagnostics panel shape

`VideoPlayer.vue` renders the diagnostics panel as an absolutely positioned child of the video surface root:

- Root surface: `player/src/components/player/VideoPlayer.vue:265-270`
  - `class="player-surface-root relative h-full w-full overflow-hidden"`
- Diagnostics panel: `player/src/components/player/VideoPlayer.vue:282-288`
  - `absolute left-5 top-16 z-30`
  - `max-h-[calc(100vh-6rem)]`
  - `overflow-y-auto overscroll-contain`
  - `pointer-events-auto`

The built CSS in `player/dist/assets/index-*.css` confirms the UnoCSS utilities are generated:

- `max-h-[calc(100vh-6rem)]` becomes `max-height: calc(100vh - 6rem)`.
- `overflow-y-auto` becomes `overflow-y: auto`.
- `overscroll-contain` becomes `overscroll-behavior: contain`.

So the issue is not likely caused by UnoCSS failing to emit the scroll utilities.

#### Ancestor clipping chain

Multiple ancestors intentionally clip to the viewport/player surface:

- `player/src/styles/global.css:1-8`: `html, body` use `overflow: hidden`.
- `player/src/styles/global.css:12-17`: `#app` is `100vw x 100vh`.
- `player/src/styles/glass.css:1-4`: `.app-window` is `100vw x 100vh` with `overflow: hidden`.
- `player/src/components/layout/AppLayout.vue:19-21`: route content lives in `main.cinema-scrollbar absolute inset-0 z-0 overflow-auto`.
- `player/src/views/PlayerView.vue:997-1007`: player route root is `relative h-screen w-full overflow-hidden`.
- `player/src/views/PlayerView.vue:1128-1136`: while the player render surface is active, `main.cinema-scrollbar` is forced to `overflow: hidden` and its scrollbar is hidden.
- `player/src/components/player/VideoPlayer.vue:265-270`: the video surface root is also `overflow-hidden`.

A scrollable child can still scroll inside clipped ancestors if its own block-size is smaller than the visible area. However, this panel currently uses a viewport-based max height (`100vh - 6rem`) while being positioned inside a clipped absolute/relative player surface. If the WebView visible region, frameless chrome area, fullscreen/maximized client area, or DPI rounding makes the panel’s effective visible area smaller than that computed viewport value, the ancestor clips the panel instead of the panel becoming the controlling scroll container.

#### Parent event handlers do not currently block wheel scrolling

Local search did not find a global wheel listener or `@wheel.prevent` in Player code. Relevant handlers around the player are:

- `player/src/views/PlayerView.vue:997-1007`: root listens for `mousemove`, `mouseleave`, and passive `touchstart` only.
- `player/src/views/PlayerView.vue:909-915`: global keydown only toggles diagnostics on Ctrl/Cmd+Shift+D.
- `player/src/components/player/PlayerControls.vue:281-293`: controls prevent only Escape handling when menus are open.

This means the local code does not show a JavaScript wheel preventer that directly cancels the diagnostics panel scroll.

#### Overlay z-index and hit-testing context

The diagnostics panel is `z-30` inside `VideoPlayer.vue` (`player/src/components/player/VideoPlayer.vue:282-288`). Other player overlays include:

- Top/bottom hover strips: `player/src/views/PlayerView.vue:1025-1038`, `z-5`.
- Top chrome gradient: `player/src/views/PlayerView.vue:1040-1045`, `z-10` and `pointer-events-none`.
- Bottom controls: `player/src/views/PlayerView.vue:1071-1078`, `z-20`.
- Floating global controls: `player/src/components/layout/FloatingControls.vue:73-80`, fixed `z-50` at bottom-right.
- Window chrome: `player/src/components/layout/WindowChrome.vue:72-83`, fixed top overlay; CSS sets `.window-chrome { z-index: 1000; }` at `player/src/components/layout/WindowChrome.vue:158-161`.

The panel is above in-player chrome, but below global window chrome. The invisible drag region covers the top 4rem (`h-16`), while the diagnostics panel starts at `top-16`. This exact boundary leaves little margin for DPI/subpixel rounding in the Windows transparent overlay. It is not enough by itself to explain all vertical scroll failure, but it is a brittle placement for a debug panel that may need reliable pointer/wheel interaction.

#### Windows transparent overlay model

The project intentionally uses a transparent Tauri/WebView overlay above a separate native mpv underlay:

- `player/src-tauri/tauri.conf.json:20-23`: the Tauri window is frameless and transparent, with transparent background color.
- `player/src-tauri/src/main.rs:89-94`: runtime `set_background_color(Some(Color(0, 0, 0, 0)))` is applied where supported.
- `player/src-tauri/src/mpv/platform/windows.rs:1-14`: comments describe the native underlay and transparent WebView overlay model.
- `player/src-tauri/src/mpv/platform/windows.rs:149-162`: `SetWindowPos(hwnd, owner, ...)` places the mpv HWND directly behind the Tauri overlay.
- `player/src-tauri/src/mpv/platform/windows.rs:581-584`: the mpv window is a borderless `WS_POPUP`, `WS_EX_NOACTIVATE`, `WS_EX_TOOLWINDOW`, intentionally not an owned top-level window.

Recent local Windows render logs at `/mnt/c/Users/VibeCoder/AppData/Local/com.ohmycine.player/logs/render-diagnostics.log` show `scale=1.50` and repeated underlay bounds syncs. The log supports that the Windows overlay path is active; it does not show evidence that mpv is above the WebView overlay. Raw native handles are not reproduced here.

### Likely Cause

The strongest local-code explanation is not a missing `overflow-y-auto` rule and not a discovered wheel-preventing handler. The fragile part is the DOM placement and sizing contract:

1. The diagnostics panel is an absolutely positioned scroll container inside the video surface root.
2. The video surface root, player view, app window, and document are all clipped with `overflow-hidden` during playback.
3. The panel relies on `max-height: calc(100vh - 6rem)` rather than explicit `top` + `bottom` insets or an inner scroll container.
4. It sits directly at the bottom edge of the fixed Tauri drag strip (`top-16` vs `WindowChrome` `h-16`) and below the global `z-index:1000` window chrome layer.

In a Windows Tauri transparent overlay, this makes scrolling sensitive to viewport/client-area differences, DPI rounding, and hit-test overlap. If the panel’s computed `max-height` is larger than the portion actually available inside the clipped player route, the ancestor clips the overflow and the panel itself may not expose a usable scroll range where the user expects it. If wheel/pointer events bubble out of the panel, they also still reach the player root chrome-reveal handlers, even though those handlers do not currently prevent scrolling.

### Concrete Implementation Recommendations

#### Preferred robust fix: fixed outer shell + inner scroll body

Move the diagnostics panel out of the clipped video surface contract, or at least make it a viewport-fixed overlay with explicit insets and an inner scroll region.

Recommended shape:

- Render diagnostics from `PlayerView.vue` as a sibling overlay after `<VideoPlayer />`, or use Vue `<Teleport to="body">` from `VideoPlayer.vue`.
- Use a fixed outer shell with explicit viewport insets instead of `absolute top-16 max-h-[calc(100vh-6rem)]`:
  - `fixed left-5 top-20 bottom-6 z-[1100]`
  - `w-[min(44rem,calc(100vw-2.5rem))]`
  - `min-h-0 overflow-hidden`
  - `flex flex-col`
- Put diagnostics content inside a child scroll container:
  - header/buttons stay in a non-scrolling flex header.
  - body uses `min-h-0 flex-1 overflow-y-auto overscroll-contain cinema-scrollbar pr-2`.

Why this is robust:

- `top` + `bottom` insets define the actual visible area; the browser computes the scroll body from the available viewport rather than a viewport max-height inside several clipped ancestors.
- `min-h-0` prevents flex children from refusing to shrink, a common reason inner scroll regions fail.
- `z-[1100]` places the diagnostics overlay above the fixed `WindowChrome` layer (`z-index:1000`) if necessary; using `top-20` keeps it visually below the chrome.
- `overflow-hidden` belongs only to the outer glass shell; actual vertical scrolling belongs to the inner body.

#### Event isolation for transparent overlay reliability

Add event isolation on the diagnostics shell/body:

- `@wheel.stop` on the scroll body or shell.
- `@pointerdown.stop`, `@mousedown.stop`, `@touchstart.stop`, `@mousemove.stop` on the shell.
- Do not use `@wheel.prevent` unless implementing manual scrolling; preventing the wheel event can disable native scrolling.
- Optional: focus the panel or scroll body when opened and set `tabindex="0"` so keyboard scrolling/PageDown can work.

This is defensive. Local code does not currently cancel wheel events, but stopping propagation keeps future player-level wheel/shortcut handlers and chrome-reveal logic from interfering with the diagnostic surface.

#### If keeping the panel inside `VideoPlayer.vue`

If moving/teleporting is too much for the current fix, still avoid viewport max-height on the panel itself:

- Change the shell from `absolute left-5 top-16 max-h-[calc(100vh-6rem)] overflow-y-auto` to an explicit inset shell such as `absolute left-5 top-20 bottom-6 z-30 flex min-h-0 w-[min(44rem,calc(100%-2.5rem))] overflow-hidden`.
- Move `overflow-y-auto overscroll-contain cinema-scrollbar` to an inner content element with `min-h-0 flex-1`.
- Add the same `@wheel.stop` / pointer event isolation.

This keeps the panel within the player surface, but it still remains below global `WindowChrome` and inside the clipped player route. The fixed/teleport approach is safer for the Windows transparent overlay.

#### Validation notes

After implementation, verify in Windows Tauri app, not only browser dev server:

- Open diagnostics with Ctrl+Shift+D during active mpv playback.
- Confirm mouse wheel and touchpad two-finger vertical scroll work when the pointer is over the panel body.
- Confirm dragging/selecting/clicking inside diagnostics does not trigger window dragging, hide/reveal glitches, or mpv underlay focus changes.
- Confirm panel does not cover or conflict with the fixed window controls unless intentionally placed above them.
- Test at 150% Windows display scale because local logs show `scale=1.50`.

### External References

No live external web-search tool was available in this agent runtime. Relevant platform rules used for interpretation:

- CSS scrolling requires the element to have overflow plus a constrained block size; otherwise a clipped ancestor can hide overflow without making the child itself scroll.
- `overscroll-behavior: contain` controls scroll chaining; it does not make an element scrollable by itself.
- Wheel events should generally not be prevented on native scroll containers unless manually applying scroll deltas.
- Tauri/WebView2 transparent-window setups require both native window/WebView transparency and transparent DOM backgrounds for native underlays to show through; this is already captured in the local project spec and implementation.

### Related Specs

- `.trellis/spec/frontend/type-safety.md:430-431` — Windows transparent overlay requires native Tauri/WebView transparency; CSS-only transparency is insufficient.
- `.trellis/spec/frontend/type-safety.md:453` — active playback should keep the video area transparent/full-bleed except hover-revealed controls.
- `.trellis/spec/frontend/type-safety.md:468` — Windows runtime review must verify WebView/Vue overlay hit-testing.
- `.trellis/spec/frontend/component-guidelines.md:73` — decorative overlays should not intercept pointer events unless they contain real controls.
- `.trellis/spec/frontend/component-guidelines.md:119` — render diagnostics must remain behind explicit debug shortcuts/panels.
- `.trellis/spec/frontend/component-guidelines.md:123-129` — immersive player chrome should be hover-revealed and not obstruct active video.

## Caveats / Not Found

- No local code path was found that explicitly cancels wheel events for the diagnostics panel.
- Built CSS contains the expected max-height, overflow-y, and overscroll utilities, so the issue is unlikely to be missing UnoCSS output.
- The exact Windows UI failure could not be reproduced from this Linux/WSL agent environment. Findings are based on local source, generated CSS, and available Windows render diagnostics logs.
- The Windows render log confirms the transparent-overlay path and 150% scale behavior, but it does not log WebView DOM wheel/hit-test events; runtime DevTools or temporary event logging would be needed to prove the event target during failed scroll attempts.
