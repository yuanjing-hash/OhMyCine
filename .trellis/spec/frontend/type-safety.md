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

OpenList/Alist API responses must be parsed from `unknown` envelopes. Treat `code !== 200 && code !== 0` as a provider failure, validate file records before mapping, normalize paths to rooted paths, strip trailing slashes except for `/`, and reject `.` / `..` path segments before constructing `/d{path}` stream URLs. When `DataSourceConfig.extra.rootPath` is set, normalize it as a rooted path, default missing values to `/`, scope `listLibraries()`/`list()`/`search()` to that root, and reject browse/detail/stream paths outside that root.

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
- Episode thumbnail mapping must prefer episode-owned 16:9 artwork before inherited series/season artwork: `ImageTags.Thumb` → own `BackdropImageTags[index]` → own `ImageTags.Primary` → parent thumb → parent backdrop. Non-episode poster/backdrop priority should remain stable unless a task explicitly changes it.
- Runtime ticks, ratings, dates, media streams, people, provider IDs, media source options, stills, collections, and similar items are optional and must not require non-null assertions.
- Detail-page media source options may expose neutral labels, container/codec/bitrate/resolution, and track metadata, but must not expose provider filesystem paths, STRM paths, credentials, or tokenized playback URLs.
- Source-home hero sections should choose backdrop-capable movie/series items across libraries where possible, not episode-only rows or a single narrow library subset.
- Latest media rows intended for source landing pages should use movie/series-level items or explicitly label episode-level rows; avoid episode spam in generic latest-video sections.
- Stream URL generation must return a string for the playback layer, but UI labels/errors must display a redacted representation.
- STRM/remote-provider playback must inspect provider playback metadata such as direct-play/direct-stream/transcoding URLs, Emby playback endpoints, plugin redirect endpoints, or safe remote media paths. Emby-exposed playback/redirect endpoints may be passed to mpv so it can follow HTTP 302, but they must not be displayed, logged, cached, or treated as user-facing final media URLs. Reject local filesystem paths, `.strm` paths, embedded-credential URLs, and non-HTTP(S) remote URLs.

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
| Emby media source points to `.strm` or another remote-provider indirection | Resolve through playback metadata to a playable URL or Emby/plug-in playback endpoint; otherwise show a safe unsupported/unresolved playback error |
| Emby/plugin returns a playback redirect endpoint such as `/api/v1/plugin/.../redirect_url` | Pass it to mpv only as a provider-exposed playback endpoint that may 302 to real media; never display/cache/log it as the final media URL |
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

### Player Preference Command Contract

- Non-sensitive Player preferences that must survive app restart should use Tauri app-data SQLite commands instead of browser `localStorage` when practical.
- Keep preference storage separate from credential storage unless the value is sensitive and belongs in the encrypted credential boundary.
- Frontend command calls must define explicit payload/return types near the composable/service that owns the behavior, for example `invoke<PlaybackSpeedPreference>('player_get_playback_speed_preference')` and a typed `{ speed: number }` payload for `player_set_playback_speed_preference`.
- Preference persistence failures should not expose internal database paths or native details; for convenience-only preferences, composables may fall back to in-memory session defaults without noisy user-facing errors.

### Provider Playback Progress Sync Contract

#### 1. Scope / Trigger
- Trigger: adding or changing DataSource provider-native playback progress sync, watched/completed sync, Player playback-history save hooks, provider progress metadata such as `mediaSourceId` / `playSessionId`, or native HTTP commands used for provider playback reporting.
- Applies to the shared `DataSource` interface, Player route/context progress payloads, Emby/Jellyfin provider implementations, Tauri playback-sync HTTP commands, local SQLite playback-history coordination, and token redaction boundaries.

