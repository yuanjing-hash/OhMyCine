# Research: Emby/Jellyfin playback progress sync

- **Query**: Research how Emby/Jellyfin clients report playback progress so OhMyCine Player can sync local playback history back to Emby continue-watching. Focus on Emby/Jellyfin API endpoints and open-source client patterns for PlaybackInfo, PlaySessionId, MediaSourceId, Sessions/Playing, Sessions/Playing/Progress, Sessions/Playing/Stopped, Users/{UserId}/PlayingItems, UserData updates, event names, required headers/body/query fields, and why progress may not appear in Resume/Continue Watching.
- **Scope**: mixed
- **Date**: 2026-05-19

## Findings

### Files Found

| File Path | Description |
|---|---|
| `player/src/services/datasource/emby.ts` | Current Emby DataSource; includes authenticated request helper, `/Items/{id}/PlaybackInfo`, cached `mediaSourceId`/`playSessionId`, session progress reports, user-item fallback reports, played marking, resume mapping from `UserData`. |
| `player/src/services/datasource/types.ts` | Shared DataSource contract and `ProviderPlaybackProgressInput` / event union. |
| `player/src/views/PlayerView.vue` | Player-side local SQLite save lifecycle and best-effort provider sync calls on start/progress/pause/resume/stop/completed. |
| `player/src/services/playbackContext.ts` | Playback context carries optional `mediaSourceId` plus queue state. |
| `player/src/services/playbackHistory.ts` | Local SQLite history wrapper, completion heuristic, resume threshold, and token-safe identities. |
| `.trellis/spec/frontend/type-safety.md` | Provider playback progress sync contract and local playback-history contract. |
| `.trellis/tasks/archive/2026-05/05-15-player-emby-progress-sync-queue-panel/research/emby-progress-api.md` | Earlier Emby progress API research with endpoint matrix and OhMyCine mapping notes. |
| `.trellis/tasks/archive/2026-05/05-07-player-embedded-video-rendering/research/emby-api-media-loading-playback.md` | Earlier PlaybackInfo/media-source research including `PlaySessionId`, stream resolution, and Emby web client playback decisions. |

### Code Patterns

#### Current OhMyCine Emby progress path

