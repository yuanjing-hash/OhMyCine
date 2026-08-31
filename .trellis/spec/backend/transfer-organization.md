# Transfer Organization

Automatic organization uses the immutable template captured by the DownloadTask. New snapshots always begin with the fixed media-type root (`电影` or `电视剧`) followed by the matching Profile category. This same snapshot is consumed by local, same-Connection 115 and cross-source executors; provider adapters never add their own prefix. Corrective reorganization uses the current normalized MediaLibrary policy, while legacy queued snapshots remain unchanged.

## 1. Scope / Trigger

Apply this contract when changing `TransferTask`, local or cloud transfer workers, cloud mutation adapters, `/api/v1/transfers`, `/automation/organization`, or download-to-organization deep links.

The media organization page represents only automatic post-download TransferTasks. Manual file selection and manual organization belong to the future file-management domain and must not be added here. MediaLibrary watchers and reconciliation remain independent supervisors and do not become TransferTasks or occupy queue slots.

## 2. Signatures

- Database: v16 column `transfer_tasks.plan_summary_json TEXT NOT NULL DEFAULT ''`; v23 adds private `transfer_tasks.cloud_state_json` plus private DownloadTask target Storage type/Connection/provider-root snapshots; v30 adds private `source_manifest_json` and the persisted `cleanup_status/cleanup_removed/cleanup_error_code` staging-cleanup state.
- Optional cloud contracts: `MutationDriver` extends `Driver` with singleton `CreateDirectory`, `Move`, `Copy`, `Rename`, and `Recycle`; `BatchMutationDriver` is a capability-detected acceleration that adds `MoveMany`, `CopyMany`, and `RecycleMany`. Batch requests are provider-neutral bounded to at most 100 opaque IDs and never replace per-item reconciliation.
- Routes: `GET /api/v1/transfers`, `GET /api/v1/transfers/:id`, and `DELETE /api/v1/transfers/:id`.
- List query: `scope=active|history|all`, `status`, `library_id`, `category`, `transfer_mode`, `keyword`, `page`, and `page_size`.
- Read permissions: `transfers.read_own` and `transfers.read_all`.
- Mutations reuse exact Job contracts: `POST /api/v1/jobs/:id/retry` with `jobs.control_own/all`, and `POST /api/v1/jobs/:id/actions/:version/respond` with `jobs.respond`.
- `transfer_tasks.plan_summary_json` is a private persisted cache of an explicitly safe public projection, not a replacement for the private manifest.

## 3. Contracts

