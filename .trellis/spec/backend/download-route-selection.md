# Download Route Selection and 115 Ownership Contract

> Executable rules for source compatibility, downloader execution, final MediaLibrary placement, and conflict-free 115 life-event adoption.

## 1. Scope / Trigger

Apply this contract whenever Server, Follow, a plugin, or WebUI selects or derives a Site, source kind, Downloader, Storage, Connection, provider directory, or target MediaLibrary.

The authoritative identities are:

```text
source/site -> execution Downloader -> Downloader Storage/Connection
            -> compatible target MediaLibrary -> Transfer / Import
```

A `pan115_offline` Downloader is one integrated 115 route:

```text
115 Downloader = bound 115 Storage/Connection + download directory
                 + optional life-event auto-listen
```

It does not gain a second intake directory. MediaLibrary must not own a new provider-intake directory or Downloader binding.

## 2. Source Compatibility

- A PT Site produces a private `.torrent` handoff and may use only a non-cloud BT Downloader that explicitly supports torrent submission, such as qBittorrent or a future Transmission adapter.
- A 115 native-offline Downloader never accepts a Site whose authoritative `SiteType` is `pt`. Never convert a private torrent to magnet to bypass this boundary.
- A Site whose authoritative `SiteType` is `bt` may reach 115 when it resolves directly to a provider-supported magnet/HTTP(S)/ed2k URL, or when its bounded `.torrent` payload is safely parsed and converted from the exact raw `info` dictionary to a BTIH magnet.
- Torrent bytes, tracker shape, host names, and browser labels never establish public provenance. The persisted/reloaded Site definition does. Unknown Site provenance fails closed before conversion.
- A 115 target MediaLibrary accepts a 115 Downloader only when their Storage records have the same non-null `connection_id`.
- A local target never accepts 115 native offline; different 115 Connections never cross.
- Follow defaults choose one complete compatible tuple. Never independently select all Sites, the first Downloader, and the first MediaLibrary.
- WebUI filtering is advisory. Follow Create/Update, Worker pre-search, Site handoff, and Download submit reload authoritative records and fail closed through the same helper.

## 3. 115 New Download Contract

- 115 share receive stays in `下载管理 -> 新建下载`; there is no dedicated 115 sidebar or route.
- After selecting a 115 Downloader, WebUI shows `离线下载 / 分享转存` and compatible MediaLibraries on the same Connection.
- Both modes use the Downloader's configured download directory. UI must not ask for an intake/staging directory.
- Explicit offline/share tasks select a compatible target MediaLibrary and snapshot its Profile, naming, transfer, and notification route.
- Share URL/extraction facts are encrypted in DownloadTask and never enter public DTOs, events, logs, audit metadata, localStorage, or sessionStorage.
- Provider receive/download completion is not import completion. The task continues through manifest verification, recognition, Transfer, MediaLibrary reconciliation, and notification.

## 4. 115 Directory Ownership

### 4.1 Server-owned tasks

- Every new OMC native-offline or share task owns a stable direct child named `omc-<download-task-id>` below the Downloader directory.
- The Server creates/reconciles this directory idempotently before provider submission and freezes its provider identity in the task snapshot.
- Offline and share provider output must target that directory. The owning Download Worker alone monitors and imports it.
- Retry reconciles the original provider task and stable directory before any submission; it must not create a second provider task or directory when output exists.

### 4.2 User/App-owned content