- `DataSource` defines optional provider sync as `syncPlaybackProgress?: (progress: ProviderPlaybackProgressInput) => Promise<void>` and the event union is `'started' | 'progress' | 'paused' | 'resumed' | 'stopped' | 'completed'` (`player/src/services/datasource/types.ts:109-143`).
- `PlayerView` saves local SQLite progress first, then fire-and-forgets provider sync; provider errors are swallowed so playback/local history are not blocked (`player/src/views/PlayerView.vue:294-318`, `player/src/views/PlayerView.vue:333-345`).
- `PlayerView` sends provider events from route media load/start, interval saves, pause/resume watcher, route switch, file drop, queue switch, and explicit pause (`player/src/views/PlayerView.vue:576-618`, `player/src/views/PlayerView.vue:624-724`).
- `currentMediaSourceId()` reads route `mediaSourceId` first, then in-memory playback context `mediaSourceId` (`player/src/views/PlayerView.vue:273-276`); playback context supports `mediaSourceId?: string` (`player/src/services/playbackContext.ts:24-40`).
- `EmbyDataSource.getStreamURL()` obtains playable media sources, remembers `MediaSourceInfo.Id`, and falls back to `/Videos/{id}/stream?Static=true&api_key=...&MediaSourceId=...` when needed (`player/src/services/datasource/emby.ts:372-394`).
- `fetchPlaybackInfo()` calls `POST /Items/{id}/PlaybackInfo` with `UserId`, direct play/stream/transcode flags, `AllowVideoStreamCopy`, `AllowAudioStreamCopy`, and `IsPlayback=true`; `parsePlaybackInfo()` extracts both `MediaSources[]` and `PlaySessionId` (`player/src/services/datasource/emby.ts:639-649`, `player/src/services/datasource/emby.ts:1182-1190`).
- `ensurePlaybackSession()` caches/reobtains `mediaSourceId` and `playSessionId` before provider sync (`player/src/services/datasource/emby.ts:652-682`).
- `syncSessionPlaybackProgress()` sends session body endpoints with `ItemId`, `MediaSourceId`, `PlaySessionId`, `PositionTicks`, optional `RunTimeTicks`, `CanSeek`, `IsPaused`, `PlayMethod='DirectPlay'`, and `EventName`; it uses `/Sessions/Playing`, `/Sessions/Playing/Progress`, or `/Sessions/Playing/Stopped` based on the local event (`player/src/services/datasource/emby.ts:417-447`).
- `syncUserPlaybackProgress()` also sends the older Emby user/item compatibility endpoints when `MediaSourceId` is known: `POST /Users/{UserId}/PlayingItems/{Id}`, `POST /Users/{UserId}/PlayingItems/{Id}/Progress`, and `POST /Users/{UserId}/PlayingItems/{Id}/Delete` with query fields (`player/src/services/datasource/emby.ts:449-475`).
- Completed sync calls `POST /Users/{UserId}/PlayedItems/{Id}` best-effort (`player/src/services/datasource/emby.ts:413-415`, `player/src/services/datasource/emby.ts:684-690`).
- `request()` sends JSON body for POST session calls, query params for GET, and both `X-Emby-Token` plus `X-Emby-Authorization`; `postWithQuery()` sends POST with query parameters for the older user/item endpoints (`player/src/services/datasource/emby.ts:815-855`).
- Provider resume cards are read from `/Users/{UserId}/Items/Resume`, and `mapItem()` maps `UserData.PlaybackPositionTicks` and `UserData.PlayedPercentage` into `resumePosition` and `progress` (`player/src/services/datasource/emby.ts:524-532`, `player/src/services/datasource/emby.ts:869-895`).
- Time conversion is `seconds * 10_000_000`, and Emby ticks are converted back with `ticks / 10_000_000` (`player/src/services/datasource/emby.ts:61`, `player/src/services/datasource/emby.ts:1265-1273`).

#### Open-source Emby/Jellyfin client reporting pattern