- One list row equals one TransferTask joined with only allowlisted DownloadTask and transfer Job facts. Do not build a second queue or copy Job state machines.
- Own scope always applies `transfer.owner_id = actor.User.ID`; all scope may see every row. Navigation, route middleware and service policy use canonical generated permission constants.
- Filters and pagination are server-side and stably ordered by `transfer.created_at DESC, transfer.id DESC`. Statistics and library/category filter options use the actor's whole visible range rather than the filtered page. Active retry state comes from the Job, so a stale failed TransferTask phase must not make one record count as both processing and failed; cancelled Jobs remain explicitly visible as cancelled.
- The organization page defaults to `active` (queued/running/retry-wait/waiting-action/paused) and exposes `history` (failed/cancelled/completed) as a top management tab. Existing status filters apply inside the selected scope, and a task deep link carries/restores its intended scope.
- Detail may expose the immutable library/Profile/import-policy snapshot, safe metadata/classification summary, transfer Job/action, attempts and timeline. It never serializes whole GORM models or private JSON.
- Target naming results contain destination-relative paths only. Normalize to `/`, reject absolute paths, drive/UNC syntax, colon, traversal, control characters and oversized segments at both write and read time.
- Retain at most 100 plan items and 48 KiB of encoded summary. Return `total_files` and `truncated`; historical tasks without summaries remain readable.
- API, WebSocket, logs and audit must not contain staging/Storage absolute roots, source paths, provider task IDs, raw manifests, credentials, signed URLs or raw OS errors.
- Failed transfer retry targets only its transfer Job. A completed download Job is never resubmitted. Conflict responses use the current ActionRequest version and allowlisted option; stale versions refresh the detail.
- Record deletion is available only when the transfer Job is `failed`, `cancelled`, or `completed`. It transactionally removes the TransferTask and its transfer Job history, but never deletes the DownloadTask, provider task, source/staging files, library files, or seeding facts. The UI states this boundary in a second confirmation.
- Transfer deletion reuses `jobs.control_own/all`, rechecks owner and terminal status inside the service transaction, and writes a `transfer.delete` audit containing only allowlisted status/phase/library identifiers.
- MediaLibrary watchers and reconciliation remain independent supervisors and do not become TransferTasks or occupy queue slots.
- Cloud Transfer Jobs use a Connection-scoped resource key. Only the worker reads provider item IDs from the private manifest/cloud state; Job payload/checkpoint, plan summary, API, WebSocket, logs and audit never receive those IDs or full provider paths.
- TransferService accepts only a package-selected manifest plus a trustworthy task-level `completed_verified` TMDB/Profile snapshot. Local and cloud planners repeat this validation and never fall back to parsing every video as an independent title or to creating an `未分类` destination. Movies contain exactly one selected primary video; TV items require trustworthy episode numbers; sidecars may only follow their selected media group.
- The planner reruns the same provider-neutral package selector and requires the supplied file set to be identical. This second gate rechecks minimum video size, TV anchor-relative thresholds and sidecar association, preventing a stale or forged episode-looking advertisement from bypassing completion verification.
- A retry of a failed legacy transfer may invoke the Download completion verifier when its stored snapshot/manifest fails the current gate. Re-verification and filtered-manifest persistence finish before local path resolution or cloud directory creation; failure remains `transfer_media_unrecognized` and retains every source/provider item.
- Before that legacy re-verification, atomically clear `plan_summary_json`, `processed_files`, `total_files`, and provider checkpoint projection. If recognition still fails, detail must show no current plan rather than stale advertisement items; this reset never deletes or moves source/provider data.
- 115 cloud import is same-Connection only. It validates source and target Storage ancestry by stable item ID, ensures target directories uniquely, and executes provider calls outside database transactions. The short completion transaction only updates TransferTask, audit and `dirty_generation`.
- A 115 batch mutation persists a versioned private intent before the provider call, reconciles every item from the exact proven target/source parent afterwards, then checkpoints only verified per-item results. Retry first binds the intent back to the current immutable manifest and target-directory projection; provider success followed by checkpoint/process loss must converge without resubmitting completed IDs. Other drivers and capability-missing 115 clients retain the singleton path.
- Move is restart-safe across `move -> rename -> checkpoint`: a retry accepts the stable item either under its original source parent or under the exact already-validated target parent with its original/target name. Provider root ID `0` is a valid configured root.
- Copy uses a TransferTask-specific temporary directory below the target library root. It identifies the copied result by name plus size/SHA1, persists the new ID privately, then renames/moves it. Files whose original names collide case-insensitively cannot share one batch into that directory and use the singleton path; zero candidates may retry, while multiple candidates fail closed and retain every candidate for manual inspection.
- `overwrite` first proves the conflicting item remains below the target library root and sends it to provider recycle bin. `skip` changes neither source nor target. `rename` chooses one group suffix for video and sidecars. `ask` persists only safe conflict counts in the ActionRequest and releases the worker slot.
- Successful 115 move/copy never creates a SeedingTask. It increments the selected library's `dirty_generation`; life events and periodic reconciliation remain independent consistency backstops.
- Download completion persists both the provider's complete immutable package manifest and the package-selected import manifest. Automatic staging cleanup starts from their strict identity-keyed difference, but unselected video files and unmatched subtitle files (`srt/ssa/ass/vtt/sub/idx/sup`) are protected leftovers rather than deletion candidates. The selected manifest must be a complete strict subset of the complete source manifest before any deletion is allowed.
- Cleanup manifest identity is the canonical provider-neutral tuple of relative path, provider item/parent identity, size and normalized SHA1. Reject absolute, drive/UNC, traversal, control-character, whitespace-variant, or path-cleaning-dependent input instead of normalizing it into a deletable identity.
- Staging cleanup runs only after recognition, transfer and target reconciliation are complete. Local cleanup revalidates the configured absolute staging boundary, regular-file/reparse safety and snapshotted size at every item. 115 cleanup revalidates stable item ID, source-root ancestry, parent, size and optional SHA1 before recycling that exact item. Missing items are idempotent; changed or ambiguous items stop the run and remain untouched.
- For 115, the destructive boundary is the immutable `DownloadTask.provider_output_id` package directory, not merely the wider Storage root. The package directory must itself be proven below the Storage root and each candidate below the package directory; legacy rows without that snapshot fail closed.
- Cleanup removes only manifest-owned unselected items and then only empty directories below the staging root. It never scans or guesses extra directory content, deletes a normal MediaLibrary/source tree, follows a symlink/reparse point, or deletes an item after a failed/partial recognition or transfer.
- qBittorrent `copy|symlink` cleanup is `deferred` until the durable SeedingTask has acknowledged provider cleanup. Copy may use provider `deleteData=true` only when the immutable manifests contain no protected leftovers; otherwise it downgrades to `deleteData=false` and performs only the safe difference cleanup. This decision is re-evaluated at every destructive Stop/Interrupt/worker boundary so legacy persisted tasks cannot bypass the rule. Symlink always uses `deleteData=false`. Manual stop and queued cancellation invoke the same guarded staging-cleanup callback before completion.
- Sidecars follow only a selected video in the same directory. Transfer naming preserves the suffix between the source video stem and subtitle extension, including language and `forced/default/HI` markers, and resolves target collisions case-insensitively without overwriting another subtitle.
- Movie naming may expose the optional `{version}` placeholder. Its value is a deterministic, provider-neutral label extracted only from an allowlisted set of edition, resolution, source, REMUX, HDR and Dolby Vision markers; never copy an entire release name, site adornment, codec/audio string or release group into the destination. Existing movie templates without `{version}` append a non-empty label using Emby's ` - Version` convention so separately imported versions remain distinct, while subtitles continue to share the selected video's versioned stem.
- The dashboard organization card uses transfer read permissions. Until a separate dashboard aggregate exists, it must not claim that the transfer API is unimplemented or display fabricated counts.

