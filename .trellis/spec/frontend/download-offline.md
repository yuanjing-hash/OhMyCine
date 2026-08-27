# Player Download and Offline Media Contract

> Executable cross-layer rules for the Vue/Pinia, Tauri/Rust, SQLite, desktop filesystem, and Android SAF download path.

---

## 1. Scope / Trigger

Apply this contract whenever Player code changes download planning, queue control, provider resolution, segmented transfer, offline assets, offline browsing, local-first playback, or deletion.

The Player owns this feature and must remain useful without Server. Server is only an optional source resolver. Server pipeline downloads and Player offline downloads are separate actions.

## 2. Signatures

### Stable enqueue request

`player_download_enqueue(request: DownloadEnqueueRequest) -> DownloadTask`

The request may contain only stable identity and bounded display metadata:

```ts
interface DownloadEnqueueRequest {
  sourceId: string
  sourceType: string
  itemId: string
  displayName: string
  mediaType: string
  expectedBytes?: number
  destinationDirectory?: string
  parentId?: string
  groupName?: string
  mediaSourceId?: string
  variantId?: string
  libraryId?: string
  onlineIdentity?: { libraryId: string, workId: string, segmentId: string, versionId: string }
  detailSnapshot?: OfflineDetailSnapshot
}
```

It must not gain URL, redirect, Header, Cookie, credential, token, signature, or provider-secret fields.

### Commands and events

- Settings: `player_download_settings`, `player_download_update_settings`, `player_download_default_directory`, `player_download_set_default_directory`, `player_download_pick_directory`.
- Queue: `player_download_list`, `player_download_enqueue`, `player_download_pause`, `player_download_resume`, `player_download_retry`, `player_download_cancel`, `player_download_remove`.
- Offline: `player_download_offline_list`, `player_download_offline_detail`, `player_download_resolve_local`, `player_download_sync_attachments`, `player_download_offline_asset`.
- Events: `player-download:progress` carries `DownloadTask`; `player-download:removed` carries only `{ taskId }`.

### SQLite facts

- `downloads.sqlite`: `download_tasks`, `download_segments`, and internal `download_cleanup` facts.
- `offline_media.sqlite`: `offline_packages`, `offline_items`, and `offline_assets`.
- `download_tasks` persists stable media/version identity, destination reference/name, status, byte facts, retry facts, safe error text, grouping, and attachment state.
- `download_segments` persists byte ranges and checkpoints only.
- `offline_assets.relative_asset_path` is package-owned and relative; the database must not store its acquisition URL or Headers.

## 3. Contracts

### Scheduling and state

- Settings validate `concurrentTasks` in `1..=8`, `segmentsPerTask` in `1..=16`, and an optional positive global bytes-per-second limit.
- Product UI exposes one `下载` concept: it creates the complete offline package, including the video, detail snapshot, artwork, subtitles, and danmaku. Do not add a parallel `离线下载` entry for the same operation.
- Download confirmation must use the shared `MediaActionController` confirmation runtime and `MediaActionConfirmationDialog`. Do not call Tauri dialog `ask`/`message` for the core download path; capability ACL differences must not make Download unusable.
- Desktop media-action popovers must clamp both position and maximum height to the live viewport. Keep the header fixed and make the action-group region independently scrollable so every download and management action remains reachable.
- The scheduler claims the oldest runnable task. A user-paused task never consumes a slot and remains paused after restart; an interrupted active task returns to the queue.
- Desktop segmentation is allowed only when total length, trustworthy Range semantics, and entity identity are available. Otherwise transfer safely falls back to one stream.
- All task and segment workers share one global rate schedule. Runtime setting changes reset old reservations.
- Public status is `queued | interrupted | resolving | downloading | finalizing | paused | failed | completed`; request/cleanup facts are internal and must not become retry cards.

### Resolution and resume

