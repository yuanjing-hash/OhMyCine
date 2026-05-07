# Frontend Directory Structure

> How OhMyCine Player frontend and Tauri code are organized.

---

## Overview

Player uses Tauri v2 + Vue 3 + TypeScript + UnoCSS. The web UI lives under `player/src/`; Rust/Tauri integration lives under `player/src-tauri/`. Keep desktop runtime concerns explicit and do not assume WSL-built artifacts are Windows-native desktop packages.

The repository is design-first. Use existing directories when present and do not create broad rewrites to match a planned tree unless the task explicitly asks for it.

---

## Target Directory Layout

```text
player/
├── src-tauri/
│   ├── src/
│   │   ├── commands/       # Tauri commands exposed to Vue
│   │   ├── mpv/            # libmpv FFI/player integration
│   │   └── render/         # platform rendering contexts
│   ├── Cargo.toml
│   └── tauri.conf.json
├── src/
│   ├── assets/             # fonts, images, icons
│   ├── components/
│   │   ├── ui/             # reusable Cinema OS UI primitives
│   │   ├── layout/         # app layout, sidebar, window chrome
│   │   ├── player/         # playback UI, controls, track menus
│   │   ├── media/          # cards, grids, hero, poster wall
│   │   └── common/         # search, settings, server status
│   ├── views/              # route-level pages
│   ├── stores/             # Pinia stores
│   ├── composables/        # reusable Composition API logic
│   ├── services/           # datasource, scraper, AI, sync clients
│   ├── router/             # Vue Router
│   ├── i18n/               # translations
│   ├── styles/             # CSS variables, glass, animations, global
│   └── utils/              # pure utilities
└── scripts/                # build/download helpers
```

---

## Module Organization

### Views

Views are route-level orchestration components. They may compose stores, services, and UI components, but should not contain reusable business logic that belongs in composables/services.

Important views:

- `HomeView`: aggregated home from all configured data sources.
- `SourceLibraryView`: one data source's library/file view.
- `PlayerView`: video surface and controls.
- `SettingsView`: local settings and data source management.
- Server-enhanced views such as downloads/discovery/follow must degrade when Server is disconnected.

### Components

- `components/ui`: generic primitives such as buttons, cards, dialogs, inputs, sliders.
- `components/layout`: shell, dynamic data-source sidebar, window chrome.
- `components/player`: playback controls, progress, subtitle/audio menus, playlist, danmaku overlay.
- `components/media`: MediaCard, MediaGrid, MediaRow, HeroCarousel, PosterWall, details.

### Services

- `services/datasource`: DataSource types, manager, credential helpers, safe error/redaction helpers, and Emby/Jellyfin/OpenList/Alist/CloudDrive2/local/server implementations.
- `services/scraper`: filename parser, TMDB, metadata DB/cache.
- `services/ai`: Player-side RAG recommendations using user-provided keys.
- `services/sync`: Player ↔ Server structural/full sync flows.
- `services/danmaku`: parsers, sources, renderer.

### DataSource Implementations

When implementing concrete sources such as Emby, keep provider-specific protocol logic under `services/datasource/` and expose it through a manager/factory rather than directly from views.

#### 1. Scope / Trigger
- Trigger: adding or changing a concrete media source (`emby`, `jellyfin`, `alist`, `clouddrive2`, `local`, `server`, or future cloud sources).
- Applies to source clients, DataSource classes, manager/factory wiring, settings forms, source library views, home aggregation, and playback URL routing.

#### 2. Signatures
- DataSource class: `class EmbyDataSource implements DataSource` or equivalent provider-specific implementation.
- Factory: `createDataSource(config: DataSourceConfig): DataSource` validates `config.type` before constructing.
- Manager: `DataSourceManager` owns instantiated sources and exposes source lookup/refresh methods by id.
- View boundary: `SettingsView`, `SourceLibraryView`, and `HomeView` call store/manager/DataSource methods, not provider HTTP endpoints.

#### 3. Contracts
- `init(config)` normalizes non-sensitive config, loads credentials through a credential reference when available, and does not log tokens.
- Emby add/edit flows authenticate with account/password through `/Users/AuthenticateByName` or equivalent, then store the returned access token behind `credentialRef`; normal Emby setup must not ask the user to manually paste an access token.
- `test()` returns connection/auth success without exposing raw provider errors or credentials.
- `listLibraries()` returns `MediaLibrary[]` for source-level library cards and should be fetched after successful add/login so the source is known usable before it appears as connected.
- `list(path?)`, `search(keyword)`, and `getDetail(id)` map provider responses into shared media types.
- `getStreamURL(id)` returns a playable URL for mpv/player loading and must be treated as sensitive when tokenized.
- Emby hierarchy browsing must preserve views/libraries at the root, direct children for libraries/folders, series → seasons, and season → episodes; only search/home/recent aggregation should use recursive queries by default.
- `exportConfig()` returns non-sensitive fields and credential references only.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| Unsupported `DataSourceConfig.type` | Reject construction with a user-safe unsupported-source error |
| Missing server URL or required source identifier | Fail `init`/`test` with a user-safe validation error |
| Missing credential for a source that requires auth | Show an auth-required state; do not create a fake connected source |
| Emby account/password auth fails | Do not persist the source; show a redacted, user-safe login failure |
| Emby auth succeeds but library fetch fails | Do not mark setup as complete unless the UI explicitly supports a connected-but-empty/error state |
| Provider API returns unexpected shape | Treat as invalid external data and show a safe error/empty state |
| Provider returns tokenized image/stream URLs | Redact tokens in UI/log/error output; pass real URL only to the component/service that needs it |
| Source is disabled | Manager must skip initialization and route views must show disabled state instead of browsing |
| Source is offline/auth fails | Keep local files and other sources usable; show source-specific error state |