## 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Actor has neither transfer read permission | Return `403`; hide the route and navigation entry. |
| Actor has only `transfers.read_own` | Scope list, statistics, filter options, detail, and WebSocket events by owner; another owner's ID appears as not found. |
| Unknown status/transfer mode, malformed library ID, or invalid pagination | Return a safe `400` validation error without echoing private fields. |
| Unknown scope | Return `400`; never silently broaden to all records. |
| Historical `plan_summary_json` is empty | Return `plan_summary: null`; keep the rest of the task readable. |
| Summary contains an absolute, drive, UNC, traversal, colon, control-character, oversized path, or trailing JSON | Reject it on write; suppress the corrupt summary on read. |
| Summary exceeds 100 items or 48 KiB | Truncate deterministically and set `truncated`; never fall back to returning the manifest. |
| Transfer Job is failed | Retry only that Job after normal QueueService authorization. |
| ActionRequest version is stale | Reject through QueueService and refresh the current detail in the UI. |
| Actor lacks the applicable `jobs.control_own/all` scope | Return `403` and retain the TransferTask and Job history. |
| Transfer Job is not failed, cancelled, or completed | Reject deletion with `queue_state_conflict`; retain every record and file. |
| Terminal transfer record is deleted | Cascade its Job attempts/timeline/actions, retain DownloadTask/provider/source/library/seeding state, and emit a refresh event after commit. |
| Source/target Connection or provider-root ancestry cannot be proven | Fail closed with a stable safe code; perform no mutation. |
| A legacy/in-flight `pan115_offline` snapshot targets local Storage | Fail before local planning with `transfer_route_unsupported`; retain the completed provider data and instruct the user to use a same-account 115 library. |
| Metadata snapshot is unrecognized/incomplete or a movie manifest still contains multiple videos | Reject before planning/ensure-directory; perform no local or provider mutation. |
| 115 move succeeded but rename/checkpoint did not | Retry by stable item ID from the exact target parent; do not require it to remain under the source root. |
| Copy result has zero / one / multiple matching candidates | Retry / persist and continue / non-retryable ambiguity while retaining all data. |
| Overwrite conflict is outside the target MediaLibrary root | Reject the task; never recycle the item. |
| Complete source manifest is absent/partial, selected manifest is not its subset, or an identity is duplicate/invalid | Mark cleanup failed, retain every remaining source item, and retry only after safe state can be proven. |
| Cleanup manifest contains an absolute/drive/UNC/traversal/non-canonical path or its item/parent/size/SHA1 snapshot differs | Reject the cleanup plan; retain every source item. |
| Local file size/type/reparse boundary or 115 item parent/size/SHA1 changed | Stop cleanup at that exact item, retain it and all later items, persist a safe retry state. |
| 115 package output ID is absent, not a directory, or outside the configured Storage root | Fail closed before recycling any item. |
| Source-minus-selected contains another video or unmatched subtitle | Protect it from automatic local/115 cleanup and force qBittorrent whole-package deletion off. |
| qBittorrent copy/symlink is still seeding or retained | Persist `deferred`; do not remove selected or unselected source files until provider cleanup is acknowledged. |

