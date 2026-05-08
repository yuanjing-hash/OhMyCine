# Research: True libmpv render API code impact

- **Query**: Research the OhMyCine repo code impact for implementing the True libmpv render API route for task `/home/develop/development/Code/OhMyCine/.trellis/tasks/05-08-player-mvp`.
- **Scope**: internal
- **Date**: 2026-05-08

## Findings

### Files Found

| File Path | Description |
|---|---|
| `player/src-tauri/src/main.rs` | Tauri startup: creates and manages `MpvState`, registers mpv commands, starts event forwarder. |
| `player/src-tauri/src/mpv/player.rs` | Current raw `libmpv-sys` owner for `mpv_handle`; initializes libmpv in no-visible-video safety mode and exposes load/control/property methods. |
| `player/src-tauri/src/mpv/render.rs` | Current render placeholder: only `MpvRenderContext::new() -> Self`. This is the main landing point for true render API code. |
| `player/src-tauri/src/mpv/events.rs` | Polling-based event forwarder for `time-pos`, `duration`, and `pause` state. |
| `player/src-tauri/src/mpv/mod.rs` | Currently exports only `events` and `player`; does not export `render`. |
| `player/src-tauri/src/commands/player.rs` | Tauri command bridge for `mpv_load`, `mpv_pause`, `mpv_resume`, `mpv_seek`, `mpv_get_property`, `mpv_set_property`. |
| `player/src-tauri/Cargo.toml` | Rust dependencies: `tauri = 2`, `libmpv-sys = 3.1`, no graphics/window-handle/render-loop crates yet. |
| `player/src-tauri/build.rs` | Windows GNU link-search setup for `player/src-tauri/lib` when `TARGET == x86_64-pc-windows-gnu`. |
| `player/src-tauri/tauri.conf.json` | Current Tauri window is frameless and transparent; bundle resources include `libmpv-2.dll` and plugin wrapper libraries. |
| `player/package.json` | Player scripts and dependencies; includes `tauri-plugin-libmpv-api@0.3.2`, but true render API route would primarily affect custom Rust FFI path. |
| `player/src/composables/useMpv.ts` | Frontend mpv composable using Tauri invokes/events; public API consumed by `PlayerView` is stable and small. |
| `player/src/views/PlayerView.vue` | Route-level playback orchestration: reads `route.query.path`, calls `load(path)`, handles file drop, auto-hides chrome, passes simple props to `VideoPlayer`. |
| `player/src/components/player/VideoPlayer.vue` | Current in-app placeholder and drag/drop surface; should become render surface/status UI boundary. |
| `player/src/components/player/PlayerControls.vue` | Playback control overlay; emits pause/seek/volume and interaction state for auto-hide. |
| `player/src/router/index.ts` | `/player` route receives query-based playback entry from local/Emby flows. |
| `player/src/views/MediaDetailView.vue` | Emby/detail playback entry: calls `source.getStreamURL(target.id)` then routes to `/player?path=...`. |
| `player/src/services/datasource/types.ts` | `DataSource.getStreamURL(id): Promise<string>` contract used by playback route. |
| `player/src/services/datasource/emby.ts` | Emby stream URL resolution, including playback metadata, direct stream/play/transcode URLs, 302-capable endpoints, token handling, and static stream fallback. |
| `.trellis/tasks/05-08-player-mvp/prd.md` | Task PRD; user now chose True render API over plugin/wid, so code impact should target custom render modules rather than plugin adoption. |
| `.trellis/spec/frontend/directory-structure.md` | Tauri/Rust boundaries, libmpv FFI contract, Windows GNU libmpv build contract. |
| `.trellis/spec/frontend/component-guidelines.md` | Player component and immersive chrome contracts; placeholder must remain truthful until render succeeds. |
| `.trellis/spec/frontend/quality-guidelines.md` | Validation matrix for UI/Rust/runtime/render/Windows package changes. |
| `.trellis/spec/frontend/type-safety.md` | Tauri command type safety and DataSource/Emby stream URL contracts. |
| `.trellis/tasks/05-08-player-mvp/research/libmpv-embedding-approaches.md` | Existing comparison; marks true render API as high-ceiling but complex. |
| `.trellis/tasks/05-08-player-mvp/research/tauri-v2-webview-video.md` | Existing Tauri video composition research; true render API still requires native surface/render loop plus WebView composition strategy. |
| `.trellis/tasks/05-08-player-mvp/research/tauri-plugin-libmpv.md` | Existing plugin research; relevant mainly as contrast because user selected true render API, not plugin/wid. |

