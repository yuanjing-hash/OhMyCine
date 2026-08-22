# Current implementation boundaries

## Player

- `player/src/services/datasource/types.ts` already declares `DataSourceType = ... | 'server' | ...` and the common list/library/home/search/detail/playback contract.
- `player/src/services/datasource/manager.ts` has no `server` factory branch, so a saved Server config currently fails as unimplemented.
- `player/src/services/settingsSourceOptions.ts` excludes `server` from editable/login source types and the add-source cards.
- `player/src/services/datasource/credentialStore.ts` already provides the OS-backed credential reference boundary. A Server envelope can store only the device token; unlike the current Emby envelope it must not store the Server password.
- `player/src/services/datasource/emby.ts` already requests `/System/Info`, `ProviderIds`, `MediaSources` and `Path`, but does not retain a safe Emby instance fingerprint or artifact equivalence identity.
- Aggregate home and search currently deduplicate only by `${sourceId}:${itemId}` in `homeAggregation.ts` and `searchAggregation.ts`. Cross-source equivalence requires a typed identity/alternate-target field and shared merge helper.
- The existing playback path uses `DataSource.getStreamRequest()` and can carry transient headers into the native Rust/libmpv bridge. Cross-origin redirects must drop provider-private Authorization.

## Server

- `server/internal/services/auth.go` and `server/internal/middleware/context.go` implement browser Cookie session + CSRF only.
- `server/internal/models/models.go` has `Session`, but no device-token model.
- `server/internal/httpserver/router.go` places management routes behind Cookie auth and CSRF. Player routes need a separate Bearer-only group so a device token cannot become a CSRF bypass for admin writes.
- `server/pkg/mediaserver/emby/client.go` already validates the administrator-selected Emby endpoint and `Probe()` returns the stable Emby `ID`, name and version from `/System/Info`.
- `server/internal/services/media_catalog.go` already groups entries into works/seasons/episodes. Its current DTO lacks the full TMDB snapshot projection, artwork identities, safe playback versions and cross-source identity fields required by Player.
- `server/internal/models/models.go` persists `MediaArtifact.OpaqueID`, `SourceIdentity`, `ProviderItemID`, kind, target kind, active/managed state and status. STRM creation uses `SourceIdentity = entry:<MediaLibraryEntry.ID>`.
- `server/internal/services/signed_proxy.go` already provides `ResolveArtifactForClient()` and repeats manifest/library/storage validation before resolving a 115 temporary URL. The Player direct stream endpoint can authorize the media entry first, find its active STRM artifact, then reuse this internal boundary without reading `.strm` content.
- 115 multi-device routing and short-lived URL caching are already inside `SignedProxyService`; a separate Player-specific 115 implementation would duplicate and diverge from the proven path.

## Identity findings

- Emby authentication method is irrelevant to instance identity. The stable instance key is the normalized `/System/Info.Id`; exchange a SHA-256 fingerprint instead of credentials.
- Emby library equality requires the Emby instance fingerprint plus the provider library/CollectionFolder ID. Names and URLs are display/routing properties only.
- Work-level aggregation may use `(media type, TMDB ID)`, but it cannot erase media versions.
- Exact Server-native ↔ Emby-STRM equivalence can use the artifact opaque identity already embedded in the signed STRM URL. Player may parse only the stable path component in memory and must discard the complete signed URL/query.
- If Emby does not expose the needed MediaSource path to the current user, the safe fallback is no exact merge. Title-only guessing is forbidden.

## Validation implications

- Add migration tests for hashed device tokens, expiry/revocation and password-reset/user-disable invalidation.
- Add router tests proving Bearer tokens work only on Player routes and cannot invoke Cookie/CSRF management writes.
- Add service tests proving a requested entry belongs to a readable library and an active managed STRM artifact before 302 resolution.
- Add a real redirect test proving Server Authorization is absent at the simulated cross-origin CDN.
- Add Player tests for same/different Emby `SystemId`, same-name different libraries, artifact exact merge, TMDB work merge with versions preserved, uncertain items remaining separate, and source-specific pages remaining unmodified.