## 5. Good / Base / Bad Cases

- Good: an Operator filters failed copy imports, opens a detail deep link, sees destination-relative planned names plus Job attempts/timeline, and retries only the transfer stage.
- Good: 115 move places an item, rename temporarily fails, and the next attempt resumes from the exact target parent without duplicating or losing the item.
- Base: an older completed task has no plan summary; the page still shows classification, target library, progress, status, and safe timestamps.
- Base: two possible copies exist in the private temporary directory; the task fails without deleting either candidate.
- Bad: the API serializes a `TransferTask` or `DownloadTask` model directly and leaks `ManifestJSON`, `StagingAbsolutePath`, `TargetStorageRoot`, or `ProviderTaskID`.
- Bad: a feature page creates another retry state machine or reruns the completed download Job when transfer failed.

## 6. Tests Required

- Migration tests cover fresh/idempotent/previous-version v16 upgrades and the Operator transfer-read grant.
- Service tests cover own/all isolation, combined keyword ownership scope, filters, pagination/statistics/options, safe list/detail DTOs, plan truncation, path rejection, historical empty summaries, Job detail reuse, deletion status/owner gates, cascade cleanup, audit safety, real-file preservation, 115 move/copy, all four conflict policies, root `0`, move-before-rename restart, copy ambiguity, boundary rejection and dirty-generation handoff.
- Staging-cleanup tests cover a large selected media item plus small advertisements, protected alternate videos and unmatched subtitle formats, non-canonical/incomplete/non-subset manifests, changed local files, retry count accumulation, exact 115 package-root/item recycle, qBittorrent seeding deferral, legacy `deleteData=true` downgrade and mode-specific post-seeding callbacks. Migration tests assert historical v29 transfer rows become `skipped` rather than unexpectedly deleting old data.
- Sidecar planner/local/cloud tests cover language and `forced/default/HI` suffix preservation, `sub+idx`, `vtt`, `sup`, case-insensitive duplicate target resolution and group conflict renaming.
- Release-version tests cover noisy local/qBittorrent/115 filenames, deterministic marker ordering, false-positive boundaries, legacy-template automatic suffixes, explicit `{version}` templates, empty versions and title-length truncation that preserves the version suffix.
- Planner tests also assert that unrecognized snapshots and unfiltered multi-video movie manifests create no TransferTask or destination directories, while accepted sidecars retain the primary media group.
- Retry tests pre-populate a stale public plan and cloud checkpoint, force re-verification failure, and assert both projections plus progress are cleared while source files remain untouched.
- HTTP tests cover authentication, CSRF, RBAC, no-store responses, invalid filters and absence of private fields.
- Web tests cover navigation/route permissions, dashboard permission consistency, exact stage labels including retry/cancelled states, deep links, filters/pagination, retry, terminal-only delete actions, stale action refresh, responsive table/cards and light/dark token usage.
- Browser smoke covers empty and populated states, drawer initial focus, desktop/narrow layouts, both themes, and console warnings/errors with an isolated database and port.
- Run Server Go test/vet/build, Web UI permission check/test/typecheck/lint/build, both module verifications, embedded build, `git diff --check`, and the Windows isolated Server gate.

