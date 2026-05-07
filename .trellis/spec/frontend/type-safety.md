# Frontend Type Safety

> TypeScript patterns for OhMyCine Player and frontend code.

---

## Overview

Use TypeScript strict mode. Shared interfaces define the contracts between views, components, DataSources, scraper services, AI services, and Tauri commands.

---

## Type Organization

- DataSource contracts live under `services/datasource/types.ts` or equivalent.
- Component-local types can stay in component files when not reused.
- Shared UI/domain types should be exported from service/store modules.
- Avoid duplicating backend response types by hand once OpenAPI/generated types exist.

---

## Core DataSource Types

Keep all media sources behind a common interface with these concepts:

- `MediaItem`: common list/search item fields, including `sourceId`, `type`, title/name, poster/backdrop, path, duration/size/modified.
- `MediaLibrary`: source library/folder grouping.
- `HomeSection`: hero, continue watching, recently added, recommended, library row.
- `MediaDetail`: extended media metadata, genres, people, IDs, resolution/codec, tracks.
- `SubtitleTrack` and `AudioTrack`.
- `DataSourceConfig`: id, type, display data, order, URL, credential references/extra config.
- `DataSource`: lifecycle, list/search/detail/stream URL, optional home/library methods, config export.

`DataSourceType` should include `emby`, `jellyfin`, `alist` (code identifier for OpenList/Alist compatibility), `clouddrive2`, `server`, `115`, `123`, and `quark` as planned types.

---

## Validation

- Validate external API responses before mapping when practical.
- Treat `unknown` responses from fetch/Tauri as untrusted until parsed.
- Use type guards or schema validation for imported config files.
- Validate DataSource type before constructing a source.
- Do not trust file metadata or filenames to be complete.

---

## Tauri Command Types

- Keep Tauri command argument/return types explicit at call sites.
- Convert command errors to typed user-safe errors in composables/services.
- For Rust command changes, update frontend types and usages together.

---

## Common Patterns

- Use discriminated unions for media item types and source types where helpful.
- Use `Readonly`/readonly arrays for values not intended to mutate.
- Use `Partial<T>` only for explicit update/draft objects, not as a substitute for incomplete modeling.
- Prefer `const` literal arrays plus derived union types for known options.

---

## Forbidden Patterns

- Broad `any` for external responses, config, or DataSource items.
- Type assertions that hide missing validation.
- Optional credentials leaking through normal config types without secure-storage references.
- Hard-coding only one DataSource type in views/components.
- Treating plugin/AI/server response data as trusted without validation.