#### 2. Signatures
- Shared event type:
```ts
type ProviderPlaybackProgressEvent = 'started' | 'progress' | 'paused' | 'resumed' | 'stopped' | 'completed'
```
- Shared input:
```ts
interface ProviderPlaybackProgressInput {
  itemId: string
  mediaSourceId?: string
  playSessionId?: string
  mediaType?: MediaItem['type']
  position: number
  duration?: number
  startPosition?: number
  isPaused: boolean
  completed: boolean
  event: ProviderPlaybackProgressEvent
  playbackRate?: number
}
```
- DataSource optional method:
```ts
interface DataSource {
  syncPlaybackProgress?: (progress: ProviderPlaybackProgressInput) => Promise<void>
}
```
- Tauri native playback POST command:
```rust
emby_post_playback_json(request: EmbyPlaybackJsonRequest) -> Result<EmbyPlaybackJsonResponse, String>
```
```ts
interface EmbyNativePlaybackJsonRequest {
  baseUrl: string
  path: string
  query?: Record<string, string | number | boolean | null>
  body?: EmbyNativeJsonValue
  token: string
  userId: string
  deviceId: string
  authMode: 'default' | 'official-compatible'
}
interface EmbyNativePlaybackJsonResponse {
  status: number
  body: unknown
}
```
- Emby-compatible endpoints:
  - `POST /Items/{Id}/PlaybackInfo` to negotiate playable media sources and, when the server supports the request shape, `PlaySessionId`.
  - `POST /Sessions/Playing` with `ItemId`, `MediaSourceId`, `PlaySessionId`, `PositionTicks`, `RunTimeTicks`, `PlaybackStartTimeTicks`, `CanSeek`, `IsPaused`, `PlayMethod`, `PlaybackRate`, `RepeatMode`, and `VolumeLevel`.
  - `POST /Sessions/Playing/Progress` with the same identity/session fields plus `EventName` such as `TimeUpdate`, `Pause`, or `Unpause`.
  - `POST /Sessions/Playing/Stopped` with `ItemId`, `MediaSourceId`, `PlaySessionId`, `PositionTicks`, and `RunTimeTicks`.
  - Optional completed marker: `POST /Users/{UserId}/PlayedItems/{Id}` after a completed event.

#### 3. Contracts
- Provider sync is optional. Player code must call `source.syncPlaybackProgress?.(...)` or equivalent optional checks; non-provider/local sources must not implement no-op shims solely to satisfy typing.
- Local Tauri SQLite playback history remains the primary persistence path. Provider sync is best-effort and must never block playback startup, route leave, queue switching, pause/resume, close cleanup, or local history save completion.
- Player payload positions, durations, and optional playback start/resume positions are seconds. Provider implementations convert to provider-native units internally; Emby uses ticks (`seconds * 10_000_000`).
- Remote-provider progress identity must use stable provider item/session fields (`itemId`, optional `mediaSourceId`, optional `playSessionId`) rather than tokenized playback URLs.
- Provider implementations must reuse their existing authenticated request helper and redaction path. Tokenized stream URLs, image URLs, credentials, filesystem paths, and provider redirect targets must not be used as progress identity, displayed in errors, logged, or persisted.
- Emby playback-sync POST traffic (`/Items/{Id}/PlaybackInfo` and `/Sessions/Playing*`) must go through the Tauri native HTTP command, not WebView `fetch`, because `POST + JSON + Emby auth headers` can be blocked by browser CORS/OPTIONS preflight before Emby returns an HTTP status. Generic browsing/home/detail requests can stay on the existing DataSource request path unless they hit the same boundary.
- The native Emby playback command must accept only `http`/`https` base URLs, reject base URL query/fragment/userinfo, reject unsafe playback paths/query-in-path, disable redirects, bound timeouts/body sizes, and return only short redacted error details.
- Emby sync should preserve or re-obtain `MediaSourceId` and `PlaySessionId` from `/Items/{id}/PlaybackInfo` when practical. Start with the provider's default Emby auth headers and safe request shapes; only try official-compatible `Authorization` / `X-MediaBrowser-*` headers after default-auth requests return media sources but still lack `PlaySessionId`.
- Emby `/Sessions/Playing*` reports require both `MediaSourceId` and `PlaySessionId` for current server compatibility. If `PlaySessionId` is missing, skip the session report and record a safe diagnostic instead of sending a known-bad payload that returns 400.
- Do not use legacy `/Users/{UserId}/PlayingItems*` routes as online playback-state fallback for Emby session reporting; source research showed similar clients use `/Sessions/Playing*`, and legacy routes can return 400 without updating active/cloud history.
- Completed provider sync may send a normal stopped/progress event plus provider-specific watched marking when supported. Failure to mark watched must not undo local completion.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| DataSource lacks `syncPlaybackProgress` | Skip provider sync silently; keep local history and playback working |
| position/duration is NaN, infinite, or negative | Skip provider sync; do not send malformed provider payloads |
| provider request fails/offline/401 | Swallow or convert to user-safe non-blocking state; do not interrupt playback |
| WebView `fetch` reports `<no response> Failed to fetch` for Emby playback POST | Route playback-sync POST through the Tauri native command instead of changing payloads blindly |
| native playback command receives non-http(s), userinfo, query/fragment base URL, or query/fragment embedded in path | Reject before sending the request; return a generic invalid endpoint error |
| native Emby response is huge or non-success | Bound response reads and return only status plus a short redacted body snippet; never expose tokens/UserId/hostnames |
| Emby `MediaSourceId` or `PlaySessionId` is missing | Reuse cached playback metadata or refetch playback info when practical; if `PlaySessionId` is still missing, skip `/Sessions/Playing*` and store a safe diagnostic instead of sending a 400-prone report |
| event is `paused` / `resumed` | Map to provider progress endpoint with an appropriate pause/unpause event where supported |
| event is `stopped` during route leave or queue switch | Fire provider sync without awaiting it in blocking cleanup paths |
| event is `completed` | Mark local history completed and optionally send provider watched/completed marker best-effort |
| tokenized URL appears in stream/artwork/provider error | Redact/drop before display, logging, storage, or provider progress identity construction |