## 7. Wrong vs Correct

Wrong:

```go
// Leaks private worker state and bypasses the owner-scoped projection.
c.JSON(http.StatusOK, transferTask)
tx.Transaction(func(tx *gorm.DB) error { return cloud.Move(ctx, itemID, parentID) })
```

Correct:

```go
// The service authorizes first and returns an explicit allowlisted projection.
detail, err := transfers.Get(actor, transferID)
success(c, http.StatusOK, detail)
item, err := providerItemWithinRoot(ctx, driver, itemID, configuredRootID)
// Provider calls occur before/after, never inside, the short state transaction.
```

Wrong:

```ts
await controlJob(downloadJobId, 'retry') // reruns a completed prerequisite
```

Correct:

```ts
await controlJob(transfer.job_id, 'retry') // retries only the failed import stage
```

Wrong:

```go
extras := sourceMinusSelected(source, selected)
client.Cancel(ctx, torrentID, true) // may destroy an unselected real movie
```

Correct:

```go
plan := buildTransferCleanupPlan(transferTask)
deleteData := transferMode == "copy" && plan.ProtectedCount == 0
// Re-evaluate the immutable plan again immediately before provider deletion.
client.Cancel(ctx, torrentID, deleteData)
```

## Scenario: Managed-Only Corrective Reorganization

### 1. Scope / Trigger

- Trigger: correcting a completed import's TMDB identity, previewing old-to-new paths, moving existing local/115 outputs, or rebuilding metadata/STRM after correction.

### 2. Signatures

```text
DB migration v50:
  media_managed_items
  media_reorganization_previews
  media_reorganization_tasks
  queue policy media_reorganization

POST /api/v1/media/reorganizations/preview
  { transfer_task_id, tmdb_id, media_type, conflict_policy }
POST /api/v1/media/reorganizations/confirm
  { confirmation_token }
GET  /api/v1/media/reorganizations/:id
```

- A preview token is a 256-bit opaque value; SQLite stores SHA-256 only. It binds actor, Transfer, library, source/target identity revisions, managed-manifest digest, current Profile/rule fingerprint, conflict policy and five-minute expiry.

### 3. Contracts

