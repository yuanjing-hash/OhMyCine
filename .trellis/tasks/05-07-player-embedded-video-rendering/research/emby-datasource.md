# Research: Emby DataSource for OhMyCine Player

- **Query**: Research Emby as a Player DataSource for OhMyCine. Cover authentication/config fields, common endpoints for libraries/items/images/stream URLs, generic DataSource mapping, security concerns for API keys/tokens, and implementation risks.
- **Scope**: mixed
- **Date**: 2026-05-07

## Findings

### Files Found

| File Path | Description |
|---|---|
| `docs/architecture/01-overview.md` | Product architecture states Player is independently usable and directly calls Emby/Jellyfin REST APIs. Lines 31-39 show DataSourceManager with Emby as native API; lines 86-96 describe Player independent-first and Player → Emby/Jellyfin direct browsing/playback. |
| `docs/architecture/03-player-design.md` | Primary Player DataSource design. Lines 157-188 define the abstraction architecture; lines 191-312 define `MediaItem`, `MediaLibrary`, `HomeSection`, `MediaDetail`, `DataSourceConfig`, and `DataSource`; lines 315-386 include an Emby/Jellyfin example implementation. |
| `docs/architecture/07-security-design.md` | Security design for credentials. Lines 14-26 list Emby/Jellyfin API keys as sensitive assets; lines 168-203 require Player credentials in OS keychain/secure storage and normal config to store only non-sensitive fields plus a credential reference; lines 205-218 forbid default sync of API keys/cookies/passwords. |
| `.trellis/spec/frontend/type-safety.md` | Frontend type-safety contract. Lines 21-33 require common DataSource concepts and `emby` as a source type; lines 37-44 require validation of external API responses; lines 64-70 forbid broad `any`, credential leakage in config types, and hard-coding one source type in views. |
| `player/src/services/datasource/types.ts` | Current concrete TypeScript DataSource contract. Lines 0-18 define `MediaItem`; lines 20-36 define `MediaLibrary` and `HomeSection`; lines 44-70 define detail/track types; lines 72-110 define `DataSourceType`, `DataSourceConfig`, and `DataSource`. |
| `player/src/stores/datasource.ts` | Current source config store persists `DataSourceConfig[]` to `localStorage` (`STORAGE_KEY = 'ohmycine-datasources'`, lines 4, 20-33) and currently generates placeholder home sections (lines 70-108). |
| `player/src/views/SourceLibraryView.vue` | Current source library UI shell resolves the route `sourceId` to config and displays placeholder library browsing text; lines 48-84 are placeholders. |
| `player/src/components/layout/DataSourceSidebar.vue` | Current dynamic sidebar enumerates configured sources and already includes an `emby` icon label (`sourceIcons.emby = 'E'`, lines 12-22; source buttons lines 55-65). |

### Internal Code Patterns

Current DataSource contract in `player/src/services/datasource/types.ts`:

```ts
export interface DataSource {
  readonly id: string
  readonly name: string
  readonly type: DataSourceType
  readonly isConnected: boolean

  init: (config: DataSourceConfig) => Promise<void>
  test: () => Promise<boolean>
  destroy: () => void

  list: (path?: string) => Promise<MediaItem[]>
  listLibraries?: () => Promise<MediaLibrary[]>
  getHomeSections?: () => Promise<HomeSection[]>
  getFeaturedItems?: () => Promise<MediaItem[]>
  getContinueWatching?: () => Promise<MediaItem[]>
  getRecentlyAdded?: () => Promise<MediaItem[]>
  search: (keyword: string) => Promise<MediaItem[]>
  getDetail: (id: string) => Promise<MediaDetail>

  getStreamURL: (id: string) => Promise<string>

  exportConfig: () => DataSourceConfig
}
```

Current `DataSourceConfig` still contains optional sensitive fields (`apiKey`, `username`, `password`) in `player/src/services/datasource/types.ts:74-86`, while security design prefers normal config with `credentialRef` and secure storage. Any Emby implementation should account for that existing mismatch if it touches persistence.

Current persistence pattern in `player/src/stores/datasource.ts:20-33` reads/writes raw source configs through browser `localStorage`. This is relevant because Emby access tokens/API keys must not be stored there in plaintext under the security design.

The planned Emby sample in `docs/architecture/03-player-design.md:315-386` maps:

