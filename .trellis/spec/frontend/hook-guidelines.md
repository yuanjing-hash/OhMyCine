# Frontend Composable Guidelines

> How Composition API composables are used in OhMyCine.

---

## Overview

Reusable stateful logic belongs in composables under `player/src/composables/`. Composables should expose small reactive APIs and hide Tauri event wiring, keyboard listeners, and client calls from components.

---

## Required Composables

Planned reusable composables include:

- `useMpv`: libmpv load/play/pause/seek/volume/tracks and event subscriptions.
- `useServer`: optional Server API connection and enhancement endpoints.
- `useMedia`: media browsing/play actions that remain DataSource-driven.
- `useTheme`: theme and Cinema OS presentation settings.
- `useKeyboard`: global/local shortcuts.

Add new composables only when logic is reused or complex enough to keep components readable.

---

## Custom Composable Patterns

- Names must start with `use`.
- Return refs/computed/functions explicitly.
- Do not create hidden global mutable state unless documented and intended.
- Accept dependencies as parameters when that improves testability.
- Clean up event listeners, intervals, and Tauri `listen` subscriptions using `onUnmounted` or explicit dispose functions.

---

## Data Fetching

- DataSource browsing/search goes through DataSource implementations and DataSourceManager, not direct component fetches.
- Server API calls go through `useServer` or a Server client service.
- External APIs such as TMDB/AI should use services with timeout/error handling and no credential logging.
- Fetch results should expose loading/error states for components.

---

## Tauri IPC

- Call `invoke` from composables/services, not scattered across templates.
- Tauri command errors should be converted to user-safe messages.
- Do not pass secrets through IPC unless the command is explicitly designed for secure credential storage.
- Keep platform-specific assumptions explicit.

### Local File Picker Playback Contract

Use Tauri dialog APIs for Player local video selection because the libmpv backend needs a native filesystem path, not a browser-only `File` blob.

#### 1. Scope / Trigger
- Trigger: adding or changing local file open buttons, drag/drop playback routing, or Tauri dialog permissions in Player.
- Applies to the floating play entry, Player route query handling, Tauri plugin registration, and capability files.

#### 2. Signatures
- Frontend file picker call: `open({ multiple: false, directory: false, filters: [{ name: 'Video', extensions: [...] }] })`.
- Route contract: navigate to `/player` with query `{ path: string, title?: string }`.
- Playback contract: `PlayerView` watches `route.query.path` with `immediate: true` and calls `useMpv().load(path)` when it changes.
- Tauri capability: grant `dialog:allow-open` only for file-open behavior.

#### 3. Contracts
- Supported loose video extensions: `mp4`, `mkv`, `avi`, `mov`, `webm`, `m4v`, `flv`, `wmv`, `ts`, `m2ts`, `rmvb`, `mpg`, `mpeg`, `3gp`, `ogv`, `divx`, `vob`, `iso`.
- Cancelled selection returns without navigation, playback changes, or user-visible error.
- Selected local paths stay inside Player playback flow; do not send local absolute paths to AI providers or Server by default.
- Do not implement media-library import, cloud-drive selection, or Server file selection as part of local file picker playback.
- Reuse `useMpv().load(path)` and existing `/player` route behavior instead of duplicating mpv IPC calls in UI controls.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| Dialog returns `null` or no file | Stay on current route; do not call `router.push` or `load` |
| Dialog returns a string path | Navigate to `/player?path=<path>&title=<basename>` |
| User selects another file while already on `/player` | `PlayerView` reacts to query change and loads the new path |
| Dialog permission missing | Add the narrow `dialog:allow-open` capability, not broad unrelated dialog permissions |
| File extension is not in the filter | Native dialog should hide/disallow it; mpv load errors remain backend/runtime errors |

#### 5. Good/Base/Bad Cases
- Good: floating play button opens Tauri dialog, cancels cleanly, routes selected files to `/player`, and `PlayerView` watches query changes.
- Base: playback page drag/drop continues to call the same load path without being rewritten.
- Bad: using browser `<input type="file">` and passing a blob URL to libmpv, or loading only in `onMounted` so repeated same-page file selection does not work.

#### 6. Tests Required
- Typecheck that the dialog result is narrowed before route navigation.
- Manual or e2e check: cancel selection causes no route change.
- Manual or e2e check: selecting file from a non-player route opens `/player` and starts load.
- Manual or e2e check: selecting a second file while already on `/player` starts a new load.
- Run `npm run typecheck`, `npm run lint`, `npm run build`, and `cargo check` when the dialog plugin/capability changes.

#### 7. Wrong vs Correct

