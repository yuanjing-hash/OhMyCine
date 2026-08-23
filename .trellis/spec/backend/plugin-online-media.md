# Plugin Online Media Gateway

> Executable contracts for exposing enabled plugin connections as Player online libraries without exposing provider credentials or upstream URLs.

## Scenario: Player online libraries, history, progress, and assets

### 1. Scope / Trigger

- Trigger: changing plugin online-library DTOs, Player-device routes, provider history/progress calls, playback-plan assets, or Host HTTP streaming.
- The Server is the only plugin runtime. Player consumes normalized DTOs and Server same-origin assets; it never calls provider APIs or receives persistent provider credentials.

### 2. Signatures

Player Device Bearer routes:

```text
GET  /api/v1/player/online-libraries
GET  /api/v1/player/online-libraries/:id/navigation
GET  /api/v1/player/online-libraries/:id/navigation/:nodeToken/children
GET  /api/v1/player/online-libraries/:id/feeds/:routeKey
GET  /api/v1/player/online-libraries/:id/search
GET  /api/v1/player/online-libraries/:id/items/:itemId
POST /api/v1/player/online-libraries/:id/items/:itemId/playback
POST /api/v1/player/online-libraries/:id/items/:itemId/download
POST /api/v1/player/online-libraries/:id/items/:itemId/progress
GET  /api/v1/player/online-history
GET|HEAD /api/v1/player/online-assets/:opaque
```

Plugin history request and response:

```json
{"connectionId":"uuid","cursor":null,"pageSize":24}
{"list":[],"cursor":"opaque-provider-cursor","hasMore":true}
```

Progress events are `started|progress|paused|resumed|stopped|completed` and include exact `itemId`, `segmentId`, `versionId`, bounded positions, an idempotency key, and optional occurrence time.

### 3. Contracts

