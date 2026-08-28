# Backend Security Guidelines

> Mandatory security rules for OhMyCine Server, CLI integrations, and backend-adjacent design.

---

## Overview

OhMyCine handles sensitive media-server keys, cloud-drive credentials, PT cookies/passkeys, downloader passwords, AI API keys, JWTs, and proxy URLs. Security defaults must be safe for self-hosted users and local development.

Consult this guide before implementing credentials, 302 proxy, config sync, plugins, file operations, external HTTP clients, AI integrations, or deployment examples.

---

## Authentication and Sessions

- Server management APIs require authentication by default.
- Use bcrypt or argon2id for password hashes.
- Reject default weak JWT secrets such as `change-me` in production mode.
- Access tokens should be short-lived; refresh/device tokens should be revocable.
- Login must be rate-limited.
- Initial admin setup must force a secure password path; never ship a silently usable default admin password.

---

## Credential Storage

Sensitive fields include:

- Emby/Jellyfin API keys.
- OpenList/Alist tokens, usernames, and passwords.
- CloudDrive2 API Tokens.
- Generic WebDAV usernames and passwords.
- 115 cookies and API proxy credentials.
- PT cookies, passkeys, and user IDs.
- qBittorrent/Transmission credentials.
- AI provider API keys.
- JWT/session secrets.

Rules:

- Encrypt sensitive config at rest with AES-256-GCM or an approved equivalent.
- Master keys come from environment, secret file, or generated local key file.
- Master keys are never logged, returned by APIs, or committed.
- Exported configs are redacted by default. Full export requires explicit confirmation.
- API responses must not include sensitive plaintext or encrypted blobs unless explicitly designed as backup export.
- Saved third-party credentials may be revealed only by the dedicated `POST /api/v1/credentials/reveal` action after an explicit UI gesture. The route requires authentication, CSRF, `connections.secrets.export`, `Cache-Control: no-store`, a hard resource/field allowlist, bounded identifiers, and success/failure audit records that never include the value. Ordinary list/detail DTOs return per-field configured booleans only.
- Never reveal OhMyCine user passwords, JWT/session/device access tokens, credential master keys, proxy signing secrets, built-in application credentials, or deployment-injected credentials. A reveal value is transient UI state and must not enter the edit model unless the user actually types a replacement.
- Server TMDB credentials use an explicit `read_access_token|api_key` kind and resolve in strict order: AES-GCM custom credential, runtime deployment credential, linker-injected application credential. Read Access Tokens use Bearer; API Keys use the `api_key` query parameter. Runtime and build inputs use mutually exclusive typed variables (`OMC_TMDB_READ_ACCESS_TOKEN` / `OMC_TMDB_API_KEY` and `OHMYCINE_TMDB_READ_ACCESS_TOKEN` / `OHMYCINE_TMDB_API_KEY`). Build-only values must not be read as runtime config or inherited by npm/Vite/Server subprocesses. APIs expose source and kind labels only. Official artifacts fail when neither or both build Secrets are set, or when a value has linker-unsafe characters, without printing it; distributed application credentials remain extractable and therefore require read-only scope, independent quota, revocation and rotation. Pre-v11 ciphertext defaults to `read_access_token` without decryption or rewriting.
- TMDB API/image proxy prefixes are non-secret but privileged network routes. Persist each only after its own bounded HTTPS test succeeds; reject userinfo/query/fragment, redirects, traversal and oversized bodies. A custom API never falls back to another host.
- Check the metadata-settings revision before sending a candidate credential or the current effective credential to a probe route, and check it again with database CAS after the probe. Stale administrator requests must fail before any credential-bearing network request.
- Player Rust storage must use the shared storage layout. Windows standard mode stores app databases under LocalAppData and DPAPI-wraps the credential master key; portable mode uses EXE-adjacent data with an explicit reduced-protection warning.
- Legacy Player storage migration is file/key allowlisted and never overwrites newer target data. It runs only in standard mode; portable mode never imports standard-profile, legacy Roaming, or shared WebView localStorage data automatically.

---

## 302 Proxy Security

`/proxy/*` is high risk and must not be naked public access by default.

Supported modes:

1. `signed-url` for STRM playback.
2. `authenticated` for Player direct playback through Server.
3. `trusted-lan` only when explicitly configured.

Signed URL requirements:

- Include expiration.
- Sign method + normalized path + expiration + optional scope using HMAC-SHA256 or equivalent.
- Reject expired or invalid signatures.
- Normalize paths before signature verification and upstream lookup.
- Optionally bind to library/user/client scope when available.

URL cache requirements:

- TTL must not exceed upstream URL expiry.
- Cache key includes driver, path, and permission context.
- Cached CDN URLs and token query params are never logged.
- Cache hit still requires proxy authorization.
- Treat provider URL-acquisition headers and redirected-playback requirements as separate contracts. An SDK response may echo Cookie, Authorization, Referer, Content-Type, or other headers used only to call the provider API; the provider adapter must discard those acquisition headers rather than forwarding, persisting, caching, or logging them. `TemporaryURL.Headers` contains only headers the final CDN request actually requires. A plain signed 302 may accept an exact downstream `User-Agent` binding, but must fail closed when the final request truly requires Cookie, Authorization, Referer, a mismatched User-Agent, or any other header that the client cannot safely reproduce.

Emby 302 gateway requirements:

- Reverse proxy only to the fixed, successfully probed HTTP(S) Connection endpoint. The encrypted Server API key is reserved for explicit administration calls and is never injected into gateway traffic.
- Preserve client Emby authentication and WebSocket behavior, strip hop-by-hop headers, do not follow upstream redirects, and rewrite same-endpoint `Location` values without allowing relative redirects to escape into OhMyCine routes.
- Take over only active managed signed-STRM sources and explicit stream/download/file routes. A reserved playback-ticket query on any other route, an invalid/ambiguous ticket, or a duplicate MediaSource binding fails closed and is never forwarded upstream.
- Bind short-lived tickets to gateway public ID, gateway policy revision, Emby item, MediaSource, artifact opaque identity and `media-read` scope. Connection/gateway configuration changes advance the policy revision so stale and concurrent tickets cannot reactivate an untested endpoint.
- PlaybackInfo rewrites must return Emby API-relative media paths such as `/videos/{item}/stream`, never the outer `/emby/{gateway}` mount. Emby clients append their configured gateway base and `/emby` API prefix; embedding the mount in `DirectStreamUrl` duplicates the path and invalidates the ticket route.
- Browser 302 playback cannot manufacture CORS permission on a third-party CDN response. The gateway may patch only the fixed allowlist of Emby Web player assets needed to suppress `crossOrigin=anonymous` for remote DirectPlay. Because an already cached player module can bypass a module-only patch, the exact `/web/index.html`, `/web`, and `/web/` shell allowlist may receive one hard-coded same-origin compatibility script tag, and the gateway may serve that immutable source at one fixed path. Both the shell and module paths are deterministic, size-bounded, identity-encoded, cache-revalidation-safe and non-configurable; clear validators/source maps on modified output, set `no-store`, preserve the upstream CSP, and never include user content or credentials. Never expose arbitrary HTML/JavaScript injection, user scripts, broad path matching, or API CORS relaxation.
- Built-in external-player links may be rendered only for a PlaybackInfo source whose relative gateway stream URL contains exactly one valid short-lived `omc_ticket` and one MediaSource binding. Construct the absolute URL from the fixed same-origin gateway base; never put the Emby token/API key, provider identity, signed STRM source, 115 credential, or final CDN URL in a custom protocol. Ordinary Emby sources receive no external-player entry. Built-in Fanart UI reads only the current user's Emby `BackdropImageTags` through the same-origin image API, has no third-party script/icon/CDN dependency, and both modules remain fixed code controlled by revisioned per-gateway booleans rather than user-provided JavaScript.
- Reject encoded separators, repeated-encoding path traversal and endpoint-prefix escape. Bound PlaybackInfo request/response bodies, mark ticket-bearing responses `no-store`, and never log tickets, MediaSource paths, signed STRM URLs, provider identities or final CDN URLs.
- Keep Emby administration in the dedicated Player Management UI while reusing the encrypted Connection model. Its summary endpoint may expose only bounded aggregate server/version/library/item counts; partial failures stay unknown instead of becoming zero.
- Listen addresses and advertised origins are separate trust domains. Wildcard bind addresses are valid for the listener but invalid for `OMC_PUBLIC_ORIGIN`, persisted STRM URLs, copied gateway URLs, and CSRF origins.

## Scenario: Fixed Emby Web External Players and Fanart

### 1. Scope / Trigger

- Trigger: changing the Emby gateway Web compatibility asset, its external-player/Fanart controls, or PlaybackInfo-to-custom-protocol behavior.

### 2. Signatures

- DB: `emby_proxy_gateways.external_player_enabled INTEGER NOT NULL DEFAULT 1` and `fanart_enabled INTEGER NOT NULL DEFAULT 1`.
- `GET /api/v1/connections/{id}/emby-gateway` returns both booleans with `revision`.
- `PATCH /api/v1/connections/{id}/emby-gateway` accepts optional `external_player_enabled` and `fanart_enabled` booleans plus the required current `revision`; omitted booleans preserve stored values.
- Fixed asset: `GET|HEAD /emby/{alias}/web/ohmycine-directplay.js` with `no-store` and no user-provided source.

### 3. Contracts

- Every successful gateway policy mutation advances `policy_revision`; old playback tickets then fail validation.
- External-player discovery calls the current Emby session's PlaybackInfo and renders only when a candidate has an API-relative DirectStreamUrl with exactly one non-empty `omc_ticket` and exactly one MediaSource binding under the current same-origin gateway base.
- Protocol links contain only the same-origin gateway stream URL and short ticket. Never append Emby auth, Server API key, provider identity, signed STRM source, Cookie, or final CDN URL.
- Fanart reads at most 30 unique `BackdropImageTags` for `Movie`, `Series`, `Person`, or `Video` through `ApiClient.getImageUrl`; construct all labels with DOM `textContent` and load no remote script/icon dependency.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Ordinary Emby source or missing/duplicate ticket | Do not render external-player buttons |
| Candidate stream resolves off-origin or outside the active gateway base | Reject it in the browser module |
| Ticket expires or policy revision changes before click | Request fresh PlaybackInfo; if unavailable, show a local safe error and do not launch |
| Fanart item/type/tags/container is unavailable | Render no section; do not fail the Emby detail page |
| Unknown JSON field or stale revision in PATCH | Existing strict JSON/conflict handling rejects the mutation |

### 5. Good/Base/Bad Cases

- Good: Windows Emby Web shows PotPlayer/VLC/MPV/弹弹Play for a managed STRM, and the launched URL reaches the gateway with only a short ticket.
- Base: a normal local Emby movie still displays and plays normally but has no OhMyCine external-player row.
- Bad: building `vlc://...&api_key=<Emby token>` or loading third-party player icons/scripts from a public CDN.

### 6. Tests Required

- Migration test proves legacy gateways receive both defaults without changing revision.
- Service tests prove omitted settings preserve booleans, explicit settings persist, revision advances, and generated source contains the selected literal booleans plus the ticket-only gate.
- Real router tests cover fixed asset GET/HEAD, `no-store`, `nosniff`, CSP preservation, and signed PlaybackInfo/stream routing. Run the extracted generated JavaScript through `node --check` when editing its source.
- Frontend tests cover the revision-bound PATCH payload; full Go tests, vet, frontend test/typecheck/lint/build, and both Server build modes remain required.

### 7. Wrong vs Correct

#### Wrong

```javascript
const stream = ApiClient.serverAddress() + "/emby/videos/1/stream?api_key=" + ApiClient.accessToken()
window.location.href = "vlc://" + stream
```

#### Correct

```javascript
const candidate = new URL(playbackSource.DirectStreamUrl, fixedGatewayAPIBase)
if (candidate.origin === location.origin && candidate.searchParams.getAll("omc_ticket").length === 1) {
  window.location.href = "vlc://" + encodeURI(candidate.href)
}
```

---

## Scenario: Player Device Authentication and Direct Server Media

### 1. Scope / Trigger

- Trigger: changing Player login/device management, `/api/v1/player/*`, Player catalog DTOs, Emby identity summaries, ServerDataSource playback, or authenticated 115 redirects.
- This boundary is separate from browser administration and from future CLI/API-token and config-sync designs.

### 2. Signatures