- root `list()` to media folders via `getMediaFolders()`;
- `list(path)` to library items via `getItems(path)`;
- `search(keyword)` to Emby search;
- `getDetail(id)` to item metadata, people, provider IDs, streams;
- `getStreamURL(id)` to `/emby/Videos/{id}/stream?api_key=...&Static=true`.

### Emby Authentication and Configuration Fields

Emby clients commonly need these configuration fields for an OhMyCine Player source:

| Field | Purpose | Sensitive |
|---|---|---|
| `id` | OhMyCine DataSource id. | No |
| `type` | `emby`. | No |
| `name` / `displayName` | User-facing source label. | No |
| `url` / base URL | Emby server origin, usually `http://host:8096` or `https://host:8920`, optionally with `/emby` depending deployment/reverse proxy. | Usually no, but can reveal LAN topology. |
| `userId` | Emby user id returned by authentication; needed by user-scoped item endpoints. | Low/medium sensitivity. |
| `accessToken` or `apiKey` | Token used for Emby API calls and stream/image URLs. | Yes |
| `username` + `password` | Only needed to create/refresh initial token with `/Users/AuthenticateByName`; should not persist as normal config. | Yes |
| `deviceId` | Stable per-install device identifier for Emby auth header/device tracking. | Medium; not a secret but persistent identifier. |
| `clientName` / `clientVersion` | Sent in Emby auth header, e.g. `OhMyCine Player` and app version. | No |
| optional TLS/trust fields | Whether to allow self-signed certificates or use custom proxy path. | Depends on exact value. |
| optional library filters | User-selected library ids, default sort, parental/profile filters if later supported. | No/low |

Authentication flow used by native Emby clients:

1. Probe public server info with `GET /System/Info/Public` or authenticated info with `GET /System/Info`.
2. Authenticate with `POST /Users/AuthenticateByName`, body containing `Username` and `Pw`/password field per Emby API docs and server version.
3. Store the returned `User.Id` and `AccessToken` in secure storage; store only a credential reference in normal config.
4. Send future requests with Emby authorization metadata. Common forms are:
   - `X-Emby-Authorization: MediaBrowser Client="OhMyCine Player", Device="<platform>", DeviceId="<stable-id>", Version="<app-version>", Token="<access-token>"`
   - and/or `X-Emby-Token: <access-token>` where supported.
   - Query `api_key=<token>` is widely used for images/stream URLs but leaks into logs/history more easily than headers.

The Emby official REST reference page index confirms `POST /Users/AuthenticateByName` under `UserService`, `GET /System/Info` and `GET /System/Info/Public` under `SystemService`.

### Common Emby Endpoints

Base path note: Emby deployments often accept both root paths and `/emby/...` paths. A robust source should normalize `baseURL` and use one consistent convention, while allowing reverse-proxy deployments where `/emby` is already part of the configured URL.

#### Server / auth / user

| Operation | Endpoint | Notes |
|---|---|---|
| Public server probe | `GET /System/Info/Public` | Does not require token; useful for connection test and server name/version. |
| Authenticated server probe | `GET /System/Info` | Confirms token works. |
| Username/password auth | `POST /Users/AuthenticateByName` | Returns authenticated `User` and `AccessToken`. |
| Current/public users | `GET /Users/Public` or `GET /Users/Query` | Useful for login user selection if needed; query endpoint may require auth. |

#### Libraries and items

| DataSource need | Endpoint | Common query parameters |
|---|---|---|
| List libraries | `GET /Users/{UserId}/Views` | Returns CollectionFolder views such as movies, TV, music, mixed. |
| List library contents | `GET /Users/{UserId}/Items` | `ParentId=<libraryId>`, `Recursive=true`, `IncludeItemTypes=Movie,Series,Episode,Folder`, `Fields=Overview,Genres,People,ProviderIds,MediaSources,MediaStreams,Path,DateCreated,SortName,RunTimeTicks,ImageTags,BackdropImageTags`, `StartIndex`, `Limit`, `SortBy`, `SortOrder`. |
| Search | `GET /Users/{UserId}/Items` | `SearchTerm=<keyword>`, `Recursive=true`, `IncludeItemTypes=Movie,Series,Episode`, `Limit`. |
| Detail | `GET /Users/{UserId}/Items/{Id}` or `GET /Items/{Id}` | User-scoped detail is better for permissions/play state. Include `Fields` for media streams, people, provider IDs, path, chapters, etc. |
| Continue watching | `GET /Users/{UserId}/Items/Resume` | Query by `MediaTypes=Video`, `Limit`, optional `ParentId`. |
| Recently added | `GET /Users/{UserId}/Items` | `Recursive=true`, `SortBy=DateCreated`, `SortOrder=Descending`, `Limit`, `IncludeItemTypes=Movie,Series,Episode`. |
| Seasons/episodes | `GET /Shows/{Id}/Seasons`, `GET /Shows/{Id}/Episodes` | Useful when mapping series detail and episode browsing; verify exact Emby server version behavior during implementation. |
| Playback info | `POST /Items/{Id}/PlaybackInfo` | Returns media sources/transcoding options; useful before choosing direct stream vs transcoding. |