### Current mpv Lifecycle Map

#### Startup and state creation

- `player/src-tauri/src/main.rs:15` calls `mpv::player::create_state().expect("failed to initialize libmpv")` before building the Tauri app.
- `player/src-tauri/src/main.rs:17-31` stores that state with `.manage(mpv_state)` and registers command handlers.
- `player/src-tauri/src/main.rs:32-35` starts `mpv::events::start_event_forwarder(app.handle().clone())` in setup.
- `player/src-tauri/src/mpv/player.rs:238-240` returns `Arc<Mutex<MpvPlayer>>` from `create_state()`.
- `player/src-tauri/src/mpv/player.rs:33-54` constructs `MpvPlayer`, sets safety options, and calls `mpv_initialize`.

Current startup implication for true render API: mpv is initialized before any explicit render target exists. True render API work needs a lifecycle decision: either create a render-capable state during startup with platform renderer initialization later, or delay visible-video enabling until `mpv_init_render`/equivalent has a native surface and graphics context.

#### Current no-external-window safety guard

`MpvPlayer::new()` deliberately suppresses visible video:

```rust
player.set_option("force-window", "no")?;
player.set_option("vo", "null")?;
player.set_option("video", "no")?;
```

These are at `player/src-tauri/src/mpv/player.rs:45-47`, with explanatory comments at `player/src-tauri/src/mpv/player.rs:41-44`. True render API implementation must not simply remove these options. The equivalent invariant is: visible video output is enabled only after a real app-owned render context/surface is initialized; otherwise fallback remains non-windowing and no external mpv window is created.

#### Tauri command bridge

`player/src-tauri/src/commands/player.rs` exposes:

- `mpv_load(path, state)` at lines 5-9 → `MpvPlayer::load_file`.
- `mpv_pause(state)` at lines 11-15 → `MpvPlayer::pause`.
- `mpv_resume(state)` at lines 17-21 → `MpvPlayer::resume`.
- `mpv_seek(position, state)` at lines 23-27 → `MpvPlayer::seek`.
- `mpv_get_property(prop, state)` at lines 29-33 → `MpvPlayer::get_property`.
- `mpv_set_property(prop, value, state)` at lines 35-43 → `MpvPlayer::set_property`.

The command names are consumed directly by `useMpv.ts`. For render API, add render-specific commands only if needed; do not change these control command names unless frontend and all callers are updated together.

#### Backend control and property patterns

`player/src-tauri/src/mpv/player.rs` owns the raw `mpv_handle` as `ctx: *mut mpv_handle` at lines 17-19.

Current key methods:

- `load_file(&self, path: &str)` → `mpv_command(["loadfile", path, "replace"])`, lines 56-58.
- `pause()` / `resume()` set `pause` flag strings, lines 60-66.
- `seek(position)` calls `seek` absolute, lines 68-70.
- `set_property()` branches by property type for `pause`, numeric properties, `sid`/`aid`, and string fallback, lines 78-126.
- `get_property()` tries string, double, int64, flag, lines 128-142.
- returned libmpv strings are freed with `mpv_free`, lines 170-182.
- `Drop` releases `mpv_terminate_destroy`, lines 25-31.

This layer is a usable control owner for true render API. Impact is to expose the raw handle internally to render modules without leaking it to Tauri commands or frontend.

#### Event forwarding

`player/src-tauri/src/mpv/events.rs` uses a polling loop every 250ms:

- looks up `MpvState` with `app_handle.try_state::<MpvState>()`, lines 23-26.
- locks player and snapshots `(time_pos, duration, paused)`, lines 28-31.
- emits `mpv:time-update`, `mpv:duration-change`, `mpv:paused`, and `mpv:resumed`, lines 33-49.

True render API may need additional render-status events, but existing playback events should remain unchanged for `useMpv.ts` compatibility.

#### Frontend composable lifecycle

`player/src/composables/useMpv.ts`:

- subscribes to `mpv:time-update`, `mpv:duration-change`, `mpv:paused`, `mpv:resumed`, lines 25-38.
- `load(path)` invokes `mpv_load` and immediately sets `isPlaying.value = true`, lines 40-43.
- transport methods invoke `mpv_pause`, `mpv_resume`, `mpv_seek`, and `mpv_set_property`, lines 45-75.
- `stop()` pauses and sets `isPlaying = false`, lines 77-80.
- unsubscribes listeners on unmount, lines 82-86.

For render API, `useMpv` is the right place to add refs such as `renderStatus`, `renderError`, and `initializeRender()` while keeping existing return members and method names stable.

#### Player route and local file path flow

`player/src/views/PlayerView.vue`:

- reads route query `path` in a watcher at lines 81-93.
- sets `mediaPath`, `mediaTitle`, reveals chrome, and calls `await load(nextPath)` at lines 84-90.
- handles drag/drop via `handleFileDrop(path)` at lines 99-104 and calls `await load(path)`.
- passes only `isPlaying` and `hasMedia` to `VideoPlayer`, lines 127-131.
- keeps chrome visible when paused/no media/control interaction and auto-hides while playing, lines 32-54 and 95-97.

True render API should not alter `/player?path=...` routing or local file drop semantics. It should add render initialization around this flow without changing the path query contract.

#### Emby stream URL playback flow

`player/src/views/MediaDetailView.vue:150-179` implements playback:

- resolves the active `DataSource`, line 158.
- calls `const path = await source.getStreamURL(target.id)`, line 159.
- routes to `name: 'player'` with query `title`, `path`, `sourceId`, `itemId`, `mediaSourceId`, `audioIndex`, `subtitleIndex`, lines 160-171.

`player/src/services/datasource/types.ts:122` defines `getStreamURL: (id: string) => Promise<string>`.

`player/src/services/datasource/emby.ts:352-370` resolves stream URL by inspecting playback media sources and falling back to static stream URL. It can return tokenized Emby URLs and provider playback endpoints that mpv may follow. Render API work must keep passing the exact resolved URL to `mpv_load`; do not display, log, or cache the URL beyond existing behavior.

### Exact Files/Functions Requiring Changes for True Render API

#### Rust/Tauri backend

1. `player/src-tauri/src/mpv/render.rs`
   - Replace placeholder `MpvRenderContext` with the true render API ownership boundary.
   - Expected responsibility: wrap `mpv_render_context_create`, render params, update callback, frame render entrypoint, teardown.
   - Keep unsafe libmpv render calls inside this module or submodules.

2. `player/src-tauri/src/mpv/player.rs`
   - Add controlled access for render module to the `mpv_handle` without exposing raw pointers outside `mpv` internals.
   - Add state fields for render readiness if owned directly by `MpvPlayer`, e.g. `render: Option<MpvRenderContext>` or equivalent separate state.
   - Modify initialization so `video=no` / `vo=null` remains fallback until render init succeeds; visible rendering must not create a standalone mpv window.
   - Potentially add lifecycle methods such as `init_render(...)`, `render_status()`, `shutdown_render()`.

3. `player/src-tauri/src/mpv/mod.rs`
   - Export `render` and any new platform modules.
   - Keep module boundaries explicit; do not re-export raw platform internals to commands unnecessarily.

4. `player/src-tauri/src/mpv/events.rs`
   - Keep existing playback events unchanged.
   - Add render-status events only if frontend needs async notification, e.g. `mpv:render-ready`, `mpv:render-error`, `mpv:render-frame`/wake signal. Avoid mixing high-frequency frame callbacks with Tauri event emissions unless deliberately throttled.

5. `player/src-tauri/src/commands/player.rs`
   - Preserve existing commands.
   - Add render commands if lifecycle is frontend-triggered, e.g. initialize/query/shutdown render. Their returned errors must be user-safe strings.
   - If a render surface needs container bounds, add a command with explicit typed args rather than overloading `mpv_load`.

6. `player/src-tauri/src/main.rs`
   - Register new render commands in `tauri::generate_handler![...]`.
   - If render initialization needs `AppHandle`/window setup at `.setup(...)`, initialize there but keep failure non-fatal if the UI is expected to show fallback.
   - Avoid starting two mpv players.

