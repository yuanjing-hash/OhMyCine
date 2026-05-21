# Research: Emby/Jellyfin client playback reporting source

- **Query**: Research how real Emby/Jellyfin clients implement playback reporting so OhMyCine can fix active/current playing and cloud resume sync.
- **Scope**: mixed internal/external source-code research
- **Date**: 2026-05-21

## Findings

### Files Found

| File Path | Description |
|---|---|
| `player/src/services/datasource/emby.ts` | OhMyCine Emby datasource: PlaybackInfo negotiation, session cache, `/Sessions/Playing*` reports, legacy user-item fallback, auth headers. |
| `.trellis/spec/frontend/type-safety.md` | Existing project contract for provider playback sync and Emby-compatible endpoint expectations. |
| `https://github.com/jellyfin/jellyfin-web/blob/master/src/components/playback/playbackmanager.js` | Jellyfin Web playback negotiation and reporting source. |
| `https://github.com/jellyfin/jellyfin-androidtv/blob/master/playback/jellyfin/src/main/kotlin/mediastream/JellyfinMediaStreamResolver.kt` | Jellyfin Android TV rewritten playback stream resolver; obtains `PlaybackInfoResponse.playSessionId`. |
| `https://github.com/jellyfin/jellyfin-androidtv/blob/master/playback/jellyfin/src/main/kotlin/playsession/PlaySessionService.kt` | Jellyfin Android TV rewritten playback reporting service. |
| `https://github.com/jellyfin/jellyfin-androidtv/blob/master/app/src/main/java/org/jellyfin/androidtv/util/apiclient/ReportingHelper.kt` | Jellyfin Android TV legacy reporting helper with explicit `mediaSourceId`, `liveStreamId`, and `playSessionId`. |
| `https://github.com/MediaBrowser/Emby.ApiClient.Javascript/blob/master/apiclient.js` | Emby official JavaScript API client: auth header format, PlaybackInfo POST, Sessions playback reports. |
| `https://github.com/MediaBrowser/Emby.ApiClient/blob/master/Emby.ApiClient/Playback/PlaybackManager.cs` | Emby official C# playback manager: obtains PlaySessionId from PlaybackInfo and applies it to progress/stopped. |
| `https://github.com/MediaBrowser/Emby.ApiClient/blob/master/Emby.ApiClient/ApiClient.cs` | Emby official C# API client: Sessions playback endpoint calls and PlaybackInfo call. |
| `https://github.com/MediaBrowser/Emby.ApiClient/blob/master/Emby.ApiClient/BaseApiClient.cs` | Emby official C# auth/session header construction. |
| `https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/MediaInfoController.cs` | Jellyfin server API implementation for `/Items/{itemId}/PlaybackInfo`. |
| `https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Helpers/MediaInfoHelper.cs` | Jellyfin server code that creates `PlaybackInfoResponse.PlaySessionId`. |
| `https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/PlaystateController.cs` | Jellyfin server implementation for `/Sessions/Playing*` and obsolete `/PlayingItems*` endpoints. |
| `https://github.com/jellyfin/jellyfin-sdk-typescript/blob/master/src/generated-client/models/playback-info-dto.ts` | Jellyfin TypeScript SDK generated `PlaybackInfoDto` fields. |
| `https://github.com/jellyfin/jellyfin-sdk-typescript/blob/master/src/generated-client/models/playback-start-info.ts` | Jellyfin TypeScript SDK generated `PlaybackStartInfo` fields. |
| `https://github.com/jellyfin/jellyfin-sdk-typescript/blob/master/src/generated-client/models/playback-progress-info.ts` | Jellyfin TypeScript SDK generated `PlaybackProgressInfo` fields. |
| `https://github.com/jellyfin/jellyfin-sdk-typescript/blob/master/src/generated-client/models/playback-stop-info.ts` | Jellyfin TypeScript SDK generated `PlaybackStopInfo` fields. |

### Code Patterns

#### 1. PlaybackInfo request shape

