# Research: Emby playback progress reporting API

- **Query**: Research Emby playback progress reporting APIs for implementing Player provider sync. Cover start/progress/stopped endpoints, required payload fields, auth/token handling, played/completed marking, and mapping to current OhMyCine Player constraints.
- **Scope**: mixed
- **Date**: 2026-05-15

## Findings

### Files Found

| File Path | Description |
|---|---|
| `player/src/services/datasource/emby.ts` | Current Emby DataSource implementation, authentication/token request helper, playback-info lookup, stream URL construction, and Emby item mapping. |
| `player/src/services/datasource/types.ts` | Current shared DataSource interface; no provider progress sync method exists yet. |
| `player/src/services/datasource/manager.ts` | DataSourceManager exposes configured sources by source id and aggregates home sections. |
| `player/src/services/playbackHistory.ts` | Local SQLite-backed playback progress service wrapper, identity shape, local completion heuristic, and redaction/sanitization. |
| `player/src/services/playbackContext.ts` | In-memory playback context carrying `itemId`, `sourceId`, optional `mediaSourceId`, tracks, and queue state into PlayerView. |
| `player/src/views/PlayerView.vue` | Current playback progress save timing: interval saves, pause force-save, route/media switch, queue switch, route leave/unmount, and beforeunload. |
| `player/src/services/datasource/errors.ts` | Shared redaction helper covering `api_key`, tokens, `X-Emby-Token`, and Emby authorization token strings. |
| `player/src/services/datasource/credentialStore.ts` | Credential reference and secure-storage fallback wrapper for Emby access token/username/password. |
| `player/src/stores/datasource.ts` | DataSource configs currently persist in `localStorage`; sensitive keys are stripped; local continue-watching is merged before provider sections. |
| `.trellis/spec/frontend/type-safety.md` | Frontend/DataSource contracts for Emby mapping, token redaction, stream URL handling, and local playback history. |
| `docs/architecture/03-player-design.md` | Player design: independent-first Player and DataSource abstraction. |
| `docs/architecture/07-security-design.md` | Security design: Player credential storage and sensitive-token handling. |
| `.trellis/tasks/05-15-player-emby-progress-sync-queue-panel/prd.md` | Task requirements for Emby progress sync and queue panel behavior. |

### Code Patterns

#### Current Emby authentication and request shape

- `EmbyDataSource.init()` resolves `userId`, `deviceId`, and access token, then marks the source connected only when all are present (`player/src/services/datasource/emby.ts:208-216`).
- Login uses `POST /Users/AuthenticateByName` with `Username` and `Pw`, plus `X-Emby-Authorization` (`player/src/services/datasource/emby.ts:225-243`).
- All Emby API calls go through `request()`, which sends both:
  - `X-Emby-Token: <token>`
  - `X-Emby-Authorization: Emby ... Token="<token>"`
  and redacts caught errors before surfacing them (`player/src/services/datasource/emby.ts:667-685`).
- Current redaction covers sensitive query keys including `api_key`, `access_token`, `token`, `x-emby-token`, and also `Token="..."`, bearer auth, and `X-Emby-Token` header text (`player/src/services/datasource/errors.ts:1-41`).

#### Current playback info / media source shape

- The current `EmbyPlaybackInfoResponse` only declares `MediaSources?: unknown[]` (`player/src/services/datasource/emby.ts:161-163`), but Emby official `PlaybackInfoResponse` also contains `PlaySessionId`.
- Current playback-info request is `POST /Items/{id}/PlaybackInfo` with `UserId`, direct-play/direct-stream/transcoding flags, and `IsPlayback: true` (`player/src/services/datasource/emby.ts:523-533`).
- `mapDetail()` extracts the first `MediaSources[].Id` as `mediaSourceId` and passes it into subtitle URL construction (`player/src/services/datasource/emby.ts:456-475`).
- `PlaybackMediaContext` already has optional `mediaSourceId` (`player/src/services/playbackContext.ts:22-40`), but `PlayerView.playQueueItemAt()` currently clears `mediaSourceId` when switching queue items (`player/src/views/PlayerView.vue:531-570`).