- DB migration v32 adds `device_tokens`: random public record ID, SHA-256 `token_hash`, user ID, SHA-256 `device_id_hash`, safe device name, `client_kind=player`, created/last-seen/idle-expiry/absolute-expiry/revoked timestamps.
- Anonymous login: `POST /api/v1/player/auth/login` with `username`, `password`, `device_id`, and `device_name`; returns the raw `omc_player_` token exactly once.
- Bearer-only routes: logout, bootstrap, device list/revoke, media-library list/catalog/detail/search, plus GET/HEAD `/api/v1/player/media-entries/:id/stream`.
- Browser device management: `GET /api/v1/player-devices` requires Cookie Session + `connections.read`; `DELETE /api/v1/player-devices/:id` additionally requires Origin/CSRF + `connections.update`. Both reuse the Player device DTO/service but never accept Player Bearer authentication.
- `PlayerMediaVersion.delivery_kind` is an optional bounded enum: `server_stream` for authenticated local GET/HEAD/Range and `server_redirect` for authenticated Server resolution followed by 302.
- Emby instance identity: `SHA-256("ohmycine:emby-instance:v1\0" + lower(trim(SystemId)))`.

### 3. Contracts

- Player routes use a dedicated strict Bearer middleware and never accept query/cookie fallback. Browser management routes keep Cookie session + Origin/CSRF and never accept a Player Bearer as a substitute.
- Persist no raw token, password, IP, User-Agent or device ID. Default lifetime is 30 days idle and 180 days absolute; touch idle expiry at a bounded cadence and never past the absolute expiry.
- Re-authenticating the same user/device revokes the prior token. Logout, explicit device revoke, user disable, password reset and user removal revoke affected device tokens.
- Player media DTOs expose safe logical IDs, metadata and opaque identity only; never absolute/provider paths, provider item IDs, Cookie, Emby API key, signed STRM URL or upstream temporary URL.
- `playable` means only that the version can currently be selected. It never means STRM. `delivery_kind` describes the Server-to-Player HTTP delivery mode and must not reveal that 115 internally uses a managed STRM artifact as its authorization/mapping prerequisite.
- Browser device list/revoke stays scoped to the current actor through `AuthService.ListDevices`/`RevokeDevice`, returns only public record ID, safe name/client kind and lifecycle timestamps, and uses `Cache-Control: no-store`. Never return the token/hash, raw device ID/hash, IP or User-Agent.
- Player catalog/detail/search expose only enabled media libraries backed by enabled Storage rows. Search must exhaust each readable library's catalog pagination before applying global sorting and pagination; a per-library first-page cap must not undercount `total`.
- Entry streaming rechecks actor permission plus enabled entry/library/storage ownership on every request. Local Storage resolves only a root-confined ordinary file after per-component symlink/Reparse Point rejection and serves authenticated GET/HEAD/Range without serializing its absolute path; 115 still requires an active managed completed local-projection STRM artifact before resolving a short-lived provider URL.
- Signed-proxy resolution rechecks the current Storage enabled state before every redirect, including cache hits, so disabling a Storage immediately invalidates known artifact URLs.
- Return 302 with `Cache-Control: no-store`; unavailable targets map to a safe 404/503-class response rather than a generic 500. Do not log the redirect URL or Authorization.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Missing/malformed/non-`omc_player_` Bearer | 401; no management fallback |
| Player Bearer sent to `/api/v1/users` or another management route | 401; it cannot bypass Cookie/CSRF |
| Player Bearer sent to `/api/v1/player-devices` | 401; browser management never falls back to device auth |
| Browser device DELETE lacks CSRF or update permission | 403; device remains valid |
| Revoked, idle-expired, absolute-expired or disabled-user token | 401 |
| Entry/library/artifact missing, disabled, unmanaged, inactive or wrong target kind | Safe not-found/unavailable response; no provider call |
| Media library or backing Storage is disabled after a prior catalog/cache hit | Catalog/detail no longer expose it and stream/proxy resolution refuses it immediately |
| Actor lacks media-library read | 403 |
| Valid local ordinary media entry | `playable=true`, `delivery_kind=server_stream`; authenticated GET/HEAD/Range with `no-store`; no absolute path in DTO/error/log |
| Valid active 115 STRM entry | `playable=true`, `delivery_kind=server_redirect`; 302 `no-store` to the current short-lived provider URL |
| Bootstrap lacks optional Emby identity or connection permission | Keep bootstrap usable and omit the optional identity |

### 5. Good/Base/Bad Cases

- Good: Player logs in once, restarts with its secure credential, browses a safe catalog and plays through entry ID → validated artifact → 302.
- Good: Server Web UI lists the current account's safe device card and, after explicit confirmation, revokes it so the next Player Bearer request returns 401.
- Base: an old Player ignores `delivery_kind`; an old Server omits it. Playback remains compatible because the field is optional.
- Base: Server is offline or the device was revoked; only that ServerDataSource reports reconnect/authentication failure and other Player sources continue working.
- Bad: accept `Authorization: Bearer omc_player_*` in the ordinary management group, serialize `relative_path`, or store the raw device token for troubleshooting.

### 6. Tests Required

- Migration tests assert the v32 table, indexes and repeated migration behavior.
- Router/service tests cover first login, same-device replacement, logout/revoke, user disable/password reset, expiry, permission denial, catalog DTO redaction, browser Cookie/CSRF device list/revoke, Player Bearer isolation, disabled library/Storage, catalogs larger than one page, local `delivery_kind=server_stream` plus GET/HEAD/single-range/invalid-range and traversal/symlink/Reparse rejection, invalid 115 artifact states and valid `delivery_kind=server_redirect` GET/HEAD redirect.
- Assert a Player Bearer cannot enter a representative browser management route.
- Cross-layer playback tests must prove a foreign-origin redirect receives Range but not Server Authorization/Cookie/private headers.
- Run `CGO_ENABLED=0 go test ./...`, `go vet ./...`, Player typecheck/lint/build, Rust tests and Clippy.

### 7. Wrong vs Correct

#### Wrong

```go
// Broadens a device credential into the browser administration surface.
protected.Use(AuthFromCookieOrBearer(auth))
```

#### Correct

```go
player := router.Group("/api/v1/player")
playerProtected := player.Group("")
playerProtected.Use(DeviceAuth(auth))
// Browser routes remain Cookie + CSRF only.
```

