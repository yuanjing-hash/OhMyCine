# Research: Emby API media loading and playback

- **Query**: Research correct Emby API patterns for image loading and playback URL resolution, especially images/thumbnails/backdrops, latest media granularity for TV series vs episodes, random featured/backdrop selection, and playback of STRM/remote items where Emby/plug-ins may return internal redirect URLs.
- **Scope**: mixed
- **Date**: 2026-05-08

## Findings

### Files Found

| File Path | Description |
|---|---|
| `player/src/services/datasource/emby.ts` | Current concrete Emby DataSource implementation. Relevant lines: item field selection at 17-30; home sections at 246-291; featured/latest list queries at 367-397; playback source resolution at 314-330 and 473-512; image URL construction at 565-621; remote/STRM URL detection at 890-937. |
| `.trellis/tasks/05-07-player-embedded-video-rendering/research/emby-datasource.md` | Earlier Emby API mapping research, including basic endpoints, DataSource mapping, and security concerns. |
| `docs/architecture/03-player-design.md` | Planned Player/DataSource design. Contains sample Emby stream URL shape around the EmbyDataSource design section. |
| `docs/architecture/07-security-design.md` | Security requirements for media-server credentials, tokenized URLs, Player secure storage, sync defaults, and path/credential redaction. |
| `.trellis/spec/frontend/type-safety.md` | Frontend contract requiring validation of external responses and avoiding credential leakage in config types. |
| `.trellis/spec/frontend/directory-structure.md` | Frontend service/store organization expectations for Player code. |
| `.trellis/spec/backend/security-guidelines.md` | Security guidance relevant to logs/redaction if Emby credentials or URLs later flow through Server. |

### Code Patterns

#### Current Emby item/image fields

`player/src/services/datasource/emby.ts:17-30` requests `ImageTags` and `BackdropImageTags` in `ITEM_FIELDS`, but it does not request parent image fields such as `ParentBackdropImageTags`, `ParentBackdropItemId`, `ParentThumbImageTag`, or `ParentLogoImageTag`. Emby clients commonly use parent image fields as fallbacks for episodes/seasons because episodes may not have their own backdrops/logos.

`player/src/services/datasource/emby.ts:600-614` builds image URLs as:

```ts
/Items/{itemId}/Images/{type}?api_key=<token>&quality=82&maxWidth=<width>&tag=<tag>
```

That matches the official `ImageService` endpoint shape and uses `tag` for cache headers. Current widths are poster `420`, backdrop `1280`, logo `520` (`emby.ts:32-35`). Current image URLs do not include `maxHeight`, `format`, `CropWhitespace`, or parent image fallback.

#### Current latest/featured granularity

`player/src/services/datasource/emby.ts:367-397` currently uses `/Users/{UserId}/Items` with `Recursive=true`, `IncludeItemTypes=Movie,Series,Episode`, `SortBy=DateCreated`, and a small `Limit` for both featured and recently added. This shape can naturally return every new episode, because episodes are explicitly included and sorted by each episode item creation time.

For series-level latest rows, the query shape should use `IncludeItemTypes=Series` when the UI row is intended to represent shows rather than individual episodes. For episode-level latest rows, `IncludeItemTypes=Episode` is correct and should be displayed as episodes with parent series context.

#### Current playback URL resolution

`player/src/services/datasource/emby.ts:473-487` calls `POST /Items/{Id}/PlaybackInfo` with only `UserId`, then falls back to item `MediaSources` on error.

`player/src/services/datasource/emby.ts:489-512` resolves playback by first trying `Path`, `DirectStreamUrl`, `DirectPlayUrl`, `TranscodingUrl`, then prefixing relative URLs with `baseUrl`, else constructing `/Videos/{id}/stream?Static=true&api_key=<token>&MediaSourceId=<id>`.

This is close to Emby client practice for normal direct stream/transcode flows, but caution is needed for `Path`: Emby `MediaSourceInfo.Path` may be a local server path, a `.strm` file path, or an HTTP URL that is only reachable inside the Emby server/LAN/plugin environment. Returning `Path` directly is only safe/playable when it is an actual externally reachable `http(s)` media URL and does not require hidden headers/cookies.

### Recommended Emby Image Endpoints and Query Parameters

#### Endpoint patterns

Official `ImageService` confirms:

| Need | Endpoint |
|---|---|
| Primary/poster | `GET /Items/{Id}/Images/Primary` |
| Backdrop | `GET /Items/{Id}/Images/Backdrop` or `GET /Items/{Id}/Images/Backdrop/{Index}` / with `Index` query depending client helper |
| Thumb | `GET /Items/{Id}/Images/Thumb` |
| Logo/title art | `GET /Items/{Id}/Images/Logo` |
| Art/banner/disc where available | `GET /Items/{Id}/Images/{Type}` |