Jellyfin Web builds a single `query` object and posts it as `playbackInfoDto` to `/Items/{itemId}/PlaybackInfo`:

- `src/components/playback/playbackmanager.js:438-441`: `UserId` and `StartTimeTicks`.
- `playbackmanager.js:446-452`: `IsPlayback` and `AutoOpenLiveStream` toggled by call context.
- `playbackmanager.js:454-483`: optional `AudioStreamIndex`, `SubtitleStreamIndex`, `SecondarySubtitleStreamIndex`, `EnableDirectPlay`, `EnableDirectStream`, `AllowVideoStreamCopy`, `AllowAudioStreamCopy`, `MediaSourceId`, `LiveStreamId`, `MaxStreamingBitrate`.
- `playbackmanager.js:495-501`: `DirectPlayProtocols` from player and `DeviceProfile` in the posted DTO.
- `playbackmanager.js:503`: `mediaInfoApi.getPostedPlaybackInfo({ itemId, playbackInfoDto: query })`.

Jellyfin Android TV rewritten resolver posts `PlaybackInfoDto` with the fields it needs for local player stream choice:

- `JellyfinMediaStreamResolver.kt:86-98`: `itemId`, `mediaSourceId`, `deviceProfile`, `enableDirectPlay=true`, `enableDirectStream=true`, `enableTranscoding=true`, `allowVideoStreamCopy=true`, `allowAudioStreamCopy=true`, `autoOpenLiveStream=false`.
- It then stores `response.playSessionId.orEmpty()` into its `MediaInfo` and playable stream identifier at `JellyfinMediaStreamResolver.kt:114-126`.

Jellyfin server accepts both query parameters and body fields for the POST endpoint, but marks query parameters obsolete:

- `MediaInfoController.cs:119-135`: route and obsolete query parameters for `userId`, `maxStreamingBitrate`, `startTimeTicks`, audio/subtitle indexes, `mediaSourceId`, `liveStreamId`, `autoOpenLiveStream`, direct/transcode flags, and body `PlaybackInfoDto`.
- `MediaInfoController.cs:149-166`: copies body fields to query variables when query variables are absent.
- `MediaInfoController.cs:189-213`: if a `DeviceProfile` is present, applies device-specific data using bitrate, start ticks, media source, stream indexes, enabled flags, copy flags, and generated `info.PlaySessionId`.
- `MediaInfoController.cs:219-244`: if `autoOpenLiveStream=true`, opens media source with `PlaySessionId = info.PlaySessionId`.

Jellyfin generated SDK `PlaybackInfoDto` fields are:

- `UserId`, `MaxStreamingBitrate`, `StartTimeTicks`, `AudioStreamIndex`, `SubtitleStreamIndex`, `MaxAudioChannels`, `MediaSourceId`, `LiveStreamId`, `DeviceProfile`, `EnableDirectPlay`, `EnableDirectStream`, `EnableTranscoding`, `AllowVideoStreamCopy`, `AllowAudioStreamCopy`, `AutoOpenLiveStream`, `AlwaysBurnInSubtitleWhenTranscoding` (`playback-info-dto.ts:22-84`).
- No generated Jellyfin `PlaybackInfoDto` field for `CurrentPlaySessionId`; searches in Jellyfin Web/server/SDK/AndroidTV and Emby public API clients did not find source usage of `CurrentPlaySessionId`.

Emby official JavaScript API client has a simpler wrapper:

- `apiclient.js:1889-1901`: `POST Items/{itemId}/PlaybackInfo` with query `options`, JSON body `{ DeviceProfile: deviceProfile }`, content type JSON.

Emby official C# API client is older/simpler:

- `PlaybackManager.cs:251-260`: calls `apiClient.GetPlaybackInfo(new PlaybackInfoRequest { Id, UserId, MaxStreamingBitrate, MediaSourceId, AudioStreamIndex, SubtitleStreamIndex })`.
- `ApiClient.cs:2605-2616`: `GetPlaybackInfo` only adds `UserId` to query and deserializes `PlaybackInfoResponse`.