```go
// Wrong: playability does not identify the transport or an internal artifact.
version.DeliveryKind = boolToString(version.Playable, "strm")

// Correct: assign the bounded delivery kind in the same validated provider branch.
version.Playable = true
version.DeliveryKind = playerDeliveryServerStream
```

---

## File and Path Safety

All local file operations must:

- Operate under configured roots only.
- Canonicalize paths before use.
- Reject `..`, repeated-encoding traversal, and symlink escape.
- Handle Windows separators, drive letters, and UNC paths when implementing cross-platform behavior.
- Avoid overwriting existing targets by default.

Transfer modes:

- `move`: default safe behavior; do not overwrite unless configured.
- `copy`: check destination space where feasible.
- `hardlink`: do not silently fall back to copy across filesystems without user consent.
- `symlink`: admin-only by default because of escape risk.
- delete/cleanup: require confirmation or dry-run when destructive.

STRM cleanup must:

- Delete only `.strm` files under configured STRM roots.
- Not follow symlinks outside roots.
- Support dry-run preview.
- Record the files considered/deleted without exposing credentials.

### Server directory picker

- Server Web administration must browse the Server process filesystem through authenticated APIs; browser-native file pickers represent the client device and are not a substitute.
- Protect root and child enumeration with the independent sensitive permission `storages.browse` at both route middleware and service policy. Owner, administrator, and operator receive it by default; viewer does not.
- Enumerate only one directory level per request, return directories only, cap and sort results, apply cancellation/timeouts plus per-actor rate/concurrency limits, and use `Cache-Control: no-store`.
- Windows roots are process-visible logical/mapped drives. Unix/NAS/Docker roots and mounts are only those visible in the process namespace. Never fabricate an unmounted host path.
- Navigation and selection use short-lived signed opaque tokens bound to purpose, platform, and adapter version. Clients never join separators, `..`, drive letters, hostnames, or shares to create the next request.
- Directory tokens accept only their canonical unpadded Base64URL representation before authenticated decryption; alternate text encodings of the same bytes are rejected as tampering.
- Reject symlinks, junctions, mount-point Reparse Points, and other Reparse Point children for entry and selection. Saving a selected root always repeats canonicalization, uniqueness, and the existing read-only probe.
- Browse logs, audit metadata, and safe errors must not contain absolute paths, child names, or raw OS errors. A picker response may include only the current interaction's displayed paths and names.

---

## External HTTP and SSRF Defense

Use a controlled HTTP client for external calls:

- Set timeouts.
- Limit redirects.
- Limit response size for metadata/probe calls.
- Allow only expected schemes (`http`, `https`, WebDAV equivalents).
- Reject `file://`, `gopher://`, and unexpected schemes.
- Treat user-configured URLs as privileged admin configuration; ordinary user inputs must not be able to probe internal management addresses.
- Plugins and site/cloud adapters should route network calls through the same controlled client when plugin architecture exists.
- Player subtitle providers use provider-specific controlled native clients. OpenSubtitles API Keys and optional account passwords stay in the credential boundary; account sessions stay in process memory only. Shooter/Xunlei local file paths are used only by Rust to compute bounded content hashes and are never sent externally. Xunlei name search sends only the selected media/file/custom keyword to the fixed HTTPS `api-shoulei-ssl.xunlei.com/oracle/subtitle` endpoint; remote playback URLs and headers remain inside Rust.
- Subtitle downloads allow only fixed trusted provider domains, bounded redirects/response sizes, allowlisted subtitle extensions, generated cache filenames, and the shared Player `cache/subtitles` directory. Shooter/Xunlei download URLs remain in a short-lived Rust map while Vue holds opaque references. Xunlei downloads are restricted to the allowlisted HTTPS `subtitle.v.geilijiasu.com` host.
- Player updates use the Tauri updater minisign trust root. Commit only the public key; keep the private key outside the repository and in GitHub Actions Secret storage, require signed artifacts, and fail release builds when the secret is absent.
- Update discovery is pinned to the HTTPS `yuanjing-hash/OhMyCine` GitHub Releases API and exact release-asset manifest path. Do not expose custom updater URLs. Portable updates may target the current executable directory but must not delete `portable.flag` or portable data directories.

---

## Scenario: 115 Cross-Source Read and Managed Materialization

### 1. Scope / Trigger

- Trigger: adding or changing `cloud.ReadDriver`, 115 file export, provider-to-local or provider-to-provider Transfer, resumable materialization, or cross-source staging cleanup.
- This capability is internal to the Transfer worker. It is not a generic URL fetch API and does not expose a provider download URL to handlers, jobs, WebSocket events, logs, or the Web UI.

### 2. Signatures

```go
type ReadRequest struct {
    FileID string
    Offset int64
}

type ReadResult struct {
    Body           io.ReadCloser
    OffsetAccepted bool
    TotalSize      *int64
}

type ReadDriver interface {
    Driver
    OpenRead(context.Context, ReadRequest) (ReadResult, error)
}
```

- Managed files live only below `<global staging>/.omc-cross-source/<transfer UUID>/`; durable state stores that task-relative marker and per-file size/SHA1/status, never the absolute path or temporary provider URL/header.

### 3. Contracts

- The 115 adapter resolves the stable file identity and obtains each expiring provider URL only inside `OpenRead`. Cookies, Authorization, pickcodes, acquisition headers, response bodies, and the URL never cross the adapter boundary or enter persistence/logging.
- The initial URL and every redirect must be HTTPS on port 443, contain no userinfo or fragment, and resolve only to public addresses. Validate once before dispatch and again in the transport dial path so a DNS answer cannot rebind to loopback, private, link-local, multicast, or unspecified addresses.
- Follow no more than three redirects and never allow HTTPS downgrade. On every hop retain only the exact `User-Agent`, `Range`, and `Accept-Encoding` needed for the media read; drop Cookie, Authorization, Referer, and every other inherited header.
- A non-zero resume offset is accepted only with `206` and a matching `Content-Range` start. If the provider safely ignores Range, close that response and restart the task-owned partial from byte zero. Any contradictory range or source size fails closed.
- Before and after streaming, revalidate Storage root -> immutable package root -> exact file ID/parent/size/SHA1. Bound the response stream to the frozen remaining manifest size plus at most one proof byte; an oversized or endless CDN response must stop before it can fill staging. Write only to a task-owned `.partial`, flush it, verify full size plus SHA1, and atomically rename before recognition or target import can observe it.
- Cancellation may remove only `.partial` files below the exact task UUID root. Completed materialized files remain available for safe retry/manual correction. Successful target reconciliation may remove the complete task root after revalidating ownership; it must never recurse from the global staging root or follow symlink/Junction/Reparse Point boundaries.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Initial or redirected URL is HTTP, non-443, contains userinfo/fragment, or resolves privately | Reject before sending bytes; expose only a stable safe error |
| DNS validation is public but dial-time resolution is private | Reject the dial as rebinding; do not connect |
| Redirect count exceeds three | Stop the request; retain the resumable task checkpoint |
| Redirect carries Cookie/Authorization or an unrelated provider header | Strip it before the redirected request |
| Resume response is `200` instead of a valid `206` | Close it and restart from zero without appending duplicate bytes |
| Provider file size/SHA1/parent/root changes | Fail closed; retain provider data and do not expose a completed staging file |
| Cancellation observes completed and partial files | Remove only task-owned partials; preserve completed files and provider/library data |
| Successful reconciliation cleanup meets an unsafe path or ownership mismatch | Retain the task root and report retryable cleanup failure |