#### 5. Good/Base/Bad Cases
- Good: Player saves local SQLite progress, then fire-and-forgets `source.syncPlaybackProgress({ itemId, mediaSourceId, playSessionId, position, duration, startPosition, isPaused, completed, event })`; Emby converts seconds to ticks and reports through native HTTP `/Sessions/Playing/Progress` only when `PlaySessionId` is available.
- Good: Emby `PlaybackInfo` fails in WebView with CORS-like `<no response> Failed to fetch`; the provider uses `invoke('emby_post_playback_json')` for playback POSTs and diagnostics show native HTTP status/network outcomes instead.
- Base: A local file or unsupported DataSource has no `syncPlaybackProgress`; local resume still works and no provider call is attempted.
- Base: Emby `PlaybackInfo` returns media sources but no `PlaySessionId`; playback continues from a stable Emby/static stream URL, provider sync records a redacted diagnostic, and known-bad session reports are skipped.
- Bad: Player awaits an Emby network progress request before route leave or queue switch, causing the UI to hang when Emby is slow or offline.
- Bad: Sending legacy `/Users/{UserId}/PlayingItems*` fallback reports after `/Sessions/Playing*` cannot obtain a `PlaySessionId`.
- Bad: Retrying WebView `fetch` payload variations after repeated `<no response> Failed to fetch` for playback POSTs instead of moving that boundary to native HTTP.
- Bad: Using `streamUrl` or a signed redirect URL as `itemId` / `mediaIdentity` / provider progress identity.

#### 6. Tests Required
- `npm run typecheck` must catch drift in `ProviderPlaybackProgressInput` and optional `DataSource.syncPlaybackProgress` calls.
- `npm run lint` must pass without broad provider-progress `any` payloads or unused queue/progress state.
- `npm run build` must pass after Player route/control integration.
- If native playback sync commands change, run `cargo check --manifest-path player/src-tauri/Cargo.toml` in addition to frontend checks.
- DataSource/provider work should run `npm run tauri:build:windows --prefix player` when Player packaging is in scope; Rust checks are required only if Rust/Tauri files changed.
- Manual Windows-host checks should verify Emby pause/resume/stop/completed progress appears on the Emby server when Provider sync diagnostics show `playSession=yes`; if `playSession=no`, diagnostics should explain the skipped session report without breaking playback or local continue-watching.

