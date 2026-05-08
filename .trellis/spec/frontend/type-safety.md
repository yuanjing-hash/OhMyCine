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

### Emby DataSource Mapping Contract

When mapping Emby or Emby-compatible responses into Player types:

#### 1. Scope / Trigger
- Trigger: implementing or changing `emby` DataSource list/search/detail/home/stream behavior.
- Applies to Emby REST responses, image URL construction, stream URL construction, and DataSource item mapping.

#### 2. Signatures
- Config inputs: `DataSourceConfig` with `type: 'emby'`, `url`, display fields, and `extra.credentialRef` / `extra.userId` or equivalent non-sensitive fields.
- External fetch outputs: treat Emby JSON as `unknown` until minimally validated.
- Internal outputs: map only to `MediaLibrary`, `MediaItem`, `HomeSection`, `MediaDetail`, `MediaSourceOption`, `SubtitleTrack`, and `AudioTrack`.

#### 3. Contracts
- Source root maps Emby views/collection folders to `MediaLibrary[]`; do not flatten all media at the root.
- Primary library/folder browsing uses direct children (`Recursive=false`) by default so Emby categories and folders remain navigable.
- Series/anime navigation maps series to seasons, and seasons to episodes/direct children.
- Search, home, recently added, and continue-watching sections may use recursive queries where the UI explicitly asks for cross-library aggregation.
- Item endpoints map `Movie`, `Series`, `Episode`, `Season`, `Folder`, and collection folders to shared item types; unknown types become a safe folder/unknown fallback or are skipped. Season and episode mapping should preserve season/episode numbers where exposed (`ParentIndexNumber` for episode season, `IndexNumber` for episode number).
- Poster/backdrop/logo URLs may be tokenized and must be treated as sensitive strings; list/detail queries should request image metadata (`EnableImages`, `EnableImageTypes`, `ImageTypeLimit`, and parent image fields where supported), include image `tag` params, and request bounded image widths/quality instead of loading original-size artwork for grid cards.
- Runtime ticks, ratings, dates, media streams, people, provider IDs, media source options, stills, collections, and similar items are optional and must not require non-null assertions.
- Detail-page media source options may expose neutral labels, container/codec/bitrate/resolution, and track metadata, but must not expose provider filesystem paths, STRM paths, credentials, or tokenized playback URLs.
- Source-home hero sections should choose backdrop-capable movie/series items across libraries where possible, not episode-only rows or a single narrow library subset.
- Latest media rows intended for source landing pages should use movie/series-level items or explicitly label episode-level rows; avoid episode spam in generic latest-video sections.
- Stream URL generation must return a string for the playback layer, but UI labels/errors must display a redacted representation.
- STRM/remote-provider playback must inspect provider playback metadata such as direct-play/direct-stream/transcoding URLs or safe remote media paths; internal LAN/control-plane/plugin redirect URLs must not be treated as final playable URLs. If the provider does not expose a real playable URL, return a user-safe playback error instead of presenting an internal redirect or unplayable static `/Videos/{id}/stream` URL as success.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| Emby response is not an object/array where expected | Return safe empty state or user-safe invalid-response error |
| Item lacks `Id` or `Name` | Skip the item or use a clear fallback; do not crash rendering |
| Item lacks image tags | Render missing-poster fallback in media components |
| `RunTimeTicks`/ratings/year are missing or malformed | Omit the derived field rather than forcing `0` as real data |
| User opens a library such as 动漫 | Show direct category/folder/series children first; do not recursive-flatten by default |
| User opens a series detail page | Render season selection and episode list through DataSource `list(seriesId)` / `list(seasonId)`; do not show movie-only audio/subtitle/version panels at series level |
| User opens a season in source browsing | Treat it as a navigable container and load episodes before opening generic detail |
| Breadcrumb represents search results | Mark it non-navigable or route it through search logic; do not call `list('search')` accidentally |
| Stream URL contains `api_key`, token, or signed params | Pass to playback only; redact in display/errors/logs |
| Emby media source points to `.strm` or another remote-provider indirection | Resolve through playback metadata to the real playable URL when exposed; otherwise show a safe unsupported/unresolved playback error |
| Emby/plugin returns an internal LAN/control-plane redirect URL such as `/api/v1/plugin/.../redirect_url` | Do not pass it to mpv as final media; reject with a redacted user-safe unresolved-playback error unless the provider exposes the real playable URL |
| Latest source-home query returns every episode for a TV library | Use movie/series-level rows for generic latest sections, or label/render an explicit episode row separately |
| Detail metadata contains provider paths or raw media-source names that may include paths | Do not surface them as primary UI labels; use neutral version labels and safe codec/resolution/runtime fields |

#### 5. Good/Base/Bad Cases
- Good: mapper reads optional fields defensively, returns a complete `MediaItem` with fallbacks, and resolves STRM/remote playback through provider playback metadata before loading mpv.
- Base: direct-play stream URLs and visible detail metadata are supported while advanced user-selected transcoding remains future work.
- Bad: `const item = response.Items[0] as any` followed by unconditional `item.ImageTags.Primary` access, or showing `MediaSource.Path` in the detail page.

#### 6. Tests Required
- Typecheck must catch missing mapped fields.
- Lint must pass without broad `any` in provider mappers.
- Manual/code review must verify missing poster, empty library, auth failure, token redaction paths, detail-page safe metadata labels, and STRM/remote playback unresolved-error behavior.

#### 7. Wrong vs Correct

Wrong:
```ts
const posterUrl = `${baseUrl}/Items/${item.Id}/Images/Primary?api_key=${token}`
console.error('Failed to load', posterUrl)
```

Correct:
```ts
const posterUrl = buildImageUrl(item.Id, 'Primary')
console.error('Failed to load', redactSensitiveUrl(posterUrl))
```

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