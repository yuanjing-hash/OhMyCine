# Downloader Management

## Scenario: Encrypted downloader configuration and restart-safe download jobs

### 1. Scope / Trigger

- Trigger: adding or changing downloader providers, downloader CRUD/test APIs, manual/discovery download submission, download telemetry, provider task controls, or the Server download administration UI.
- Downloader configuration is a Connection-like capability boundary. It is separate from Storage, MediaLibrary, and transfer/import placement; download submission selects a MediaLibrary directly or requests ordered automatic selection.

### 2. Signatures

- Environment: `OMC_CREDENTIAL_MASTER_KEY` is an optional Base64-encoded 32-byte key; `OMC_CREDENTIAL_KEY_FILE` is the explicit/generated key-file path. The generated default is `credentials.key` beside the SQLite database.
- Tables: `downloaders`, singleton `download_settings`, `download_tasks`, and private `transfer_tasks`; every DownloadTask has exactly one durable download `jobs` row and at most one idempotent transfer task/job.
- Migration v23 adds private `download_tasks.target_storage_type`, `target_connection_id`, `target_provider_root_id`, and `transfer_tasks.cloud_state_json`. The private 115 manifest file shape includes provider item/parent IDs and SHA1; no public DTO reuses that shape.
- Provider contract: `Client.Test`, `Submit`, `Get`, `Pause`, `Resume`, and `Cancel(ctx, taskID, deleteData)` under `server/pkg/downloader`; capabilities include `native_offline` and `output_constraint=none|local_staging|provider_storage`.
- Management routes: `GET/POST /api/v1/downloaders`, `PATCH/DELETE /api/v1/downloaders/:id`, and `POST /api/v1/downloaders/:id/test`.
- Download routes: `GET /api/v1/downloads?scope=active|history|all`, `POST /api/v1/downloads`, and `DELETE /api/v1/downloads/:id`; public source kinds are `url`, `torrent`, and `115_share`. `provider_item` is an internal-only adoption source and HTTP must reject it.
- Migration v26 adds `media_libraries.ingest_*`, `download_tasks.staging_provider_directory_id`, `download_tasks.ingest_source_key`, and `download_tasks.source_origin=user|share|provider_ingest`. The non-empty ingest key has a partial unique index and is never a public identifier.
- Migration v45 adds private `download_tasks.completed_manifest_json` and `download_tasks.staging_category`. The former is the authoritative completed provider-relative package snapshot; the latter is the physical downloader category/location snapshot and is distinct from the later logical `scrape_category`.
- Download settings route: `GET/PATCH /api/v1/settings/downloads`; PATCH accepts only a global `directory_token` and optimistic `revision`, never a client-authored absolute path or `storage_id` for new settings.
- The authenticated, no-store `settings.read` response exposes the configured staging absolute path so administrators can verify it. `GET /api/v1/settings/downloads/directory` additionally requires `storages.browse` and opens the picker at that Server-owned path; it never accepts a client-authored path.
- If the saved staging directory can no longer be opened, the Web UI keeps the safe error visible and falls back to the Server filesystem roots so an authorized administrator can repair the setting without recreating a Storage.
- Metadata settings routes: `GET/PATCH /api/v1/settings/metadata`, `POST /api/v1/settings/metadata/test`, `POST /api/v1/settings/metadata/test-token`, `POST /api/v1/settings/metadata/test-api`, and `POST /api/v1/settings/metadata/test-image`. Token PATCH accepts write-only `tmdb_token`, `clear_tmdb`, and optimistic `revision`; candidate token and route tests persist only after the matching real probe succeeds and the revision CAS still matches.
- Permissions: `downloaders.read/create/update/delete/test`, `downloads.read_own/read_all/create/manage_all`, `settings.read/update`, and the existing `jobs.*` control/read permissions.

### 3. Contracts

