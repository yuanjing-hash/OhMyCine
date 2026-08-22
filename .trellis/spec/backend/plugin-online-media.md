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

### 5. Good / Base / Bad Cases

- Good: Bilibili returns a provider cursor, Server stores it opaquely, and the next Player page resumes without title-based merging.
- Good: a plugin supplies a danmaku asset reference; Player receives only the authenticated Server URL while Cookie and provider URL remain in Host memory.
- Base: one online source fails in an aggregate history request; other enabled sources still render and the bad source does not loop forever.
- Bad: expose a signed provider URL in DTO/history, proxy arbitrary plugin URLs, trust plugin error text, or reset aggregate history to its first source on every page.

### 6. Tests Required

- Service tests cover permission duplication, enabled-state checks, capability checks, safe error mapping, `libraryId` injection, single-source cursor pass-through, aggregate exhaustion, exact-page boundaries, malformed-source isolation, and cursor tampering.
- Host tests cover GET/HEAD, 206/416, response-size/header limits, private-IP rejection, DNS rebinding, redirect revalidation, cross-origin credential stripping, and package/permission revalidation.
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