- Every route requires an authenticated Player device with `media_libraries.read`; handlers and services both enforce the boundary.
- Only enabled installations and enabled plugin connections are published. Every invoked operation must also exist in the active Manifest capability set.
- Core code knows only generic plugin operations and DTOs. Provider-specific APIs, cursors, IDs, and error bodies remain inside the plugin.
- `navigationMode` defaults to `flat`. A plugin may return branch nodes only when its Manifest explicitly declares `hierarchical`; existing v1 flat array responses remain accepted unchanged.
- Hierarchical navigation uses the strict v2 `{version:2,mode:"hierarchical",nodes:[...]}` envelope. The Server accepts only `branch|feed|search|user-library`, limits one level to 100 nodes and the active path to 8 levels, and rejects unknown fields, sibling IDs, ancestor node-key reuse, excessive identifiers, malformed leaf routes, and unsupported node kinds.
- A branch node key never crosses the Player boundary. The Server replaces it with a short-lived HMAC token bound to the online library/connection, branch kind, depth, complete ancestor chain and expiry. Child requests accept only this token and reject tampered, expired, cross-library, non-branch, cyclic or over-depth claims.
- Provider errors map to stable Server codes and safe messages. Never return a plugin-supplied message directly through ordinary APIs.
- A single-library history query passes the provider cursor through opaquely. Aggregate history encodes a bounded base64url map of connection ID to provider cursor or an internal exhausted sentinel.
- Aggregate history must remember exhausted sources. A source that exactly fills one page but reports `hasMore=false` must not be called again; the next page must reach later sources.
- `hasMore` and `cursor` must agree, provider rows must not exceed the requested remaining page size, and malformed aggregate sources are marked exhausted so they cannot create an infinite cursor. The same malformed response in single-library mode returns `plugin_response_invalid`.
- The Server injects reliable `libraryId` into history rows. It never deduplicates records by title.
- Playback-plan UUID asset references are rewritten to same-origin `/api/v1/player/online-assets/{uuid}` URLs. Asset lookup revalidates installation state, connection, permission, domain, and package generation on every read.
- Online assets allow GET/HEAD and one Range only. Stream responses preserve only allowlisted media headers, support 206/416, use `Cache-Control: no-store`, and never buffer whole remote media.
- Host HTTP disables ambient environment proxies, resolves and dials a validated public IP, revalidates every redirect, and strips Cookie/Authorization on cross-origin redirects.
- Download requests select a physical local media library explicitly or by its configured ordering, snapshot its classification/naming/transfer settings, and enter the existing persistent `download` queue. The plugin returns only a validated `DownloadPlan`; it never selects a filesystem path or command line.
- Every plugin download attempt resolves a fresh plan from immutable Work/Segment/Version/Variant identities. Plans may contain one video asset, optional sidecars, or exactly one DASH video/audio pair with a fixed `dash-av` merge. Asset references are UUIDs bound to the owning plugin, package generation and exact connection and are never persisted as reusable upstream URLs.
- Plugin downloads write only below `<staging>/.ohmycine-plugin-downloads/<download-task-uuid>`. Cancel, retry, terminal-history deletion, and post-transfer cleanup revalidate this exact task boundary and reject directories or symlinks. DASH merge uses the host-owned MediaTool with fixed FFmpeg arguments and no plugin-provided flags.
- A plugin returns only provider-specific `DownloadPlan` and optional `ProviderMetadata`. Server owns download, merge, queues, classification/naming snapshots, conflict handling, local transfer, cloud upload, NFO/artwork rendering, cleanup and library reconciliation. A plugin never receives a local absolute path, Storage credential, 115 Cookie, or raw upload/move/delete capability.
- `media.metadata` is connection-scoped. The Host binds plugin ID, actual package version, connection, work and segment identities, validates the response, and persists an immutable task snapshot. A saved snapshot is consumed before consulting the current installation, so disabling or uninstalling the plugin cannot break an already queued transfer. The capability is never registered as a scraper for local/115 scans, qBittorrent downloads or another plugin.
- Provider NFO/artwork is rendered by Server and appended to both selected and complete managed manifests. Ordinary provider video can bypass the generic package minimum-size rule only when its exact plugin download identity has already been validated; non-plugin flows retain the normal advertisement filter.
- The target library snapshots the Storage connection/root, Profile, naming, transfer mode and conflict policy at submission. Local staging routes through the existing local transfer executor; an upload-capable cloud target routes through the Server-only `cloud.UploadDriver`. Cloud drivers reconcile ambiguous prior results before retry and plugins never select remote paths.
- Manifest `settingsPage` is a versioned Host-owned component tree. Only tabs, sections, notices, credential status, switches, text, number and select fields are supported, and every field must remain within `configSchema` including enums and numeric bounds. A plugin controls where `credential-status` appears; the Host renders login state, QR/re-login actions and the QR image at that declared position while retaining credential capture, polling and encrypted storage. Generic credential controls are a legacy fallback only when no declarative login component exists. Plugins cannot inject routes, Vue, JavaScript, HTML or CSS.
- Every external HTTPS URL returned for Host-rendered authentication UI, including a QR-code target, must use a hostname covered by the active package's exact `network.http` permission. Provider login-domain migrations therefore require a new explicitly reviewed package permission; the Host must reject an undeclared hostname instead of widening trust dynamically.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Missing/revoked Player token or permission | Return 401/403 with no plugin invocation |
| Installation or connection disabled | Return a safe unavailable/not-found result |
| Capability absent | Reject before invoking WASM |
| Hierarchical response is malformed, too wide/deep, cyclic, duplicated, or uses an unsupported node kind | Return `plugin_response_invalid`; expose no partial tree |
| Navigation token is expired, tampered, non-branch, or replayed against another library | Reject before invoking WASM |
| Provider returns `hasMore=true` without cursor, too many rows, or invalid JSON | Single library: `plugin_response_invalid`; aggregate: exhaust that source and continue |
| First aggregate source is exhausted after filling the page | Cursor records exhaustion and the next request starts at a later source |
| Asset reference is unknown, expired, or package changed | Return `plugin_asset_expired` without revealing internals |
| Asset targets private/reserved IP, unsafe redirect, or unauthorized domain | Reject before response bytes are exposed |
| Cross-origin redirect occurs | Remove Cookie and Authorization before the next hop |
| DownloadPlan contains a path, raw URL, header reference, extra audio/video, unknown asset type, or mismatched identity | Reject with `plugin_response_invalid`; create no unmanaged output |
| Download asset expires before completion | Resolve one fresh plan in the current attempt; never reuse the old URL reference |
| FFmpeg is missing or merge fails | Persist a safe media-tool error and retain only task-scoped managed output for controlled retry/deletion |
| Provider metadata identity/version/connection does not match the task | Reject it as `plugin_response_invalid`; do not fall back to another plugin provider |
| Cloud upload is ambiguous after a timeout/restart | Retain staging and reconcile the exact target name/size before retry; do not upload a duplicate |
| Declarative settings contain an unknown component, schema-external key, enum-external option or wider numeric range | Reject the Manifest or configuration before runtime invocation |

### 5. Good / Base / Bad Cases

- Good: Bilibili returns a provider cursor, Server stores it opaquely, and the next Player page resumes without title-based merging.
- Good: a plugin supplies a danmaku asset reference; Player receives only the authenticated Server URL while Cookie and provider URL remain in Host memory.
- Base: one online source fails in an aggregate history request; other enabled sources still render and the bad source does not loop forever.
- Bad: expose a signed provider URL in DTO/history, proxy arbitrary plugin URLs, trust plugin error text, or reset aggregate history to its first source on every page.