- Enabling `自动监听生活事件` on a 115 Downloader means: adopt ordinary direct children manually placed in its existing download directory through the 115 App.
- Provider life events only wake the supervisor. An authoritative bounded directory listing determines candidates.
- Delayed stability rechecks are coalesced per 115 Connection: an event storm may have at most one pending recheck for that Connection. Service shutdown cancels and waits for pending rechecks instead of leaving detached goroutines.
- The supervisor skips the full `omc-*` namespace. A user-created `omc-*` item is not adopted and produces a safe diagnostic warning.
- A normal direct child becomes eligible only after a quiet window and two equivalent authoritative snapshots, so partially received content is not claimed.
- Claim identity is a digest of `connection_id + downloader_id + provider_item_id`; a database unique constraint is the concurrency authority.
- Duplicate events, concurrent sweeps, missed events plus periodic reconciliation, and process restart must create at most one durable task per provider item.
- The adopted task references the discovering Downloader and remains inside that Downloader's 115 Storage/Connection. Existing classification/Profile/destination rules choose only a compatible MediaLibrary on that 115; ambiguous classification becomes needs-action and never crosses Storage.
- A failed or needs-action task retains its claim. Retry operates on the same durable task.

### 4.3 Directory boundaries

- An enabled listened Downloader directory must be within its bound Storage root.
- On the same Connection it must not overlap another enabled listened Downloader directory or any final MediaLibrary root.
- Save/enable performs authoritative provider ancestry checks. Legacy conflicts are marked for repair; no real provider item is moved or deleted automatically.

## 5. Status and Lifecycle

- pan115 status mapping is `0 -> queued`, `1 -> downloading`, `2 -> completed`, `-1 -> failed`.
- Unknown provider status is non-terminal/retryable; it must not be fabricated as failed.
- Manifest and provider item boundaries are revalidated after completion.
- Source cleanup is allowed only after Transfer/Import reconciliation succeeds under the frozen route. Unsafe or unrecognized content is retained and exposed as needs-action.
- Download terminal transitions update Follow episode claims so subscriptions cannot remain permanently downloading.
- Cancelling an active download pipeline is available during download, recognition, waiting/needs-action, Transfer, Import, and retry states, including after provider completion. It first calls provider `Cancel(taskID, false)` to remove the provider task while retaining downloaded files, then stops OhMyCine jobs/retries, records cancelled history, and releases Follow claims. Provider task-not-found is idempotent success; any other provider failure preserves the local pipeline facts and must not report a false cancellation.
- `POST /downloads/:id/cancel` is idempotent pipeline cancellation. `DELETE /downloads/:id` is a separate idempotent terminal-record operation: its default `delete_data=false` removes the provider task while retaining files before deleting local facts; explicit `delete_data=true` also requests deletion of source/temporary files. Missing provider identity permits ordinary local cleanup, while destructive deletion requires a verified OMC-owned cleanup boundary or fails closed.
- If cancellation wins while provider `Submit` is in flight, the worker persists the late provider identity and immediately calls `Cancel(taskID, false)` using a fresh bounded cleanup context. A cleanup failure remains visible on the cancelled task so the default DELETE can retry it.

## 6. Legacy Compatibility

- `MediaLibrary.ingest_downloader_id`, `ingest_provider_root_id`, and `ingest_relative_root` are legacy read-only compatibility facts for existing tasks/configuration; new WebUI/API saves do not require or expose them as current configuration.
- WebUI omits legacy intake fields from every new MediaLibrary write. Server ignores those fields on create and preserves the complete existing legacy intake snapshot on unrelated updates, so editing naming, scan, or STRM settings cannot silently disable an in-flight legacy route.
- Existing intake DownloadTasks keep their frozen route for completion or explicit retry. Do not migrate, resubmit, delete, or relocate provider work automatically.
- Follow snapshots remain readable. An incompatible old snapshot is blocked before Site access until the user saves a valid revision.

## 7. Required Error Matrix