- qBittorrent uses one Server-wide local staging setting. Downloader configuration stores connection capability only and never chooses a Storage, path, or final MediaLibrary.
- The staging setting selects any Server-visible Windows drive/UNC path or Unix mount through the global Server directory picker. Saving and worker execution both canonicalize the full path and reject symlink/Reparse Point escapes; it is independent of registered Storage.
- Staging directory selection accepts only canonical signed picker tokens; a non-canonical Base64URL spelling is rejected even when it decodes to the same ciphertext.
- New DownloadTasks snapshot `staging_absolute_path` at enqueue time. Job payloads, DownloadTask DTOs, logs, audit metadata, and WebSocket events never contain local paths; the privileged download-settings response and directory-picker interaction intentionally display the configured/current path. Later default changes do not redirect queued tasks. Legacy `staging_storage_id + staging_relative_path` tasks remain losslessly executable only when the absolute snapshot is empty.
- Download staging no longer pins a Storage. Migration v12 lexically backfills absolute settings/task snapshots from legacy Storage-relative rows without probing disks or failing startup when an old drive is offline; legacy columns remain compatible fallback state.
- A cloud-native offline adapter must declare `native_offline=true` plus `output_constraint=provider_storage`; its provider-owned staging Storage is non-overridable even though the final MediaLibrary remains the task's routing target. qBittorrent declares `local_staging`.
- A `pan115_offline` downloader binds one enabled 115 Storage plus one Server-validated directory inside that Storage. Create/update accepts only the Storage-scoped opaque directory selection token; the private stable provider directory ID is persisted for submission, while API/UI expose only the Storage name and safe Storage-relative display path. Changing the Storage invalidates the previous selection, and migration v22 maps legacy downloaders to their former Storage root `/`.
- `115_share` requires a selected or automatically resolved 115 MediaLibrary whose intake is enabled and bound to the submitting downloader. The task snapshots the intake provider directory privately, creates/reuses `omc-<DownloadTask ID>` below it, performs bounded share inspection/receive through the cloud capability, and treats that stable directory as the authoritative provider task/manifest root.
- Share URL, receive code and internal provider item ID use the existing per-task AES-GCM source envelope. Job payload contains only `download_task_id`; DTOs, WebSocket events, audit metadata and runtime logs contain neither plaintext nor ciphertext. `source_origin` is a safe enum for lifecycle decisions, not source material.
- A successful, ambiguous, or restart-interrupted share receive reconciles the stable `omc-*` directory before another receive call. An internally adopted direct child uses a SHA-256 key over Connection, library and provider identity; the partial unique index is the concurrency authority. Provider calls remain outside database transactions.
- Share and adopted-provider tasks do not support pause, resume or seeding. Explicit destructive cancellation recycles only their system/adopted provider root after confirmation; provider task-not-found is idempotent success.
- Before testing or submitting a `pan115_offline` task, the adapter walks the selected directory's current provider parent chain and requires it to still terminate at the bound Storage root. A moved directory or a later Storage root/connection change fails with a safe non-path error and must not submit outside the current Storage boundary.
- 115 provider ID `0` is both the valid account root and the terminal parent outside every non-root Storage. Parent walking must therefore accept `current == "0"` when the bound Storage root is also `"0"`, and reject it only when the bound root is another ID. Directory-picker, offline-downloader, media-library and cloud-transfer boundary checks must keep this ordering consistent.
- Cookie-authenticated 115 offline submission uses the 115 Browser User-Agent. A successful HTTP status or SDK return is not the task identity authority: for magnets, normalize the BTIH (hex or Base32) and use the provider-reported page count under a fixed upper bound to adopt an already-created task whenever add returns an ambiguous body, no identity, or an existing-task error. A cached task page is only a fast probe; if it no longer contains the hash, fall back to the bounded full listing because newer tasks can shift page positions. If 115 explicitly reports an existing task but bounded reconciliation cannot find it, return terminal `downloader_task_exists` with safe deletion/retry guidance instead of entering automatic retries. Provider status `0/3` remains active, `1/-1` is failed, and `2` is completed. Invalid links and exhausted offline quota are terminal errors; only transport unavailability, rate limits, and risk-control cooling enter automatic retry.
- Storage capability JSON is a derived adapter snapshot. Storage list/get reconciles stale 115 snapshots from the current connection driver and materializes the refreshed JSON without provider network I/O, so capabilities added by a Server upgrade (including native offline download) become selectable without recreating the data source. Downloader execution still rechecks the live driver capability.
- New download UI/API selects `media_library_id`; `0` means the first actually usable enabled library in `sort_order,id`, while a positive ID is strict and never falls through. Missing `media_library_id` remains a legacy compatibility path with no automatic transfer.
- The Web UI derives compatible targets for feedback: local-staging downloaders show local libraries; `pan115_offline` shows only move/copy libraries on the source Storage's Connection. This is not an authorization boundary: submit rechecks type, enabled state, Connection equality, provider-root ancestry and live mutation capabilities.
- The selected MediaLibrary supplies the Profile used for qBittorrent preclassification/category assignment. The task also snapshots the final Storage root, transfer/conflict policy, and naming templates so later configuration edits cannot redirect active work.
- A 115 target additionally snapshots Storage type, Connection ID and stable provider root ID. These remain private. Completion manifest IDs feed only the private TransferTask state, and 115 move/copy bypasses seeding management because native offline download has no seeding contract.
- Download completion performs a fresh bounded manifest verification and creates a separate transfer Job. Download worker completion never performs file writes inline and transfer `ask` does not consume a downloader slot while waiting for user input.
- Download summaries expose the safe transfer Job ID/status alongside transfer phase so the download page can retry a failed transfer directly. Stage retry always targets the failed Job: it must not resubmit a completed download when only transfer failed, or restart transfer when only seeding monitoring failed.
- Download list scope is derived from the complete pipeline, not provider telemetry alone. `history` contains cancelled records or completed download Jobs whose transfer and optional seeding Jobs are absent/already removed or completed; failed, paused, waiting, retrying, or unfinished downstream Jobs remain `active`. Scope filtering and total count happen on the authorized Server query before `limit`.
- New DownloadTasks snapshot the singleton seeding policy (`enabled`, minimum minutes, minimum ratio, and `all|any`) together with their import policy. Automatic cleanup is disabled by default and later settings changes never alter an active task.
- A seeding-capable provider enters durable seeding management after `copy|symlink` import. Sampling is a short leased job that returns to `retry_wait`; it never sleeps while holding downloader or transfer capacity. Public DTOs expose only safe ratios, durations, uploaded bytes, thresholds and cleanup semantics, never provider IDs or paths.
- Threshold cleanup is mode-bound: `copy` calls provider cancellation with `deleteData=true`; `symlink` always uses `deleteData=false` because the library link permanently depends on the source. Successful `move` removes the now-stale provider task with `deleteData=false` and does not create a SeedingTask.
- Provider task-not-found is idempotent cleanup success. Other provider failures preserve the SeedingTask and retry without claiming that files were deleted. Manual stop requires destructive confirmation and uses the same immutable mode-derived `deleteData` behavior.
- Seeding completion, provider-task-not-found reconciliation, manual stop and queued cancellation all invoke the same Transfer staging-cleanup callback before the SeedingTask is finalized. `copy + deleteData=true` records provider whole-package removal; `symlink + deleteData=false` performs only the exact unselected-manifest cleanup and preserves link targets.
- Base URLs are HTTP(S) origins without userinfo, path, query, or fragment. Clients have bounded timeouts, disabled redirects, and bounded response bodies.
- qBittorrent login accepts both the legacy `200` + `Ok.` + `SID` response and the modern `204 No Content` + port-scoped `QBT_SID_<1..65535>` cookie. A successful status without a recognized non-empty session cookie remains an authentication failure; cookie values never enter logs or persistence.
- qBittorrent add accepts legacy `200 + Ok.` and modern `200/202` JSON with `added_torrent_ids`. Before every add, reconcile the stable `omc-<DownloadTask ID>` tag; a successful but unfamiliar response is ambiguous and must continue tag reconciliation instead of becoming a terminal failure or causing a duplicate add.
- Treat a modern add ID as authoritative only when the single-source response has one success, no failure/pending count, exactly one 40- or 64-character hexadecimal torrent hash, and no contradictory count. Malformed/multiple IDs fall back to stable-tag reconciliation and never become provider task IDs.
- A bare magnet uses `stopCondition=MetadataReceived` when supported. Older qBittorrent versions may reject that field; retry the add without it, poll a bounded manifest, and pause immediately after metadata becomes available.
- Legacy qBittorrent may reject `stopCondition` as either HTTP 400/404 or `200 Fails.`. Both shapes trigger the one-time no-stop-condition fallback; an ignored field is handled by bounded manifest polling plus immediate provider pause.
- Username/password are separate AES-256-GCM envelopes. APIs return only `username_configured` and `password_configured`; empty update fields preserve existing secrets, while explicit clear flags remove them.
- Magnet/HTTP(S) URLs and torrent bytes are also AES-GCM encrypted. Job payload contains only `download_task_id`; source material never enters Job checkpoint, public DTO, event, audit metadata, or logs.
- Torrent submission uses same-origin JSON with bounded Base64. The decoded file must be a non-empty `.torrent`, start with a bencoded dictionary, and be at most 4 MiB. It remains in memory and is not written to a public temporary directory.
- Unknown progress, byte counts, speeds, and ETA remain `null`. DownloadTask holds provider telemetry; Job holds scheduling progress and the REST fact remains authoritative over WebSocket deltas.
- DownloadTask snapshots the selected MediaClassificationProfile ID, revision, and canonical rules at enqueue time. Preclassification reuses `medialibrary.ParseFilename` and `classification.Classify`; later Profile edits never alter an active task.
- TMDB credential priority is custom AES-GCM credential, deployment credential, then linker-injected application credential, then unavailable. Each credential has an explicit `read_access_token|api_key` kind; Read Access Tokens use Bearer and API Keys use `api_key` query. APIs expose only `credential_source=custom|deployment|builtin|none`, `credential_kind`, configured flags, non-sensitive routes and revision; clearing custom falls through to the next source. Pre-v11 encrypted Token records default to `read_access_token` without rewriting their ciphertext.
- Runtime deployment inputs are the mutually exclusive `OMC_TMDB_READ_ACCESS_TOKEN` / `OMC_TMDB_API_KEY`; build-only inputs are `OHMYCINE_TMDB_READ_ACCESS_TOKEN` / `OHMYCINE_TMDB_API_KEY` and must never become runtime overrides. Launchers capture the chosen build value into a non-exported local value and remove both inputs before npm/Vite or Server runtime starts. Validation accepts only bounded linker-safe characters and never prints the value.
- Official builds inject exactly one application credential with linker `-X` into `tmdb.BuiltinReadAccessToken` or `tmdb.BuiltinAPIKey`; both source variables remain empty. Manual official artifacts fail closed when neither or both Secrets are configured. The application credential is revocable/read-only and considered extractable from the distributed binary, so it must have independent quota and rotation.
- Default API uses `https://api.tmdb.org/3` then `https://api.themoviedb.org/3` only after DNS/connect/timeout failure. Any HTTP response, including redirects and 401/403, prevents fallback. A tested custom API prefix is single-route and never falls back. Search and completion recheck always use the effective persisted route.
- API and image prefixes are independent non-sensitive v10 settings. They require HTTPS and reject userinfo, query, fragment, redirect, traversal and oversized responses. API testing calls fixed `/movie/550` and verifies ID 550; image testing fetches a fixed `w92` asset and validates status, image content type and bounded non-empty bytes. Failed tests preserve both old routes and revision.
- Metadata/download settings use non-zero optimistic revisions bounded by SQLite's signed integer range. A request cannot both clear and replace the TMDB token; rejected or overflowing mutations leave ciphertext and revision unchanged.
- A candidate credential or API route probe checks the submitted revision against the current row before any external request, then still uses revision CAS after the probe. A stale request must not send a candidate credential or the current effective credential to any metadata route.
- Missing TMDB configuration, authentication/network failure, no match, ambiguity, low confidence, or fallback-only classification records an allowlisted reason, assigns the provider category `未识别`, and resumes automatically. Preclassification never creates `download_classification` ActionRequests or blocks later queue work.
- Classification fallback runtime logs contain only the stable reason code plus allowlisted `credential_source` and `credential_kind`; they never contain credential values, query URLs, provider responses, filenames, or staging paths.
- qBittorrent category names come from the snapshotted Profile result. Existing categories with an empty or mismatched save path are rejected; managed category paths must remain below unified staging. Routing must call both `setCategory` and `setLocation(staging/category)` before resume because category assignment alone does not change the task save path when qBittorrent Automatic Torrent Management is disabled.
- New tasks use the exact managed `staging/category` boundary established during qBittorrent routing. Transfer source resolution checks that path first, then supports `staging/relative` only as a compatibility fallback for already-created tasks from versions that assigned a category without changing location. Every candidate and existing source/target ancestor is independently rechecked for symlink, Junction, mount-point or Reparse Point escape immediately before file access.
- A successful metadata match may be persisted as an intermediate `matched` scrape state, but `classified` is written only after provider category assignment and resume both succeed. Provider routing failure must remain retryable and re-enter classification/routing instead of skipping directly to telemetry polling.
- Completion retrieves the bounded provider manifest again and persists only an allowlisted scrape summary: title, media type, category, TMDB ID, confidence, match status, and file count. Provider raw bodies and paths are not public facts.
- Every completed provider manifest enters one provider-neutral package takeover before Transfer enqueue. Movie packages retain only the plausible primary video; TV packages retain size-plausible episode-numbered videos; tiny advertisement/sample videos are excluded. `srt|ssa|ass|jpg` follow only an accepted video in the same directory through a stem/language-suffix association. The filtered private manifest preserves stable provider identities, while logs and public summaries expose only safe counts.
- Automatic import requires a trustworthy task-level TMDB/Profile snapshot. No match, authentication/network failure, low confidence, incomplete classification, or no plausible primary media records `transfer_media_unrecognized`, leaves provider/source files untouched, and creates no TransferTask or destination directory. This completion gate applies equally to qBittorrent, Transmission, 115 native offline and future downloader adapters.
- Retrying a legacy failed TransferTask whose persisted manifest predates package selection must re-run the same completion verifier before any transfer mutation, replace the private manifest with the filtered result, and then continue the existing transfer Job. It must not resubmit the completed provider download or re-include items excluded by the new selector.
- Manifest paths are provider-relative facts only. Reject empty/dot/dot-dot segments, absolute Unix paths, Windows drive/UNC/device paths, alternate-data-stream colons, control characters, repeated separators, and non-canonical traversal before any service consumes the manifest.
- The `download` Job resource key is the downloader ID. A retry releases its lease and slot. After restart, lease recovery reclaims the Job and provider reconciliation resumes from `provider_task_id` or the stable `omc-<task-id>` tag.
- A `pan115_offline` worker subscribes to the Connection-scoped life-event broadcast after submission. Any durable allowlisted life-event batch wakes every waiting offline task on that Connection to re-read authoritative offline-task state and, on completion, its bounded manifest. Life events never directly mark a task complete because they do not carry a trustworthy task-to-output contract. A 20-second provider-state poll remains as missed/delayed-event compensation, while a separate 10-second queue heartbeat keeps the running Job lease valid without calling 115.
- When an adopted 115 task itself reaches provider failure, persist terminal `downloader_provider_failed`. Only a later explicit user retry may delete that old 115 task record with `deleteFiles=false`, clear the local provider identity/telemetry, and submit once again. A completed provider task whose later recognition, manifest verification, or transfer failed is never deleted or resubmitted by download retry; the user retries the exact failed downstream stage.
- A completed download with `scrape_status=completed_unrecognized` exposes `Re-recognize and import`, never `Retry download`. The automatic action keeps the provider task/output identity and reuses its completed manifest before continuing through Transfer → Import → Notify.
- Every targeted completion persists the canonical complete manifest before recognition. It is limited to 1 MiB and 5,000 strict provider-relative entries, remains `json:"-"`, and never enters a Job payload, DTO, audit event, WebSocket event, or log. Recovery with this snapshot bypasses downloader construction and every `Submit/Get/Manifest/Pause/Resume/SetCategory` call; a legacy row without the snapshot may fetch `Manifest` exactly once, persist it, and then use the same recovery path.
- `staging_category` is immutable physical placement once provider routing succeeds. `scrape_category` may change from `未识别` to the Profile's logical category after automatic retry or verified TMDB override. Transfer source resolution and post-import staging cleanup use `staging_category`, with `scrape_category` only as a pre-v45 compatibility fallback; naming/classification continue to use `scrape_category`.
- Manual TMDB correction is an explicit recovery tool only after automatic recognition failed. Keyword search returns at most ten credential-free summaries; choosing a result only fills `media_type + tmdb_id`. The Server persists no browser-supplied title/year/category/artwork and must validate the identity with `GetByID` before queuing recovery and again before creating the verified transfer snapshot.
- Running pause/cancel first persists an interrupt intent. An interrupt-capable worker calls the provider before acknowledging the queue interrupt. Provider failure clears the pending intent and leaves the Job running with a safe error.
- The same provider-first rule covers queued, retry-wait, paused, failed, and legacy waiting-action qBittorrent Jobs. Queue control stores a restart-safe queued intent; Scheduler claims it, calls `Pause` or destructive `Cancel(..., true)`, and only then acknowledges the state. A failed provider call clears the intent and resumes or restores the original task instead of reporting false success.
- Migration v13 closes legacy `download_classification` prompts and requeues only their waiting download Jobs for automatic classification. It clears stale classification checkpoints/errors without changing unrelated user-action Jobs; the provider remains paused until routing safely resumes it.
- Cancel is explicitly destructive: the Web UI requires confirmation, then calls `Cancel(..., true)` to remove the provider task and downloaded/temporary data. After provider success or explicit task-not-found, an acknowledgement hook transactionally deletes DownloadTask and Job; Job foreign keys cascade attempts, events and action requests. Provider failure retains local facts.
- `DELETE /api/v1/downloads/:id` keeps provider-first `deleteData=true` semantics for failed/cancelled tasks. A successfully settled completed history record uses a separate record-only branch: after transactionally rechecking download/transfer/seeding Job completion, it removes their domain rows and Job histories, writes `download.history_delete`, and never calls the provider or touches staging/library files. Any unfinished or failed downstream stage rejects history deletion. Both branches require owner + `jobs.control_own` or `downloads.manage_all`.
- The Web UI renders downloader health and task aggregates as cards; connection fields appear only after Edit. Download administration uses URL-restorable top tabs for active work, history, creation, seeding, and downloader connections. Completed history deletion states its record-only boundary; destructive failed/cancelled deletion retains its stronger confirmation. Mutations and connection tests publish bounded App-root toast notifications that remain visible regardless of scroll position and auto-dismiss.
- A card-level Test checks the persisted encrypted configuration. The edit form's Save and Test action must persist the current fields first and then test that same saved revision; it must never silently test stale credentials while presenting edited values.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Master key is missing and key file does not exist | Generate a random owner-only key file atomically; never print the key |
| Master key length/Base64 or ciphertext AAD is invalid | Fail startup/decryption safely; never fall back to plaintext |
| qBittorrent URL has credentials, path, query, fragment, or unsafe scheme | Reject with a stable safe validation error; send no request |
| qBittorrent returns legacy or modern successful login shape with a recognized session cookie | Use the short-lived cookie in memory and continue the API request |
| qBittorrent returns 200/204 without `SID` or valid port-scoped `QBT_SID_*` | Reject as `downloader_auth_failed`; never accept an arbitrary cookie as the provider session |
| qBittorrent login returns legacy `200 Fails.` or HTTP 401/403 | Return `downloader_auth_failed` without exposing the response body |
| qBittorrent login returns 404/other 4xx or 5xx | Classify as request/configuration failure or retryable unavailability instead of blaming the saved credentials |
| qBittorrent add returns modern JSON, an unknown success body, or a previous local failure already created the torrent | Bind `added_torrent_ids` when present and otherwise reconcile the stable tag; never blindly add twice |
| Magnet metadata is ready but TMDB/Profile evidence is incomplete | Assign `未识别`, record only a safe reason code/message, resume, and create no ActionRequest |
| Existing qBittorrent category points outside unified staging | Reject routing and keep the task paused; never silently reuse or overwrite it |
| qBittorrent Automatic Torrent Management is disabled | Explicitly call `setLocation` after category assignment, then resume; never assume Category changes the save path |
| Custom TMDB Token is cleared | Resolve deployment, then builtin; report `none` only when every source is absent |
| Default short TMDB API has a network failure | Try the legacy official API once; do not fallback after any HTTP response |
| Custom TMDB API/image probe fails | Preserve the old route and revision; do not change the other route |
| Unified staging is not configured | Allow downloader CRUD/test; reject qBittorrent task submission with `download_staging_required` |
| Configured staging Storage/path is disabled, missing, unsafe, or unreadable | Reject save/submit/worker resolution with `download_staging_unavailable`; never fall back to another directory |
| Settings revision changed concurrently | Reject PATCH with conflict and preserve the newer setting |
| Torrent is empty, oversized, wrong extension, or not bencoded dictionary data | Reject with `download_torrent_invalid`; create no Job or DownloadTask |
| Downloader credentials or source contain passkeys/tokens | Persist only ciphertext; public DTO/log/audit/Job payload contains none of the plaintext |
| Provider telemetry omits a value | Persist/return `null`, never manufacture zero |
| Provider pause/cancel fails | Keep Job running, clear interrupt pending, record safe error, and do not claim the operation succeeded |
| User confirms cancellation | Remove provider task and data with `deleteData=true`; then remove local DownloadTask/Job facts |
| Provider task was already manually deleted | Treat explicit task-not-found as success and idempotently remove the terminal local record |
| Provider deletion fails or Downloader reference is missing | Keep the local record and return an actionable safe error |
| Completed download has queued/running/failed transfer or seeding work | Keep it in active scope and reject record-only history deletion |
| Completed download and every existing downstream Job completed | Return it in history scope; allow record-only deletion without provider/file operations |
| Server restarts during an active qBittorrent task | Expire/recover lease, then reconcile the existing provider task instead of resubmitting |
| 115 emits a life event while multiple offline tasks wait on one Connection | Broadcast one wake to every worker; each worker independently rechecks its own provider task ID |
| 115 life events are delayed, missing, duplicated, or replayed | Keep completion idempotent and use bounded low-frequency offline-task polling as the authoritative fallback |
| 115 accepts a magnet but its add response is undecodable, empty, or reports an existing task | Reconcile by normalized info hash and persist the provider task identity; never leave the local task blindly resubmitting |
| An existing 115 task moved beyond the old/cached task-list page | Follow the bounded provider page count, adopt the matching hash, and continue from its active/completed/failed state |
| 115 reports an existing task but bounded reconciliation cannot find it | Fail once with `downloader_task_exists`; tell the user to remove the stale 115 task record and retry, without deleting provider files automatically |
| An adopted 115 task is in provider-failed state and the user explicitly retries | Delete only the old provider task record (`deleteFiles=false`), clear its local identity, then submit once; provider cleanup failure preserves the failed local task |
| A completed 115 task later fails recognition/manifest/import | Keep its provider identity/output and retry only that failed stage; never delete or resubmit the completed offline download |
| A completed-unrecognized task has a valid v45 manifest snapshot | Re-run recognition and enqueue at most one TransferTask without loading or calling the downloader; preserve the original `staging_category` |
| A legacy completed-unrecognized task has no manifest snapshot | Read the existing provider task manifest once, persist the canonical snapshot, then recover; if unavailable, return `download_completion_manifest_unavailable` without submission |
| A stored completion manifest is oversized, incomplete, duplicated, absolute, traversal-dependent, or otherwise non-canonical | Fail closed with `download_completion_manifest_invalid`; create no TransferTask and mutate no source/target file |
| 115 rejects an invalid link or reports exhausted offline quota | Mark a safe actionable terminal failure; do not schedule a ten-second retry loop |
| A 115 offline directory reaches parent ID `0` | Accept and verify it when the bound Storage root is `0`; otherwise reject as `downloader_storage_unavailable` before provider submission |
| 115 download targets a local/cross-account/symlink library or a target without live mutation capability | Reject before enqueue; explicit selection never redirects and automatic selection skips it |
| HTTP submits `provider_item`, or submits `115_share` to a non-115 downloader | Reject with `download_source_invalid`; create no Job or DownloadTask |
| Share target is absent/incompatible or its intake directory moved outside the current Storage root | Reject before receive with a safe target/storage error; never fall back to the downloader default directory |
| Share is invalid, empty, over the bounded top-level limit, or provider result remains ambiguous after directory reconciliation | Return a stable `pan115_share_*` error without logging the link, code, IDs or response body |
| Duplicate life events/sweeps adopt the same provider child concurrently | The partial unique index admits one DownloadTask; all other attempts are idempotent no-ops |
| Downloader has an active DownloadTask | Reject configuration deletion with `downloader_in_use` |
| Automatic target starts with an unavailable library | Skip it and choose the next usable ordered library |
| Explicit target is unavailable | Reject submission; never silently reroute |
| Download completes without a bounded manifest for a targeted task | Fail safely with `transfer_manifest_unavailable`; do not guess paths |
| Completed package is unrecognized or contains no plausible primary media | Fail with `transfer_media_unrecognized`; retain the source and perform no transfer/provider mutation |
| Auto-cleanup is enabled with both thresholds disabled | Reject settings update; preserve the previous revision |
| Copy seeding reaches its snapshotted threshold | Delete provider task and staging data, then mark SeedingTask completed |
| Symlink seeding reaches its snapshotted threshold | Delete provider task only; retain source data so the library link remains valid |
| Provider task was already deleted before seeding cleanup | Treat task-not-found as idempotent success and finish the durable cleanup fact |