OhMyCine current shape in `player/src/services/datasource/emby.ts`:

- `emby.ts:821-855`: sends query and body with `UserId`, `StartTimeTicks`, `MediaSourceId`, `CurrentPlaySessionId`, `MaxStreamingBitrate`, `EnableDirectPlay`, `EnableDirectStream`, `EnableTranscoding`, copy flags, `DirectPlayProtocols`, `AutoOpenLiveStream`, `IsPlayback`, and body `DeviceProfile`.
- `emby.ts:1528-1587`: `DeviceProfile` includes `Name`, `Id`, max bitrates, audio/video `DirectPlayProfiles`, video/audio `TranscodingProfiles`, empty container/codec/response profiles, and subtitle profiles.

#### 2. How clients obtain/store PlaySessionId and when it may be absent

Jellyfin server creates the play session id in `MediaInfoHelper`:

- `MediaInfoHelper.cs:119-124`: if no media sources are available, returns empty `MediaSources` and `PlaybackErrorCode.NoCompatibleStream`.
- `MediaInfoHelper.cs:127-142`: when media sources exist, clones them into `result.MediaSources` and sets `result.PlaySessionId = Guid.NewGuid().ToString("N")`.

Implication from source: for Jellyfin, `PlaySessionId` should be present when `PlaybackInfoResponse` has compatible media sources; absence is expected when media sources are empty/error, or when a non-server/offline/local path bypasses server PlaybackInfo.

Client storage patterns:

- Jellyfin Android TV rewritten resolver explicitly stores `response.playSessionId.orEmpty()` into `MediaInfo` (`JellyfinMediaStreamResolver.kt:114-117`) and uses it as `PlayableMediaStream.identifier` (`JellyfinMediaStreamResolver.kt:124-126`). `PlaySessionService` later reports `playSessionId = stream.identifier` (`PlaySessionService.kt:75-79`, `105-109`, `135-139`).
- Emby C# stores `playbackInfo.PlaySessionId` into local `streamInfo.PlaySessionId` after `GetPlaybackInfo` (`PlaybackManager.cs:251-268`, `285-289`) and injects it into progress/stopped reports (`PlaybackManager.cs:383-397`, `411-443`).
- Jellyfin Web creates `streamInfo.playSessionId` by parsing `playSessionId` from the selected media URL (`playbackmanager.js:2886-2900`), and then `self.playSessionId(player)` reads it from `streamInfo` (`playbackmanager.js:764-774`). For direct static stream URLs it builds locally (`playbackmanager.js:2841-2859`), no `playSessionId` parameter is added by that snippet; Jellyfin session endpoints therefore tolerate absent `PlaySessionId` for at least some direct-play paths.
- OhMyCine caches `mediaSourceId` and `playSessionId` in an in-memory `playbackSessions` map (`emby.ts:220`, `888-900`) and parses `PlaybackInfoResponse.PlaySessionId` (`emby.ts:1401-1409`).

#### 3. `/Sessions/Playing`, `/Sessions/Playing/Progress`, `/Sessions/Playing/Stopped` payloads

Real clients use modern session endpoints, not `/Users/{UserId}/PlayingItems*`, for normal playback reporting.

Emby JavaScript API client:

- `apiclient.js:4253-4269`: `reportPlaybackStart(options)` posts JSON `options` to `Sessions/Playing`.
- `apiclient.js:4277-4320`: `reportPlaybackProgress(options)` posts JSON `options` to `Sessions/Playing/Progress`, throttling ordinary `timeupdate` reports to about 10 seconds unless position jump is large.
- `apiclient.js:4404-4420`: `reportPlaybackStopped(options)` posts JSON `options` to `Sessions/Playing/Stopped`.

Jellyfin Web constructs the report body from `state.PlayState` and adds `ItemId`:

- `playbackmanager.js:77-98`: `info = Object.assign({}, state.PlayState)`, `info.ItemId = state.NowPlayingItem.Id`, optional `EventName`, optional playlist queue, then `apiClient[method](info)`.
- `playbackmanager.js:2168-2199`: `state.PlayState` includes `VolumeLevel`, `IsMuted`, `IsPaused`, `RepeatMode`, `ShuffleMode`, `MaxStreamingBitrate`, `PositionTicks`, `PlaybackStartTimeTicks`, `PlaybackRate`, stream indexes, buffered ranges, `PlayMethod`, `LiveStreamId`, `PlaySessionId`, `PlaylistItemId`, and `MediaSourceId`.
- `playbackmanager.js:3291-3294`: start report calls `reportPlaybackStart`.
- `playbackmanager.js:3675-3689`: progress report calls `reportPlaybackProgress`; Jellyfin Web's periodic timer is 10 seconds (`playbackmanager.js:3246-3255`).
- `playbackmanager.js:3457-3474`: stopped report adds `NextMediaType`, marks stream ended, then calls `reportPlaybackStopped`.

Jellyfin generated SDK models define the payload fields:

- `PlaybackStartInfo` (`playback-start-info.ts:34-92`): `CanSeek`, `Item`, `ItemId`, `SessionId`, `MediaSourceId`, `AudioStreamIndex`, `SubtitleStreamIndex`, `IsPaused`, `IsMuted`, `PositionTicks`, `PlaybackStartTimeTicks`, `VolumeLevel`, `Brightness`, `AspectRatio`, `PlayMethod`, `LiveStreamId`, `PlaySessionId`, `RepeatMode`, `PlaybackOrder`, `NowPlayingQueue`, `PlaylistItemId`.
- `PlaybackProgressInfo` (`playback-progress-info.ts:34-92`): same shape as start, but progress reports may also carry an event name in JavaScript clients; Jellyfin Web adds `EventName` before calling API client (`playbackmanager.js:88-90`).
- `PlaybackStopInfo` (`playback-stop-info.ts:25-58`): `Item`, `ItemId`, `SessionId`, `MediaSourceId`, `PositionTicks`, `LiveStreamId`, `PlaySessionId`, `Failed`, `NextMediaType`, `PlaylistItemId`, `NowPlayingQueue`.

Jellyfin server endpoint behavior:

- `/Sessions/Playing`: `ReportPlaybackStart` validates play method, obtains `SessionId` from the authenticated HTTP context, calls `_sessionManager.OnPlaybackStart`, returns 204 (`PlaystateController.cs:201-208`).
- `/Sessions/Playing/Progress`: same pattern for progress (`PlaystateController.cs:217-224`).
- `/Sessions/Playing/Stopped`: if `PlaySessionId` is non-empty, kills transcoding jobs for current device/session; then obtains `SessionId`, calls `_sessionManager.OnPlaybackStopped`, returns 204 (`PlaystateController.cs:247-259`).
- `ValidatePlayMethod` downgrades `Transcode` to `DirectPlay` when there is no transcode job for the supplied/absent play session id (`PlaystateController.cs:527-535`).

OhMyCine current report body:

- `emby.ts:491-506`: sends `ItemId`, `MediaSourceId`, `PlaySessionId`, `PositionTicks`, `RunTimeTicks`, `PlaybackStartTimeTicks`, `CanSeek`, `IsPaused`, `IsMuted`, `PlayMethod='DirectPlay'`, `EventName`, `PlaybackRate`, `RepeatMode`, `VolumeLevel`.
- `emby.ts:483-489`: currently skips session endpoints entirely when `PlaySessionId` is missing.

#### 4. Legacy `/Users/{UserId}/PlayingItems/{Id}(/Progress/Delete)` validity

Jellyfin server keeps legacy user-item routes, but marks them obsolete and hidden from API explorer:

- Start: `POST /Users/{userId}/PlayingItems/{itemId}` is obsolete, passes through to session start (`PlaystateController.cs:321-336`). Parameters are query fields: `mediaSourceId`, `audioStreamIndex`, `subtitleStreamIndex`, `playMethod`, `liveStreamId`, `playSessionId`, `canSeek`.
- Progress: `POST /Users/{userId}/PlayingItems/{itemId}/Progress` is obsolete, passes through to session progress (`PlaystateController.cs:413-432`). Query fields include `mediaSourceId`, `positionTicks`, stream indexes, `volumeLevel`, `playMethod`, `liveStreamId`, `playSessionId`, `repeatMode`, `isPaused`, `isMuted`.
- Stop: Jellyfin uses `DELETE /Users/{userId}/PlayingItems/{itemId}`, not `POST .../Delete` (`PlaystateController.cs:490-503`). Query fields include `mediaSourceId`, `nextMediaType`, `positionTicks`, `liveStreamId`, `playSessionId`.
- Non-user legacy routes also exist: `POST /PlayingItems/{itemId}`, `POST /PlayingItems/{itemId}/Progress`, `DELETE /PlayingItems/{itemId}` (`PlaystateController.cs:275-304`, `355-392`, `445-475`).

Jellyfin source signatures do not require `PlaySessionId` for legacy start/progress/stop; `playSessionId` is nullable query input (`PlaystateController.cs:286-287`, `368`, `428`, `501-502`). Missing play session id should not alone cause 400 in current Jellyfin server source for these routes. A 400 from Emby when omitting `PlaySessionId` therefore points to Emby-specific stricter validation, an unsupported method/path, missing required item/session context, or request shape mismatch.

Emby public client source found in this research does not use legacy `/Users/{UserId}/PlayingItems*` for playback reporting. Emby JavaScript and C# clients use only `/Sessions/Playing`, `/Sessions/Playing/Progress`, and `/Sessions/Playing/Stopped` for online playback reports (`apiclient.js:4253-4320`, `4404-4420`; `ApiClient.cs:1050-1102`).

OhMyCine current legacy fallback differs from Jellyfin server source for stop:

- `emby.ts:565-570`: sends `POST /Users/{UserId}/PlayingItems/{Id}/Delete` with `NextMediaType`.
- Jellyfin source expects `DELETE /Users/{userId}/PlayingItems/{itemId}` and has no `/Delete` route.

#### 5. Authorization/session headers and active session identity

Emby JavaScript API client auth header source:

- `apiclient.js:680-699`: auth values include `Device`, `DeviceId`, and `Version`.
- `apiclient.js:701-706`: token is included as `Token="..."` unless split into auth values.
- `apiclient.js:723-727`: sends `X-Emby-Authorization: MediaBrowser Client="...", Device="...", DeviceId="...", Version="...", Token="..."`.
- It can also place `X-Emby-Token` into query/auth values (`apiclient.js:701-703`) depending on auth mode.

Emby C# API client auth/session identity:

- `BaseApiClient.cs:167-192`: authorization scheme is `MediaBrowser`; parameter contains `Client`, `DeviceId`, `Device`, `Version`, and, if known, `UserId`.
- `BaseApiClient.cs:228-242`: sets the standard `Authorization` header to `MediaBrowser <parameters>`.
- `HttpHeaders.cs:22-31`: sets token header `X-MediaBrowser-Token` when an access token exists.

Jellyfin TypeScript SDK generated clients use an `Authorization` API key header for generated API calls (`media-info-api.ts:196-198`, `session-api.ts` repeated generated endpoints). Jellyfin Web uses the configured API client/SDK wrapper to provide that auth context.

Jellyfin server session reporting obtains the active server session id from the authenticated HTTP context, not from a `SessionId` sent by the client:

- `PlaystateController.cs:205-207`, `221-223`, and `257-258` call `RequestHelpers.GetSessionId(_sessionManager, _userManager, HttpContext)` and assign `playback*Info.SessionId` before invoking the session manager.
- This means stable `Client`, `Device`, `DeviceId`, `Version`, token/user context, and consistent device id are important for the server admin “active/current playing” session to attach to the expected device session.