### 5. Good / Base / Bad Cases

- Good: a 115 file resumes from a verified partial through one public HTTPS redirect, matches size/SHA1, atomically finalizes, uploads to the target library, and then removes only its transfer UUID root.
- Base: the CDN ignores Range; the worker closes the response, truncates the same task-owned partial, and restarts from byte zero.
- Bad: persist the CDN URL, forward the 115 Cookie to a redirect host, trust only the preflight DNS answer, append a `200` body to an existing partial, or recursively delete `.omc-cross-source`.

### 6. Tests Required

- Cover initial URL scheme/port/userinfo/fragment, public/private DNS answers, dial-time DNS rebinding, HTTPS downgrade, redirect count, and per-hop header stripping.
- Cover partial resume, ignored Range restart, invalid `Content-Range`, source size/SHA1 drift, atomic finalize, restart checkpoint reuse, cancellation partial-only cleanup, and successful task-root cleanup.
- Assert database rows, Job payload/checkpoint, DTOs, WebSocket events, audit, and logs contain no temporary URL, Cookie, Authorization, pickcode, provider response, or absolute staging path.

### 7. Wrong vs Correct

Wrong:

```go
checkpoint.URL = temporary.URL
request.Header = temporary.Headers
http.DefaultClient.Do(request)
os.RemoveAll(globalStagingRoot)
```

Correct:

```go
stream, err := reader.OpenRead(ctx, cloud.ReadRequest{FileID: file.ID, Offset: partialSize})
// The adapter validates every URL/DNS hop and exposes only the response stream.
verifySizeAndSHA1(partial, file)
atomicFinalizeWithinTaskRoot(partial)
```

---

## Config Sync Security

Default Player ↔ Server sync is structural only.

Do sync by default:

- Data source name/type.
- URL/base URL.
- paths, media library IDs, ordering, display metadata.

Do not sync by default:

- API keys, cookies, passwords, AI keys, PT passkeys, downloader passwords.

Full credential sync requires explicit user confirmation and clear destination disclosure.

---

## Plugin and Hub Security

Hub is a distribution site, not a trusted runtime backend. Third-party plugins are untrusted by default.

Rules:

- Do not auto-install or auto-update plugins by default.
- Show plugin permissions before install/update.
- Do not grant plugins global credential access.
- Record plugin install/enable/update/delete in audit logs.
- Prefer WASM or external-process isolation for long-term plugin runtime. Go plugin loading may remain a candidate but must not be treated as a settled safe default.
- High-risk permissions include arbitrary network access, file deletion, credential read, system command execution, and user/permission mutation.

---

## AI Data Boundary

AI features are primarily Player-side unless explicitly designed otherwise.

Server-side AI work, if introduced, must:

- Store AI keys as credentials.
- Avoid sending local absolute paths, credentials, proxy URLs, cookies, or passkeys to LLM providers by default.
- Keep recommendations constrained to media the user owns/has indexed.
- Never allow AI to directly delete files, submit downloads, or change configuration without explicit user action.

---

## Logs and Audit

Security-relevant events should be auditable:

- Login success/failure.
- User and permission changes.
- Connection/downloader/site/storage/category changes.
- Download/follow creation and deletion.
- File delete/move/rename.
- STRM cleanup.
- Plugin install/enable/update/delete.
- Proxy authorization failures.

Audit logs must not include sensitive field values.

Download cancellation/deletion must be confirmed in the UI and provider-first in the service. Cancellation and default deletion call provider `Cancel(taskID, false)` to remove the provider task while retaining files; destructive source/temporary-file deletion requires an explicit `delete_data=true` opt-in. Remove local DownloadTask/Job facts only after provider success or explicit task-not-found. Provider failure or a missing Downloader reference with a non-empty provider task ID retains the local record. A missing provider task ID permits ordinary local cleanup, but destructive cleanup requires a verified OMC-owned output boundary or fails closed. If Submit returns after cancellation, persist the late provider identity and immediately retry `Cancel(..., false)` with a fresh bounded context; failure must remain diagnosable and retryable. Record only task/resource IDs, outcome, and stable cleanup status in audit metadata—never source URLs, provider responses, filenames, or staging paths.

Post-import garbage cleanup is a narrower system-owned staging operation, not permission to recursively clean a source directory. Its candidates come only from the strict difference between a complete provider manifest and its complete, verified package-selected manifest after transfer reconciliation succeeds, but unselected videos and unmatched subtitles are protected leftovers and never automatic deletion candidates. Revalidate canonical manifest identity, local root ancestry/type/reparse/size, or the 115 Storage-root → immutable package-output-root → exact item ID/parent/size/SHA1 chain at every item; any incomplete/non-subset/non-canonical manifest, missing package-root snapshot, or changed item fails closed. qBittorrent copy/symlink data stays protected until seeding cleanup is acknowledged, and copy may use whole-package `deleteData=true` only when the immutable manifests contain no protected leftovers; re-evaluate this immediately before every provider deletion so legacy task state cannot bypass the rule.

---

## Scenario: Registered Local Storage Roots

### 1. Scope / Trigger

- Trigger: adding or changing local Storage registration, probing, path-based media access, Windows filesystem handling, or the Storage administration API/UI.
- Applies across the explicit SQLite migration, Storage model/service, local driver, Gin routes, permission catalog, audit metadata, and Web UI controls.
- A registered Storage is a physical access boundary. It is not a MediaLibrary, StorageDestination, scan request, or authorization to modify files beneath the root.