### 5. Good/Base/Bad Cases

- Good: an administrator selects one Server-visible global staging directory in Settings, orders MediaLibraries, adds multiple qBittorrent connections without path fields, and each new task snapshots both staging and final-library routing before enqueue.
- Good: a 115 offline task selects a same-Connection cloud library, snapshots its stable root privately, and automatically enters cloud organization after authoritative completion manifest verification.
- Good: a 115 share URL selects an intake-enabled library, receives into `omc-<task-id>`, survives a lost success response by directory reconciliation, and then enters the ordinary recognition and Transfer stages exactly once.
- Good: a manual 115 App transfer appears as one non-`omc-*` direct child; startup/event/periodic sweeps may all observe it but create only one internal adopted task.
- Good: a 115 Storage bound to account root `0` uses any validated descendant as its offline directory; downloader test and task submission both accept the same selection.
- Good: 115 reports a duplicate completed magnet on a later page; the adapter adopts its hash/output and continues manifest verification without submitting again.
- Good: an adopted failed 115 task remains terminal until the user clicks retry; that explicit retry removes only the stale task record and preserves provider files before one fresh submission.
- Good: a qBittorrent task downloaded under `未识别`, then a verified override classifies it as `国产剧`; transfer and guarded cleanup still read `staging/未识别`, while destination naming uses the new logical category.
- Base: qBittorrent CRUD and connection tests work before staging is configured, while task submission gives an actionable settings error.
- Good: a running cancel displays a destructive confirmation, reaches qBittorrent with `deleteFiles=true`, then atomically removes the local task after provider acknowledgement.
- Base: qBittorrent is temporarily offline; the worker writes a safe retry code, releases the slot, and later resumes by provider task ID/tag.
- Bad: storing magnet URLs in `jobs.payload_json`, returning encrypted credential blobs through API DTOs, or logging a qBittorrent response body.
- Bad: marking a Job paused when the provider pause request failed, or using cancellation as an implicit delete-data operation.
- Bad: putting a MediaLibrary selector on a downloader form, resolving staging from the current global setting after a task was queued, or storing an absolute path in Job payload.
- Bad: treating every parent ID `0` as outside the Storage before comparing it with a configured account root of `0`.
- Bad: scanning only five fixed task pages, mapping `ErrOfflineTaskExisted` to retryable unavailability, and submitting the same magnet three times.
- Bad: reusing `scrape_category` as both physical staging location and logical metadata category, or re-entering the normal downloader worker after a completed-manifest recovery is queued.