The official `ImageService` page documents these query parameters for `/Items/{Id}/Images/{Type}`:

| Param | Use |
|---|---|
| `maxWidth` / `maxHeight` | Return an image constrained to fit the requested dimensions. Preferred for responsive poster/backdrop/thumb loading because it avoids downloading originals. |
| `width` / `height` | Request a fixed image dimension. Use when exact generated size is desired. |
| `quality` | JPEG/image quality from `0-100`; default is `90`. Emby client code sets `quality=100` when no resize is requested; for UI cards, lower quality such as `80-90` is typical for speed. |
| `tag` | Supply the image cache tag from the item object (`ImageTags.Primary`, `BackdropImageTags[index]`, etc.) to receive strong caching headers and avoid stale/missing cache behavior. |
| `CropWhitespace` | Useful for logos/clear art. If omitted, Emby crops whitespace for logos and clear art by default. |
| `EnableImageEnhancers` | Enables/disables image enhancers such as cover art. |
| `Format` | Output format: `original`, `gif`, `jpg`, `png`. |
| `BackgroundColor`, `ForegroundLayer`, `AutoOrient`, `KeepAnimation`, `Index` | Optional transformations/selection details. |

For ordinary browser `<img src>`, clients often use `api_key=<token>` in the image URL because custom Emby auth headers cannot be attached by a plain `<img>` request. JSON/API calls should use `X-Emby-Token` / authorization headers instead.

#### Item list fields needed for image availability

Official `ItemsService` confirms `/Users/{UserId}/Items` supports:

- `EnableImages`: include image information in output.
- `ImageTypeLimit`: max number of images to return per image type.
- `EnableImageTypes`: comma-delimited image types to include in output.
- `ImageTypes`: filter results to items containing specified image types.
- `Fields`: additional fields such as `PrimaryImageAspectRatio`, `ParentId`, `Path`, `MediaStreams`, `Overview`, `ProviderIds`, etc.

Common client list query pattern:

```text
EnableImageTypes=Primary,Backdrop,Thumb,Logo
ImageTypeLimit=1
Fields=PrimaryImageAspectRatio,Overview,ParentId,SortName,DateCreated,ProviderIds
```

For hero/backdrop candidates, clients can add:

```text
EnableImageTypes=Backdrop
ImageTypes=Backdrop
```

so items without backdrops are filtered out before the UI tries to fill a wide hero.

#### Parent fallback pattern

Emby webcomponents `playbackmanager.js` includes a `backdropImageUrl` helper that checks:

1. `item.BackdropImageTags[0]` + `item.Id`
2. `item.ParentBackdropImageTags[0]` + `item.ParentBackdropItemId`
3. else no backdrop

This matters for episodes/seasons: an episode item may not have a backdrop, while its parent series does. If OhMyCine maps an episode to a hero/latest card, the hero image may fail or not fill unless parent backdrop fields are requested and used.

### Recommended Latest/List Granularity

#### General `/Users/{UserId}/Items` controls

Official `ItemsService` documents these relevant query params:

| Param | Use |
|---|---|
| `ParentId` | Limit search/listing to a library/folder. Omit for root/global. |
| `Recursive=true/false` | Recursive library/folder traversal. |
| `IncludeItemTypes` | Comma-delimited item types such as `Movie`, `Series`, `Episode`, `Season`, `Folder`. |
| `MediaTypes=Video` | Restrict to video media where needed. |
| `SortBy=DateCreated` / `SortOrder=Descending` | Latest-created item ordering. |
| `SortBy=Random` | Random featured selection. |
| `Limit`, `StartIndex` | Pagination/window size. |
| `ImageTypeLimit`, `EnableImageTypes`, `ImageTypes` | Image metadata and image-availability filtering. |
| `Filters=IsNotFolder` | Common client helper merge for playable/non-folder rows. |
| `GroupItemsIntoCollections` | Hide items behind box sets/collections where desired. |
| `IsMovie`, `IsSeries`, `IsFolder` | Type-specific filters in addition to `IncludeItemTypes`. |

#### Series-level latest vs episode-level latest

Observed official skin patterns distinguish TV episode latest from random series spotlight:

- `MediaBrowser/emby-web-defaultskin/home/views.tv.js` `loadLatest`: `IncludeItemTypes: "Episode"`, `Limit: 12`, `ParentId`, `ImageTypeLimit: 1`, `EnableImageTypes: "Primary,Backdrop,Thumb"`.
- Same file `loadSpotlight`: `SortBy: "Random"`, `IncludeItemTypes: "Series"`, `Limit: 20`, `Recursive: true`, `ParentId`, `EnableImageTypes: "Backdrop"`, `ImageTypes: "Backdrop"`.
- `home/views.movies.js` `loadLatest`: `IncludeItemTypes: "Movie"` for movie latest.
- `home/views.movies.js` `loadSpotlight`: `SortBy: "Random"`, `IncludeItemTypes: "Movie"`, `EnableImageTypes: "Backdrop"`, `ImageTypes: "Backdrop"`, `Fields: "Taglines"`.

Implication for OhMyCine rows:

| UI intent | Query granularity |
|---|---|
| Latest movies | `IncludeItemTypes=Movie` |
| Latest TV episodes | `IncludeItemTypes=Episode`; display as episode cards with series/season context. |
| Latest TV series/shows | `IncludeItemTypes=Series`; sort by an appropriate date field supported by the server/version (`DateCreated` or available series-level latest-added metadata), and do not include `Episode` in the same row. |
| Mixed home latest without episode spam | Either split movie and TV sections, or combine `Movie,Series` and exclude `Episode`; do not use `Movie,Series,Episode` for a series-level row. |

Emby may have model fields such as `DateLastMediaAdded` for series items. Current OhMyCine types include `DateLastMediaAdded` in `EmbyItemRecord`, but `ITEM_FIELDS` does not request it explicitly and official `ItemsService` `Fields` list in the scraped reference does not show it among selectable additional fields. If returned by the server for Series, it can be used for display/mapping; if not returned, the reliable API-level granularity control remains `IncludeItemTypes=Series` vs `Episode`.

### Random / Featured / Backdrop Selection Across Libraries

Common client pattern for spotlight/featured cards:

```text
GET /Users/{UserId}/Items
  ?Recursive=true
  &ParentId=<libraryId optional>
  &SortBy=Random
  &IncludeItemTypes=Movie,Series
  &Limit=20
  &EnableImageTypes=Backdrop
  &ImageTypes=Backdrop
  &ImageTypeLimit=1
  &Fields=Taglines,Overview,PrimaryImageAspectRatio
```

Notes:

- Omit `ParentId` or run per-library queries and merge if the UI wants all libraries.
- Use `ImageTypes=Backdrop` to avoid hero candidates that cannot fill a wide background.
- Use `IncludeItemTypes=Movie,Series` for hero/featured so episodes do not dominate the hero unless episode-level hero is intentional.
- Use `SortBy=Random` when a rotating/random hero is desired. For deterministic latest hero, use `SortBy=DateCreated&SortOrder=Descending` but still filter image types.
- For TV-specific spotlight, Emby default skin uses `IncludeItemTypes=Series`, not `Episode`.
- For Movies-specific spotlight, Emby default skin uses `IncludeItemTypes=Movie`.

### Playback URL / PlaybackInfo Handling

#### Official `PlaybackInfo` fields

Official `MediaInfoService` confirms `POST /Items/{Id}/PlaybackInfo` accepts a `PlaybackInfoRequest` with fields including:

- `UserId`
- `MaxStreamingBitrate`
- `StartTimeTicks`
- `AudioStreamIndex`
- `SubtitleStreamIndex`
- `MaxAudioChannels`
- `MediaSourceId`
- `LiveStreamId`
- `DeviceProfile`
- `EnableDirectPlay`
- `EnableDirectStream`
- `EnableTranscoding`
- `AllowVideoStreamCopy`
- `AllowAudioStreamCopy`
- `IsPlayback`
- `AutoOpenLiveStream`
- `CurrentPlaySessionId`

The response contains `PlaybackInfoResponse.MediaSources[]`, `PlaySessionId`, and each `MediaSourceInfo` can include:

- `Protocol` (`File`, `Http`, `Rtmp`, `Rtsp`, `Udp`, `Rtp`, `Ftp`, `Mms`)
- `Id`, `Path`, `Container`, `Size`, `Name`, `IsRemote`
- `SupportsTranscoding`, `SupportsDirectStream`, `SupportsDirectPlay`
- `RequiresOpening`, `OpenToken`, `RequiresClosing`, `LiveStreamId`
- `MediaStreams[]`, `Formats`, `Bitrate`
- `RequiredHttpHeaders`
- `DirectStreamUrl`
- `AddApiKeyToDirectStreamUrl`
- `TranscodingUrl`, `TranscodingSubProtocol`, `TranscodingContainer`
- `DefaultAudioStreamIndex`, `DefaultSubtitleStreamIndex`