#### 7. Wrong vs Correct

Wrong:
```ts
await source.syncPlaybackProgress?.({
  itemId: streamUrl,
  position: currentTime.value,
  isPaused: false,
  completed: false,
  event: 'progress',
})
```

Correct:
```ts
void source.syncPlaybackProgress?.({
  itemId,
  mediaSourceId,
  playSessionId,
  position: currentTime.value,
  duration: duration.value,
  isPaused: !isPlaying.value,
  completed,
  event: completed ? 'completed' : 'progress',
})
```

Wrong:
```ts
try {
  await embyProgressSync()
} finally {
  await router.replace(nextEpisodeRoute)
}
```

Correct:
```ts
void embyProgressSync().catch(() => undefined)
await router.replace(nextEpisodeRoute)
```

Wrong:
```ts
await fetch(`${baseUrl}/Items/${itemId}/PlaybackInfo`, {
  method: 'POST',
  headers: authHeaders,
  body: JSON.stringify(body),
})
```

Correct:
```ts
await invoke('emby_post_playback_json', {
  request: { baseUrl, path: `/Items/${itemId}/PlaybackInfo`, query, body, token, userId, deviceId, authMode },
})
```

### Player Playback History Command Contract

#### 1. Scope / Trigger
- Trigger: adding or changing Player playback history, continue-watching rows, resume-position behavior, or Tauri commands that persist playback progress.
- Applies to Tauri app-data SQLite schema, Rust history commands, typed frontend `invoke` wrappers, Player route/query metadata used for identity, and local continue-watching home sections.

#### 2. Signatures
- Rust command: `player_upsert_playback_progress(app: AppHandle, progress: PlaybackProgressUpsert) -> Result<PlaybackHistoryEntry, String>`.
- Rust command: `player_get_playback_progress(app: AppHandle, identity: PlaybackProgressIdentity) -> Result<Option<PlaybackHistoryEntry>, String>`.
- Rust command: `player_list_continue_watching(app: AppHandle, limit: Option<u32>) -> Result<Vec<PlaybackHistoryEntry>, String>`.
- SQLite database: Tauri app-data `history/playback_history.sqlite`.
- SQLite table: `playback_history(identity_key, source_id, library_id, item_id, media_identity, title, stream_identity, media_type, poster_url, backdrop_url, position, duration, completed, progress_source, created_at, updated_at)`.
- TypeScript payload:
```ts
interface PlaybackProgressIdentity {
  sourceId: string
  mediaIdentity: string
}
interface PlaybackProgressUpsert extends PlaybackProgressIdentity {
  libraryId?: string
  itemId?: string
  title: string
  streamIdentity?: string
  mediaType?: MediaItem['type']
  posterUrl?: string
  backdropUrl?: string
  position: number
  duration?: number
  completed?: boolean
}
interface PlaybackHistoryEntry extends PlaybackProgressIdentity {
  libraryId?: string | null
  itemId?: string | null
  title: string
  streamIdentity?: string | null
  mediaType?: MediaItem['type'] | null
  posterUrl?: string | null
  backdropUrl?: string | null
  position: number
  duration?: number | null
  progress?: number | null
  updatedAt: number
  completed: boolean
  progressSource: 'local'
}
```