### 2. Signatures

- Table: `storages`; `type` is currently restricted to `local`; nullable `connection_id` is reserved for the future Connection migration and has no premature foreign key.
- Management APIs:
  - `GET /api/v1/storages`
  - `POST /api/v1/storages`
  - `PATCH /api/v1/storages/{id}`
  - `DELETE /api/v1/storages/{id}`
  - `POST /api/v1/storages/{id}/test`
- Stable permissions: `storages.read`, `storages.create`, `storages.update`, `storages.delete`, and `storages.test`.
- Driver entry points: `LocalDriver.CanonicalizeRoot(input string)`, `LocalDriver.ProbeRoot(root string)`, and `storage.Constrain(root, candidate string)`.
- Stable path errors: `storage_path_not_absolute`, `storage_path_not_found`, `storage_path_not_directory`, `storage_path_reparse_point`, `storage_path_outside_root`, and `storage_unreadable`.

### 3. Contracts

- Persist a canonical absolute root plus a comparison form. Windows comparison is case-insensitive and must deliberately support drive-letter and UNC roots.
- Registration validates only the configured root. Reject a root that is a symlink, junction, mount point, or other Reparse Point. Future traversal must still re-check every traversed component before any file operation.
- Probe is bounded and read-only: open the root, read at most one directory entry, close it, and query capacity. Never create a sentinel file to infer writability and never recursively enumerate media during Storage registration/test.
- On Windows, capacity reports bytes available to the current caller (`GetDiskFreeSpaceEx` caller-available value), not volume-wide free bytes that may ignore quotas.
- Driver capabilities are generated by the driver and stored as a snapshot. The client cannot claim unsupported cloud, offline-download, direct-URL, signed-proxy, cursor, or watch capabilities.
- Create/update/test/delete are independently permission-protected in the router and service. The Web UI reuses generated permission constants, but hiding a control never replaces API authorization.
- Delete removes the Storage configuration only. It must not enumerate, rename, move, overwrite, or delete any child path.
- Audit metadata may record resource ID, type, enabled state, outcome, and stable error code. It must not contain the configured absolute root, child names, raw OS errors, ACLs, or usernames.
- Database unique constraints are the concurrency authority. Preflight checks improve UX, but create/update races must map SQLite uniqueness failures to stable `storage_name_conflict` or `storage_path_conflict` responses instead of leaking SQL or returning a generic 500.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Empty or relative root | Reject with `storage_path_not_absolute`; create no record |
| Missing root | Reject with `storage_path_not_found` |
| Root resolves to a file | Reject with `storage_path_not_directory` |
| Root is a symlink/junction/Reparse Point | Reject with `storage_path_reparse_point` |
| Candidate is the root itself or a normalized descendant | Accept from `Constrain` |
| Candidate is a sibling, another volume/share, or escapes with `..` | Reject with `storage_path_outside_root` |
| Directory can be opened but capacity is unavailable | Preserve readable/available state and return `storage_capacity_unknown` with null capacity values |
| Duplicate normalized name/root is submitted concurrently | Return stable conflict code and HTTP 409 |
| Actor lacks any one Storage action permission | The matching API returns 403 even if another Storage permission is present |
| Storage configuration is deleted | Remove only the database row; filesystem contents and timestamps remain unchanged |

### 5. Good/Base/Bad Cases

- Good: an authorized operator registers an existing Windows or UNC directory, sees a bounded health/capacity summary, and the audit event contains no absolute path.
- Good: a second concurrent request loses the unique race and receives `storage_path_conflict`, while the Server log retains internal diagnostic context without exposing it through the API.
- Base: capacity lookup fails after the directory was read successfully; the Storage remains registered/readable and reports an explicit unknown-capacity state.
- Bad: the probe creates and deletes a temporary file in the user's real media root to test write access.
- Bad: deleting a Storage record recursively cleans the root, or audit metadata stores `root_path`/a child filename.
- Bad: Windows path comparison is case-sensitive, or a sibling prefix such as `D:\\media-old` is accepted beneath `D:\\media`.

### 6. Tests Required

- Migration tests cover fresh install, idempotency, and an actual previous-version database upgrading to the Storage migration.
- Local driver tests cover relative/missing/file roots, root symlink/Reparse Point rejection, empty and non-empty read-only probes, root/descendant/sibling traversal, and no file mutation.
- Windows tests cover drive-letter and UNC acceptance, case-insensitive comparison, caller-available capacity behavior where injectable, and explicit skip reporting when the test process cannot create a symlink.
- Service tests cover name/root normalization, duplicate preflight and unique-race mapping, stable safe errors, capabilities, audit redaction, and delete-config-only behavior.
- Router tests cover authentication plus independent denial for list/create/update/delete/test permissions; frontend tests cover route/navigation/action visibility from generated constants.
- Windows PowerShell 5 live/API tests that send non-ASCII names or paths must encode `ConvertTo-Json` output with `[Text.Encoding]::UTF8.GetBytes(...)` and declare `application/json; charset=utf-8`. Passing a Unicode JSON `String` directly to `Invoke-RestMethod -Body` may use the system code page and falsely surface a valid directory as `storage_unreadable`.
- Run `server/test.ps1`, `go build -tags webui ./cmd/server`, root and Web UI `go mod verify`, and `git diff --check`.

### 7. Wrong vs Correct

#### Wrong

```go
// Mutates the user's media root and still does not prove future writes are safe.
probe := filepath.Join(root, ".ohmycine-write-test")
if err := os.WriteFile(probe, nil, 0o600); err != nil {
    return err
}
_ = os.Remove(probe)
```

```go
// Prefix checks accept siblings such as D:\\media-old.
if !strings.HasPrefix(candidate, root) {
    return ErrOutsideRoot
}
```

#### Correct

```go
canonical, err := localDriver.CanonicalizeRoot(input)
if err != nil {
    return mapSafeStoragePathError(err)
}
probe := localDriver.ProbeRoot(canonical) // bounded read + capacity only
```

```go
relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
if err != nil || relative == ".." ||
    strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
    filepath.IsAbs(relative) {
    return ErrOutsideRoot
}
```

## Scenario: 115 Share and Manual-Transfer Intake