#### Current local progress lifecycle

- Local history payload includes `sourceId`, stable `mediaIdentity`, optional `itemId`, optional `libraryId`, title, redacted/stable `streamIdentity`, media type, artwork, position seconds, duration seconds, and local-completed flag (`player/src/views/PlayerView.vue:243-268`).
- Playback progress is saved locally every 10 seconds while playing (`HISTORY_SAVE_INTERVAL = 10000`) and force-saved on pause, media route switch, local file drop, route leave, unmount, and beforeunload (`player/src/views/PlayerView.vue:18-20`, `player/src/views/PlayerView.vue:280-327`, `player/src/views/PlayerView.vue:460-498`, `player/src/views/PlayerView.vue:683-695`, `player/src/views/PlayerView.vue:731-750`).
- Local completion uses `position >= duration * 0.92` or `duration - position <= 90` seconds (`player/src/services/playbackHistory.ts:41-44`, `player/src/services/playbackHistory.ts:93-98`).
- Local history intentionally treats provider progress as local-only today: `PlaybackProgressSource = 'local'`, `MediaItem.progressSource?: 'local'`, and the spec says local continue-watching must be identified separately from provider-native continue-watching (`player/src/services/playbackHistory.ts:5`, `player/src/services/datasource/types.ts:18-20`, `.trellis/spec/frontend/type-safety.md:130-185`).

#### DataSource abstraction constraints

- Current `DataSource` contract supports lifecycle, list/library/home/search/detail/stream URL, cache clearing, and config export; no progress-sync method exists (`player/src/services/datasource/types.ts:109-133`).
- Project docs define Player's DataSource abstraction as the common boundary for Emby/Jellyfin/OpenList/Alist/etc. with `list / search / getDetail / getStreamURL` (`docs/architecture/03-player-design.md:158-176`).
- Type-safety spec says DataSource contracts live under `services/datasource/types.ts`, external API responses are untrusted until parsed, and stream/tokenized URLs must not be displayed/logged/cached (`.trellis/spec/frontend/type-safety.md:13-18`, `.trellis/spec/frontend/type-safety.md:38-45`, `.trellis/spec/frontend/type-safety.md:46-90`).

### Emby Playback Progress API

Emby exposes two relevant groups under `PlaystateService`:

1. **Modern/session body endpoints** under `/Sessions/Playing...`.
2. **User/item query endpoints** under `/Users/{UserId}/PlayingItems...` plus explicit played/unplayed endpoints.

Both groups require authentication as user in the official docs.

#### Session playback reporting endpoints

| Event | Endpoint | Body type | Official purpose | Important fields |
|---|---|---|---|---|
| Playback started | `POST /Sessions/Playing` | `PlaybackStartInfo` | Reports playback has started within a session. | `ItemId`, `MediaSourceId`, `PositionTicks`, `PlaySessionId`, `CanSeek`, `IsPaused`, `PlayMethod`, optional stream indexes, queue/playlist fields. |
| Playback progress | `POST /Sessions/Playing/Progress` | `PlaybackProgressInfo` | Reports playback progress within a session. | Same broad `BaseProgressInfo` fields as start; include `ItemId`, `MediaSourceId`, `PositionTicks`, `PlaySessionId`, `IsPaused`, `EventName` such as `TimeUpdate`/`Pause`/`Unpause`, optional stream indexes. |
| Playback stopped | `POST /Sessions/Playing/Stopped` | `PlaybackStopInfo` | Reports playback has stopped within a session. | `ItemId`, `MediaSourceId`, `PositionTicks`, `PlaySessionId`, optional `Failed`, `IsAutomated`, `NextMediaType`. |

Notes from official schemas:

- The docs list a required request body for all three session endpoints; the schema itself does not mark individual properties as required.
- `PlaybackInfoResponse` from `/Items/{Id}/PlaybackInfo` includes `MediaSources` and `PlaySessionId`; this is the natural source for `PlaySessionId` when `IsPlayback=true` is used.
- `MediaSourceId` is described as the media version identifier.
- `PositionTicks` is Emby's tick position. Other Emby docs state `1ms = 10000 ticks`, so seconds map as `Math.round(seconds * 10_000_000)`.
- `ProgressEvent` enum values include `TimeUpdate`, `Pause`, `Unpause`, `VolumeChange`, `AudioTrackChange`, `SubtitleTrackChange`, `QualityChange`, `StateChange`, `PlaybackRateChange`, and others.

#### User/item playback reporting endpoints

| Event | Endpoint | Parameters | Official purpose |
|---|---|---|---|
| Playback started | `POST /Users/{UserId}/PlayingItems/{Id}` | Path: `UserId`, `Id`; query: required `MediaSourceId`, optional `CanSeek`, `AudioStreamIndex`, `SubtitleStreamIndex`, `PlayMethod`, `LiveStreamId`, `PlaySessionId`. | Reports that a user has begun playing an item. |
| Playback progress | `POST /Users/{UserId}/PlayingItems/{Id}/Progress` | Path: `UserId`, `Id`; query: required `MediaSourceId`, optional `PositionTicks`, `IsPaused`, `IsMuted`, stream indexes, `VolumeLevel`, `PlayMethod`, `LiveStreamId`, `PlaySessionId`, `RepeatMode`, `SubtitleOffset`, `PlaybackRate`; body: `OnPlaybackProgress` with optional queue/event fields. | Reports a user's playback progress. |
| Playback stopped | `POST /Users/{UserId}/PlayingItems/{Id}/Delete` | Path: `UserId`, `Id`; query: required `MediaSourceId`, required `NextMediaType`, optional `PositionTicks`, `LiveStreamId`, `PlaySessionId`. | Reports that a user has stopped playing an item. |

Notes:

- These endpoints make `MediaSourceId` required in docs for start/progress/stop.
- The progress endpoint accepts `PositionTicks` as optional but this field is needed for resume progress synchronization.
- The stopped endpoint's docs require `NextMediaType`; for a final stop where nothing follows, clients commonly send an empty string or omit only if the server accepts it, but the official table marks it required.
- These endpoints are narrower than the session body endpoints and can be called without constructing the full `PlaybackStartInfo`/`PlaybackProgressInfo` body, but they are older-looking and duplicate session endpoint behavior.

#### Explicit played/unplayed endpoints

| Action | Endpoint | Parameters | Response |
|---|---|---|---|
| Mark played | `POST /Users/{UserId}/PlayedItems/{Id}` | Path: `UserId`, `Id`; optional query `DatePlayed` formatted `yyyyMMddHHmmss`. | `UserItemDataDto` with `Played`, `PlaybackPositionTicks`, `PlayedPercentage`, `PlayCount`, `LastPlayedDate`, etc. |
| Mark unplayed | `DELETE /Users/{UserId}/PlayedItems/{Id}` | Path: `UserId`, `Id`. | `UserItemDataDto`. |

How this relates to completion:

- Normal clients report start/progress/stopped and let Emby update `UserData.PlaybackPositionTicks`, `PlayedPercentage`, resume state, and watched status according to server-side rules.
- If the Player has a local completed decision and needs an explicit server-side watched state, Emby provides `POST /Users/{UserId}/PlayedItems/{Id}`.
- The current OhMyCine local completion rule is independent and local: 92% played or <=90 seconds remaining (`player/src/services/playbackHistory.ts:41-44`, `player/src/services/playbackHistory.ts:93-98`). Mapping that local completion to Emby can use either the stopped/progress event at near-end or the explicit mark-played endpoint, depending on implementation choice.

### Auth / Token Handling

Official Emby user-auth docs:

- Clients add an Emby authorization header on every request with values like `UserId`, `Client`, `Device`, `DeviceId`, and `Version`.
- User login is `POST /Users/AuthenticateByName` with password in the body (`pw`/`Pw` in practice); the returned `AccessToken` is included in subsequent requests using the `X-Emby-Token` header.
- If requests fail with HTTP 401, the access token has generally been revoked and the user should be redirected/re-authenticated.
- API-key auth can also use `X-Emby-Token` or `api_key` query parameter, but user-context playback progress should use the authenticated user token because the endpoints require authentication as user and include `UserId`.

