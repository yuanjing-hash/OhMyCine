# 115 分享转存与中转目录自动摄取设计

## 1. Boundaries

```text
manual / site 115_share
  -> DownloadService snapshot
  -> pan115 downloader adapter
  -> stable per-task intake folder
  -> share snap + receive
  -> authoritative folder manifest
  -> existing completion verifier
  -> existing TransferService

115 App manual transfer
  -> life event wake / periodic sweep
  -> direct-child intake discovery
  -> internal adopted DownloadTask
  -> authoritative folder manifest
  -> same verifier + TransferService
```

- `pkg/cloud/pan115` owns 115 share HTTP shapes and rate/risk behavior.
- `pkg/downloader/pan115offline` maps share/adopted sources to the common Client/Manifest contracts.
- `MediaLibraryService` owns intake configuration and per-library sweep coordination，but never identifies titles or writes media.
- `DownloadService` owns immutable Profile/target snapshots and adopted task creation.
- `TransferService` remains the only final library mutation path.

## 2. Persistence

Migration v26 adds to `media_libraries`:

```text
ingest_enabled
ingest_downloader_id
ingest_owner_id
ingest_provider_root_id       # private
ingest_relative_root          # Storage-relative safe display
```

and to `download_tasks`:

```text
staging_provider_directory_id # private immutable intake snapshot
ingest_source_key             # private unique hash for adopted idempotency
source_origin                 # user|share|provider_ingest safe enum
```

`ingest_source_key` hashes Connection + library + provider item identity and is unique only when non-empty. No new queue type is required: adopted items enter the existing `download` worker and then the existing `transfer` worker.

## 3. Share driver contract

```go
type ShareReceiveDriver interface {
    Driver
    InspectShare(ctx, link) (ShareSnapshot, error)
    ReceiveShare(ctx, snapshot, directoryID) error
}
```

The concrete 115 driver parses the link, obtains bounded top-level IDs from share snap, and posts them to share receive. The downloader creates/reconciles the stable task directory before receive. A provider task ID prefix distinguishes share/adopted directory roots from offline info hashes without embedding credentials.

## 4. Intake reconciliation

- Life events publish a per-Connection generation as today.
- `MediaLibraryService.ProviderEventsChanged` wakes both read-only library reconciliation and intake sweep.
- Sweep lists the configured intake root directly, validates every child/root ancestry, skips `omc-` names, and creates adopted tasks with a unique source key.
- A quiet debounce coalesces event bursts. The existing incremental timer also calls sweep, covering lost events.
- Unique DB constraint is the concurrency/idempotency authority.

## 5. Compatibility and safety

- Existing URL/torrent and 115 offline task behavior remains unchanged.
- Existing `pan115_offline` downloader directory remains its default offline target; share/adopted tasks use the selected media library intake snapshot.
- HTTP accepts `115_share` but never accepts `provider_item`.
- Share/adopted tasks have no seeding path. Cancel recycles their task/intake root only after existing destructive confirmation.
- Provider calls happen outside database transactions. Short transactions persist intent and task identity.

## 6. Rollout and rollback

- v26 is additive. Disabling intake leaves existing tasks recoverable and stops new adopted tasks.
- Rolling back code leaves extra nullable/defaulted columns harmless; no source data is deleted by migration.
- Provider ambiguity fails closed and retains the task directory for retry/inspection.
