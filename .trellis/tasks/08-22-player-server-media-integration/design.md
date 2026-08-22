# Technical design

## Architecture boundary

```text
Player Settings
  └─ username/password (first authentication only)
       └─ POST /api/v1/player/auth/login
            └─ hashed DeviceToken row

Player ServerDataSource
  ├─ Bearer-only /api/v1/player/* JSON API
  ├─ Server media catalog
  └─ GET/HEAD entry stream + Bearer
       └─ authorize entry/library
            └─ active managed STRM MediaArtifact
                 └─ SignedProxyService.ResolveArtifactForClient
                      └─ 115 temporary direct URL → 302

Player aggregate presentation
  ├─ Server item identities
  └─ direct Emby item identities
       └─ exact artifact key or work-level TMDB key
            └─ one card, preserved playback targets
```

Browser management routes retain Cookie session + CSRF. Device Bearer tokens are accepted only by the dedicated Player route group and the Player stream endpoint.

## Server persistence

### Migration v32

Add `device_tokens`:

```text
id                  TEXT primary key, random public record ID
token_hash          TEXT unique, SHA-256 of the random bearer token
user_id             INTEGER indexed, FK users ON DELETE CASCADE
device_id_hash      TEXT indexed, irreversible stable device key
device_name         TEXT safe display label
client_kind         TEXT = player
created_at          DATETIME
last_seen_at        DATETIME
idle_expires_at     DATETIME indexed
absolute_expires_at DATETIME indexed
revoked_at          DATETIME nullable indexed
```

No raw token, password, IP, User-Agent or third-party credential is persisted. Device-token lifetime uses explicit Server config defaults and is bounded by an absolute expiry. Authentication resolves the current user/RBAC on every request, so user disable and role changes take effect immediately. Password reset/user disable additionally revokes both browser sessions and device tokens in the same transaction.

No new Emby mapping table is required for this slice. The durable exact bridge already exists:

```text
MediaLibraryEntry.ID
  ↔ MediaArtifact.SourceIdentity = "entry:<id>"
  ↔ MediaArtifact.OpaqueID
  ↔ OpaqueID embedded in the signed STRM URL indexed by Emby
```

If later providers do not preserve this artifact identity, a separate provider-binding table can be added without changing the Player identity contract.

## Server authentication/API contracts

### Anonymous, rate-limited login

`POST /api/v1/player/auth/login`

```json
{
  "username": "owner",
  "password": "transient",
  "device_id": "player-installation-id",
  "device_name": "客厅 Windows Player"
}
```

Success returns one raw token exactly once plus safe bootstrap identity. Password validation reuses the existing constant-time/bcrypt login path and limiter; browser session creation is not performed.

### Bearer middleware

`Authorization: Bearer <device-token>` is parsed strictly: one scheme/value, bounded token length, no query/cookie fallback. Middleware resolves the hashed row, expiry/revocation and current actor. It updates `last_seen_at`/idle expiry with throttling to avoid a database write on every image/list request.

### Player routes

```text
POST   /api/v1/player/auth/logout
GET    /api/v1/player/bootstrap
GET    /api/v1/player/devices
DELETE /api/v1/player/devices/:id

GET    /api/v1/player/media-libraries
GET    /api/v1/player/media-libraries/:id/catalog
GET    /api/v1/player/media-libraries/:id/catalog/:work
GET    /api/v1/player/search?q=&page=&page_size=
GET    /api/v1/player/media-entries/:id/stream
HEAD   /api/v1/player/media-entries/:id/stream
```

Normal JSON endpoints keep the standard response envelope. The stream endpoint is an explicit non-JSON exception and returns a controlled 302 or safe status.

`bootstrap` includes:

- Server product name/version capability values.
- Current safe user projection.
- media-library count.
- enabled Emby integration summaries containing connection alias, health and `instance_fingerprint`, never endpoint/API Key/SystemId.

Server already persists the tested Emby `SystemId` in private `Connection.AccountID`. Compute:

```text
SHA-256("ohmycine:emby-instance:v1\0" + lower(trim(SystemId)))
```

When `AccountID` is missing/stale, report identity as unavailable instead of blocking Player bootstrap or probing upstream inside the database transaction.

## Player media DTO

Create Player-specific DTOs rather than serializing GORM models. A work contains:

- stable Server work ID and library ID;
- name/original title/type/year/rating/overview/tagline/runtime;
- genres/directors/cast/IMDb/TMDB projections decoded from the credential-free recognition snapshot;
- safe poster/backdrop address;
- seasons/episodes/files;
- `work_identity_keys` such as `tmdb:movie:346`;
- versions with opaque Player-facing version ID, size/container and `exact_identity_keys`;
- no relative/absolute/provider path in normal list DTOs.

An active STRM version receives:

```text
exact identity = omc-artifact:<artifact opaque id>
playback route = server-direct entry:<entry id>
```

Artifact opaque identity alone cannot access the media; signature or authenticated Player entry route is still required.

## Stream authorization and redirect safety

The entry stream service performs all checks on every GET/HEAD:

1. actor has media-library read permission;
2. entry exists and belongs to an enabled readable library;
3. storage is 115 and the library has STRM + signed proxy enabled;
4. active managed completed STRM artifact exists with `SourceIdentity=entry:<id>`;
5. artifact/library/connection state is revalidated by `ResolveArtifactForClient`;
6. provider returns a safe HTTP(S) URL and compatible headers;
7. response is a 302 with `Location`, `Cache-Control: no-store`, no upstream body.

The Player stream request carries device Authorization only to the Server origin. The Rust remote playback bridge must strip `Authorization`, Cookie and other provider-private headers whenever the redirect changes origin, while retaining Range behavior. Tests use a real local Server → foreign-origin redirect → CDN request and assert the CDN never receives the device token.

## Emby identity and aggregation

### Direct Emby source

After username/password authentication, `EmbyDataSource` reads `/System/Info`, computes the same instance fingerprint and persists only that non-secret fingerprint in `DataSourceConfig.extra`. The Emby token/password behavior outside this task remains unchanged.

Library identity is calculated as:

```text
SHA-256("ohmycine:emby-library:v1\0" + instanceFingerprint + "\0" + libraryId)
```

Item mapping emits:

- work key from trustworthy `ProviderIds.Tmdb` plus media type;
- Emby exact key from instance fingerprint + Item ID + MediaSource ID;
- OMC exact key only when a MediaSource/Path parses as an OhMyCine `/proxy/strm/<opaque>` URL associated with a currently configured Server origin.

The complete media path/query is discarded immediately after validation and is never placed into `MediaItem`, diagnostics or persisted caches.

### Shared aggregate merger

Add one typed merge utility used by aggregate home, global search and continue-watching preparation:

- exact identity intersection means the same playable version;
- work identity intersection means the same work but potentially multiple versions;
- Server source wins default card/playback target;
- artwork/metadata may fill missing fields without changing the canonical source/item IDs;
- alternate targets remain typed references to an already configured DataSource, never copied credentials;
- no identity intersection means no merge;
- source-specific lists bypass cross-source merging.

The detail surface resolves the canonical Server detail and may add an Emby playback target only when the matching Emby DataSource is configured and connected. Selecting it delegates to that EmbyDataSource's existing `getStreamRequest`; Server never injects its management API Key.

## Player implementation boundary

- Add Server credential envelope `{version, provider:'server', deviceToken}` using the existing credential store.
- Add a strict native Server JSON request command so login/device token calls are not dependent on WebView CORS and errors are bounded/redacted.
- Add `ServerDataSource` behind the existing DataSource interface.
- Add the Server option to the existing settings source flow; do not create a second settings architecture.
- Keep Server disconnect state source-scoped.
- Preserve all existing source-specific browsing and Player-independent startup.

## Compatibility and migration

- Migration v32 is additive; existing browser sessions, connections, media libraries, artifacts and Player configs remain unchanged.
- Existing Player configs do not gain a Server source automatically.
- Existing Emby configs may not have an instance fingerprint. On their next successful test/login, populate it; until then cross-source dedup safely does nothing.
- If an Emby user cannot see MediaSource Path, work-level TMDB aggregation may still combine the card while preserving separate targets. No title-only fallback.

## Logging and audit

- Add module operations for Player device authentication and Player direct playback.
- Audit successful/failed device login, logout/revoke and denied playback using IDs/status codes only.
- Never log request bodies, Authorization, token hashes, SystemId, Emby item paths, signed URLs, 115 URLs or provider IDs.

## Rollback

- Player Server config can be removed without touching other DataSources.
- Disabling the Player route registration/device-token authentication restores the prior Server exposure; migration v32 remains harmless additive data.
- Direct playback uses the existing resolver, so rollback does not alter STRM files or Emby gateway behavior.
