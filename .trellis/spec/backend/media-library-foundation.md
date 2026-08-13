# Media Library Foundation

## 1. Scope / Trigger

Apply this contract when changing `MediaLibrary`, local or provider scanning, scan runs, file-tree entries, watchers, Profile/Storage references, or `/api/v1/media-libraries` and its Server Web UI.

Media libraries are read-only indexes over a registered Storage boundary. They do not rename, move, upload, delete, scrape over the network, generate STRM files, or enter the download/transfer job queue.

## 2. Signatures

REST routes:

```text
GET    /api/v1/media-libraries
GET    /api/v1/media-libraries/:id
POST   /api/v1/media-libraries
PUT    /api/v1/media-libraries/:id
DELETE /api/v1/media-libraries/:id
POST   /api/v1/media-libraries/:id/scan
POST   /api/v1/media-libraries/:id/retry
GET    /api/v1/media-libraries/:id/entries
GET    /api/v1/media-libraries/:id/runs
```

Database ownership:

```text
media_libraries
  -> media_library_scan_runs (ON DELETE CASCADE)
  -> media_library_entries   (ON DELETE CASCADE)

media_libraries.storage_id -> storages.id (ON DELETE RESTRICT)
media_libraries.profile_id -> media_classification_profiles.id (ON DELETE RESTRICT)
```

Runtime lifecycle:

```text
disabled -> initializing -> attaching_listener
         -> catch_up_reconciliation -> listening
         -> initialization_failed -> bounded retry -> initializing
```

## 3. Contracts

- Source identity is `storage_id + relative_root`; `relative_root` is provider-relative and starts with `/`.
- Web clients select a directory through `DirectoryPickerDialog` and submit `relative_root_token`. The handler consumes the opaque token and resolves it inside the selected Storage.
- Enabled creation automatically starts the first full baseline. Disabled creation persists configuration without scanning; enabling later starts initialization.
- A successful baseline is committed before the watcher attaches. An immediate `catch_up` reconciliation must succeed before status becomes `listening`.
- Each enabled library owns an independent supervisor and single-library scan mutex. Supervisors do not consume persistent job-queue slots.
- Local filesystem events are debounced and periodic incremental/full reconciliation remains the final consistency backstop.
- API entries contain provider-relative paths and opaque provider IDs only. Physical roots and raw OS errors are excluded from API responses, ordinary logs, exports, and AI fields.
- `partial=true` means enumeration was incomplete. Unseen old entries must be preserved because absence was not proven.
- GORM boolean fields that accept explicit `false` must not carry `default:true` model tags. Creation must write explicit zero values rather than allowing ORM defaults to silently enable a disabled library.
- Local Storage rejects STRM fields. Cloud STRM capability and managed output are implemented only in the later STRM/proxy slice.

## 4. Validation & Error Matrix

| Condition | Stable error / behavior |
|---|---|
| Empty or duplicate name | `media_library_name_required` / `media_library_name_conflict` |
| Traversal, symlink/Reparse Point escape, missing root, or token outside Storage | `media_library_path_invalid` |
| Disabled/missing/non-local source in the local slice | `media_library_storage_unavailable` |
| Missing Profile | `media_library_profile_unavailable` |
| Same-Storage roots overlap in either direction | `media_library_overlap` |
| Local source submits STRM configuration | `invalid_request` |
| Full/incremental interval or rate/concurrency exceeds service bounds | `invalid_request` |
| Initial or catch-up scan fails | `initialization_failed`, safe error code, `next_retry_at`; no listener |
| Storage/Profile is referenced | deletion returns conflict / `media_classification_profile_in_use` |
| Actor lacks a media-library permission | `permission_denied`, even if UI control is hidden |

## 5. Good / Base / Bad Cases

- Good: select a Storage-contained directory, submit its opaque selection token, create enabled, observe `initial` then `catch_up`, and enter `listening` with relative entries.
- Base: create disabled, save the explicit `false`, and start no scan until a later update enables it.
- Bad: accept `../`, persist an absolute source path in an entry, log `scanErr` containing a filesystem path, mark listening after failed catch-up, or delete unseen entries from a partial scan.

## 6. Tests Required

- Migration: v5 is idempotent; all tables, foreign-key restrictions/cascades, and operator/viewer permission seeds are asserted.
- Service: CRUD/RBAC, overlap, local STRM rejection, explicit disabled persistence, automatic baseline plus catch-up, retry state, Storage/Profile references, and Profile revision dirtying.
- Watcher: create/update/move/delete converge; repeated runs cover debounce timing; separate libraries do not share a global lock.
- Directory token: root and nested selections resolve to `/`-based relative roots; outside selections fail.
- API: automatic initialization reaches `listening`, entries contain no physical root, strict JSON rejects unknown fields, and deletion leaves source files untouched.
- Live acceptance is opt-in through `OMC_LIVE_LIBRARY_ROOT`; compare file list, size, mode, and nanosecond timestamps before/after and never hard-code the real root.

## 7. Wrong vs Correct

Wrong:

```go
Enabled bool `gorm:"default:true"`
log.Error().Err(scanErr).Msg("scan failed")
for _, unseen := range oldEntries { tx.Delete(&unseen) } // even on partial scan
```

Correct:

```go
Enabled bool `gorm:"not null"`
log.Error().Str("error_code", CodeMediaLibraryScanFailed).
    Uint("library_id", id).Uint("scan_run_id", run.ID).Msg("scan failed")
if !result.Partial {
    for _, unseen := range oldEntries { tx.Delete(&unseen) }
}
```