#### Common client decision order

Emby webcomponents `playbackmanager.js` common logic:

1. If `mediaSource.enableDirectPlay`, use `mediaSource.Path` and mark `DirectPlay`.
2. Else if an audio `StreamUrl` exists, use it.
3. Else if `mediaSource.SupportsDirectStream`:
   - if `mediaSource.DirectStreamUrl` exists, use `apiClient.getUrl(mediaSource.DirectStreamUrl)`;
   - else build a static stream URL with `Static=true`, `mediaSourceId`, `deviceId`, `api_key`, optional `Tag`/`LiveStreamId`, and a container extension: `Videos/{ItemId}/stream.{container}` or `Audio/{ItemId}/stream.{container}`.
4. Else if `mediaSource.SupportsTranscoding`, use `apiClient.getUrl(mediaSource.TranscodingUrl)` and set HLS content type when `TranscodingSubProtocol === 'hls'`.

For OhMyCine/libmpv, the usual playable URL candidates are therefore:

| Candidate | Meaning |
|---|---|
| Relative `DirectStreamUrl` | Emby-generated direct-stream URL. Prefix with Emby base URL and add API key only when required by returned URL/field behavior. |
| Relative `TranscodingUrl` | Emby-generated transcode/HLS URL. Prefix with Emby base URL. For HLS, libmpv can usually play `.m3u8` if accessible. |
| Static stream URL | `/Videos/{Id}/stream` or `/Videos/{Id}/stream.{container}?Static=true&MediaSourceId=<id>&DeviceId=<deviceId>&api_key=<token>` for direct stream fallback. |
| Direct HTTP `Path` | Only when `Protocol=Http`, no required hidden headers, and the URL is reachable by the Player/mpv runtime. |
| File `Path` | Only when the file path is local to the Player machine and permitted by configured local roots. A server-side Emby path is not a valid Player path. |

#### STRM / remote / plugin redirect URLs

For STRM and cloud plug-in media, Emby may return one of these shapes:

- `MediaSourceInfo.Path` is the `.strm` file path on the Emby server.
- `MediaSourceInfo.Path` is the URL contained by the STRM file.
- `DirectStreamUrl` or `TranscodingUrl` is an Emby-relative URL that will stream/proxy through Emby.
- `Path`, `DirectStreamUrl`, or plugin metadata points to an internal plug-in redirect URL, e.g. `http://media-server.example.test:3033/api/v1/plugin/P115StrmHelper/redirect_url?...`.
- `RequiredHttpHeaders` may be needed for some HTTP media sources.

Safe limitations:

1. If `PlaybackInfo` only exposes an internal LAN/plugin redirect URL, a client outside that network or without the plug-in's cookies/headers cannot reliably transform it into a real external media URL unless the plug-in/API explicitly exposes such a URL to the client.
2. OhMyCine should not scrape arbitrary nested query parameters as a guarantee of real media playback. Some redirect endpoints are intentionally signed/internal and may return the real URL only after authenticated server-side logic.
3. If Emby can stream the item, prefer Emby-generated `DirectStreamUrl`, `TranscodingUrl`, or static `/Videos/{Id}/stream...` over returning a plug-in control-plane URL to mpv.
4. If Emby itself cannot provide a playable/proxied stream and only returns an internal plug-in redirect, OhMyCine can only report that the item requires Emby/plugin-side reachable playback or a direct source integration; it should not display/log the raw redirect URL.
5. If using a direct HTTP `Path`, honor `RequiredHttpHeaders`. A bare URL may fail if required headers are omitted.

### DataSource and Security Mapping

| DataSource concern | Mapping |
|---|---|
| `MediaItem.posterUrl` | Use `Primary` image URL with `tag=ImageTags.Primary`, size limit, quality, and token redaction. |
| `MediaItem.backdropUrl` / hero | Prefer item backdrop; fallback to parent backdrop fields for episodes/seasons; filter hero candidates with `ImageTypes=Backdrop`. |
| `MediaItem.titleLogoUrl` | Use `Logo` image URL with `tag=ImageTags.Logo`; consider `CropWhitespace`/default logo crop behavior. |
| Latest rows | Split by intent: movies use `Movie`, TV shows use `Series`, episode latest uses `Episode`; avoid `Movie,Series,Episode` for a series-level row. |
| Featured/hero | `SortBy=Random`, `IncludeItemTypes=Movie,Series`, `ImageTypes=Backdrop`, `EnableImageTypes=Backdrop`, optional per-library `ParentId`. |
| `getStreamURL(id)` | Call `PlaybackInfo` with playback intent/device profile flags; select a `MediaSource`; prefer Emby-generated direct stream/transcode/static URL; only use direct HTTP `Path` when externally reachable and header requirements are satisfied. |
| Sensitive values | Do not log/display raw image URLs, stream URLs, query tokens, plugin redirect URLs, `Path`, local server filesystem paths, or required headers/cookies. |
| Config sync/storage | Follow `docs/architecture/07-security-design.md`: Player credentials in OS secure storage; normal config only stores credential references; sync credentials only after explicit user confirmation. |