- Reorganization is available only for a completed Transfer with active `media_managed_items` recorded from actual successful transfer outputs. Never discover ownership by scanning a directory or matching filenames; pre-v50 imports without an ownership manifest fail closed.
- Preview calls TMDB `GetByID`, loads the MediaLibrary's current Profile revision/rules, recomputes category from verified metadata, and then calculates destination-relative names using current library templates. It does not reuse the old identity category.
- Confirmation consumes one actor-bound preview exactly once. Worker rechecks identity revision, rule fingerprint, manifest digest and every file/item boundary before each mutation.
- Preview does not reserve a destination. Immediately before every local or 115 move/rename, the worker checks the exact planned target again; a newly occupied target that is not the same managed item fails with a stable conflict and requires a new preview, including for a previously generated rename target.
- Local work uses canonical library root, per-component symlink/junction/Reparse Point rejection, `Lstat` type/size checks and root-confined rename. 115 work uses same-Connection stable item ID, root ancestry, exact parent/size and driver-native Move/Rename outside database transactions.
- Failure preserves the old authoritative identity until every planned file is accounted for. Progress checkpoints make retry idempotent. Finalization uses compare-and-swap on the source identity revision, updates managed items, marks task complete, then invokes ordinary media-library reconciliation to rebuild NFO/JPG/STRM and downstream Player/Emby/Jellyfin changes.
- Cleanup may remove only system-created empty directories after successful migration. It never deletes unmanaged siblings, scans for garbage, changes provider source data outside the manifest, or silently overwrites a conflict.
- Explicit deletion of terminal Transfer/Download history removes ownership rows, expired/current previews and terminal reorganization records/jobs in one transaction, but never media/provider files. Any non-terminal reorganization job blocks history deletion with a queue-state conflict.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Missing/inactive/unmanaged ownership item or pre-v50 import | `media_reorganization_unavailable`; perform no mutation |
| Actor does not own/control Transfer | `permission_denied`; disclose no paths/provider IDs |
| Profile revision/rules, identity revision or manifest digest changed after preview | `media_reorganization_boundary_changed`; require a new preview |
| Target conflict with `ask` | Return safe conflict count and require a new explicit policy/preview |
| Local path escapes root or crosses reparse boundary | Fail before move and keep old identity |
| 115 item/root/parent/size changed | Fail closed; do not locate a replacement by name |
| Retry observes source missing but exact target exists with expected size/ID | Treat prior step as completed and continue idempotently |
| All files moved but artifact reconciliation is temporarily unavailable | Keep managed identity/path commit and retry normal reconciliation without moving files again |

### 5. Good / Base / Bad Cases

- Good: a wrongly matched Japanese movie under `华语电影` previews under current Profile `外语电影`, moves only its managed video/subtitles, locks revision 2, then rebuilds sidecars/STRM.
- Base: a v49 historical import has no managed manifest; UI does not offer a false-safe automatic move and Server rejects direct requests.
- Bad: update TMDB ID first then attempt file moves, recurse through the old directory, or infer 115 ownership from matching names.

### 6. Tests Required

- Migration tests cover v50 fresh/upgrade/repeat tables, indexes and queue policy.
- Planner tests cover current-Profile reclassification, movie/TV templates, subtitles/sidecars, conflict policies, bounded items and destination-path validation.
- Local tests cover idempotent retry, size/type/reparse/root changes, unmanaged siblings and identity CAS rollback.
- 115 tests cover stable item/parent/root ancestry, move-then-rename restart, changed item rejection and absence of provider IDs in public DTO/log/audit.
- HTTP/Web tests cover auth/CSRF/ownership, one-time expiry/replay, safe old/new relative paths, entry visibility only for completed managed Transfers, confirmation and status polling.

### 7. Wrong vs Correct

Wrong:

```go
files, _ := os.ReadDir(oldDirectory) // guesses ownership
tx.Model(&download).Update("tmdb_id", selectedID) // commits before file moves
```

Correct:

```go
items := loadActiveManagedItems(transfer.ID)
plan := preview(items, sourceIdentityRevision, currentProfileRevision)
// Worker revalidates plan bindings; identity CAS occurs only after all managed items reconcile.
```

## Scenario: Per-File Episode Facts, 115 Operation Pacing, and Convergent Deletion

### 1. Scope / Trigger

- Trigger: planning or repairing a multi-season TV import, changing 115 directory/file mutation pacing, or previewing/confirming deletion of terminal Transfer history and its selected source/library scope.

### 2. Signatures

```text
episode resolver:
  transferEpisodeFactsForManifest(DownloadTask, Manifest)
    -> map[relative_path]{season, episode, source} | error

POST /api/v1/transfers/{id}/deletion-preview
  request:  { scope: record_only|record_and_source|record_and_library|record_source_and_library }
  response: { source_items, source_missing, source_detached, library_items,
              library_missing, blockers, confirmation_token, expires_at, ... }

POST /api/v1/transfers/{id}/deletion-confirm
  request:  { token }
  response: { deleted, scope, source_removed, library_removed }
```