- Every network attempt resolves a fresh transfer request from stable identity. Temporary URL/Header material lives only in Rust memory and is never serializable as a task/event.
- Cross-origin redirects clear provider Headers; HTTPS must never downgrade to HTTP.
- Resume requires a correct `206 Content-Range` plus a stable entity proof: strong ETag, or Last-Modified together with total size. If identity or coverage changes, delete only that task's owned partial/checkpoints and restart.
- Server physical media and Emby/Jellyfin resolve their exact entry/item/media-source identity again. Server online plugin media remains disabled unless a purpose-limited stable offline-stream contract exists.

### Cancellation, removal, and file ownership

- Cancel means stop writes, delete the task row/segments and exact owned partial/final file when applicable, emit `player-download:removed`, and disappear from UI. It is not a failed or retryable state.
- A temporarily failed cleanup creates only an internal cleanup fact, retried on startup. It must not retain a user-visible task.
- Desktop paths are constrained under the selected root and reject traversal, symlink, junction, and reparse-point escape. Android uses a persisted SAF tree grant and exact owned document names.
- Removing task history without `deleteFile` preserves the offline item/package. Removing with `deleteFile` deletes the exact video and its item assets; shared package assets survive until the last item is removed.

### Offline package and local-first playback

- A video becomes offline-ready only after atomic finalization and the offline item transaction. Attachment failure does not downgrade the video.
- `OfflineDataSource` owns route identity under `__offline__`; original source identity stays in the offline record for progress/history and exact online fallback.
- Playback asks `player_download_resolve_local` before online resolution. Rust validates root/SAF ownership, existence, size, and stored fingerprint. Invalid local facts are removed and online resolution may continue.
- Offline history, progress sync, and completion events use the original source/item identity, never the offline row ID.
- Series hierarchy is source-scoped until the public `MediaItem` contract provides a cross-provider stable `seriesIdentity`; display title must not be represented as globally stable identity.

### Attachment boundary

- Accepted kinds are `poster | backdrop | still | subtitle | danmaku`; a sync request contains at most 24 attachments and 24 failed-kind facts.
- Remote attachment transport is a single Tauri command input only. Accept HTTP(S), bounded redirects, at most 32 Headers, 4 KiB per Header value, and 16 KiB total Headers. Reject Host, Range, Content-Length, hop-by-hop, and request-shaping Headers.
- Enforce kind-specific size, MIME/signature, UTF-8, and JSON structure limits; danmaku is capped at 200,000 validated entries.
- Write a validated new content-addressed file and commit its DB row before deleting an older complete asset. A failed retry must preserve the previous valid asset and must not change `complete` to `failed`.
- Asset reads never create directories. Writes/deletes reject symlinks and Windows reparse points and use same-directory temporary write, `sync_all`, and rename.

### Async SQLite ownership

- Tauri async commands and spawned workers must not hold `rusqlite::Connection`, `Statement`, `Rows`, transaction guards, or row borrows across an `.await`. Finish the query/update scope and drop every SQLite value before awaiting Android SAF, network, or filesystem plugin work; reopen the database afterwards when a follow-up transaction is required.
- Cleanup flows use the same ordering: read the exact owned cleanup target into an owned DTO, close SQLite, await the platform cleanup, then reopen SQLite to delete or retain the cleanup fact. This keeps the Future `Send` and avoids statement-temporary lifetime errors that desktop-only compilation may not expose.

## 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Enqueue identity/name is invalid or destination escapes root | Reject before task/file creation with safe text |
| Native dialog message/ask capability is unavailable | The shared in-app media confirmation still opens and Download remains usable |
| Media-action rows exceed the remaining viewport height | Clamp the popover and scroll only its action-group region; never render unreachable rows below the window |
| Concurrent/segment setting exceeds bounds or limit is zero | Reject settings update; preserve prior settings |
| Range is ignored, malformed, or entity proof is absent | Use safe single stream or restart; never append untrusted bytes |
| Entity changes during resume | Remove only owned partial/checkpoints and restart from zero |
| Temporary URL returns recoverable auth/expiry/network failure | Re-resolve with bounded retry; expose failure only after exhaustion |
| User pauses | Preserve checkpoints and show Resume, not Retry |
| User cancels | Remove task/UI fact and exact owned temporary file; keep only internal cleanup fact on cleanup failure |
| Completed local file is missing or fingerprint differs | Remove stale offline fact and fall back online when available |
| Attachment URL/Header/MIME/size/content is invalid | Mark attachment partial with safe error; keep video and any previous complete asset |
| Source/Server is unavailable at cold start | OfflineDataSource still lists and opens persisted snapshots/assets/video |
| Server online plugin lacks an offline resolver | Disable Player offline download with a clear reason; do not reuse a playback-only stream unsafely |
| A Tauri async path needs SQLite facts before and after Android SAF work | Materialize owned values, close SQLite before `.await`, perform SAF work, then reopen SQLite for the final transaction |