### 6. Tests Required

- Credential tests assert key-file generation/reuse, AES-GCM round trip, purpose/AAD mismatch, and invalid key rejection.
- Migration tests cover fresh/idempotent/previous-version upgrades, legacy staging adoption/detachment, staging snapshot columns, and v23 private cloud-target/checkpoint columns.
- v45 migration tests assert both private completion-recovery columns and safe defaults. Recovery tests persist a complete manifest, make the downloader unavailable, apply a verified override, and assert zero downloader calls, one TransferTask, retained `staging_category`, and unchanged source-manifest identity; legacy fallback tests assert at most one manifest fetch.
- Provider tests use local HTTP servers to assert legacy `SID` and modern port-scoped `QBT_SID_*` login flows, missing/invalid session cookie rejection, bounded URL/torrent submission, telemetry parsing, v4/v5 pause/resume fallback, and `deleteFiles=true` for destructive cancellation.
- Provider tests also cover old/new add responses, tag adoption, metadata stop-condition fallback, bounded manifests, category list/create/set, explicit task location routing, and no duplicate submissions.
- Service tests cover encrypted TMDB settings, Profile snapshots, automatic `未识别` fallback without ActionRequest, safe category boundaries, and completion verification summaries.
- Package-takeover tests cover a 28.5 GiB movie plus sub-megabyte advertisement videos, clean title extraction, multi-episode TV packs, related/unrelated sidecars, and the no-mutation unrecognized gate for local and cloud targets.
- TMDB tests cover explicit Bearer/query routing without secret-bearing errors, credential priority/clear fallback, linker/build-script contracts, v9→v10 route defaults, v10→v11 legacy Token kind compatibility, network-only official fallback, no HTTP fallback, custom single-route behavior, independent API/image CAS, redirect/content-type/size rejection and failed-probe preservation.
- Service tests inspect raw SQLite rows and assert username/password/source plaintext and passkeys are absent from Downloader, DownloadTask, Job payload, public DTO, and audit metadata.
- Worker tests cover submit-once, provider-task-ID promotion from tag, progress/upload/download/ETA persistence, completion, retry, pause/resume/destructive cancel, task-not-found reconciliation, local cascade cleanup, provider-failure retention, and restart reconciliation.
- 115 offline worker tests cover life-event fanout to multiple consumers/workers, missed-event fallback polling, queue lease heartbeat during event waits, authoritative task/manifest recheck before completion, same/cross-Connection target selection, qBittorrent-to-cloud rejection, immutable cloud target snapshots, and explicit provider-failure retry using `deleteFiles=false` without touching completed-task downstream failures.
- 115 offline adapter tests cover a selected directory directly below account root `0`, the root itself, a normal non-zero Storage root, moved-outside-root rejection, parent cycles and depth limits; both `Test` and `Submit` must use the same result. They also cover hex/Base32 BTIH, duplicate adoption beyond page five, stale cached-page fallback, and terminal `downloader_task_exists` when bounded adoption fails.
- 115 share/adoption tests cover link parsing and extraction code, bounded snap/receive, stable `omc-*` reconciliation after ambiguous success, invalid/empty/oversized/risk responses, moved intake roots, manifest and destructive cancel behavior, internal-source HTTP rejection, encrypted source round trip, Job/DTO/log absence, immutable staging snapshot, unique concurrent adoption, and reuse of the completion verifier/Transfer enqueue.
- Seeding tests cover safe disabled defaults, optimistic settings revision, policy snapshots, qBittorrent ratio/seeding-time/uploaded mapping, copy versus symlink `deleteData`, move task-only cleanup, threshold `all|any`, provider task-not-found idempotency, retry retention, and safe public DTOs.
- HTTP tests cover auth/RBAC/no-store, visible configured staging path, picker reopening at that path, directory-token-only staging update, optimistic revision, source-size limits, owner/all visibility, redacted operational responses, and stable safe errors.
- Web tests cover magnet/URL versus torrent mode, target MediaLibrary/automatic order, route summary and transfer phase, 4 MiB client guard, encrypted-credential UX, global toast lifetime, downloader task aggregation, unknown telemetry, task controls, responsive layout, and light/dark themes.
- Run `go test ./...`, `go vet ./...`, `go build ./cmd/server`, `go build -tags webui ./cmd/server`, Web UI test/typecheck/lint/build, and Windows `server/test.ps1`.