Current OhMyCine alignment:

- `EmbyDataSource` already follows the user-token path and sends `X-Emby-Token` plus `X-Emby-Authorization` from the shared `request()` helper (`player/src/services/datasource/emby.ts:667-685`).
- Tokenized stream/image URLs already exist for playback and artwork, but specs require passing them only to playback/rendering and redacting them from UI/logs/cache (`.trellis/spec/frontend/type-safety.md:65-72`, `.trellis/spec/frontend/type-safety.md:85-90`).
- Credential storage uses a credential ref and secure Tauri command path with in-memory fallback if persistent secure storage is unavailable (`player/src/services/datasource/credentialStore.ts:1-130`).
- Security docs require Player credentials to use OS keychain/secure storage where available and ordinary config to contain credential references instead of raw secrets (`docs/architecture/07-security-design.md:169-204`).

### Mapping to OhMyCine Player Constraints

#### Data needed by Emby sync

Minimum Emby-side sync payload can be derived from current structures if the selected media source metadata is preserved:

| Emby field | Current OhMyCine source |
|---|---|
| `ItemId` / path `{Id}` | `PlaybackProgressUpsert.itemId`, `PlaybackMediaContext.itemId`, route `itemId`, or queue item `id`. |
| `MediaSourceId` | `PlaybackMediaContext.mediaSourceId`, route `mediaSourceId`, or `MediaDetail.mediaSources[0].id` / first `item.MediaSources[].Id` in Emby detail. Current queue switch clears route `mediaSourceId`. |
| `PositionTicks` | Current Player seconds from `currentTime`; convert with `seconds * 10_000_000`. |
| `PlaySessionId` | Officially returned by `/Items/{Id}/PlaybackInfo`; current `EmbyPlaybackInfoResponse` does not retain this field. |
| `IsPaused` / event type | Current `isPlaying` watcher and pause handler can distinguish playing vs paused; interval saves are time updates. |
| `PlayMethod` | Current stream resolution may direct-play/direct-stream/transcode but does not expose a normalized play method in shared types. |
| `AudioStreamIndex` / `SubtitleStreamIndex` | Current `PlayerView` tracks selected streams through `useMpv`, while Emby detail maps stream indexes for tracks. |
| `RunTimeTicks` | Current `duration` seconds or Emby `RunTimeTicks` mapped to `MediaItem.duration`. |

#### Local SQLite remains primary

- Task PRD states Emby sync failure must not block playback or local SQLite history (`.trellis/tasks/05-15-player-emby-progress-sync-queue-panel/prd.md:26-31`).
- Current frontend wrapper already treats local persistence failures as non-fatal (`player/src/services/playbackHistory.ts:46-68`).
- The spec states Player playback history must use Tauri app-data SQLite rather than browser `localStorage`, remote-provider identity must prefer stable source/item identity over tokenized playback URLs, and playback must continue if progress persistence fails (`.trellis/spec/frontend/type-safety.md:130-185`).

#### Provider-native continue watching and local continue watching are distinct

- Emby continue-watching is currently fetched from `/Users/{UserId}/Items/Resume` and mapped into provider items (`player/src/services/datasource/emby.ts:324-327`, `player/src/services/datasource/emby.ts:423-430`).
- Local continue-watching is separately merged as `本机继续观看` before provider sections (`player/src/stores/datasource.ts:116-135`).
- Existing `MediaItem.progressSource` only has `'local'`, so provider progress/watched state should not be mislabeled as local progress unless the DataSource contract is extended.

### External References