#### 5. Good/Base/Bad Cases
- Good: `SourceLibraryView` asks the manager/store for the active source, then calls `listLibraries()`/`list()` and renders generic `MediaCard` data.
- Base: a first concrete source supports libraries/items/stream URLs while advanced playback negotiation remains future work.
- Bad: a route view imports an Emby HTTP client and builds `/Users/{id}/Items` URLs directly.

#### 6. Tests Required
- `npm run typecheck`, `npm run lint`, and `npm run build` pass after frontend source changes.
- For desktop package confidence, `npm run tauri:build:windows` must still produce the Windows executable/installer when Player packaging is in scope.
- Review Settings add/edit/test/remove, source sidebar appearance, library loading, media item loading, play routing, loading/empty/error states, and token redaction.

#### 7. Wrong vs Correct

Wrong:
```ts
// SourceLibraryView.vue
const items = await $fetch(`${config.url}/Users/${userId}/Items`, { query: { api_key: token } })
```

Correct:
```ts
const source = dataSourceStore.getSource(sourceId)
const items = await source.list(libraryId)
```

---

## Naming Conventions

- Vue components use PascalCase file names: `HeroCarousel.vue`.
- Composables use `useX.ts`: `useMpv.ts`, `useServer.ts`, `useKeyboard.ts`.
- Pinia stores use domain names: `player.ts`, `media.ts`, `server.ts`, `settings.ts`, `ui.ts`.
- DataSource classes use PascalCase and end with `DataSource`.
- User-facing text should say `OpenList/Alist` or `OpenList (Alist-compatible API)`; code may use `alist` identifiers for API compatibility.

---

## Tauri/Rust Boundaries

Tauri commands expose platform capabilities only:

- Player commands for libmpv control.
- File commands for safe local file access under allowed roots.
- System/window commands.
- Danmaku load/fetch commands when implemented.

Rust internals should use structured errors and return strings safe for frontend display. Keep libmpv integration in dedicated modules.

### libmpv FFI Contract

When wrapping libmpv in `player/src-tauri/src/mpv/`, keep the FFI surface small and explicit:

#### 1. Scope / Trigger
- Trigger: Rust code creates, owns, or controls a native `mpv_handle`.
- Applies to player lifecycle, command execution, property get/set, event forwarding, and future render context wiring.

#### 2. Signatures
- Owner type: `MpvPlayer { handle: *mut mpv_handle, ... }` or an equivalent single-owner wrapper.
- State sharing: expose the owner through `Arc<Mutex<MpvPlayer>>` or another explicit serialization primitive.
- Frontend boundary: Tauri commands return user-safe `Result<T, String>` and do not expose raw pointers.

#### 3. Contracts
- `mpv_create()` returning null is an initialization error.
- Native strings passed into libmpv are `CString`; interior NUL bytes must become command/property errors.
- `mpv_command()` argument arrays are null-terminated and all C strings outlive the call.
- Strings returned by libmpv, such as `mpv_get_property_string()`, are freed with `mpv_free()` exactly once.
- The owner releases the handle with `mpv_terminate_destroy()` in `Drop`.
- Only implement unsafe auto-trait promises that are required by the actual state container; prefer `Send` with mutex serialization and do not add manual `Sync` unless there is a proven concurrent access contract.
- Do not enable visible video output (`vo=gpu` or equivalent) unless a native window/surface/render context is actually bound. Without `wid`/native surface/`MpvRenderContext`, libmpv may create an uncontrolled external mpv window.
- Until embedded video rendering is implemented, initialize libmpv in a non-windowing mode such as `force-window=no`, `vo=null`, and `video=no`, and make UI/docs state that backend loading/control works while in-window video is pending.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| `mpv_create()` returns null | Return an initialization error before storing state |
| `mpv_initialize()` returns non-zero | Destroy/avoid leaking the handle and return a safe error string |
| Rust string contains interior NUL | Return an argument error; do not call libmpv |
| libmpv command/property call returns non-zero | Convert to a safe error string for Tauri |
| `vo=gpu` is set without `wid`/native surface/render context | Treat as a bug: either bind a real embedded render target or suppress video output so no external mpv window appears |
| UI loads media but embedded render target is not implemented | Show an honest in-app placeholder; do not claim video is rendered in-window |
| WSL/WSLg EGL/Mesa warnings during `tauri dev` | Record runtime verification as partial, not complete |