The official REST reference index confirms `GET /Users/{UserId}/Views` under `LibraryService`, `GET /Users/{UserId}/Items` and `GET /Users/{UserId}/Items/Resume` under `ItemsService`, and `GET /Items/{Id}` under `LibraryService`.

#### Images

| Image type | URL pattern | Mapping |
|---|---|---|
| Poster / primary | `GET /Items/{Id}/Images/Primary` | `MediaItem.posterUrl`. Add `tag=<ImageTags.Primary>` when present for cache busting. |
| Backdrop | `GET /Items/{Id}/Images/Backdrop/0` | `MediaItem.backdropUrl`. Use `BackdropImageTags[0]` as `tag` when present. |
| Logo | `GET /Items/{Id}/Images/Logo` | `MediaItem.titleLogoUrl`. |
| Library icon | `GET /Items/{LibraryId}/Images/Primary` | `MediaLibrary.posterUrl`. |

The official REST reference index confirms image endpoints including `GET /Items/{Id}/Images/{Type}` under `ImageService`. Image URLs often include `api_key` when loaded by `<img>` tags because browsers cannot attach custom Emby headers to ordinary image `src` requests. This makes URL/token redaction important.

#### Streams / playback URLs

Common direct-play URL forms:

- `GET /Videos/{Id}/stream?Static=true&api_key=<token>`
- `GET /emby/Videos/{Id}/stream?Static=true&api_key=<token>`
- Include `MediaSourceId=<id>` if the item has multiple media sources.
- Include `DeviceId=<deviceId>` where needed for Emby session/device tracking.

Implementation should distinguish:

- direct stream URL for mpv/libmpv playback (`Static=true`, no transcoding when possible);
- transcoding URLs returned/negotiated through `PlaybackInfo` if direct play is unavailable;
- subtitle/audio tracks exposed via `MediaStreams`, which can be mapped to OhMyCine `SubtitleTrack` and `AudioTrack`.

The project docs currently use the direct URL pattern in `docs/architecture/03-player-design.md:378-381`:

```ts
return `${this.config.url}/emby/Videos/${id}/stream?api_key=${this.config.apiKey}&Static=true`
```

### Mapping Emby to Generic DataSource Interface

#### `init(config)`

- Normalize base URL: trim trailing slash; detect whether configured URL already ends with `/emby`.
- Load sensitive credential by reference from secure storage, not directly from persisted `DataSourceConfig`, if following security design.
- Store `sourceId = config.id`, `userId`, `deviceId`, `clientName`, `clientVersion`.
- Build an Emby client that validates unknown responses before mapping.

#### `test()`

- Unauthenticated test: `GET /System/Info/Public` to verify reachability.
- Authenticated test: `GET /System/Info` with token/header to verify stored credential.

#### `list(path?)`

Suggested path convention:

| Input | Emby call | Output |
|---|---|---|
| no `path` | `GET /Users/{UserId}/Views` | `MediaItem[]` folders or use `listLibraries()` for `MediaLibrary[]`. |
| library id | `GET /Users/{UserId}/Items?ParentId=<libraryId>&Recursive=false/true` | Movies, series, folders. |
| folder/series id | `GET /Users/{UserId}/Items?ParentId=<id>` or series-specific endpoints | Child items/seasons/episodes. |

Mapping examples:

| Emby field | OhMyCine field |
|---|---|
| `Id` | `MediaItem.id`, `MediaLibrary.id` |
| source config id | `sourceId` |
| parent/library id | `libraryId` |
| `Name` / `OriginalTitle` | `name` |
| `Type = Movie` | `type: 'movie'` |
| `Type = Series` | `type: 'series'` |
| `Type = Episode` | `type: 'episode'` |
| `Type = Folder` / `CollectionFolder` | `type: 'folder'` |
| `ProductionYear` | `year` |
| `CommunityRating` / `CriticRating` | `rating` |
| `Overview` | `overview` |
| `RunTimeTicks / 10_000_000` | `duration` seconds |
| `Size` or selected `MediaSource.Size` | `size` |
| `DateCreated` / `DateLastMediaAdded` | `modified` or source-specific date |
| `Path` or logical item id path | `path` |
| `ImageTags.Primary` | build `posterUrl` |
| `BackdropImageTags[0]` | build `backdropUrl` |
| `ImageTags.Logo` | build `titleLogoUrl` |