### 1. Scope / Trigger

- Trigger: accepting a 115 share link, receiving shared files, adopting a provider item from an intake directory, or exposing intake configuration in API/UI/logs.

### 2. Signatures

- Public source: `115_share` with URL/query extraction code accepted only by `pan115_offline`.
- Internal source: `provider_item`; service-created only and rejected by HTTP.
- Private snapshots: encrypted DownloadTask source, `staging_provider_directory_id`, hashed `ingest_source_key`.
- Safe public fields: `source_origin`, intake enabled state, downloader ID/name, and Storage-relative intake display path.

### 3. Contracts

- Share URLs and extraction codes are credential-like because links grant access. Encrypt them with AES-256-GCM using task-scoped purpose/AAD; never place plaintext or ciphertext in Job payloads, DTOs, WebSocket events, audit metadata, logs or exports.
- Provider item/directory IDs are private capabilities. Clients select an intake directory only through an actor/Connection/Storage/root/purpose/expiry-bound token; services revalidate ancestry at save, submit, sweep and worker execution.
- Intake root and final library root must not overlap in either direction. Enabled intake roots on one Connection must not overlap each other. A direct-child sweep is bounded and never recursively adopts arbitrary account content.
- 115 responses, URLs, Cookie and provider paths are untrusted external data. Use the controlled client limits/risk lane and map failures to stable codes without logging response bodies.
- Destructive cancellation requires the existing confirmation boundary and may recycle only the task/adopted root proven below the snapshotted intake root. No failure path may delete the final library root or a sibling.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Client submits internal provider ID/source kind | Reject before persistence/provider access |
| Token actor, purpose, Storage, Connection, root or expiry mismatches | Reject safely and expose no provider identity/path |
| Intake/final roots overlap or an intake root moved outside Storage | Reject configuration/execution; perform no receive, transfer or delete |
| Share response is malformed/ambiguous | Reconcile the stable system directory; fail closed if facts remain insufficient |
| Any log/error/DTO contains link, code, Cookie, provider ID/path or raw body | Redact/remove before fan-out; regression test the serialized boundary |

### 5. Good / Base / Bad Cases

- Good: encrypted share source plus opaque directory token produces a stable system folder, and public surfaces show only safe IDs/counts/status.
- Base: life event carries no proof of completion; it only wakes a bounded authoritative directory listing.
- Bad: accept `provider_item` from JSON, log a share URL for diagnostics, trust the event payload as a completed item, or delete by an unvalidated provider ID.

### 6. Tests Required

- Inspect SQLite, Job payload/checkpoint, API DTO, WebSocket/audit/runtime logs and exported logs for absence of link, extraction code, provider IDs/paths, Cookie and upstream bodies.
- Test token tampering/replay/cross-actor/cross-Connection/cross-Storage/purpose/expiry, moved directories, root `0`, overlap in both directions and destructive-cancel confinement.
- Test bounded share parsing/snap/receive, redirect/response limits, risk-control mapping, ambiguous-success reconciliation and idempotent task-not-found cleanup.

### 7. Wrong vs Correct

#### Wrong

```go
job.PayloadJSON = marshal(map[string]string{"share_url": request.URL})
log.Error().Err(err).Str("provider_item_id", itemID).Msg("share receive failed")
```

#### Correct

```go
task.SourceCiphertext = credentials.Encrypt(taskPurpose, source)
job.PayloadJSON = marshal(downloadJobPayload{DownloadTaskID: task.ID})
OperationPan115ShareIngest.Event(log.Error()).
    Str("task_id", task.ID).Str("error_code", safeCode).
    Msg(OperationPan115ShareIngest.Message("失败"))
```

## Scenario: AI Recognition and Corrective-Reorganization Trust Boundaries

### 1. Scope / Trigger

- Trigger: configuring Server-side AI recognition, sending recognition evidence to an LLM, revealing its API Key, or moving already-imported media after an identity correction.

### 2. Signatures

- Credential purpose: `settings:ai-recognition:api-key:v1`; reveal uses the existing allowlisted `/api/v1/credentials/reveal` action and never an ordinary settings response.
- Runtime AI gate: `RuntimeConfig() (Config, enabled, error)` returns before key decryption when disabled.
- OpenAI-compatible Base URL is a public HTTPS/443 API prefix ending in `/v1`, including origin + `/v1` and bounded prefixes such as `/api/v1`; provider endpoints append `models` or `chat/completions` exactly once.
- Reorganization preview/confirm/query routes and v50 tables follow the contracts in `downloader-management.md` and `transfer-organization.md`.

### 3. Contracts

- AI is advisory identity evidence only. It cannot submit downloads, mutate configuration, lock an unverified TMDB ID, choose paths, confirm conflicts, move/delete files or consume a reorganization token.
- Prompt payloads contain only bounded normalized release text and opt-in relative basenames. Never send absolute paths, Cookie/token/passkey/API key, magnet/torrent URL, provider item ID, Connection/downloader/library identity, signed URLs or upstream bodies.
- Custom OpenAI-compatible origins use the SSRF-safe client with allowed scheme, DNS/IP policy, redirect/timeout/body limits. Empty query/fragment delimiters are invalid, and this client disables environment proxies so its protected dial resolves and validates the actual configured origin rather than a proxy endpoint. A non-empty API prefix must use bounded unescaped path segments, end in `/v1`, and reject percent encoding, dot segments, duplicate separators and backslashes. Endpoint joining removes a duplicated leading `/v1` from the resource path only when the validated base already ends in `/v1`. Google native mode ignores custom origins and uses its fixed official API.
- Reorganization authorization is ownership plus an opaque one-time actor-bound preview token. Database stores only its hash. File authority comes only from active managed-item manifests and revalidated configured roots/stable provider ancestry, never from an AI response or browser-authored path.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| AI feature disabled | Return before decrypt/provider/network; deterministic local recognition continues |
| AI Base URL targets a denied scheme/address or redirects outside policy | Reject configuration/probe/request with safe code; expose no resolved address/body |
| AI Base URL is `https://host/api/v1/` | Normalize to `https://host/api/v1`; probe `/api/v1/models` and generate at `/api/v1/chat/completions` |
| AI Base path contains `%`, `..`, `//`, `\\`, too many/long segments, or does not end in `/v1` | Reject as invalid configuration before network access |
| AI JSON is oversized, malformed, schema-invalid or references unknown candidate | Reject output and use safe deterministic fallback |
| Preview token is expired/replayed/cross-actor or its bound revisions changed | Reject before any file/provider mutation |
| Managed path/item no longer proves root ancestry, type and size | Stop at that item, retain identity and all unproven data |