- [Emby REST API: `postSessionsPlaying`](https://dev.emby.media/reference/RestAPI/PlaystateService/postSessionsPlaying.html) — official session playback-start endpoint, body type `PlaybackStartInfo`, requires user authentication.
- [Emby REST API: `postSessionsPlayingProgress`](https://dev.emby.media/reference/RestAPI/PlaystateService/postSessionsPlayingProgress.html) — official session playback-progress endpoint, body type `PlaybackProgressInfo`, requires user authentication.
- [Emby REST API: `postSessionsPlayingStopped`](https://dev.emby.media/reference/RestAPI/PlaystateService/postSessionsPlayingStopped.html) — official session playback-stopped endpoint, body type `PlaybackStopInfo`, requires user authentication.
- [Emby REST API: `postUsersByUseridPlayingitemsById`](https://dev.emby.media/reference/RestAPI/PlaystateService/postUsersByUseridPlayingitemsById.html) — user/item playback-start endpoint with required `MediaSourceId` query parameter.
- [Emby REST API: `postUsersByUseridPlayingitemsByIdProgress`](https://dev.emby.media/reference/RestAPI/PlaystateService/postUsersByUseridPlayingitemsByIdProgress.html) — user/item progress endpoint with `PositionTicks`, `IsPaused`, `PlaySessionId`, and stream/playback query fields.
- [Emby REST API: `postUsersByUseridPlayingitemsByIdDelete`](https://dev.emby.media/reference/RestAPI/PlaystateService/postUsersByUseridPlayingitemsByIdDelete.html) — user/item stopped endpoint with `PositionTicks`, `PlaySessionId`, required `MediaSourceId`, and required `NextMediaType` in docs.
- [Emby REST API: `postUsersByUseridPlayeditemsById`](https://dev.emby.media/reference/RestAPI/PlaystateService/postUsersByUseridPlayeditemsById.html) — explicit mark-played endpoint returning `UserItemDataDto`.
- [Emby REST API: `deleteUsersByUseridPlayeditemsById`](https://dev.emby.media/reference/RestAPI/PlaystateService/deleteUsersByUseridPlayeditemsById.html) — explicit mark-unplayed endpoint returning `UserItemDataDto`.
- [Emby REST API: `postItemsByIdPlaybackinfo`](https://dev.emby.media/reference/RestAPI/MediaInfoService/postItemsByIdPlaybackinfo.html) — playback-info endpoint whose response includes `MediaSources` and `PlaySessionId`; current code already calls this endpoint with `IsPlayback=true`.
- [Emby docs: User Authentication](https://dev.emby.media/doc/restapi/User-Authentication.html) — `X-Emby-Authorization`, `POST /Users/AuthenticateByName`, `AccessToken`, `X-Emby-Token`, 401 handling.
- [Emby docs: API-Key Authentication](https://dev.emby.media/doc/restapi/API-Key-Authentication.html) — `X-Emby-Token` or `api_key` auth mechanisms for static API keys; user playback sync should retain user-token context.

### Related Specs

- `.trellis/spec/frontend/type-safety.md` — DataSource contracts, Emby mapping contract, tokenized stream/artwork redaction, and playback-history SQLite contract.
- `docs/architecture/03-player-design.md` — Player independent-first architecture and DataSource abstraction.
- `docs/architecture/07-security-design.md` — Player credential storage and sensitive-token handling.
- `.trellis/tasks/05-15-player-emby-progress-sync-queue-panel/prd.md` — current task requirements and acceptance criteria.

## Caveats / Not Found

- Official Emby docs for session body endpoints list a required body object but do not mark individual `PlaybackStartInfo`/`PlaybackProgressInfo`/`PlaybackStopInfo` fields as required. The practical minimum for useful sync is `ItemId`, `MediaSourceId`, `PositionTicks`, and the `PlaySessionId` from playback-info when available.
- Official docs expose both `/Sessions/Playing...` and `/Users/{UserId}/PlayingItems...` paths. This research did not verify server-version-specific differences against a live Emby instance.
- The current code does not retain `PlaySessionId` from `/Items/{Id}/PlaybackInfo`, and queue switching currently clears `mediaSourceId`; implementation will need to preserve or re-fetch these values before reporting progress.
- No existing OhMyCine Player provider-progress method was found in the DataSource interface or manager.
