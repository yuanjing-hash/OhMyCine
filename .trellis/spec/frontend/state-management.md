# Frontend State Management

> State management rules for OhMyCine Player.

---

## Overview

Use Pinia for shared state. Keep component-local UI state in components and reusable reactive logic in composables. Server state is optional enhancement state and must not block Player independent use.

---

## Store Domains

Planned stores:

- `player`: playback status, current media, time, duration, volume, tracks, fullscreen/PiP state.
- `media`: cached media lists, selected item/detail, playback history, continue watching.
- `server`: optional Server URL/session/status, sync status, enhanced feature availability.
- `settings`: UI, language, DataSource configs, AI provider config references.
- `ui`: layout, theme, sidebar state, modals/toasts.

---

## State Categories

### Local component state

Use component refs for transient UI state such as hover state, menu open state, form draft fields, and drag interactions.

### Global app state

Use Pinia for state needed across routes/components: configured data sources, playback status, settings, Server connection, theme.

### Server/external state

Use services and stores to cache external data, but source-of-truth remains the DataSource or Server. Always handle offline/disconnected states.

### URL state

Use Vue Router for route identity: current view, selected source ID, selected media ID, search query where appropriate.

---

## DataSource Configuration

- DataSource configs are ordered by `order` for the dynamic sidebar.
- User-facing display fields include name/displayName/iconUrl.
- Sensitive credentials should be stored through OS secure storage and referenced by `credentialRef` where possible.
- Ordinary config files must not contain API keys, cookies, passwords, AI keys, or server tokens in plaintext.
- Config import/export must redact sensitive values by default.

---

## Server Connection State

- Server disconnected is a normal state.
- Server-only/enhanced pages show disabled/placeholder states with clear guidance.
- Do not hide local files, Emby/Jellyfin, OpenList/Alist, or CloudDrive2 functionality because Server is unavailable.
- Config sync defaults to structural sync; full credential sync requires explicit confirmation.

---

## AI State

- AI recommendations are Player-side by default.
- AI API keys belong in secure storage, not Pinia persistence or regular config.
- RAG context should include only library metadata needed for recommendations and avoid local absolute paths/credentials by default.
- Recommendations must be constrained to media the user already has indexed.

---

## Common Mistakes

- Persisting secrets in Pinia/localStorage/config JSON.
- Promoting every component field to a global store.
- Treating Server API results as required for the home page.
- Reordering data sources without persisting the order.
- Automatically overwriting local credentials during sync.