### 5. Good / Base / Bad Cases

- Good: AI selects `c2`; Server validates it is in the supplied candidate set, performs `GetByID`, records provisional source, and does no file action.
- Good: an OpenRouter-compatible `/api/v1` prefix uses the same controlled client and reaches each endpoint without a duplicated `/v1`.
- Base: AI times out; the queue continues with deterministic provisional/local provisional behavior.
- Bad: log prompts/responses, send `D:\Downloads\...`, let an LLM return a target path, scan a corrected title directory to decide what to move, or special-case one provider hostname while accepting ambiguous paths elsewhere.

### 6. Tests Required

- Assert disabled runtime performs zero decrypt/factory/HTTP calls and explicit admin probe remains separately permissioned/audited.
- Assert custom-origin SSRF/redirect/timeout/size controls, fixed Google origin, strict response schemas, encrypted storage/reveal allowlist and log redaction.
- AI Provider 模型目录和结构化生成使用彼此独立的响应边界：OpenAI-compatible/Google 模型列表最多读取 4 MiB，结构化生成响应及其最终内容继续限制为 256 KiB。模型列表过大或无效必须映射为列表语义的安全错误，禁止返回或记录上游正文、API Key、Authorization 或 Google API Key header。
- Assert empty-prefix, `/v1` and `/api/v1` models/test/structured-generation request paths, plus rejection of encoded/dotted/duplicate/backslash prefixes without making a request.
- Assert preview token hash/replay/expiry/actor/revision bindings plus local/115 root/item revalidation and unmanaged-file preservation.

### 7. Wrong vs Correct

Wrong:

```go
prompt.Files = absolutePaths
os.Rename(model.OldPath, model.NewPath)
requestURL := base + "/v1/chat/completions" // duplicates /v1 for /api/v1 bases
```

Correct:

```go
result := provider.Adjudicate(boundedRelativeEvidence)
identity := validateCandidateRefAndGetByID(result)
plan := managedManifestPlanner.Preview(actor, identity)

base := validatePublicHTTPSVersionedPrefix(config.BaseURL)
requestURL := appendOpenAIResource(base, "/v1/chat/completions")
```

## Scenario: Public-BT Rendered Fetcher Boundary

### 1. Scope / Trigger

- Trigger: configuring FlareSolverr, connecting an optional CloakBrowser companion, or routing a public BT adapter through browser rendering.

### 2. Signatures

```text
RenderedFetchRequest {
  ProfileID string
  URL string
  AllowedHosts []string
  UserAgent string
  Timeout time.Duration
  MaxBytes int64
}

NewFlareSolverrFetcher(endpoint string)
NewCloakBrowserFetcher(loopbackEndpoint string)
```

- Automatic rendered profiles are a Server allowlist, currently `1337x` and `EXT.to`. The public request surface never accepts a solver endpoint, profile ID, rendered target URL or allowed-host override.

### 3. Contracts

- Validate the target before dispatch and the final URL after the solver returns. Both must use HTTPS, default port 443, an exact registered profile host, and a public resolved address; reject userinfo, fragments, internal/link-local/loopback/multicast/private addresses and DNS rebinding.
- CloakBrowser is a separately installed companion reachable only through loopback IPC/HTTP. OhMyCine may provide configuration and health checks but does not download, bundle or redistribute the proprietary browser binary.
- FlareSolverr is a bounded configured fallback for the same Site only. Do not accept arbitrary scheme/host redirects or environment-proxy behavior that bypasses target validation.
- `RenderedFetchRequest` is credential-free. PT Cookie/passkey/API key, Authorization, 115 Cookie and proxy credentials remain inside their owning Server clients and are never forwarded to either solver.
- Limit connect/request time, redirect count, envelope size and final HTML size. Destroy temporary Flare sessions and browser contexts on success, error, timeout and cancellation. One Site failure does not retain a cross-Site cookie jar.
- Ordinary DTOs, SSE, WebSocket, audit, browser storage and logs expose neither solver URLs/profiles nor request/response bodies. Diagnostics use stable provider-neutral codes only.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| HTTP target, non-443 port or unregistered profile/host | Reject before solver access |
| Host resolves to private/loopback/link-local/multicast address | Reject before connection |
| Final URL crosses the exact profile host set | Reject the page and discard its body |
| Cloak endpoint is non-loopback | Reject configuration/initialization |
| Cloak health unavailable | Fall back only to same-Site Flare when configured; otherwise site-level unavailable |
| Flare/Cloak response exceeds envelope/HTML limit | Abort and return a stable invalid-response error |
| PT configuration contains Cookie/passkey | Omit them from rendered request and solver payload/logs |
| Context is cancelled | Close response/session/context promptly and retain no reusable cross-Site state |

### 5. Good / Base / Bad Cases

- Good: 1337x resolves to a public address, Cloak renders its exact HTTPS profile URL, and the final URL remains on the registered host.
- Base: Cloak is not installed; the same Site uses its configured FlareSolverr and other Sites continue normally.
- Bad: expose `/render?url=...`, accept `http://127.0.0.1`, forward a PT Cookie, or trust the solver's off-host `solution.url`.

### 6. Tests Required

- Unit tests cover scheme/port/userinfo/fragment, exact host sets, public/private DNS answers, final-URL revalidation, loopback-only companion endpoints and bounded time/body behavior.
- Provider routing tests cover Cloak preference, Flare same-Site fallback only on unavailable, no validation-error fallback, cancellation cleanup and Site failure isolation.
- Serialization/log tests seed Cookie/passkey/solver URLs/upstream bodies and assert none appear in API/SSE/WebSocket/audit/browser-storage/runtime-log projections.

### 7. Wrong vs Correct

#### Wrong

```go
page := cloak.Render(ctx, request.URL, site.Cookie)
return page.HTML // trusts the companion's redirect and body size
```

#### Correct

```go
request := registeredRenderedRequest(profile, exactURL) // no credentials
validatePublicRenderedTarget(request)
page := fetcher.Fetch(ctx, request)
validateRenderedFinalURL(page.FinalURL, request.AllowedHosts)
```
