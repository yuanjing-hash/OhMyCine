# Technical Design

## 1. Scope and task map

This parent task owns the shared notification contract and final end-to-end review. Implementation is split into two children:

1. `08-24-server-media-server-refresh` — authoritative media-change revisions/outbox, Emby/Jellyfin refresh targets, persistent refresh jobs, and Server administration UI.
2. `08-24-player-server-library-refresh` — device-authenticated change delivery, reconnect compensation, Player cache invalidation, and the non-disruptive refresh UX.

The children share the `MediaLibraryChange` contract below. Neither child may invent a provider-specific or Player-only definition of “media is ready”.

## 2. Fundamental invariants

- A provider event, downloader completion, transfer completion, or scan start is only a wake-up signal. It is not proof that media is consumable.
- The database catalog and managed artifact state are authoritative. Notifications are emitted only after the required state commits.
- Emby/Jellyfin refresh and Player refresh consume the same committed change independently. Failure in one consumer does not roll back or delay the other.
- Real-time delivery is an optimization. Persistent revisions and bounded catch-up are the correctness mechanism.
- Events carry only safe logical identities and revisions. Consumers re-read details from their authenticated API.
- Player remains independently useful without Server; direct DataSources are outside this invalidation domain.

## 3. Authoritative media-change model

### 3.1 Persistence

Add an additive SQLite migration for:

- `media_library_changes`
  - monotonic public sequence / opaque cursor source;
  - `library_id`;
  - per-library `content_revision`;
  - bounded change kind set such as `catalog`, `metadata`, `artifacts`, `removed`;
  - source generation/run identity stored privately;
  - readiness state and timestamps;
  - no item path, provider ID, credential, signed URL, or raw upstream payload.
- `media_server_refresh_targets`
  - OhMyCine library, Connection, stable upstream library ID, safe label snapshot, enabled flag, revision, and latest desired/successful content revision.
- `media_server_refresh_runs`
  - target, requested content revision, Job ID, safe status/error code, attempts and timestamps.

`MediaLibrary` gains a monotonic `content_revision` or equivalent revision projection. A transaction that changes user-visible catalog state increments it once and creates/coalesces a pending change record in the same transaction.

### 3.2 Readiness barrier

The producer determines when a change becomes ready:

- local media whose Player/server delivery is immediately valid: after the catalog transaction commits;
- cloud/115 media requiring managed STRM: after the matching artifact generation completes successfully and active artifact mappings exist;
- metadata/manual recognition changes: after the recognition projection and any required replacement artifacts commit;
- removals: after the authoritative reconciliation commits; media-server refresh may wait for owned artifact cleanup when the configured projection requires it;
- partial, failed, superseded, conflict-waiting, or stale-generation work never advances a ready revision.

Readiness publishes one wake-up to in-memory waiters only after the transaction commits. The durable row remains the source for retry and reconnect.

### 3.3 Coalescing

Multiple ready changes for one library within a bounded debounce window advance the desired revision but need not create one external refresh per file. Queue payloads contain only stable record IDs. The existing Job coalescing generation is used with a target-scoped key; workers query the latest desired revision instead of relying on a stale payload.

Outbox retention is bounded by age and count. A Player cursor older than retained history receives `resync_required` plus the current accessible library revisions rather than an unbounded replay.

## 4. Media-server refresh consumer

### 4.1 Adapter boundary

Introduce a provider-neutral interface under `pkg/mediaserver`, for example:

```go
type Client interface {
    Probe(context.Context) (ServerInfo, error)
    ListLibraries(context.Context) ([]Library, error)
    RefreshLibrary(context.Context, string) error
}
```

Emby and Jellyfin remain explicit adapters even where endpoints overlap. They reuse bounded HTTP primitives, fixed validated Connection endpoints, encrypted API keys, no redirects, timeouts, response-size limits, and safe provider error mapping.

### 4.2 Target binding and jobs

- Administrators select an upstream library from a tested Connection; the Server saves its stable ID and a safe display label.
- A target-scoped system Job consumes the latest desired revision, invokes `RefreshLibrary`, and records the successful revision.
- One failing target does not block other targets or Player delivery.
- Automatic, manual, retry, and restart-recovery paths use the same Job and run records.
- `media_servers.refresh` controls execution; existing connection permissions control configuration visibility/mutation as appropriate.

### 4.3 Administration UI

Extend the Player Management workspace rather than the Storage page. It shows:

- Emby/Jellyfin connection health;
- bound OhMyCine library → upstream library targets;
- latest desired/successful revision and truthful running/failed state;
- test, manual refresh, retry, edit, and disable actions with permission gates.

No page exposes API keys, upstream paths, private Job payloads, or raw responses.

## 5. Player change delivery

### 5.1 Transport choice

Use authenticated bounded long polling through the existing native `server_request_json` bridge instead of the management Cookie WebSocket or a browser WebSocket query token:

```text
GET /api/v1/player/media-changes?cursor=<opaque>&wait_seconds=12
Authorization: Bearer omc_player_...
```

Reasons:

- the existing Rust bridge already enforces the Player route prefix, strict Bearer header, redirect rejection and bounded JSON responses;
- browser WebSocket/EventSource cannot attach the required Authorization header without adding a weaker query/cookie credential path;
- a held request wakes immediately on change, while a 12-second heartbeat stays below Player’s 20-second request timeout and Server’s 60-second write timeout;
- every reconnect naturally re-authenticates the device token and uses the durable cursor for catch-up.

The response contains only an opaque next cursor, `resync_required`, and bounded changes:

```json
{
  "cursor": "opaque",
  "resync_required": false,
  "changes": [
    {"library_id":"12","content_revision":44,"kinds":["catalog","artifacts"],"changed_at":"..."}
  ]
}
```

Before returning a woken response, Server revalidates the device/user and filters every library through the same Player visibility policy used by catalog APIs. Long-poll heartbeat, rate, concurrency and result size are bounded.

### 5.2 Player lifecycle and catch-up

- A reusable service/composable owns one loop per enabled ServerDataSource.
- It starts after source initialization, pauses/disposes on source disable/removal or app teardown, and uses bounded exponential backoff with jitter while offline.
- The opaque cursor and safe library revisions may persist in Player app settings; tokens and event payload details do not.
- `resync_required` invalidates all accessible physical libraries for that Server source and continues from the returned cursor.
- A failure remains source-scoped and never blocks Home, playback, or other DataSources.

### 5.3 UI behavior

For each received ready change:

1. invalidate only the matching Server source root/library snapshots;
2. invalidate the aggregated Home cache and start a background Home refresh;
3. if the user is currently inside the affected Server list, retain the displayed items, scroll, selection and playback context;
4. show one coalesced “媒体库已更新” notice with a refresh action;
5. on action, reload the current logical level, ignore stale requests, preserve navigation identity, and replace the list without forcing scroll-to-top;
6. if the user leaves and re-enters, load the latest state automatically.

Playback views never reload the active stream because of a library-change notification.

## 6. API and security boundaries

- Management REST stays under Cookie Session + Origin/CSRF and server-side permission checks.
- Player changes stay under the existing strict device Bearer middleware; no token in query, cookie, route state, log, or WebSocket subprotocol.
- Event cursors are opaque, bounded, non-authorizing resume hints. Authorization is repeated on every request.
- DTOs exclude paths, provider identities, credentials, signed URLs, upstream temporary URLs and raw errors.
- Disable user, password reset, logout, same-device login and explicit device revoke prevent future polls; a bounded held request revalidates before delivering.
- External media-server calls use fixed administrator-tested endpoints, context deadlines, no credential-bearing redirects and redacted structured logs.

## 7. Compatibility and rollout

- All schema changes are additive and SQLite-first.
- Existing Emby gateway and Player catalog DTOs continue to work when no refresh target exists.
- Older Players ignore the new endpoint and continue TTL/manual refresh behavior.
- New Players connected to an older Server treat endpoint absence as unsupported, back off permanently for that source version, and keep normal browsing.
- No connection, target or event feature is enabled implicitly from matching names/paths.

## 8. Operational and rollback shape

- Feature registration is isolated: producers can stop creating changes without changing media files or artifacts; consumers can be disabled independently.
- Removing/disable a refresh target deletes or stops only its configuration/jobs, never media-server libraries or OhMyCine media.
- Rollback leaves additive tables/columns unused; old binaries must continue to read existing tables.
- Outbox cleanup removes only expired notification metadata, never catalog, artifact, media, credentials or device records.

## 9. Documentation impact

Update backend/frontend specs, `docs/architecture/02-server-design.md`, `03-player-design.md`, `06-roadmap.md`, `07-security-design.md`, and OpenAPI if present. Document long polling as the Player notification transport and keep the broader planned WebSocket event scope intact.