OhMyCine current auth:

- `emby.ts:1070-1075`: sends `X-Emby-Token` and `X-Emby-Authorization` with `authorizationHeader`.
- `emby.ts:1372-1375`: header format is `MediaBrowser Client="OhMyCine Player", Device="Desktop", DeviceId="...", Version="0.1.0"` plus token when provided.
- It does not send `Authorization: MediaBrowser ...` or `X-MediaBrowser-Token`, and it does not include `UserId` in the auth parameter. Source clients show these alternate forms are used by official Emby C# and Jellyfin generated SDKs; Emby JS source shows `X-Emby-Authorization` is also a real official path.

#### 6. Concrete OhMyCine file-level implications for `player/src/services/datasource/emby.ts`

The following are implementation notes grounded in the source patterns above:

1. **Prefer modern session endpoints as the primary path.** Real Emby/Jellyfin clients report online playback through `/Sessions/Playing`, `/Sessions/Playing/Progress`, and `/Sessions/Playing/Stopped`, not user-item legacy routes (`apiclient.js:4253-4320`, `4404-4420`; `ApiClient.cs:1050-1102`; Jellyfin Web `playbackmanager.js:77-98`).

2. **Do not make Jellyfin behavior depend on `PlaySessionId` for direct play.** Jellyfin server source accepts nullable `PlaySessionId` in playback payload models and validates `PlayMethod` by downgrading only missing transcode sessions (`PlaystateController.cs:527-535`). Jellyfin Web can produce empty `streamInfo.playSessionId` for some locally built static direct stream URLs (`playbackmanager.js:2841-2859`, `2886-2900`) and still reports through session endpoints. OhMyCine currently skips `/Sessions/Playing*` when `PlaySessionId` is absent (`emby.ts:483-489`), which is stricter than Jellyfin source and may prevent active/current playing updates on Jellyfin-compatible servers.

3. **For Emby-specific 400s, a missing `PlaySessionId` should trigger a fresh, body-based PlaybackInfo negotiation before reporting, not a legacy start/progress fallback first.** Android TV and Emby C# both tie report identity to the `PlaybackInfoResponse.PlaySessionId` they obtained immediately before stream creation (`JellyfinMediaStreamResolver.kt:86-117`; `PlaybackManager.cs:251-289`). OhMyCine already tries this in `ensurePlaybackSession` (`emby.ts:858-886`), but user diagnostics show Emby returned `MediaSourceId` without `PlaySessionId`; capture full redacted request/response shape around `fetchPlaybackInfo` when investigating that condition.

4. **Re-check unsupported/obsolete legacy stop path.** OhMyCine uses `POST /Users/{UserId}/PlayingItems/{Id}/Delete` (`emby.ts:565-570`). Jellyfin source supports `DELETE /Users/{userId}/PlayingItems/{itemId}` and no `/Delete` route (`PlaystateController.cs:490-503`). Public Emby client source does not show use of a `/Delete` legacy route; it uses `/Sessions/Playing/Stopped`.

5. **Legacy `/Users/{UserId}/PlayingItems*` is not a reliable modern compatibility mechanism for Emby 400s.** Jellyfin keeps those routes only as obsolete compatibility wrappers (`PlaystateController.cs:321-336`, `413-432`, `490-503`), and official Emby public clients do not use them for online playback reporting. If kept, it should be treated as server/version-specific best effort and use method/path/query fields matching the target server.

6. **Payload field names are PascalCase for JSON bodies in official JS clients and generated models.** OhMyCine's session JSON body already uses PascalCase fields (`emby.ts:491-506`). Legacy query names in Jellyfin server source are lower camel/case-insensitive model binder names (`mediaSourceId`, `positionTicks`, etc.); ASP.NET model binding is generally case-insensitive, but Emby compatibility may not be identical.