### External References

- [Emby REST API Reference: ImageService `/Items/{Id}/Images/{Type}`](https://dev.emby.media/reference/RestAPI/ImageService/getItemsByIdImagesByType.html) — documents image endpoint, `maxWidth`, `maxHeight`, `width`, `height`, `quality`, `tag`, `CropWhitespace`, `EnableImageEnhancers`, `Format`, and related image transform params.
- [Emby REST API Reference: ItemsService `/Users/{UserId}/Items`](https://dev.emby.media/reference/RestAPI/ItemsService/getUsersByUseridItems.html) — documents list/search parameters including `ParentId`, `Recursive`, `IncludeItemTypes`, `MediaTypes`, `SortBy`, `SortOrder`, `Limit`, `StartIndex`, `Fields`, `EnableImages`, `EnableImageTypes`, `ImageTypeLimit`, `ImageTypes`, `Filters`, and `GroupItemsIntoCollections`.
- [Emby REST API Reference: ItemsService `/Users/{UserId}/Items/Resume`](https://dev.emby.media/reference/RestAPI/ItemsService/getUsersByUseridItemsResume.html) — documents resume/continue-watching endpoint with similar item query/image parameters.
- [Emby REST API Reference: MediaInfoService `POST /Items/{Id}/PlaybackInfo`](https://dev.emby.media/reference/RestAPI/MediaInfoService/postItemsByIdPlaybackinfo.html) — documents playback negotiation request/response, `MediaSources`, `DirectStreamUrl`, `TranscodingUrl`, `SupportsDirectStream`, `SupportsDirectPlay`, `RequiredHttpHeaders`, and protocol fields.
- [MediaBrowser/emby-webcomponents `playback/playbackmanager.js`](https://github.com/MediaBrowser/emby-webcomponents/blob/master/playback/playbackmanager.js) — common Emby client playback decision flow and backdrop image fallback helper.
- [MediaBrowser/emby-web-defaultskin `home/views.tv.js`](https://github.com/MediaBrowser/emby-web-defaultskin/blob/master/home/views.tv.js) — example TV home rows: latest episodes, random series spotlight, `EnableImageTypes`, `ImageTypeLimit`, and `ImageTypes=Backdrop`.
- [MediaBrowser/emby-web-defaultskin `home/views.movies.js`](https://github.com/MediaBrowser/emby-web-defaultskin/blob/master/home/views.movies.js) — example movie latest and random movie spotlight query patterns.

### Related Specs

- `.trellis/spec/frontend/type-safety.md` — validate unknown external API responses and avoid broad `any`/credential leakage.
- `.trellis/spec/frontend/directory-structure.md` — DataSource/service placement expectations.
- `.trellis/spec/frontend/component-guidelines.md` — UI component expectations relevant to image-heavy hero/cards.
- `.trellis/spec/frontend/state-management.md` — source/store shared state expectations.
- `.trellis/spec/backend/security-guidelines.md` — log redaction/security conventions relevant if Emby URLs/credentials touch Server.
- `docs/architecture/07-security-design.md` — primary security source for credentials, tokenized URLs, path disclosure, and sync defaults.

## Caveats / Not Found

- The scraped official Emby REST reference did not expose a dedicated `/Users/{UserId}/Items/Latest` page; current official reference index showed `/Users/{UserId}/Items` and `/Users/{UserId}/Items/Resume`. Emby web/default skin uses a model helper named `latestItems`, but the underlying exact helper URL was not found in the fetched repositories during this research.
- Emby version and plug-in behavior varies. STRM/cloud plug-ins may expose only internal redirect/control-plane URLs; without plug-in documentation or an explicit direct-link API, a client cannot safely derive a real external playable URL.
- Some Emby/Jellyfin-compatible fields such as parent image fields are common in clients but should be validated against the actual Emby server response before relying on them.
- This report describes API patterns and mappings only; it does not change code.