Wrong:
```ts
onMounted(() => {
  if (route.query.path)
    load(String(route.query.path))
})
```

Correct:
```ts
watch(
  () => route.query.path,
  (path) => {
    if (typeof path === 'string' && path)
      load(path)
  },
  { immediate: true },
)
```

### Android Native Playback Diagnostics Contract

#### 1. Scope / Trigger
- Trigger: changing Android libmpv initialization, SurfaceView lifecycle, native playback commands, renderer fallback, or Player playback diagnostics.
- Applies across Kotlin `MpvSurfaceHost`, the Tauri mobile plugin, Rust commands, `useMpv`, and `VideoPlayer`.

#### 2. Signatures
- Tauri command: `mpv_playback_diagnostics() -> MpvPlaybackDiagnostics`.
- Kotlin plugin command: `playbackDiagnostics` delegates to `MpvSurfaceHost.playbackDiagnostics()`.
- Frontend state: `useMpv().playbackDiagnostics: Ref<MpvPlaybackDiagnostics | null>`.

#### 3. Contracts
- Response fields: `state`, `lastEvent`, `lastError`, `fileLoaded`, `videoFormat`, `audioCodec`, `voConfigured`, `hardwareDecoder`, `videoOutput`, `videoOutputFallbackUsed`, and `logs`.
- `videoOutput` starts as `gpu-next`; `videoOutputFallbackUsed` becomes true only when native error logs caused a per-playback fallback to `gpu`.
- Observe at least `START_FILE`, `FILE_LOADED`, `END_FILE`, `VIDEO_RECONFIG`, `AUDIO_RECONFIG`, and `PLAYBACK_RESTART`.
- Diagnostic text must redact remote URLs, authorization values, cookies, API keys, tokens, signatures, and query credentials before crossing IPC.
- Diagnostics must never include the media path, request headers, provider credentials, signed playback URL, or cookies.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| Surface not attached | Keep render state initializing; do not issue load until the bounded readiness wait succeeds |
| `FILE_LOADED` received | Set `state=playing`, `fileLoaded=true`, and clear stale load errors |
| `END_FILE` before file load | Set `state=error` and expose a safe diagnostic reason |
| User or route requests stop | Report idle/stopped; do not misclassify the resulting `END_FILE` as a load error |
| `gpu-next` log explicitly reports GPU/VO failure during loading | Retry this playback with `gpu`, mark fallback used, and retain `gpu-next` as the next playback/default |
| Log contains URL/token/header material | Replace sensitive values before retaining or returning the line |

#### 5. Good/Base/Bad Cases
- Good: a failed Android load shows an in-app diagnostic action with safe state, codec/VO facts, and redacted mpv logs.
- Base: a successful `gpu-next` playback never uses fallback and reports `fileLoaded=true` plus configured video output.
- Bad: assuming every black screen is a `gpu-next` incompatibility, globally switching to `gpu`, or asking users to expose full logcat URLs and tokens.

#### 6. Tests Required
- Kotlin compilation asserts observer interfaces, event handling, SurfaceView lifecycle, and diagnostic serialization compile together.
- Static verification asserts `gpu-next` default, conditional `gpu` fallback, event coverage, and log sanitization.
- Frontend checks assert diagnostics are polled only for the Android backend and rendered behind an explicit debug/error surface.
- Run `npm run verify:android-playback`, `npm run typecheck`, `npm run lint`, Android Kotlin compilation, and the Android APK build.

#### 7. Wrong vs Correct

Wrong:
```kotlin
MPVLib.setOptionString("vo", "gpu") // globally downgrade based on an unverified black-screen report
```

Correct:
```kotlin
MPVLib.setOptionString("vo", "gpu-next")
if (nativeLogConfirmsGpuOutputFailure()) {
    MPVLib.setPropertyString("vo", "gpu")
    videoOutputFallbackUsed = true
}
```

---

## Keyboard Shortcuts

`useKeyboard` should centralize shortcuts such as:

- Space: play/pause.
- Arrow keys: seek/volume.
- `S`/`A`: subtitle/audio switching.
- `D` and Shift variants: danmaku controls.
- `F`, Escape, `P`: fullscreen/PiP/window behavior.
- Ctrl+F/Ctrl+Comma: search/settings.

Do not trigger playback shortcuts while users type in inputs/textareas/contenteditable elements.

---

## Common Mistakes

- Registering event listeners without cleanup.
- Letting `useServer` become required for local playback.
- Directly calling Emby/OpenList/Alist APIs in views instead of DataSources.
- Exposing raw API keys or credential values from composables.
- Creating a composable for one-line local component state.
