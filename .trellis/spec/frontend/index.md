# Frontend Development Guidelines

> Long-lived implementation rules for OhMyCine Player, Hub UI, and TypeScript frontend code.

---

## Overview

OhMyCine Player is the primary user-facing app and must be independently useful without Server. Frontend work should prioritize the Player independent-first experience: local/remote playback, DataSource abstraction, Cinema OS UI, local metadata scraping, and Player-side AI recommendations.

Server-connected pages are optional enhancement surfaces and must show clear disabled/placeholder states when Server is not connected.

---

## Pre-Development Checklist

Before changing frontend code or Player design:

1. Confirm the feature does not make basic playback depend on Server.
2. Use Vue 3 Composition API with `<script setup>`.
3. Keep shared state in Pinia stores and reusable behavior in composables.
4. Keep all media sources behind the common DataSource interface.
5. Use UnoCSS and CSS variables for Cinema OS styling.
6. Store credentials in OS secure storage when available; do not write secrets to ordinary config files.
7. If feature completion status changes, update `docs/architecture/06-roadmap.md` in the same task.
8. Existing Player work is to be adopted as current state and continued, not rewritten during Trellis migration tasks.

---

## Guidelines Index

| Guide | Description | Status |
|-------|-------------|--------|
| [Directory Structure](./directory-structure.md) | Player/Tauri/Vue layout and module boundaries | Active |
| [Component Guidelines](./component-guidelines.md) | Vue component, layout, Cinema OS, accessibility rules | Active |
| [Composable Guidelines](./hook-guidelines.md) | `use*` composables, Tauri events, data fetching | Active |
| [State Management](./state-management.md) | Pinia stores, config, playback and server state | Active |
| [Type Safety](./type-safety.md) | TypeScript, DataSource types, runtime validation | Active |
| [Quality Guidelines](./quality-guidelines.md) | Lint/typecheck/build and forbidden patterns | Active |

---

## Quality Check

A frontend change is not complete until the relevant commands pass when the component exists:

- `npm run typecheck`
- `npm run lint`
- `npm run build`

For Rust/Tauri backend changes, also run the relevant Cargo checks when configured.

---

**Language**: Trellis spec files are written in English. Product-facing architecture docs may remain Chinese.