- `record_only` performs zero provider I/O. Provider-backed preview work has one Server-side 45-second deadline; the browser uses an AbortSignal and a 50-second ceiling.
- 115 pacing has independent lanes for list/path lookup, mkdir, move, copy, rename, recycle/purge and offline/share operations, plus one shared risk-backoff controller.
- Optional provider acceleration is `cloud.DirectoryPathResolver.ResolveDirectory(context.Context, absoluteProviderPath) (cloud.Item, error)`. Read callers attach exactly one of `ReadClassInteractive`, `ReadClassPipeline`, or the conservative default `ReadClassBackground` to the request context; all classes still share the Connection call-slot bound and 405/429 recovery state.

### 3. Contracts

- Episode precedence is explicit manual per-file correction, then validated `identity_snapshot.episodes[relative_path]`, then deterministic package parsing, then task-level automatic `scrape_season/scrape_episode` only when there is one video or every structured file agrees. A task-level automatic season must never overwrite conflicting per-file facts in a multi-season package.
- Validation, target planning, `plan_summary_json`, `MediaManagedItem`, catalog projection and corrective reorganization consume the same resolved per-file fact map. Existing-task repair correlates the original manifest by stable provider item ID plus size and available SHA1; missing, conflicting or changed identity fails closed.
- Build and persist the target directory DAG once per plan. Within one task/attempt, deduplicate directory lookup/creation and reuse validated directory IDs. Healthy mkdir has no unconditional two-second delay; ordinary queueing is not risk backoff.
- A pure route preview uses only persisted Storage/Downloader/MediaLibrary identity and capability snapshots and performs zero provider calls. Enqueue and worker execution remain authority boundaries: enqueue revalidates the frozen roots once, and an active 115 transfer may resolve a complete target path once, compare the returned stable ID, list each unique conflict parent once, mutate in bounded batches, invalidate affected cache entries, and perform a fresh reconciliation read.
- Interactive directory browsing and active transfer reads may use provider-specific low-latency lanes, while background scans are bounded to at most one shared read slot. Separate limiters do not bypass the shared call slots, risk generation, circuit state, cancellation, or endpoint-specific mutation lanes.
- Only provider 405/429 or an explicit operation-frequency/risk response activates shared jittered exponential backoff. A late success from an older request must not clear a newer shared backoff generation. Phase DTOs describe the real action (`checking_directories`, `checking_conflicts`, `moving`, `renaming`, `reconciling`, `risk_backoff`).
- 115 transfer observability uses a request/task-scoped concurrent-safe collector carried by `context.Context`, never connection-global cumulative counters. The completion log may expose only aggregate call counts and milliseconds for provider wait, SDK call, target listing, batch mutation and DB checkpoint; labels cannot contain paths, names, provider IDs, credentials or response bodies.
- Deletion is convergent: an already-missing provider task, source item, library item, package root or empty directory counts as already removed. Source items proven outside the immutable `DownloadTask.provider_output_id` package root are `detached` and retained; only still-present items proven beneath that exact root may be recycled.
- Source reconciliation batches by provider parent directory and matches stable item IDs from bounded listings. Do not run a rate-limited `Stat` chain once per manifest item. A missing whole source root completes source cleanup; changed identity, ambiguous ancestry or SHA1 mismatch still fails closed.
- Healthy 115 move/copy/overwrite-recycle/staging-cleanup groups by exact proven parent and chunks at 100 IDs. Provider acknowledgement is not completion: partial or ambiguous results persist only the unverified intent, and a changed identity blocks its safe chunk without widening the mutation set.
- Only a worker that can still mutate state blocks deletion. A terminal Job with no live lease does not block merely because historical status is stale; a terminal transfer/reorganization/seeding worker with a still-valid mutation lease continues to block until its lease is released or expires.
- Preview/confirmation failure persists a stable error code. Closing the dialog aborts only the request; it does not corrupt the task or consume a confirmation token.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Multi-season manifest plus task `scrape_season=2` | Keep each structured S01-S04 fact; do not flatten the package to S02 |
| Snapshot path/provider item/size/SHA1 is missing or inconsistent | Reject repair before moving or renaming anything |
| Same 115 target directory is used by many files | Resolve/create once per attempt and reuse the validated ID |
| Route preview covers one or many 115 libraries | Use persisted snapshots only; make zero `Stat`, `List`, or path-resolution calls |
| Complete provider path resolves to a different stable ID | Fail the boundary proof; do not fall back to matching by name |
| Healthy mkdir/move/rename response | Advance the real phase; do not sleep in or label it risk backoff |
| Provider returns 405/429/explicit frequency control | Enter shared bounded backoff with jitter and a visible retry time |
| `record_only` preview | Use local DB/lease facts only and make zero provider calls |
| Provider task/root/file was manually deleted | Treat it as idempotently absent and continue selected local-record cleanup |
| Historical source item moved into the media library/outside package root | Count it as `source_detached`; retain it and omit it from the delete plan |
| Provider preview exceeds its deadline or browser aborts | Return/show a retryable bounded error; keep task, files and prior state intact |
| Terminal task still owns a valid mutation lease | Reject with queue-state conflict until the live worker can no longer write |