#### 3. Contracts
- Player playback history must use Tauri app-data SQLite, not browser `localStorage`, because it is saved Player state.
- Remote-provider progress identity should prefer stable `sourceId + itemId` / `sourceId + mediaIdentity` values instead of tokenized playback URLs. Local file/drop playback may use the local path as identity.
- `streamIdentity` is display/debug metadata only and must be redacted or replaced with a stable identity for remote sources; never persist raw tokenized stream URLs when a stable source/item identity exists.
- Frontend command calls must go through a typed service/composable with explicit `invoke<T>()` generics and silent/user-safe failure behavior; Player playback must continue if progress persistence fails.
- Progress saves should be throttled while playing and force-saved on pause, media switch, queue switch, route leave, unmount, and close/beforeunload where practical.
- Do not force-save a zero/trivial position on initial `isPlaying=true`; doing so can overwrite a valid previous resume position before the Player has a chance to seek.
- Resume should ignore trivial positions and completed/near-end media. Completed rows should be excluded from local continue-watching noise.
- Media detail pages should read local `player_get_playback_progress` for playable detail items and visible episode lists so the primary play action and episode actions can show `继续播放` when a resumable local row exists.
- Home continue-watching is an aggregate section. Local history rows should keep `progressSource: 'local'` for card subtitles/source labels, but the section title must not imply local-only content. If a local remote-provider row lacks safe persisted artwork, the home aggregation layer may temporarily enrich it from provider detail metadata without writing tokenized image URLs back to SQLite.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| app-data directory cannot be resolved/created | Return a generic user-safe persistence error; do not expose filesystem internals |
| SQLite open/init/upsert/query fails | Return a generic user-safe error; frontend treats history as unavailable and keeps playback working |
| `sourceId`/`itemId`/`mediaIdentity` is empty or contains control characters | Reject or skip the save/read without crashing playback |
| local path identity is longer than normal provider IDs | Allow a bounded longer identity than `sourceId`/`itemId`; do not truncate into collisions silently |
| position/duration is NaN, infinite, or negative | Reject/skip that save |
| position is below resume threshold | Do not resume and do not surface as meaningful continue-watching progress |
| position is near the end by remaining seconds or ratio | Mark completed and exclude from continue-watching |
| stream/artwork URL contains tokens or signed URL params | Redact or drop before storing/displaying; never show raw tokenized values in UI/logs |
| continue-watching source is unavailable | Do not pass stable identity strings such as `source:<id>:<item>` as playable URLs |

#### 5. Good/Base/Bad Cases
- Good: Player saves `sourceId='emby-main'`, `mediaIdentity='<episodeId>'`, `streamIdentity='source:emby-main:<episodeId>'`, position/duration, and local source marker; reopening that episode seeks to the saved position if it is not completed.
- Base: Local file playback saves the file path as identity, resumes after restart, and hides previous/next controls when no queue exists.
- Bad: Persisting `https://server/Videos/.../stream?api_key=...&X-Amz-Signature=...` as the history identity or displaying it on the home continue-watching card.

#### 6. Tests Required
- `npm run typecheck` verifies frontend command payload/response shapes and `MediaItem` progress fields.
- `npm run lint` verifies no unused progress state or broad `any` wrappers are introduced.
- `npm run build` verifies Home/Player route integration compiles.
- `cargo check --manifest-path player/src-tauri/Cargo.toml` verifies Rust command registration/schema code compiles.
- Windows package builds should run with `RUSTC="$(rustup which rustc)" npm run tauri:build:windows --prefix player` when Player packaging is in scope.
- Manual Windows runtime checks should cover pause save, close save, queue switch save, automatic resume, completed exclusion, and token redaction with a real remote source.

#### 7. Wrong vs Correct

Wrong:
```ts
await invoke('player_upsert_playback_progress', {
  progress: { sourceId, mediaIdentity: streamUrl, title, position },
})
```

Correct:
```ts
await invoke<PlaybackHistoryEntry>('player_upsert_playback_progress', {
  progress: {
    sourceId,
    itemId,
    mediaIdentity: itemId ?? localPath,
    streamIdentity: itemId ? `source:${sourceId}:${itemId}` : redactSensitiveText(localPath),
    title,
    position,
    duration,
  },
})
```

Wrong:
```ts
const path = continueWatchingItem.path
router.push({ name: 'player', query: { path } })
```

Correct:
```ts
const source = store.getSource(item.sourceId)
const path = source ? await source.getStreamURL(item.id) : item.path
router.push({ name: 'player', query: { path, sourceId: item.sourceId, itemId: item.id } })
```

### Player Render Surface Command Contract

When exposing libmpv render lifecycle to Vue, use small typed status/surface commands rather than leaking platform handles, native HWNDs, GL contexts, or raw render pointers.