7. **Auth header variants matter for session attachment.** OhMyCine currently sends `X-Emby-Token` + `X-Emby-Authorization` (`emby.ts:1070-1075`), which matches Emby JS style. Official Emby C# additionally uses `Authorization: MediaBrowser ...` plus `X-MediaBrowser-Token`, and includes `UserId` in authorization parameters when known (`BaseApiClient.cs:185-190`, `228-242`; `HttpHeaders.cs:22-31`). If admin active/current playing still does not attach to the device session, compare server behavior with these official header variants and stable `DeviceId`.

8. **Report cadence can follow clients.** Jellyfin Web sends progress every 10 seconds (`playbackmanager.js:3246-3255`) and Emby JS throttles ordinary timeupdate reports to around 10 seconds (`apiclient.js:4283-4313`). OhMyCine progress frequency should be evaluated against the caller in `PlayerView.vue` if active/continue-watching remains stale.

### External References

- Jellyfin Web source: `https://github.com/jellyfin/jellyfin-web/blob/master/src/components/playback/playbackmanager.js` — real web client PlaybackInfo/reporting implementation.
- Jellyfin Android TV source: `https://github.com/jellyfin/jellyfin-androidtv/blob/master/playback/jellyfin/src/main/kotlin/mediastream/JellyfinMediaStreamResolver.kt`, `https://github.com/jellyfin/jellyfin-androidtv/blob/master/playback/jellyfin/src/main/kotlin/playsession/PlaySessionService.kt`, `https://github.com/jellyfin/jellyfin-androidtv/blob/master/app/src/main/java/org/jellyfin/androidtv/util/apiclient/ReportingHelper.kt` — real Android TV client stream resolution and playstate reporting.
- Emby JavaScript API client: `https://github.com/MediaBrowser/Emby.ApiClient.Javascript/blob/master/apiclient.js` — official Emby API client for PlaybackInfo, reporting, and auth header construction.
- Emby C# API client: `https://github.com/MediaBrowser/Emby.ApiClient/blob/master/Emby.ApiClient/Playback/PlaybackManager.cs`, `https://github.com/MediaBrowser/Emby.ApiClient/blob/master/Emby.ApiClient/ApiClient.cs`, `https://github.com/MediaBrowser/Emby.ApiClient/blob/master/Emby.ApiClient/BaseApiClient.cs` — official Emby API client playback manager, endpoints, and auth headers.
- Jellyfin server source: `https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/MediaInfoController.cs`, `https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Helpers/MediaInfoHelper.cs`, `https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/PlaystateController.cs` — server-side endpoint behavior and legacy route handling.
- Jellyfin TypeScript SDK generated models: `https://github.com/jellyfin/jellyfin-sdk-typescript/tree/master/src/generated-client/models` — generated field names and payload shapes.

### Related Specs

- `.trellis/spec/frontend/type-safety.md` — provider playback sync contract. Relevant lines found: `ProviderPlaybackProgressInput`, optional `DataSource.syncPlaybackProgress`, Emby endpoint expectations, PlaySessionId/MediaSourceId handling, and best-effort provider sync (`type-safety.md:150-193`).

## Caveats / Not Found

- Emby Theater desktop source was not useful in the public GitHub repository queried; playback reporting behavior was therefore inferred from official Emby JavaScript and C# API clients plus Jellyfin clients/server source.
- Public Emby server source is not available in this research, so Emby-specific 400 validation for missing `PlaySessionId` could not be traced server-side. The observed 400 differs from current Jellyfin server source, where `PlaySessionId` is nullable on playback reports and legacy routes.
- No source usage of `CurrentPlaySessionId` was found in Jellyfin Web/server/SDK/AndroidTV or public Emby API clients during this search. It may exist in private Emby server/client code or older API docs, but it is not present in the open source client/server code examined.
- User diagnostics mention `POST /Users/{UserId}/PlayingItems/{Id}/Delete`; Jellyfin source supports `DELETE /Users/{userId}/PlayingItems/{itemId}` instead. Emby server may have a different private compatibility route, but no public Emby client source found here uses `/Delete`.