| Condition | Required behavior |
| --- | --- |
| PT Site + 115 native offline | Reject before torrent fetch/submission. |
| BT Site + valid resolved torrent + 115 | Parse bounded bencode, hash the exact raw `info` bytes, and submit the resulting magnet. |
| Unknown/PT Site + resolved torrent + 115 | Reject; no conversion and no DownloadTask. |
| 115 Downloader + different Connection target | Reject with stable compatibility error. |
| 115 Downloader + local target | Reject with stable target error. |
| No complete Follow tuple | Return empty defaults plus actionable reason; disable save. |
| OMC task directory appears in listened root | Skip; owning Download Worker remains authoritative. |
| Manual item is still changing | Keep pending; do not claim. |
| Duplicate event/sweep for one manual item | Existing unique claim wins; create no second task. |
| Event storm on one 115 Connection | Coalesce to one pending delayed stability recheck and cancel/wait it during shutdown. |
| Listened roots/final root overlap | Reject new save/enable; flag legacy configuration. |
| Manual content cannot select one compatible target | Needs-action within the same 115; never cross Storage. |
| Share URL invalid/oversized/incomplete | Reject before queueing and disclose no secret. |
| Provider completed but recognition unsafe | Retain source; no final-library write. |
| Provider completed but recognition/Transfer/Import failed | Offer pipeline cancellation; remove the provider task, retain provider files, and move the retained task to cancelled history. |
| Provider cancellation/deletion fails temporarily | Preserve local facts and expose a safe retryable error; never claim local success. |
| Provider task is already absent | Treat provider cleanup as idempotent success and complete the requested local transition. |

## 8. Required Tests

- Unit matrix for SiteType × resolved SourceKind × DownloaderType × target StorageType × Connection identity.
- Follow defaults, forged Create/Update, Worker drift, and pre-download handoff tests.
- PT/torrent rejection without conversion, BT torrent-to-magnet bridging, malformed torrent rejection, and BT magnet allowance for same-Connection 115.
- All-stage pipeline cancellation tests prove provider `Cancel(..., false)`, no file deletion, Follow claim release, retained cancelled history, task-not-found idempotency, provider-failure retention, and separate default/destructive record deletion.
- Submit/cancel race tests prove a provider task returned after local cancellation is immediately removed with `deleteData=false`; cleanup failure persists a diagnostic fact and provider identity for retry.
- New-download WebUI tests for the 115 offline/share switch, no independent sidebar, and no second directory selector.
- OMC offline/share integration proving stable `omc-*` directories are skipped by life-event adoption.
- Manual adoption tests for quiet-window stability, database uniqueness, duplicate/concurrent/missed events, restart, pagination, and provider ancestry.
- Directory overlap and reserved-prefix tests.
- End-to-end share/manual content -> manifest -> recognition -> Transfer -> reconciliation tests, including needs-action retention.
- Legacy intake task/configuration compatibility tests.
- Legacy MediaLibrary edit tests prove omitted or forged intake fields neither disable an existing snapshot nor create a new current intake route.
- Life-event scheduling tests prove repeated events coalesce per Connection and pending rechecks exit on service shutdown.
- Status `0/1/2/-1`, unknown state, retry reconciliation, and Follow claim synchronization tests.

## 9. Wrong vs Correct

### Wrong

```go
snapshot.SiteIDs = allEnabledSites
snapshot.DownloaderID = enabledDownloaders[0].ID
snapshot.MediaLibraryID = enabledLibraries[0].ID
```

```go
// The watcher races the provider task and adopts every direct child.
for _, item := range list(downloadDir) {
    adopt(item)
}
```

```ts
// A second intake directory and dedicated sidebar split one 115 route.
showIntakeDirectoryPicker = true
navigate('/automation/115-transfer')
```

### Correct

```go
snapshot, reason := firstCompatibleFollowTuple(sites, downloaders, libraries)
```

```go
if strings.HasPrefix(item.Name, "omc-") || !stable(item) {
    continue
}
claimOnce(connectionID, downloader.ID, item.ID)
```

```ts
// One Downloader, one directory, two explicit source modes.
sourceModes = selected.type === 'pan115_offline'
  ? ['offline', 'share']
  : genericSourceModes
```

The backend remains authoritative even when WebUI has already filtered every option.