### 5. Good / Base / Bad Cases

- Good: a 28-file package resolves to S01 6, S02 6, S03 8 and S04 8, reuses four season-directory identities and repairs only manifest-owned 115 items.
- Good: a one-file same-115 move into an existing target leaf performs one root/path proof, one unique-parent conflict listing, one batch move and one fresh reconciliation instead of an ancestor-depth walk.
- Good: a cancelled 23/28 task with 38 source entries across five parents lists those parents once, reports missing/detached counts, and returns a safe preview within the request deadline.
- Base: every source item and provider task was manually removed; confirmation deletes the terminal local history without touching the media library.
- Bad: write `ScrapeSeason` over every file, sleep two seconds before each directory action, or recycle a provider item solely because its historical ID still appears in a manifest.

### 6. Tests Required

- Use the untouched four-season 28-file fixture and assert unique targets plus exact 6/6/8/8 season counts; cover explicit override, single-file fallback and conflicting snapshot failure.
- Reorganization tests assert original provider item ID + size + SHA1 correlation, missing SHA1 fail-closed behavior, and no mutation before the complete repair plan is verified.
- 115 scheduler tests assert independent operation lanes, per-attempt directory de-duplication, no fixed healthy-mkdir delay, true 405/429 backoff, jitter/circuit recovery and late-success generation safety.
- Route and path-resolution tests assert preview zero-I/O for multiple libraries, depth-independent complete-path resolution, interactive/pipeline progress while a background scan runs, context cancellation and safe ancestry fallback for providers or legacy Storage rows without a usable display path.
- 115 batch tests assert the 100-ID ceiling, unique-parent call scaling, copy temporary-name collision fallback, provider-success/checkpoint-loss replay, partial result convergence, overwrite/cleanup reconciliation and pre-call context cancellation.
- Deletion tests assert zero provider calls for `record_only`, parent-batched source listing, provider-task/root/item missing idempotency, detached retention, immutable package-root confinement, live-lease blocking, 45-second context cancellation and persisted failure codes.
- Web tests assert real phase wording, risk wording only for `risk_backoff`, AbortController cleanup, 50-second timeout recovery and preserved task state after a closed/failed preview.

### 7. Wrong vs Correct

#### Wrong

```go
for path := range facts {
    facts[path] = transferEpisodeFact{Season: *download.ScrapeSeason}
}
for _, item := range manifest.Items {
    current, _ := driver.Stat(ctx, item.ID)
    driver.Recycle(ctx, current.ID)
}
```

#### Correct

```go
facts, err := transferEpisodeFactsForManifest(download, originalManifest)
// Per-file structured evidence wins; task-level automatic values are only compatible fallback.

parents := groupSourceManifestByParent(originalManifest)
current := listParentsWithinDeadline(ctx, parents)
plan := convergeMissingDetachedAndOwned(current, download.ProviderOutputID)
```