- Jellyfin Web builds playback reports by copying `state.PlayState`, adding `ItemId`, optionally adding `EventName`, playlist queue data, and calling `apiClient[method](info)` where method is `reportPlaybackStart`, `reportPlaybackProgress`, or `reportPlaybackStopped` ([jellyfin-web `playbackmanager.js`](https://github.com/jellyfin/jellyfin-web/blob/master/src/components/playback/playbackmanager.js), observed around lines 77-102 in fetched source).
- Jellyfin Web `getPlayerState()` populates `VolumeLevel`, `IsMuted`, `IsPaused`, `RepeatMode`, `ShuffleMode`, `MaxStreamingBitrate`, `PositionTicks`, `PlaybackStartTimeTicks`, `PlaybackRate`, subtitle/audio stream indexes, `BufferedRanges`, `PlayMethod`, `LiveStreamId`, `PlaySessionId`, `PlaylistItemId`, `MediaSourceId`, and `CanSeek` ([jellyfin-web `playbackmanager.js`](https://github.com/jellyfin/jellyfin-web/blob/master/src/components/playback/playbackmanager.js), observed around lines 2154-2206 in fetched source).
- Jellyfin Web requests `PlaybackInfo` with `UserId`, `StartTimeTicks`, `IsPlayback`, `AutoOpenLiveStream`, stream indexes, direct play/stream/copy flags, `MediaSourceId`, `LiveStreamId`, `MaxStreamingBitrate`, `EnableMediaProbe`, `DirectPlayProtocols`, `AlwaysBurnInSubtitleWhenTranscoding`, and `DeviceProfile`; it calls generated SDK `getPostedPlaybackInfo({ itemId, playbackInfoDto: query })` ([jellyfin-web `playbackmanager.js`](https://github.com/jellyfin/jellyfin-web/blob/master/src/components/playback/playbackmanager.js), observed around lines 417-504 in fetched source).
- Jellyfin Web sends progress event names from player events as lower-case strings such as `timeupdate`, `pause`, `unpause`, `volumechange`, `repeatmodechange`, `shufflequeuemodechange`, and playlist changes ([jellyfin-web `playbackmanager.js`](https://github.com/jellyfin/jellyfin-web/blob/master/src/components/playback/playbackmanager.js), observed around lines 3562-3599 in fetched source). Jellyfin's current OpenAPI/model does not expose an `EventName` field, so this field is an observed web-client payload extra for Jellyfin.
- Emby webcomponents use the same high-level pattern: copy `state.PlayState`, set `ItemId`, set `EventName` when present, add playlist fields/queue, and call `apiClient[method](info).catch(...)` ([MediaBrowser/emby-webcomponents `playbackmanager.js`](https://github.com/MediaBrowser/emby-webcomponents/blob/master/playback/playbackmanager.js), observed around lines 45-80 in fetched source).
- Emby webcomponents `getPlayerState()` includes `PositionTicks`, stream indexes, `PlayMethod`, `LiveStreamId`, `PlaySessionId`, `MediaSourceId`, and `CanSeek` ([MediaBrowser/emby-webcomponents `playbackmanager.js`](https://github.com/MediaBrowser/emby-webcomponents/blob/master/playback/playbackmanager.js), observed around lines 2144-2205 in fetched source).
- Emby webcomponents request playback info with `CurrentPlaySessionId` when present, in addition to `UserId`, `StartTimeTicks`, `IsPlayback`, `AutoOpenLiveStream`, stream indexes, direct play/stream/copy flags, `MediaSourceId`, `LiveStreamId`, `MaxStreamingBitrate`, `EnableMediaProbe`, `DirectPlayProtocols`, and `DeviceProfile` ([MediaBrowser/emby-webcomponents `playbackmanager.js`](https://github.com/MediaBrowser/emby-webcomponents/blob/master/playback/playbackmanager.js), observed around lines 456-546 in fetched source).

### Emby API Surface

#### Authentication headers

Emby user-auth docs describe `X-Emby-Authorization` values such as `MediaBrowser Client="...", Device="...", DeviceId="...", Version="...", Token="..."`. Login returns `AccessToken`, and subsequent calls use `X-Emby-Token: <token>`. User-context playback progress should use the authenticated user token because the relevant endpoints require authentication as user.

Current OhMyCine matches this shape with:

```text
X-Emby-Token: <access token>
X-Emby-Authorization: MediaBrowser Client="OhMyCine Player", Device="Desktop", DeviceId="...", Version="0.1.0", Token="..."
```

implemented at `player/src/services/datasource/emby.ts:851-855`.

#### PlaybackInfo

| Endpoint | Method | Body/query shape | Response fields used for progress sync |
|---|---|---|---|
| `/Items/{Id}/PlaybackInfo` | `POST` | `PlaybackInfoRequest` body; key fields include `UserId`, `StartTimeTicks`, `AudioStreamIndex`, `SubtitleStreamIndex`, `MediaSourceId`, `LiveStreamId`, `DeviceProfile`, `EnableDirectPlay`, `EnableDirectStream`, `EnableTranscoding`, `AllowVideoStreamCopy`, `AllowAudioStreamCopy`, `IsPlayback`, `AutoOpenLiveStream`, `CurrentPlaySessionId`. | `PlaybackInfoResponse.MediaSources[]`, `PlaybackInfoResponse.PlaySessionId`. |

Notes:

- `MediaSourceId` is the media version identifier and is normally `MediaSourceInfo.Id` from the selected playback source.
- `PlaySessionId` is the session identifier returned by playback negotiation when `IsPlayback=true`; Emby web also passes `CurrentPlaySessionId` on later negotiations.
- `StartTimeTicks` lets the server know the requested resume/start position during playback negotiation; progress reporting still requires subsequent playstate calls.

#### Session body endpoints

| Event | Endpoint | Method | Body type | Fields that matter for resume/progress |
|---|---|---|---|---|
| Started | `/Sessions/Playing` | `POST` | `PlaybackStartInfo` | `ItemId`, `MediaSourceId`, `PlaySessionId`, `PositionTicks`, `CanSeek`, `IsPaused`, `PlayMethod`, optional stream indexes, queue/playlist fields, optional `EventName`. |
| Progress | `/Sessions/Playing/Progress` | `POST` | `PlaybackProgressInfo` | Same base progress fields as start; `PositionTicks` is the key persisted resume value; `EventName` can distinguish `TimeUpdate`, `Pause`, `Unpause`, etc. |
| Stopped | `/Sessions/Playing/Stopped` | `POST` | `PlaybackStopInfo` | `ItemId`, `MediaSourceId`, `PlaySessionId`, `PositionTicks`, optional `Failed`, `NextMediaType`, queue/playlist fields. |

Emby docs show these endpoints require authentication as user. The request body object is required in the docs, but individual schema properties are not marked as required in the scraped page. Practical client reports include at least stable identity (`ItemId`), selected media source/session (`MediaSourceId`, `PlaySessionId` when available), `PositionTicks`, and pause/play method state.

Emby `ProgressEvent` enum values in the official docs include:

```text
TimeUpdate, Pause, Unpause, VolumeChange, RepeatModeChange,
AudioTrackChange, SubtitleTrackChange, PlaylistItemMove,
PlaylistItemRemove, PlaylistItemAdd, QualityChange, StateChange,
SubtitleOffsetChange, PlaybackRateChange, ShuffleChange, SleepTimerChange
```

Emby `PlayMethod` values are:

```text
Transcode, DirectStream, DirectPlay
```

#### Older Emby user/item endpoints

| Event | Endpoint | Method | Required/important path/query/body fields |
|---|---|---|---|
| Started | `/Users/{UserId}/PlayingItems/{Id}` | `POST` | Path: `UserId`, `Id`; Emby docs mark query `MediaSourceId` required. Optional query: `CanSeek`, `AudioStreamIndex`, `SubtitleStreamIndex`, `PlayMethod`, `LiveStreamId`, `PlaySessionId`. |
| Progress | `/Users/{UserId}/PlayingItems/{Id}/Progress` | `POST` | Path: `UserId`, `Id`; Emby docs mark query `MediaSourceId` required. Optional query: `PositionTicks`, `IsPaused`, `IsMuted`, stream indexes, `VolumeLevel`, `PlayMethod`, `LiveStreamId`, `PlaySessionId`, `RepeatMode`, `SubtitleOffset`, `PlaybackRate`; body type `OnPlaybackProgress` contains event/queue-style fields including `EventName`. |
| Stopped | `/Users/{UserId}/PlayingItems/{Id}/Delete` | `POST` | Path: `UserId`, `Id`; Emby docs mark query `MediaSourceId` and `NextMediaType` required. Optional query: `PositionTicks`, `LiveStreamId`, `PlaySessionId`. |

These endpoints update the same playstate concepts but use query parameters and user/item paths. They are useful compatibility targets when session body endpoints do not update a particular server/client surface's continue-watching row.

#### Played/unplayed and resume endpoints

| Purpose | Endpoint | Method | Notes |
|---|---|---|---|
| Mark played | `/Users/{UserId}/PlayedItems/{Id}` | `POST` | Optional `DatePlayed`; returns `UserItemDataDto`. Sets played state and clears resume position. |
| Mark unplayed | `/Users/{UserId}/PlayedItems/{Id}` | `DELETE` | Returns `UserItemDataDto`; clears played status. |
| Continue watching | `/Users/{UserId}/Items/Resume` | `GET` | Returns resumable items, with `UserData.PlaybackPositionTicks` and `UserData.PlayedPercentage` when user data is included/available. |

### Jellyfin API Surface

The stable Jellyfin OpenAPI document queried was version `10.11.8` (`https://api.jellyfin.org/openapi/jellyfin-openapi-stable.json`); unstable queried was version `12.0.0`.

#### PlaybackInfo

| Endpoint | Method | Body/query shape | Response fields |
|---|---|---|---|
| `/Items/{itemId}/PlaybackInfo` | `GET` | Query only; `userId` accepted. | Playback media info. |
| `/Items/{itemId}/PlaybackInfo` | `POST` | Query accepts `userId`, `maxStreamingBitrate`, `startTimeTicks`, `audioStreamIndex`, `subtitleStreamIndex`, `maxAudioChannels`, `mediaSourceId`, `liveStreamId`, `autoOpenLiveStream`, `enableDirectPlay`, `enableDirectStream`, `enableTranscoding`, `allowVideoStreamCopy`, `allowAudioStreamCopy`; JSON body is `PlaybackInfoDto` with the same playback negotiation fields plus `DeviceProfile` and `AlwaysBurnInSubtitleWhenTranscoding`. | `PlaybackInfoResponse.MediaSources[]`, nullable `PlaySessionId`, nullable `ErrorCode`. |

Jellyfin `PlaybackInfoDto`, `PlaybackInfoResponse`, `PlaybackStartInfo`, `PlaybackProgressInfo`, and `PlaybackStopInfo` have no OpenAPI `required` arrays in the stable document, but path parameters such as `itemId` are required.

#### Session body endpoints

| Event | Endpoint | Method | Status | Body type | Fields in Jellyfin OpenAPI/model |
|---|---|---|---|---|---|
| Started | `/Sessions/Playing` | `POST` | 204 | `PlaybackStartInfo` | Inherits progress fields: `CanSeek`, `Item`, `ItemId`, `SessionId`, `MediaSourceId`, stream indexes, `IsPaused`, `IsMuted`, `PositionTicks`, `PlaybackStartTimeTicks`, `VolumeLevel`, `Brightness`, `AspectRatio`, `PlayMethod`, `LiveStreamId`, `PlaySessionId`, `RepeatMode`, `PlaybackOrder`, `NowPlayingQueue`, `PlaylistItemId`. |
| Progress | `/Sessions/Playing/Progress` | `POST` | 204 | `PlaybackProgressInfo` | Same progress fields. `PositionTicks` is saved to user data when present. |
| Stopped | `/Sessions/Playing/Stopped` | `POST` | 204 | `PlaybackStopInfo` | `Item`, `ItemId`, `SessionId`, `MediaSourceId`, `PositionTicks`, `LiveStreamId`, `PlaySessionId`, `Failed`, `NextMediaType`, `PlaylistItemId`, `NowPlayingQueue`. |
| Transcode/session liveness | `/Sessions/Playing/Ping` | `POST` | 204 | query only | Required query `playSessionId`. |

Jellyfin server source confirms these body endpoints set `SessionId` from the authenticated request session and call `_sessionManager.OnPlaybackStart`, `_sessionManager.OnPlaybackProgress`, and `_sessionManager.OnPlaybackStopped` (`Jellyfin.Api/Controllers/PlaystateController.cs` in upstream Jellyfin, observed around lines 201-258).

#### Jellyfin compatibility endpoints

Jellyfin current OpenAPI exposes userless compatibility paths and keeps user-prefixed paths as obsolete hidden routes in server source:

| Event | Current Jellyfin endpoint | Legacy hidden endpoint | Method | Query fields |
|---|---|---|---|---|
| Started | `/PlayingItems/{itemId}` | `/Users/{userId}/PlayingItems/{itemId}` | `POST` | `mediaSourceId`, `audioStreamIndex`, `subtitleStreamIndex`, `playMethod`, `liveStreamId`, `playSessionId`, `canSeek`. |
| Progress | `/PlayingItems/{itemId}/Progress` | `/Users/{userId}/PlayingItems/{itemId}/Progress` | `POST` | `mediaSourceId`, `positionTicks`, `audioStreamIndex`, `subtitleStreamIndex`, `volumeLevel`, `playMethod`, `liveStreamId`, `playSessionId`, `repeatMode`, `isPaused`, `isMuted`. |
| Stopped | `/PlayingItems/{itemId}` | `/Users/{userId}/PlayingItems/{itemId}` | `DELETE` | `mediaSourceId`, `nextMediaType`, `positionTicks`, `liveStreamId`, `playSessionId`. |

Jellyfin server source marks these as obsolete in favor of `/Sessions/Playing...`, constructs the corresponding `PlaybackStartInfo` / `PlaybackProgressInfo` / `PlaybackStopInfo`, fills the authenticated request `SessionId`, validates `PlayMethod`, and forwards to the same session manager (`Jellyfin.Api/Controllers/PlaystateController.cs`, observed around lines 275-503).

#### Jellyfin UserData, played/unplayed, and resume endpoints

| Purpose | Current Jellyfin endpoint | Legacy hidden endpoint | Method | Fields |
|---|---|---|---|---|
| Mark played | `/UserPlayedItems/{itemId}` | `/Users/{userId}/PlayedItems/{itemId}` | `POST` | Optional query `userId`, optional query `datePlayed`; returns `UserItemDataDto`. |
| Mark unplayed | `/UserPlayedItems/{itemId}` | `/Users/{userId}/PlayedItems/{itemId}` | `DELETE` | Optional query `userId`; returns `UserItemDataDto`. |
| Read resume | `/UserItems/Resume` | `/Users/{userId}/Items/Resume` | `GET` | Optional query `userId`, `startIndex`, `limit`, `searchTerm`, `parentId`, `fields`, `mediaTypes`, `enableUserData`, image options, include/exclude item types, `enableTotalRecordCount`, `enableImages`, `excludeActiveSessions`. |
| Update item user data directly | `/UserItems/{itemId}/UserData` | `/Users/{userId}/Items/{itemId}/UserData` | `POST` | Query `userId`; required body `UpdateUserItemDataDto` can set `PlaybackPositionTicks`, `PlayedPercentage`, `PlayCount`, `IsFavorite`, `Likes`, `LastPlayedDate`, `Played`, `Rating`, etc. |

Jellyfin `UserDataManager.SaveUserData(user, item, UpdateUserItemDataDto, reason)` directly copies nullable `PlaybackPositionTicks`, `PlayCount`, `IsFavorite`, `Likes`, `Played`, `LastPlayedDate`, and `Rating` from `UpdateUserItemDataDto` into stored `UserItemData` when present (`Emby.Server.Implementations/Library/UserDataManager.cs` in upstream Jellyfin, observed around lines 96-139).

### Why progress may not appear in Resume / Continue Watching

The following conditions were found in official docs, Jellyfin server source, and client patterns:

1. **No user data position was saved.** Jellyfin `SessionManager.OnPlaybackProgress()` only calls `UpdatePlayState()` and saves user data if `PositionTicks` is present; track-only changes can save stream selections but not resume position (`Emby.Server.Implementations/Session/SessionManager.cs`, observed around lines 944-967).
2. **The position is too close to the beginning.** Jellyfin `UserDataManager.UpdatePlayState()` compares position percentage with `MinResumePct`; if below that threshold, it sets `positionTicks = 0` and does not leave a resumable position (`Emby.Server.Implementations/Library/UserDataManager.cs`, observed around lines 315-331).
3. **The position is too close to the end or media is considered complete.** Jellyfin clears position and marks played when `pctIn > MaxResumePct` or `positionTicks >= runtimeTicks - 1 second`; stopped events without a position assume fully played and clear resume position (`Emby.Server.Implementations/Library/UserDataManager.cs`, observed around lines 332-347; `Emby.Server.Implementations/Session/SessionManager.cs`, observed around lines 1129-1153).
4. **The item does not support played/resume state.** Jellyfin clears position and played state when `!item.SupportsPlayedStatus`; start also marks non-position-resumable items played (`Emby.Server.Implementations/Library/UserDataManager.cs`, observed around lines 373-377; `Emby.Server.Implementations/Session/SessionManager.cs`, observed around lines 824-837).
5. **The item/runtime is not known to the server.** Jellyfin progress/stopped handling resolves `libraryItem` from the current session and `ItemId`; if the item is not found, no user data update occurs (`Emby.Server.Implementations/Session/SessionManager.cs`, observed around lines 883-907 and 1040-1102). Unknown runtime is treated as fully played in `UpdatePlayState()` (`Emby.Server.Implementations/Library/UserDataManager.cs`, observed around lines 366-370).
6. **The request is not tied to an authenticated session/user.** Session endpoints fill `SessionId` from the current authenticated request session; if no session is found, Jellyfin progress handling returns without saving (`Emby.Server.Implementations/Session/SessionManager.cs`, observed around lines 883-887). Emby/Jellyfin playstate endpoints require user authentication.
7. **Only `PlaybackInfo` was called.** `/Items/{id}/PlaybackInfo` negotiates media sources and can return `PlaySessionId`, but resume rows are created by playstate/userdata updates, not by playback negotiation alone.
8. **The server marks active sessions differently from resume rows.** Jellyfin `/UserItems/Resume` supports `excludeActiveSessions`; when true, currently playing items are excluded from the resume query (`Jellyfin.Api/Controllers/ItemsController.cs`, observed around lines 888-914). Emby/Jellyfin web UIs may also display active now-playing state separately from resume rows.
9. **Different API families have provider/version differences.** Emby docs expose `/Users/{UserId}/PlayingItems/{Id}/Delete` as POST with `MediaSourceId` and `NextMediaType` required; Jellyfin exposes current `/PlayingItems/{itemId}` DELETE and keeps `/Users/{userId}/PlayingItems/{itemId}` DELETE as an obsolete hidden route. A client using only one family may get 404/405 on the other product/version.
10. **EventName differs by product.** Emby documents `ProgressEvent` enum values such as `TimeUpdate`, `Pause`, and `Unpause`. Jellyfin current OpenAPI/model does not define `EventName`, although Jellyfin Web attaches lower-case event names to the JS payload before calling the API client. Resume persistence depends on `PositionTicks`/item/session identity, not on `EventName` alone.

### Related Specs

- `.trellis/spec/frontend/type-safety.md` — provider progress sync contract (`ProviderPlaybackProgressInput`, optional `DataSource.syncPlaybackProgress`, Emby-compatible endpoint list, best-effort/non-blocking requirements), local playback-history contract, token redaction rules.
- `docs/architecture/03-player-design.md` — Player independent-first design and DataSource abstraction.
- `docs/architecture/07-security-design.md` — Player credential storage and sensitive token/path redaction.

### External References

- [Emby REST API: `POST /Items/{Id}/PlaybackInfo`](https://dev.emby.media/reference/RestAPI/MediaInfoService/postItemsByIdPlaybackinfo.html) — official playback negotiation endpoint; response includes `MediaSources` and `PlaySessionId`.
- [Emby REST API: `POST /Sessions/Playing`](https://dev.emby.media/reference/RestAPI/PlaystateService/postSessionsPlaying.html) — session playback-start endpoint with `PlaybackStartInfo` body.
- [Emby REST API: `POST /Sessions/Playing/Progress`](https://dev.emby.media/reference/RestAPI/PlaystateService/postSessionsPlayingProgress.html) — session progress endpoint with `PlaybackProgressInfo` body and documented `ProgressEvent` enum.
- [Emby REST API: `POST /Sessions/Playing/Stopped`](https://dev.emby.media/reference/RestAPI/PlaystateService/postSessionsPlayingStopped.html) — session stopped endpoint with `PlaybackStopInfo` body.
- [Emby REST API: `POST /Users/{UserId}/PlayingItems/{Id}`](https://dev.emby.media/reference/RestAPI/PlaystateService/postUsersByUseridPlayingitemsById.html) — older user/item playback-start endpoint; docs mark `MediaSourceId` required.
- [Emby REST API: `POST /Users/{UserId}/PlayingItems/{Id}/Progress`](https://dev.emby.media/reference/RestAPI/PlaystateService/postUsersByUseridPlayingitemsByIdProgress.html) — older user/item progress endpoint; docs mark `MediaSourceId` required and include `PositionTicks`, `IsPaused`, `PlaySessionId`, and event body fields.
- [Emby REST API: `POST /Users/{UserId}/PlayingItems/{Id}/Delete`](https://dev.emby.media/reference/RestAPI/PlaystateService/postUsersByUseridPlayingitemsByIdDelete.html) — older user/item stopped endpoint; docs mark `MediaSourceId` and `NextMediaType` required.
- [Emby REST API: `GET /Users/{UserId}/Items/Resume`](https://dev.emby.media/reference/RestAPI/ItemsService/getUsersByUseridItemsResume.html) — continue-watching/resume query endpoint.
- [Emby REST API: `POST /Users/{UserId}/PlayedItems/{Id}`](https://dev.emby.media/reference/RestAPI/PlaystateService/postUsersByUseridPlayeditemsById.html) — explicit mark-played endpoint returning user item data.
- [Emby REST API: `DELETE /Users/{UserId}/PlayedItems/{Id}`](https://dev.emby.media/reference/RestAPI/PlaystateService/deleteUsersByUseridPlayeditemsById.html) — explicit mark-unplayed endpoint returning user item data.
- [Emby docs: User Authentication](https://dev.emby.media/doc/restapi/User-Authentication.html) — `X-Emby-Authorization`, `POST /Users/AuthenticateByName`, `AccessToken`, `X-Emby-Token`.
- [Jellyfin OpenAPI stable](https://api.jellyfin.org/openapi/jellyfin-openapi-stable.json) — official generated Jellyfin API schema; queried version `10.11.8`.
- [Jellyfin OpenAPI unstable](https://api.jellyfin.org/openapi/jellyfin-openapi-unstable.json) — queried version `12.0.0` for schema comparison.
- [Jellyfin Web playback manager](https://github.com/jellyfin/jellyfin-web/blob/master/src/components/playback/playbackmanager.js) — client pattern for `PlaybackInfo`, player-state fields, playback reports, and progress event names.
- [Emby webcomponents playback manager](https://github.com/MediaBrowser/emby-webcomponents/blob/master/playback/playbackmanager.js) — Emby client pattern for `PlaybackInfo`, player-state fields, playback reports, and event names.
- [Jellyfin `PlaystateController.cs`](https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/PlaystateController.cs) — server routes for `/Sessions/Playing...`, `/PlayingItems...`, legacy `/Users/{userId}/PlayingItems...`, mark played/unplayed, and forwarding to session manager.
- [Jellyfin `ItemsController.cs`](https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/ItemsController.cs) — `/UserItems/Resume`, legacy `/Users/{userId}/Items/Resume`, and `IsResumable=true` query construction.
- [Jellyfin `SessionManager.cs`](https://github.com/jellyfin/jellyfin/blob/master/Emby.Server.Implementations/Session/SessionManager.cs) — server persistence behavior for playback start/progress/stopped.
- [Jellyfin `UserDataManager.cs`](https://github.com/jellyfin/jellyfin/blob/master/Emby.Server.Implementations/Library/UserDataManager.cs) — `UpdateUserItemDataDto` persistence and resume threshold/completion rules.

## Caveats / Not Found

- No dedicated `player/src/services/datasource/jellyfin.ts` implementation was found in the current tree; Jellyfin behavior above is from official Jellyfin OpenAPI/source and open-source Jellyfin Web.
- Emby docs list both session-body and user/item endpoint families; Jellyfin current API exposes session-body and userless `/PlayingItems...` endpoints, with `/Users/{userId}/PlayingItems...` kept as obsolete hidden compatibility routes.
- Emby documents `EventName`/`ProgressEvent`; Jellyfin's current OpenAPI and `PlaybackProgressInfo` model do not define `EventName`, even though Jellyfin Web attaches it before dispatching through its API client.
- Official docs and source do not state that `PlaySessionId` is mandatory for resume persistence; observed client/server patterns use it for session/transcoding identity, while `ItemId` + authenticated session/user + `PositionTicks` are the persistence-critical fields.
- Resume/continue-watching visibility depends on server configuration thresholds (`MinResumePct`, `MaxResumePct`, `MinResumeDurationSeconds` in Jellyfin), media type capabilities, and whether the target UI filters active sessions or played items.