#### `listLibraries?()`

- Call `GET /Users/{UserId}/Views`.
- Map Emby CollectionFolder `CollectionType`:
  - `movies` → `MediaLibrary.type = 'movies'`
  - `tvshows` → `MediaLibrary.type = 'series'`
  - anime may be an Emby folder name/metadata convention rather than a stable Emby `CollectionType`; if no explicit signal, keep `mixed` or `series`.
  - unknown/mixed → `mixed` or `folders`.

#### `getHomeSections?()`

Can be composed from:

- hero/featured: latest or high-rated items from `GET /Users/{UserId}/Items` with `Recursive=true`, `SortBy=DateCreated` or rating sort;
- continue watching: `GET /Users/{UserId}/Items/Resume`;
- recently added: `GET /Users/{UserId}/Items?SortBy=DateCreated&SortOrder=Descending&Limit=...`;
- library rows: `GET /Users/{UserId}/Views` then per-library item queries.

#### `search(keyword)`

- Call `GET /Users/{UserId}/Items?SearchTerm=<keyword>&Recursive=true&IncludeItemTypes=Movie,Series,Episode&Limit=...`.
- Map results through the same item mapper.

#### `getDetail(id)`

- Call `GET /Users/{UserId}/Items/{Id}` or `GET /Items/{Id}` with fields including people, provider ids, genres, media streams, media sources, chapters, images.
- Map:
  - `Genres` → `genres`
  - `People` where `Type === 'Director'` → `directors`
  - `People` where `Type === 'Actor'` → `cast`
  - `ProviderIds.Imdb` → `imdbId`
  - `ProviderIds.Tmdb` parsed as number → `tmdbId`
  - video `MediaStreams` width/height → `resolution`
  - video stream `Codec` → `codec`
  - first/default audio stream `Codec` → `audioCodec`
  - subtitle streams where `Type === 'Subtitle'` → `SubtitleTrack[]`
  - audio streams where `Type === 'Audio'` → `AudioTrack[]`

#### `getStreamURL(id)`

- Basic direct-play mapping: build `/Videos/{id}/stream?Static=true&api_key=<token>`.
- More complete mapping: fetch `PlaybackInfo` first, select a playable `MediaSourceId`, then append `MediaSourceId` and any required query parameters.
- For libmpv, the URL can be passed to the Rust/mpv layer, but avoid logging the full tokenized URL.

#### `exportConfig()`

Per `docs/architecture/07-security-design.md:187-203`, return non-sensitive config plus a credential reference, not plaintext token/password. Current code types do not yet model `credentialRef`, but the security design does.

### External References