#### 1. Scope / Trigger
- Trigger: adding or changing Player render-status commands, render-surface commands, render backend status fields, or frontend `useMpv` render-state handling.
- Applies to Rust `mpv_render_status`, `mpv_init_render_surface`, `mpv_update_render_surface_bounds`, and TypeScript `useMpv`/`VideoPlayer` consumers.
- This is a cross-layer contract: Vue reports logical surface bounds; Rust owns native video surface/windowing, z-order, and libmpv `wid` initialization lifetimes.

#### 2. Signatures
- Rust command: `mpv_render_status(state: State<'_, MpvState>) -> Result<MpvRenderState, String>`.
- Rust command: `mpv_init_render_surface(window: tauri::Window, state: State<'_, MpvState>) -> Result<MpvRenderState, String>`.
- Rust command: `mpv_update_render_surface_bounds(bounds: RenderSurfaceBounds, state: State<'_, MpvState>) -> Result<MpvRenderState, String>`.
- Rust payload:
```rust
#[serde(rename_all = "camelCase")]
struct RenderSurfaceBounds {
    x: f64,
    y: f64,
    width: f64,
    height: f64,
    scale_factor: f64,
}
```
- TypeScript shape:
```ts
type MpvRenderStatus = 'idle' | 'initializing' | 'ready' | 'unsupported' | 'error'
type MpvRenderBackend = 'windowsTransparentOverlay' | 'windowsOpenGl' | 'linuxFuture' | 'macosFuture' | 'mobileFuture' | 'unsupported'
type MpvZOrderStrategy = 'transparentOverlay' | 'ownedTopLevel' | 'bottomTransparentHole' | 'topDisabledFallback'
interface MpvRenderDiagnostics {
  ownerHwndAttached: boolean
  mpvHwndCreated: boolean
  mpvHwndShown: boolean
  overlayWindowTransparent: boolean
  webviewBackgroundTransparentApplied: boolean
  zOrderUnderlayApplied: boolean
  geometryFollowing: boolean
  taskbarIgnored: boolean
  fullscreenState: string
  lastSyncResult: string
  mpvWidAccepted: boolean
  mpvInitialized: boolean
}
interface MpvRenderState {
  status: MpvRenderStatus
  backend: MpvRenderBackend
  strategy: MpvZOrderStrategy
  message?: string | null
  diagnostics: MpvRenderDiagnostics
}
interface RenderSurfaceBounds {
  x: number
  y: number
  width: number
  height: number
  scaleFactor: number
}
```

