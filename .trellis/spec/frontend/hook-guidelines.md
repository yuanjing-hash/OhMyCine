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