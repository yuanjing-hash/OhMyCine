# Technical Design

## 1. Ownership

This child owns the Player-facing change query/long-poll service, Player notification controller, cache invalidation and refresh UX. It consumes the parent’s durable ready-change contract and does not create media changes or media-server refresh jobs.

## 2. Player API

Add a Bearer-only endpoint under the existing player group:

```text
GET /api/v1/player/media-changes?cursor=<opaque>&wait_seconds=12
```

Behavior:

- DeviceAuth runs first and stores actor/device context.
- Validate cursor length/shape and clamp wait duration/result count.
- Query ready changes after cursor, filtered through the same accessible-media-library policy as Player catalog.
- If empty, register a bounded waiter and return immediately when a ready change commits or after heartbeat timeout.
- Re-query the database and revalidate user/device before responding; in-memory wake data is never returned directly.
- Return a new opaque cursor, bounded change summaries, and `resync_required` when retention no longer covers the cursor.
- Use the standard JSON envelope, `Cache-Control: no-store`, safe errors and request cancellation.

The cursor is a non-authorizing opaque resume marker. Bearer authentication and library visibility are checked on every poll.

## 3. Native HTTP bridge

Reuse `server_request_json` with the existing Bearer header and route allowlist. Keep the Server wait below both its 60-second write timeout and the bridge’s 20-second total timeout; 12 seconds is the default heartbeat.

If testing shows transport overhead can approach the bridge timeout, add a narrowly scoped optional timeout field clamped for this Player endpoint only. Do not weaken redirect, path, method, body or response-size restrictions.

## 4. Player notification controller

Create a testable service/composable with explicit dependencies:

- source ID/base URL and credential reader;
- request function;
- cursor persistence boundary;
- callbacks for ready changes and capability unsupported;
- lifecycle start/stop and cancellation.

Guarantees:

- one in-flight poll per Server source;
- abort/ignore stale results after config revision, credential change, disable/remove or dispose;
- bounded exponential backoff with jitter for network/5xx errors;
- 401/403 stops and lets normal reconnect UX own credentials;
- 404/unsupported capability stops the loop for that Server version without noisy retries;
- cursor persists only after a valid response is handled;
- resync invalidates the whole Server source; normal changes carry library/revision scope.

## 5. Store invalidation contract

Extend the DataSource store with source/library-scoped invalidation rather than clearing every media cache:

- invalidate Server source-root snapshot;
- mark affected library revision stale;
- invalidate aggregated Home and call `loadHomeSections({ force: true, background: true })` once for a coalesced batch;
- publish a typed local event for views currently displaying that source/library;
- never call credential removal, playback-history cleanup, raw-source cache clearing or unrelated `DataSource.clearCache` paths.

Multiple revisions arriving before a refresh merge to the highest revision per library.

## 6. View behavior

`SourceLibraryView`/the owning list surface listens for the typed event:

- if source/library does not match, ignore it;
- if at source root or a logical Server folder, keep current refs and navigation stack;
- show one non-destructive update chip/banner with refresh action;
- on action, capture scroll anchor/position and current selection, increment the existing load generation, force the current logical request, ignore stale responses, apply results, then restore the best available anchor/position;
- clear the prompt only through the highest applied revision;
- on route/source/library change, consume the stale marker by loading latest normally.

`PlayerView` does not subscribe to list reload events. Global Home may update behind playback without touching mpv state.

## 7. Security and data handling

- Device token remains only in the provider-specific credential store and native request header.
- Cursor/revision persistence is non-sensitive and source-scoped.
- Runtime validation parses all Server responses from `unknown`, clamps arrays/text/revisions and rejects unknown unsafe shapes.
- Error redaction uses existing ServerDataSource rules; do not include request URL query when it may contain cursor internals.
- App teardown and source removal cancel requests/listeners to prevent an old device/source from mutating a replacement source.

## 8. Testing

- Server service/router tests cover device auth, revocation during wait, permission filtering, cursor retention/resync, cancellation, no-store and redaction.
- TypeScript tests use a fake poll bridge for immediate change, heartbeat, batching, out-of-order/stale response, retry/backoff, unsupported old Server, disable/remove and multiple sources.
- Store/view tests cover home background refresh, current-list banner/action, scroll/selection preservation, next-entry refresh, playback isolation and non-Server DataSource isolation.
- Rust tests cover any narrowly scoped bridge timeout change and retain all route/token/redirect constraints.