#### 3. Contracts
- Frontend command calls must use explicit generics: `invoke<MpvRenderState>('mpv_render_status')`, `invoke<MpvRenderState>('mpv_init_render_surface')`, and `invoke<MpvRenderState>('mpv_update_render_surface_bounds', { bounds })`.
- Vue reports only viewport-relative logical bounds and device pixel ratio. Rust sanitizes and converts them to platform physical pixels; Vue must never receive or construct native handles.
- Windows embedded playback uses `windowsTransparentOverlay`: a transparent Tauri/WebView overlay above an owned mpv top-level HWND underlay initialized through `wid` + `vo=gpu-next` + `hwdec=auto-safe`.
- The Tauri window and WebView native background must both be transparent (`transparent: true`, transparent `backgroundColor`, and runtime `set_background_color` where supported); CSS-only transparency is not sufficient on WebView2.
- UI must treat `ready` as the only state that can imply visible render readiness.
- `ready` means the native video underlay and mpv `wid` initialization succeeded. It still does not prove Windows host runtime video presentation until a real desktop playback check is performed.
- `idle` means scaffold/backend exists but visible video is not active yet.
- `unsupported` means the current platform backend is planned/future, not removed from product scope.
- `error` messages must be user-safe and must not include tokenized media URLs, local absolute paths unless user-selected, raw pointers, HWND/HDC/HGLRC values, or GL/window handles.
- On Windows, Rust must keep libmpv handle ownership inside `MpvPlayer`; render/windowing modules may receive the raw handle only through internal mpv-module APIs and must destroy/hide the owned video HWND after libmpv teardown.
- Safety invariant: non-Windows or failed render initialization must keep visible video suppressed (`vo=null` / `video=no` or equivalent) so mpv cannot create an unmanaged external player window.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| command returns malformed data | Typecheck should fail if shape changes without updating frontend types |
| command rejects | `useMpv` sets `renderStatus = 'error'` and `renderError` to a safe display string |
| bounds contain `NaN`, infinity, negative size, or tiny scale | Rust sanitizes to finite coordinates, non-negative size, and a minimum safe scale |
| bounds update arrives before native surface init | Return the current render state without crashing or creating an external mpv window |
| native surface creation fails | Return `error` on Windows or `unsupported`/safe fallback on future platforms; do not leak native handles |
| WebView/native background transparency fails | Report diagnostics such as `webviewBackgroundTransparentApplied=false`; do not mark Windows runtime verification complete until the host shows video through the overlay |
| mpv underlay exists but player is black | Treat the full-screen overlay as opaque; remove player-route/root/video-surface black backgrounds and verify native WebView transparency |
| status is `unsupported` | `VideoPlayer` shows an explicit fallback and keeps drag/drop/control shell stable |
| status is `idle` | UI says render backend is being prepared/scaffolded; do not claim video is embedded |
| status is `ready` with no loaded media | Keep idle/no-media UI truthful; do not show a fake video placeholder |
| status is `ready` with loaded media | Keep the video area transparent/full-bleed except hover-revealed controls; playback controls still use existing mpv commands |

#### 5. Good/Base/Bad Cases
- Good: `useMpv` exposes `renderStatus`, `renderBackend`, `renderStrategy`, `renderDiagnostics`, `renderError`, `initializeRender`, and `updateRenderSurfaceBounds` while keeping existing `load`, pause, seek, and volume APIs stable.
- Good: `VideoPlayer` measures its native-surface host with `ResizeObserver`/`getBoundingClientRect()` and emits typed bounds; Rust owns all native resources.
- Good: active Windows playback keeps route/root/video-surface backgrounds transparent so the mpv underlay is visible, while fallback states use intentional dark placeholders.
- Base: render status is queried on Player mount, native surface is initialized once, and unsupported/error states display truthful placeholders.
- Bad: frontend infers platform support from `navigator.platform`, displays "video ready" when backend only reports scaffold/idle, keeps a centered placeholder over an active native video surface, or relies on CSS transparency without native WebView transparency.

#### 6. Tests Required
- `npm run typecheck` catches command response/interface drift.
- `npm run lint` passes without broad `any` or unused render state.
- `cargo check --manifest-path player/src-tauri/Cargo.toml` passes for host Rust changes.
- Windows render changes must also pass `RUSTC="$(rustup which rustc)" rustup run stable cargo check --manifest-path player/src-tauri/Cargo.toml --target x86_64-pc-windows-gnu` when the target is installed.
- Windows package changes must pass `RUSTC="$(rustup which rustc)" npm run tauri:build:windows --prefix player`.
- Manual Windows-host runtime review verifies local playback, Emby playback, no external mpv window, WebView/Vue overlay hit-testing, resize/maximize/fullscreen alignment, and close-during-playback cleanup.

#### 7. Wrong vs Correct

Wrong:
```ts
const state = await invoke('mpv_render_status') as any
if (state.backend) showVideoReady()
```

Correct:
```ts
const state = await invoke<MpvRenderState>('mpv_render_status')
renderStatus.value = state.status
```

Wrong:
```ts
await invoke('mpv_update_render_surface_bounds', { hwnd: nativeHandleFromBrowser })
```

Correct:
```ts
const rect = host.getBoundingClientRect()
await invoke<MpvRenderState>('mpv_update_render_surface_bounds', {
  bounds: {
    x: rect.left,
    y: rect.top,
    width: rect.width,
    height: rect.height,
    scaleFactor: window.devicePixelRatio || 1,
  },
})
```

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
