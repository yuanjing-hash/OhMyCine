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

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Missing/revoked Player token or permission | Return 401/403 with no plugin invocation |
| Installation or connection disabled | Return a safe unavailable/not-found result |
| Capability absent | Reject before invoking WASM |
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
- Host tests cover GET/HEAD, 206/416, response-size/header limits, private-IP rejection, DNS rebinding, redirect revalidation, cross-origin credential stripping, and package/permission revalidation.
- Download tests cover plan identity/topology validation, plugin-owned assets, single-file and DASH execution, subtitle/danmaku manifests, task-root confinement, retry re-resolution, cancellation/deletion cleanup, and fixed MediaTool behavior.
- Metadata/transfer tests cover provenance backfill, immutable snapshot reuse after plugin disable, package-version binding, no cross-plugin/global invocation, Server-rendered NFO/artwork manifests, local import, 115 upload conflict/retry reconciliation, staging retention and task-root confinement.
- Settings tests share the Manifest contract across Go, JSON Schema and Web UI and reject unknown components, duplicate bindings, schema-external fields, invalid select options and widened numeric constraints.
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