## 5. Good / Base / Bad Cases

- Good: a four-segment desktop transfer resumes after an expired 302 only after the same entity is proven, then atomically creates an offline item.
- Good: the single Download action opens the in-app summary confirmation, enqueues the complete offline package, and later exposes the separate downloaded badge.
- Good: a poster refresh fails after the old poster was downloaded; the old complete poster remains visible and attachment state becomes partial only for missing work.
- Base: a provider does not support trustworthy Range; Player downloads one stream with the same global task/rate controls.
- Base: Android SAF supports sequential write only; queue concurrency and shared rate semantics remain consistent while per-task segments degrade to one.
- Good: Android cancellation loads the owned document name, drops the SQLite connection, awaits SAF deletion, then opens a new connection to clear or retain the cleanup fact.
- Bad: store the final CDN URL in `download_tasks`, forward Authorization to a CDN redirect, append bytes after ETag changed, or expose `cancelled` as Retry.
- Bad: keep a `rusqlite::Connection`, prepared statement, row iterator, or transaction guard alive while awaiting an Android plugin call.
- Bad: add a second Offline Download button for the same queue operation, or depend on Tauri dialog `ask`/`message` ACL for the only Download path.
- Bad: delete the package directory when removing one episode, use a title as global series identity, or delete an existing asset before replacement bytes are validated and registered.

## 6. Tests Required

- Rust: schema migration/idempotency, stable task serialization, scheduler fairness, pause/recovery, cancellation races, exact cleanup, segment coverage, Content-Range parsing, entity change, shared live rate-limit update, and local fingerprint correction.
- Rust security: traversal/symlink/reparse rejection, redirect downgrade/origin behavior, visible error redaction, attachment Header/MIME/size/JSON bounds, cascade ownership, and failed retry preservation.
- TypeScript: current media version/static variant selection, grouped plan behavior, the single Download entry, in-app confirmation without native dialog ACL, viewport-bounded media actions, download-center controls/settings, offline route ownership, badge aggregation/removal, local-first playback/history identity, and mobile entry points.
- Integration fixture: local HTTP Range, ignored Range, expiring 302, 401/403, early EOF, entity change, and final checksum. Do not mark this complete from static source assertions alone.
- Platform compile: compile the Rust crate for `aarch64-linux-android` and assemble the universal debug APK so non-`Send` async futures and Android-only bindings are checked in addition to desktop Rust gates.
- Manual: Windows interruption/limit/302/offline cold-start and Android SAF/notification/cancel/restart/offline playback. Use isolated profiles and preserve owner data.

## 7. Wrong vs Correct

Wrong:

```ts
await enqueueDownload({ ...media, url: stream.url, headers: stream.headers })
```

```ts
await ask(downloadSummary) // native dialog ACL can disable the only Download path
```

```rust
fs::remove_dir_all(package_dir)?; // one episode was deleted
```

Correct:

```ts
await enqueueDownload(target, {
  mediaSourceId: selectedMediaSourceId,
  variantId: selectedVariantId,
  detailSnapshot,
})
```

```ts
const result = await requestMediaActionConfirmation(downloadConfirmation)
if (result.confirmed)
  await enqueueCompleteOfflinePackage(plan)
```

```rust
let resolved = resolve_from_stable_identity(&task).await?;
validate_range_and_entity(&resolved, &checkpoint)?;
delete_only_item_owned_assets_then_prune_empty_package(&offline_item)?;
```