### 7. Wrong vs Correct

#### Wrong

```go
queue.Enqueue(EnqueueJobInput{Payload: map[string]any{
    "magnet_url": request.URL, // may contain a PT passkey
}})
client.Cancel(ctx, providerID, false) // reports cancellation but leaves unwanted provider data and local facts
task.TargetProviderRootID = request.ProviderRootID // trusts a client/provider identity directly
sourceRoot := filepath.Join(task.StagingAbsolutePath, task.ScrapeCategory) // breaks after manual reclassification

if current == "" || current == "0" { // rejects valid descendants of account root 0
    return ErrOutsideStorage
}
```

#### Correct

```go
encrypted, err := credentials.Encrypt("download-task:"+taskID+":source", sourceJSON)
if err != nil {
    return err
}
queue.EnqueueWith(EnqueueJobInput{
    Payload: downloadJobPayload{DownloadTaskID: taskID},
}, func(tx *gorm.DB, job models.Job) error {
    return tx.Create(&models.DownloadTask{ID: taskID, JobID: job.ID, SourceCiphertext: encrypted}).Error
})

// Snapshot the validated global absolute staging directory at enqueue.
task.StagingAbsolutePath = settings.AbsolutePath

// Keep physical placement separate from classification. A later manual match
// may change ScrapeCategory, but transfer and cleanup still use this snapshot.
task.StagingCategory = assignedProviderCategory

// Persist the bounded canonical provider-relative manifest before recognition.
completedManifestJSON, err := encodeCompletedDownloadManifest(manifest)
if err != nil {
    return err
}
task.CompletedManifestJSON = completedManifestJSON

// Resolve from the selected library and current driver; persist privately.
target, err := downloads.snapshotDownloadTarget(ctx, downloader, library)

// The UI confirms this destructive action; cleanup is provider-first, audited,
// and local facts are deleted only after success or explicit task-not-found.
client.Cancel(ctx, providerID, true)

if current == "" || (current == "0" && rootID != "0") {
    return ErrOutsideStorage
}
```