- [Emby REST API Reference: UserService](https://dev.emby.media/reference/RestAPI/UserService.html) — confirms user/auth endpoints including `POST /Users/AuthenticateByName`.
- [Emby REST API Reference: SystemService](https://dev.emby.media/reference/RestAPI/SystemService.html) — confirms `/System/Info` and `/System/Info/Public` server info endpoints.
- [Emby REST API Reference: LibraryService](https://dev.emby.media/reference/RestAPI/LibraryService.html) — confirms library browsing endpoints including `/Users/{UserId}/Views` and item endpoints under `/Items/{Id}`.
- [Emby REST API Reference: ItemsService](https://dev.emby.media/reference/RestAPI/ItemsService.html) — confirms item query endpoints including `/Users/{UserId}/Items` and `/Users/{UserId}/Items/Resume`.
- [Emby REST API Reference: ImageService](https://dev.emby.media/reference/RestAPI/ImageService.html) — confirms image endpoints including `/Items/{Id}/Images/{Type}`.
- [Emby REST API Reference: VideosService](https://dev.emby.media/reference/RestAPI/VideosService.html) — API section for video operations; direct stream URLs are also reflected in the project Player design sample.
- [Jellyfin generated API client AuthHeaderBuilder](https://github.com/jellyfin/jellyfin-sdk-typescript) — useful compatibility reference for the MediaBrowser/Emby authorization header style, though Jellyfin is not identical to Emby.

### Related Specs

- `.trellis/spec/frontend/type-safety.md` — source contracts, external response validation, credential leakage restrictions.
- `.trellis/spec/frontend/directory-structure.md` — expected service/store organization for Player code.
- `.trellis/spec/frontend/component-guidelines.md` — UI component and immersive chrome rules relevant to source/library views.
- `.trellis/spec/frontend/state-management.md` — Pinia/store expectations for shared UI/source state.
- `.trellis/spec/backend/security-guidelines.md` — relevant if Server later stores or syncs Emby credentials, but Player-side credential storage is primarily in `docs/architecture/07-security-design.md`.

## Security Concerns

1. **Token storage**: Emby access tokens/API keys are explicitly sensitive assets in `docs/architecture/07-security-design.md:14-26`. They should be stored in OS secure storage on desktop, not raw `localStorage`.
2. **Current config mismatch**: `DataSourceConfig` currently has `apiKey`, `username`, and `password` fields, and `player/src/stores/datasource.ts` persists configs to `localStorage`. This is an implementation risk for Emby unless credentials are moved behind a `credentialRef` or equivalent secure-storage layer.
3. **Tokenized URLs**: image and stream URLs often include `api_key`. These URLs can leak via logs, error messages, devtools, mpv logs, HTTP referers, persisted playback history, or crash reports. Logs should redact `api_key`, access tokens, and signed media URLs.
4. **Config sync**: Security design says DataSource name/type/url/library ids sync by default, but API keys/cookies/passwords do not (`docs/architecture/07-security-design.md:205-218`). Emby `exportConfig()` must respect that.
5. **HTTP vs HTTPS**: Many home Emby deployments use LAN HTTP. UI/config should make transport explicit; remote/WAN usage should avoid plaintext credentials and tokenized stream URLs over HTTP.
6. **User permissions**: Use user-scoped endpoints (`/Users/{UserId}/...`) where possible so the Player sees the same library/playback permissions as the authenticated Emby user.
7. **Path disclosure**: Emby item details can include server filesystem paths in `Path`/`MediaSources`. Do not expose or send local absolute paths to AI providers or unnecessary UI surfaces by default.
8. **Headers vs query tokens**: Prefer headers for API JSON calls. Query `api_key` may still be needed for `<img src>` and direct stream URLs, so redact aggressively.

## Implementation Risks

1. **Emby version/API variance**: Emby versions and reverse-proxy setups may differ in accepted base paths (`/` vs `/emby`) and auth/body details for `AuthenticateByName`.
2. **Direct play vs transcoding**: `/Videos/{Id}/stream?Static=true` is simple but may fail for media requiring transcoding, permissions, or specific `MediaSourceId`; robust playback may need `PlaybackInfo` negotiation.
3. **Multiple media sources**: Movies/episodes can have several `MediaSources`; stream URL generation needs source selection or a default rule.
4. **Series hierarchy**: Mapping series → seasons → episodes does not fit a flat `list(path?)` perfectly; path/id conventions should preserve library/series context.
5. **Image loading with auth**: Browser image tags cannot set Emby auth headers, pushing implementation toward tokenized image URLs or an in-app/Tauri image proxy; tokenized URLs raise redaction/cache concerns.
6. **Large libraries**: Item endpoints need pagination (`StartIndex`, `Limit`) and sorting; current `DataSource.list()` returns a full `Promise<MediaItem[]>`, so UI pagination/infinite loading behavior may need an extension later.
7. **External response typing**: Emby API responses should be parsed as `unknown` and validated/guarded before mapping, per `.trellis/spec/frontend/type-safety.md:37-44`.
8. **Playback progress/session integration**: Basic `getStreamURL()` is enough for playback, but resume/progress scrobbling would need additional Emby session/playstate endpoints not covered by the current OhMyCine `DataSource` interface.
9. **Current UI state**: `SourceLibraryView.vue` and `datasource.ts` are still placeholder-driven, so Emby implementation also needs a source manager/client wiring path before UI can display real libraries.

## Caveats / Not Found

- No concrete `EmbyDataSource` or `EmbyClient` implementation exists in `player/src` yet; only docs and shared types are present.
- The active task PRD is for embedded video rendering and explicitly lists DataSource browsing as out of scope for that task; this research file is informational for future implementation.
- External Emby reference pages are index-style pages; exact request/response schemas and server-version differences should be verified against a live Emby instance or OpenAPI/schema artifact during implementation.
