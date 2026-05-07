# Frontend Quality Guidelines

> Code quality standards for OhMyCine frontend development.

---

## Overview

Frontend quality is measured by Player independence, type safety, immersive UI consistency, secure credential handling, and passing local checks. Do not rewrite existing Player work just to match a planned architecture; adopt the current state and improve incrementally through Trellis tasks.

---

## Required Patterns

- Vue 3 Composition API with `<script setup>`.
- TypeScript strict mode.
- Pinia for shared state.
- Vue Router for route-level navigation.
- Vue I18n for user-facing text where configured.
- UnoCSS + CSS variables for styling.
- DataSourceManager/DataSource abstractions for all media source browsing/playback.
- libmpv integration through Rust/Tauri modules, keeping platform rendering explicit.
- Secure storage for credentials when available.
- Disabled/placeholder states for Server-dependent UI when Server is disconnected.

---

## Forbidden Patterns

- Making Server required for basic local/Emby/Jellyfin/OpenList/Alist/CloudDrive2 playback.
- Storing API keys, cookies, passwords, AI keys, or Server tokens in regular config/localStorage/Pinia persistence.
- Sending local absolute paths or credentials to AI providers by default.
- Hard-coding `Alist` user-facing support without acknowledging OpenList/Alist compatibility.
- Assuming Android/iOS parity with desktop before implementation.
- Adding persistent controls that obstruct artwork/video when hover-revealed chrome is more appropriate.
- Using broad `any` for core media/config/API types.
- Rewriting current Player modules during Trellis migration tasks.

---

## Testing and Verification

When `player/` exists, run from WSL/Linux unless the task explicitly targets Windows-native packaging/runtime:

```bash
cd player
npm run typecheck
npm run lint
npm run build
```

For Tauri/Rust changes, also run the relevant Cargo command when configured, such as `cargo check` from `player/src-tauri`.

Do not treat Docker as a local development prerequisite.

---

## Code Review Checklist

- Components use `<script setup lang="ts">` and explicit props/events.
- Views do not bypass DataSource abstractions.
- Server disconnected state is handled.
- Credentials are stored securely and not exposed in logs/config/export.
- External requests have error handling and timeouts where service code controls them.
- Player-side AI only uses allowed metadata by default.
- Cinema OS tokens/classes are used instead of arbitrary styling.
- Keyboard shortcuts avoid input focus conflicts.
- Roadmap status is updated when implementation completion changes.

---

## Documentation Consistency

- Use `OpenList/Alist` or `OpenList (Alist-compatible API)` in docs and UI copy.
- Preserve Player independent-first wording.
- Keep Server as enhancement/automation layer.
- Clarify README quick-start or architecture examples as target design if files/features are not yet implemented.
- Keep final planned scope documented; adjust order, not scope.