7. `player/src-tauri/Cargo.toml`
   - True render API likely requires additional bindings beyond current `libmpv-sys` imports. At minimum, confirm `libmpv-sys = 3.1` exposes render API types/functions; otherwise add a narrow local FFI layer or compatible crate.
   - Platform/surface work may require crates such as raw-window-handle/window APIs/graphics bindings; keep them platform-scoped where possible.

8. `player/src-tauri/build.rs`
   - If Windows true render API introduces additional native libraries/import libraries, keep link-search target-scoped like current lines 9-14.
   - Do not break native Linux `cargo check` by unconditionally linking Windows libraries.

9. `player/src-tauri/tauri.conf.json`
   - Current `transparent: true` may remain useful for native-underlay strategies, but true render API requires a defined composition target. Any added resources for graphics/runtime DLLs should follow existing bundle resource pattern at lines 39-46.

#### Frontend

1. `player/src/composables/useMpv.ts`
   - Add render state refs while keeping current public control API stable: `isPlaying`, `currentTime`, `duration`, `volume`, `load`, `togglePause`, `seek`, `seekRelative`, `setVolume` should remain.
   - Recommended state shape: `renderStatus: 'idle' | 'initializing' | 'ready' | 'unsupported' | 'error'`, `renderError: string | null`, and `initializeRender(): Promise<void>`.
   - `load(path)` should continue invoking `mpv_load`, but if render is not ready it should either initialize first or let `PlayerView` initialize before load. It must not suppress local/Emby load if the backend intentionally supports audio/control fallback.

2. `player/src/views/PlayerView.vue`
   - Keep route query and file-drop API unchanged.
   - Use new render state from `useMpv()` and pass it down to `VideoPlayer`.
   - Ensure `shouldShowChrome` and auto-hide logic remain based on media/play/control/window state, not on render status alone, unless error/unsupported states must keep chrome visible.

3. `player/src/components/player/VideoPlayer.vue`
   - Replace the current static “视频内嵌渲染仍在接入中” placeholder with render-aware states.
   - Keep drag/drop surface behavior (`@dragover.prevent`, `@drop.prevent`, `fileDrop` emit) intact.
   - For true render API, this component should represent the visual render host/status boundary, not own DataSource or mpv command logic.
   - If bounds are needed, this is the place for a `ResizeObserver` that emits or calls a composable command with container dimensions.

4. `player/src/components/player/PlayerControls.vue`
   - Avoid changes unless render status needs disabling controls. Current events and interaction aggregation should stay unchanged.

5. `player/src/views/MediaDetailView.vue`, `player/src/services/datasource/emby.ts`, `player/src/services/datasource/types.ts`
   - No render-specific changes expected. Treat as regression-sensitive: Emby stream URL routing must continue to produce a string passed to `/player?path=...`.

### Proposed Rust Module Layout Under `player/src-tauri/src/mpv/`

Keep unsafe and platform-specific rendering explicit and contained:

```text
player/src-tauri/src/mpv/
├── mod.rs              # exports player, events, render; optional platform module gates
├── player.rs           # mpv_handle owner, command/property API, high-level render lifecycle hooks
├── events.rs           # existing playback event forwarder; optional render status events only
├── render.rs           # safe-ish public render context facade for mpv module
├── render_api.rs       # raw mpv_render_* FFI wrapper if libmpv-sys coverage is insufficient
├── surface.rs          # platform-neutral traits/types: RenderSurface, SurfaceSize, RenderBackendKind
└── platform/
    ├── mod.rs          # cfg dispatch only
    ├── windows.rs      # Windows HWND/D3D/ANGLE/OpenGL surface setup, cfg(windows)
    ├── linux.rs        # Linux/X11/Wayland explicit unsupported or future surface, cfg(target_os="linux")
    └── macos.rs        # macOS explicit unsupported or future NSView/Metal surface, cfg(target_os="macos")
```

Boundary recommendations:

- `player.rs` remains the only owner of `mpv_handle` lifecycle and `mpv_terminate_destroy`.
- `render.rs` owns `mpv_render_context` lifecycle and exposes small methods such as `new(...)`, `status()`, `render_frame(...)`, `resize(...)`, `destroy()`.
- `render_api.rs` contains raw `unsafe extern` declarations or `libmpv_sys` render imports, so unsafe render API usage is not scattered through commands/views.
- `surface.rs` defines data-only structs and traits used by `render.rs`; no Tauri command logic.
- `platform/windows.rs` is the first real backend if MVP is Windows-first. `linux.rs` and `macos.rs` should return explicit unsupported results rather than silent no-ops.
- `commands/player.rs` talks to high-level lifecycle methods only. It should not call raw `mpv_render_*` or platform APIs.

