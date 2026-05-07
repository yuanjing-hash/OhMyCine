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