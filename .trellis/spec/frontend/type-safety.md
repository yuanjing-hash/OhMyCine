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
- Internal outputs: map only to `MediaLibrary`, `MediaItem`, `HomeSection`, `MediaDetail`, `SubtitleTrack`, and `AudioTrack`.

#### 3. Contracts
- Library endpoint maps Emby views/collection folders to `MediaLibrary[]`.
- Item endpoints map `Movie`, `Series`, `Episode`, `Folder`, and collection folders to shared item types; unknown types become a safe folder/unknown fallback or are skipped.
- Poster/backdrop/logo URLs may be tokenized and must be treated as sensitive strings.
- Runtime ticks, ratings, dates, media streams, people, and provider IDs are optional and must not require non-null assertions.
- Stream URL generation must return a string for the playback layer, but UI labels/errors must display a redacted representation.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| Emby response is not an object/array where expected | Return safe empty state or user-safe invalid-response error |
| Item lacks `Id` or `Name` | Skip the item or use a clear fallback; do not crash rendering |
| Item lacks image tags | Render missing-poster fallback in media components |
| `RunTimeTicks`/ratings/year are missing or malformed | Omit the derived field rather than forcing `0` as real data |
| Stream URL contains `api_key`, token, or signed params | Pass to playback only; redact in display/errors/logs |

#### 5. Good/Base/Bad Cases
- Good: mapper reads optional fields defensively and returns a complete `MediaItem` with fallbacks.
- Base: direct-play stream URLs are supported while advanced PlaybackInfo/transcoding remains future work.
- Bad: `const item = response.Items[0] as any` followed by unconditional `item.ImageTags.Primary` access.

#### 6. Tests Required
- Typecheck must catch missing mapped fields.
- Lint must pass without broad `any` in provider mappers.
- Manual/code review must verify missing poster, empty library, auth failure, and token redaction paths.

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