This layout follows `.trellis/spec/frontend/directory-structure.md:150-183`: Tauri commands expose platform capabilities, while libmpv integration stays in dedicated modules with a small FFI surface.

### Frontend Render State Recommendation

Keep `PlayerView` API stable and move render complexity into `useMpv` + `VideoPlayer` props.

Recommended `useMpv` public additions:

```ts
type MpvRenderStatus = 'idle' | 'initializing' | 'ready' | 'unsupported' | 'error'
```

Return additional refs/methods:

- `renderStatus`
- `renderError`
- `initializeRender`
- optional `isRenderReady = computed(() => renderStatus.value === 'ready')`

Recommended `VideoPlayer.vue` props:

- existing `isPlaying: boolean`
- existing `hasMedia: boolean`
- new `renderStatus: MpvRenderStatus`
- new `renderError?: string | null`

`PlayerView.vue` should continue to own route/file-drop orchestration. It can call `initializeRender()` on mount or before first `load(path)`, but should still pass the same `load(path)` workflow for local and Emby sources. If render initialization fails, `VideoPlayer` shows an explicit error/unsupported placeholder while the mpv safety guard prevents external windows.

### Regression-Sensitive Behavior That Must Remain Unchanged

1. Local file picker/drop route
   - `VideoPlayer.vue:11-16` reads `File.path` from a drag event and emits `fileDrop`.
   - `PlayerView.vue:99-104` updates `mediaPath`, derives a title, reveals chrome, and calls `load(path)`.
   - This flow should continue to work even if render initialization reports unsupported/error.

2. Query-based Player route
   - `router/index.ts:12-15` defines `/player`.
   - `PlayerView.vue:81-93` watches `route.query.path` and loads immediately.
   - Do not replace with provider-specific route state.

3. Emby stream URL playback
   - `MediaDetailView.vue:158-171` obtains `source.getStreamURL()` and passes the result as `query.path`.
   - `EmbyDataSource.getStreamURL()` at `emby.ts:352-370` handles direct stream/play/transcoding/static URLs.
   - True render API should not alter DataSource contracts or expose tokenized URLs in UI.

4. Controls auto-hide and liquid-glass overlay
   - `PlayerView.vue:32-54` computes and schedules chrome visibility.
   - `PlayerControls.vue:21-42` aggregates pointer/focus/child interaction state.
   - `component-guidelines.md:115-123` requires hover-revealed chrome and truthful placeholder when rendering is incomplete.

5. No external mpv window safety
   - `player.rs:41-47` intentionally suppresses external visible video today.
   - `directory-structure.md:181-183` and `component-guidelines.md:122` require not letting an uncontrolled external mpv window become the user-visible player.

6. Existing playback controls and events
   - `useMpv.ts` currently relies on `mpv:time-update`, `mpv:duration-change`, `mpv:paused`, `mpv:resumed`.
   - Keep these event names/payloads stable unless all callers are updated.

### Spec / Research Files for Trellis Context JSONL

Before `task.py start`, populate `implement.jsonl` and `check.jsonl` with spec/research files only, not code paths.

#### Suggested `implement.jsonl`

```jsonl
{"file":".trellis/tasks/05-08-player-mvp/prd.md","reason":"Task requirements, acceptance criteria, and explicit decision context for embedded rendering; update interpretation to True render API route."}
{"file":".trellis/tasks/05-08-player-mvp/research/true-render-api-code-impact.md","reason":"Repo-specific lifecycle, file impact, render module boundaries, regression-sensitive flows, and validation notes for implementation."}
{"file":".trellis/tasks/05-08-player-mvp/research/libmpv-embedding-approaches.md","reason":"Existing comparison that explains render API tradeoffs and how it differs from wid/plugin/overlay approaches."}
{"file":".trellis/tasks/05-08-player-mvp/research/tauri-v2-webview-video.md","reason":"Tauri WebView composition constraints relevant to native render surface plus UI overlay."}
{"file":".trellis/spec/frontend/directory-structure.md","reason":"Authoritative frontend/Tauri module boundaries, libmpv FFI contract, and Windows GNU libmpv packaging contract."}
{"file":".trellis/spec/frontend/component-guidelines.md","reason":"Player component contracts, immersive chrome behavior, drag/drop surface, and truthful render placeholder requirements."}
{"file":".trellis/spec/frontend/type-safety.md","reason":"Tauri invoke type safety and DataSource/Emby playback URL contracts that must remain stable."}
```