### 6. Tests Required

- Service tests cover permission duplication, enabled-state checks, capability checks, safe error mapping, `libraryId` injection, single-source cursor pass-through, aggregate exhaustion, exact-page boundaries, malformed-source isolation, and cursor tampering.
- Navigation tests cover v1 compatibility, strict v2 parsing, branch token binding, sibling duplication, ancestor cycles, depth/width limits, expiry, tampering and cross-library rejection.
- Host tests cover GET/HEAD, 206/416, response-size/header limits, private-IP rejection, DNS rebinding, redirect revalidation, cross-origin credential stripping, and package/permission revalidation.
- Download tests cover plan identity/topology validation, plugin-owned assets, single-file and DASH execution, subtitle/danmaku manifests, task-root confinement, retry re-resolution, cancellation/deletion cleanup, and fixed MediaTool behavior.
- Metadata/transfer tests cover provenance backfill, immutable snapshot reuse after plugin disable, package-version binding, no cross-plugin/global invocation, Server-rendered NFO/artwork manifests, local import, 115 upload conflict/retry reconciliation, staging retention and task-root confinement.
- Settings tests share the Manifest contract across Go, JSON Schema and Web UI and reject unknown components, duplicate bindings, schema-external fields, invalid select options and widened numeric constraints.
- Authentication fixtures preserve the provider's complete current response shape (with opaque credentials replaced), and tests assert that every returned QR/login hostname is explicitly declared while broad provider-domain wildcards remain absent.
- HTTP tests cover Player Device Bearer only, no-store, route parameter bounds, and safe error envelopes.
- Run `go test ./...`, `go vet ./...`, both Server builds, and a Windows isolated runtime smoke.

### 7. Wrong vs Correct

#### Wrong

```go
return pluginMessage, pluginPlaybackURL
```

#### Correct

```go
safeError := mapPluginErrorToStableServerError(pluginEnvelope)
assetURL := "/api/v1/player/online-assets/" + validatedOpaqueReference
```

## Scenario: Plugin-owned library artwork

### 1. Scope / Trigger

- Trigger: adding or changing plugin library-card artwork, Manifest packaging, package extraction, Player online-library summaries, or artwork HTTP routes.

### 2. Signatures

- Manifest v1 optional field: `libraryArtwork: string`.
- Public inert asset route: `GET /api/v1/assets/plugin-covers/:packageSha256`.
- Online library DTO optional field: `artworkUrl`, containing only a Server-relative `/api/v1/assets/` path.

### 3. Contracts

- `libraryArtwork` is a package-relative PNG/JPEG/WebP path of at most 240 characters; absolute paths, backslashes, dot segments, SVG, HTML, empty files and files larger than 4 MiB are forbidden.
- The packer includes exactly the declared artwork and the Server validates extension, magic bytes, size, managed-tree digest and active installation before every read.
- The public URL is content-addressed by the lowercase package SHA-256. It contains no Player token, plugin credential, installation path, connection ID or provider URL.
- Public artwork is permitted only because it is inert, bounded release content. Provider media, user artwork, history and playback assets remain behind Player Device authentication.
- Player resolves relative artwork only against the configured Server origin and only below `/api/v1/assets/`; cross-origin, userinfo and other paths are rejected.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Missing/unsafe/active-content Manifest path | Reject the Manifest before installation |
| Declared file missing, oversized, linked or magic bytes mismatch extension | Reject package extraction/validation |
| Digest malformed, unknown, disabled or no longer active | Return 404 without package or plugin details |
| Managed package changed after install | Return a safe package-invalid error and no bytes |
| Player receives cross-origin artwork URL | Discard it and render the normal fallback |

### 5. Good / Base / Bad Cases

- Good: an enabled plugin declares `assets/library-cover.png`; Player receives a same-origin digest URL and renders it on the library card.
- Base: an old plugin has no `libraryArtwork`; the library remains usable with the existing fallback.
- Bad: embed a Player Bearer in an image query, serve arbitrary package files, trust MIME from the Manifest, or load a remote plugin-provided cover URL directly.

### 6. Tests Required

- JSON Schema and Go Manifest tests reject traversal and active-content extensions.
- Pack/extraction tests require the declared raster and reject magic-byte substitution and tree mutation.
- Service/HTTP tests cover active-package lookup, disabled/unknown digest, inert MIME, cache headers and `nosniff`.
- Player verification covers same-origin resolution, library/category mapping and cross-origin rejection.

### 7. Wrong vs Correct

Wrong:

```json
{"artworkUrl":"https://plugin.example/cover.svg?token=player-token"}
```