#### 5. Good/Base/Bad Cases
- Good: direct libmpv FFI wrapper owns the handle, frees returned strings, binds an explicit render target before enabling visible video, and is accessed through a mutex-backed Tauri state.
- Base: backend media loading/control works with visible video suppressed while embedded rendering is still pending and documented.
- Bad: high-level wrapper or global state hides the native handle lifecycle, or `vo=gpu` opens an external mpv window that OhMyCine cannot close/control.

#### 6. Tests Required
- Rust check/build must compile the Tauri crate after any libmpv wrapper change.
- Frontend typecheck/lint/build must still pass because Tauri command contracts are frontend-facing.
- `tauri dev` must be attempted for runtime/libmpv changes; assert whether the app launches fully or is only partially verified due to graphics environment limits.
- Manually verify that local file playback does not create an external mpv video window unless an explicit sidecar prototype was requested.

#### 7. Wrong vs Correct

Wrong:
```rust
unsafe impl Send for MpvPlayer {}
unsafe impl Sync for MpvPlayer {}
```

Correct:
```rust
unsafe impl Send for MpvPlayer {}
// Access is serialized by Arc<Mutex<MpvPlayer>>; do not promise Sync without a separate contract.
```

Wrong:
```rust
set_option("vo", "gpu")?;
set_option("osc", "no")?;
// No `wid`, native surface, or render context is bound.
```

Correct:
```rust
set_option("force-window", "no")?;
set_option("vo", "null")?;
set_option("video", "no")?;
// Enable visible video only after a real embedded render target exists.
```

### Windows GNU libmpv Build Contract

When cross-building Player for `x86_64-pc-windows-gnu` from WSL/Linux, treat libmpv as both a link-time and runtime dependency:

#### 1. Scope / Trigger
- Trigger: `npm run tauri:build:windows`, `tauri build --target x86_64-pc-windows-gnu`, or any change to libmpv setup/bundling.
- Applies to `player/scripts/setup-libmpv.mjs`, `player/src-tauri/build.rs`, `player/src-tauri/tauri.conf.json`, and `.gitignore`.

#### 2. Signatures
- Setup script command: `npm run setup:libmpv -- windows` installs Windows libmpv artifacts under `player/src-tauri/lib/`.
- Build target: `TARGET=x86_64-pc-windows-gnu` adds `player/src-tauri/lib` as a native link-search path.
- Bundle resource mapping: runtime DLLs are copied to the Windows app install root.

#### 3. Contracts
- Install `libmpv-2.dll` for Windows runtime loading.
- Install `libmpv.dll.a` for GNU link-time resolution of `-lmpv`.
- Do not bundle `libmpv.dll.a`; it is an import library, not a runtime file.
- Do not commit generated `libmpv.dll.a`, downloaded DLLs, installers, or `target/` outputs.
- Keep native Linux builds using system libmpv/pkg-config; only add the explicit link-search path for `x86_64-pc-windows-gnu`.
- WSL cross-build success proves executable/installer generation only; Windows installation, signing, launch, and playback require a Windows host.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| Linker says `cannot find -lmpv` | Ensure `libmpv.dll.a` exists in `player/src-tauri/lib/` and the Windows GNU target has that directory in `rustc-link-search` |
| `libmpv-2.dll` missing from bundle | Add it as a Tauri resource at the Windows app root |
| `libmpv.dll.a` appears in git status as tracked/addable | Ignore or remove it; regenerate via setup script instead of committing it |
| Native Linux `cargo check` starts using vendored Windows lib path | Scope link-search by `TARGET`, not unconditionally |
| WSL build produces NSIS installer | Mark cross-build as passed but keep Windows-native runtime/signing/playback unverified |

#### 5. Good/Base/Bad Cases
- Good: setup downloads `libmpv-2.dll` and `libmpv.dll.a`, build.rs exposes the lib directory only for Windows GNU, and Tauri bundles only runtime DLLs.
- Base: WSL cross-build creates `.exe` and NSIS installer while signing/runtime playback remain unverified.
- Bad: relying on Linux `libmpv.so` for a Windows GNU build, or committing generated import libraries/installer artifacts.

#### 6. Tests Required
- `npm run setup:libmpv -- windows` installs both `libmpv-2.dll` and `libmpv.dll.a`.
- `npm run tauri:build:windows` resolves `-lmpv` and produces the Windows executable/installer.
- `cargo check` without a Windows target still passes on Linux.
- Inspect git status to confirm generated DLL/import-library/target outputs are not staged.

#### 7. Wrong vs Correct

Wrong:
```rust
println!("cargo:rustc-link-search=native=lib");
```

Correct:
```rust
if std::env::var("TARGET").as_deref() == Ok("x86_64-pc-windows-gnu") {
    println!("cargo:rustc-link-search=native={}", lib_dir.display());
}
```

---

## Common Mistakes

- Putting data-source-specific logic directly in views/components instead of DataSource implementations.
- Treating ServerDataSource as mandatory.
- Writing secrets into `config.json` instead of secure storage.
- Replacing existing Player work during Trellis migration instead of adopting current state.
- Assuming mobile support is as mature as desktop without implementation proof.