#### Suggested `check.jsonl`

```jsonl
{"file":".trellis/tasks/05-08-player-mvp/prd.md","reason":"Acceptance criteria for visible in-window playback, no external mpv window, failure fallback, and Windows package build."}
{"file":".trellis/tasks/05-08-player-mvp/research/true-render-api-code-impact.md","reason":"Checklist of exact repo flows and files that must not regress during validation."}
{"file":".trellis/spec/frontend/quality-guidelines.md","reason":"Required commands and runtime/render verification rules, including WSL partial-verification caveat."}
{"file":".trellis/spec/frontend/directory-structure.md","reason":"libmpv FFI safety, no external window invariant, Windows GNU link/bundle validation."}
{"file":".trellis/spec/frontend/component-guidelines.md","reason":"Player chrome auto-hide and render placeholder behavior to verify after implementation."}
{"file":".trellis/spec/frontend/type-safety.md","reason":"Typecheck and stream URL/redaction behavior for DataSource playback flow."}
```

### Validation Commands and Runtime Caveats

From `player/package.json` and `.trellis/spec/frontend/quality-guidelines.md`, run from WSL/Linux under `player/` unless explicitly doing Windows-native runtime checks:

```bash
cd player
npm run typecheck
npm run lint
npm run build
```

For Rust/Tauri backend/render changes:

```bash
cd player/src-tauri
cargo check
```

For runtime/render/libmpv changes, attempt:

```bash
cd player
npm run tauri dev
```

Caveat: `.trellis/spec/frontend/quality-guidelines.md:60-62` says WSL/WSLg graphics limitations can make this only partial verification when EGL/Mesa/DRI warnings or unreliable windows occur. Full runtime verification requires exercising the desktop window/rendering on a suitable host.

For Windows GNU package confidence when Player packaging/rendering is in scope:

```bash
cd player
npm run setup:libmpv -- windows
npm run tauri:build:windows
```

Caveat: `.trellis/spec/frontend/directory-structure.md:235-274` says Windows GNU cross-build success proves executable/installer generation only. Windows installation, signing, launch, and actual playback/render behavior still require a Windows host.

## Related Specs

- `.trellis/spec/frontend/directory-structure.md` — module boundaries, libmpv FFI lifecycle/safety, Windows GNU packaging.
- `.trellis/spec/frontend/component-guidelines.md` — Player component structure, hover-revealed chrome, render placeholder contract.
- `.trellis/spec/frontend/quality-guidelines.md` — required checks and runtime verification caveats.
- `.trellis/spec/frontend/type-safety.md` — Tauri command typing and DataSource/Emby URL behavior.
- `docs/architecture/03-player-design.md` — product-level direction for libmpv embedding and Player architecture.

## External References

No new external search was required for this repo-focused impact note. Existing task research already covers relevant external/plugin/rendering references:

- `.trellis/tasks/05-08-player-mvp/research/libmpv-embedding-approaches.md`
- `.trellis/tasks/05-08-player-mvp/research/tauri-v2-webview-video.md`
- `.trellis/tasks/05-08-player-mvp/research/tauri-plugin-libmpv.md`

## Caveats / Not Found

- The current repo has no implemented true render API code. `player/src-tauri/src/mpv/render.rs` is only a placeholder.
- `libmpv-sys = 3.1` render API symbol coverage was not confirmed by local package source inspection in this run; implementation should verify available `mpv_render_*` bindings before choosing between crate imports and a narrow local FFI shim.
- The exact graphics backend for true render API is not specified in current code. Windows-first implementation needs an explicit decision for OpenGL/ANGLE/D3D/native surface composition.
- `implement.jsonl` and `check.jsonl` currently contain only example placeholder lines and must be populated before task start.