Correct:

```json
{"artworkUrl":"/api/v1/assets/plugin-covers/0123456789abcdef..."}
```

## Scenario: Generated dynamic library artwork

### 1. Scope / Trigger

- Trigger: changing physical/115/plugin library cover candidates, generated cover caching, Player library DTOs, or the generated artwork HTTP route.

### 2. Signatures

- Physical library DTO: `artwork_url`, `artwork_revision`, `artwork_source`.
- Online plugin library DTO: `artworkUrl`, `artworkRevision`, `artworkSource`.
- `artwork_source` / `artworkSource`: `generated | provider | custom | fallback`.
- Plugin capability: `library.artwork_candidates` (WASM v1 operation code `13`).
- Plugin response: `[{"id":"stable-media-id","assetRef":"uuid"}]`, at most 9 rows.
- Generated asset route: `GET /api/v1/assets/generated-library-covers/:contentSha256?exp=<unix>&sig=<base64url-hmac>`.

### 3. Contracts

- The index owner generates the card: Server generates for Server-managed physical, 115, and plugin libraries; Player generates for independently indexed local/raw sources. A Server DTO never exposes candidate URLs or `assetRef` values.
- Physical candidates come from distinct matched recognition rows that still own at least one current library entry. Episode joins must not duplicate one recognition before the query limit and crowd out other works.
- Plugins return only stable media identity plus a connection-bound Host asset UUID. The Host rechecks package generation, plugin/connection ownership, declared domain, public IP, redirects, MIME, compressed size and decoded dimensions before use.
- Candidate identity is deduplicated and sorted deterministically. At most 9 candidates participate in the generation key and at most 4 decoded images are rendered into one 1280x720 JPEG.
- The generation key covers template version, library title and sorted candidate identities. The public revision/path digest is SHA-256 of the actual encoded JPEG bytes; never label mutable bytes with an input-only digest while advertising `immutable`.
- Generated bytes and generation-key mappings share one bounded LRU lifecycle. Evicting content also removes every key mapping to that digest.
- Generated cover URLs use a process-private HMAC ticket. Expiration is quantized to a fixed 15-minute bucket so repeated list requests reuse the URL; actual validity is 15-30 minutes. The response is `private`, short cached and `nosniff`.
- A failed or empty generation leaves the previous provider/static fallback usable. Logs contain only module, library kind, plugin ID and stable error code, never upstream artwork URL, headers or asset UUID.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Plugin returns an empty/oversized list, duplicate ID, unknown field or non-UUID asset | Reject as `plugin_response_invalid`; keep fallback |
| Candidate body exceeds the compressed limit, MIME is not JPEG/PNG, or decoded dimensions/pixels exceed the bound | Skip that candidate without allocating the full hostile image |
| All candidates fail | Keep fallback; do not return an empty card |
| Digest, `exp` or HMAC is malformed, expired or unknown | Return 404 without revealing cache state |
| Candidate order changes but membership does not | Produce identical bytes and revision |
| Candidate membership or actual rendered bytes change | Produce a new revision and URL |

### 5. Good / Base / Bad Cases

- Good: a 115 library with one matched work produces a one-image landscape card; four distinct works produce a deterministic four-panel cover.
- Good: Bilibili registers recommendation covers with Host assets; Server renders them and Player sees only one signed same-origin JPEG URL.
- Base: scanning is unfinished or TMDB/plugin artwork is unavailable; the existing static local/cloud/plugin cover remains visible.
- Bad: send Bilibili `pic`, TMDB paths, Cookie headers or asset UUIDs to Player; make `/generated-library-covers` anonymous; hash only candidate IDs but serve changed bytes as immutable.

### 6. Tests Required

- Generator tests assert stable ordering, 1280x720 output, content-digest equality, bounded LRU cleanup, signed-ticket success/tamper/expiry and hostile decoded-dimension rejection.
- Database tests create many episode rows for one recognition and prove other distinct works remain candidates.
- Contract tests reject duplicate identities, raw URLs, non-UUID assets, unknown/trailing JSON and more than 9 rows.
- Plugin tests verify capability/operation alignment and that only Host asset references are returned.
- Player verification asserts same-origin generated artwork, revision/source mapping, cache invalidation and absence of Server candidate arrays.

### 7. Wrong vs Correct

Wrong:

```json
{"artworkUrl":"https://i0.hdslb.com/bfs/archive/poster.jpg?token=...","artworkRevision":"BV1..."}
```

Correct:

```json
{"artworkUrl":"/api/v1/assets/generated-library-covers/<jpeg-sha256>?exp=...&sig=...","artworkRevision":"<jpeg-sha256>","artworkSource":"generated"}
```
