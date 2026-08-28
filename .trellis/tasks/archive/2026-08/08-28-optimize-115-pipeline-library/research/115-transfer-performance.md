# Research: 115 transfer performance

- Query: Why does one user-visible 115 organization task take more than ten minutes, and which batching, directory, cache, and risk-control patterns from `DDSRem-Dev/MoviePilot-Plugins` are safe to reuse?
- Scope: mixed (OhMyCine source/runtime evidence plus external GitHub source)
- Date: 2026-08-28

## Findings

### Executive conclusion

The observed delay is real and is primarily an algorithmic request-amplification problem, not evidence that 115 actually triggered risk control. The user-visible “one item” was one transfer package containing 28 selected media files and 10 removable extras. The cloud move phase took 1,242,267 ms (20m42s), followed by 175s of cleanup. OhMyCine currently validates ancestry and performs move/rename item-by-item. Each `Stat` is placed on a two-second lane, and `providerItemWithinRoot` repeats `Stat` for every ancestor. Cleanup repeats the same ancestry walk and then recycles each extra separately.

MoviePilot-Plugins solves the same 115-to-115 shape by batching tasks, grouping files by target directory, recursively creating only leaf directories, calling 115 batch move/copy once per group, batch-renaming, and updating path/ID caches after mutations. OhMyCine should adopt that request-shape while retaining its stricter ancestry, restart, manifest, and destructive-boundary contracts.

### Runtime evidence from the reported task

Read-only inspection of the local runtime on 2026-08-28 found transfer task `7a0bfa1a-c145-4ff7-923b-5b1b047e880a`:

- It was enqueued with 28 selected files at 09:28:56 (`server/.runtime/windows/logs/runtime.jsonl:53712`) and started immediately (`runtime.jsonl:53713`).
- Naming/planning began at 09:28:56 (`runtime.jsonl:53716`) and reached “start checking target directories” at 09:29:02 (`runtime.jsonl:53735`), so the initial planning portion was only about six seconds.
- The 115 move phase finished at 09:49:38 with `duration_ms=1242267` and `files=28` (`runtime.jsonl:54790`): approximately 44.4 seconds per selected file.
- Cleanup then removed 10 non-media extras at 09:52:34 (`runtime.jsonl:54881`), about 175 seconds after the cloud move completed; the queue job did not finish until that cleanup ended (`runtime.jsonl:54882`).
- Read-only SQLite aggregation (without retaining raw names or provider IDs in this report) showed 38 source-manifest files, 28 selected files, 10 cleanup extras, 4 unique source parent directories, 7 persisted target-directory entries including the root, and 4 unique target parents.
- The task completed on attempt 1 and its task-correlated logs contain no rate-limit/retry warning. This does not prove 115 never slowed an individual response, but it rules out the retry queue or circuit breaker as the main explanation for this run.

This also explains the wording mismatch: the UI exposed one package/organization task, while the worker executed 28 media mutations plus 10 cleanup mutations.

### Current OhMyCine call chain and amplification

1. `runCloudTransfer` validates the target root, builds targets, ensures the complete target directory DAG, checks conflicts, then loops over every target sequentially (`server/internal/services/transfer_cloud.go:56`, `:86`, `:133-140`, `:150`, `:204-253`).
2. Directory creation is already deduplicated by logical relative path (`uniqueCloudTargetDirectories`, `transfer_cloud.go:321-342`), but every directory check lists its parent (`transfer_cloud.go:394-447`) and conflict detection separately lists each target parent (`transfer_cloud.go:481-529`). The same target listings are not shared across these phases.
3. For every not-yet-applied move item, the worker:
   - calls `driver.Stat` once (`transfer_cloud.go:211-215`);
   - calls `providerItemWithinRoot` on the same item (`transfer_cloud.go:221-227`), which stats the item again and then every ancestor;
   - performs one move (`transfer_cloud.go:596-604`);
   - stats the item again (`transfer_cloud.go:605-608`);
   - performs one rename if needed (`transfer_cloud.go:609-618`);
   - persists the complete cloud state and summary and heartbeats after each item (`transfer_cloud.go:238-253`).
4. `providerItemWithinRoot` performs one remote `Stat` per depth until it reaches the configured root (`server/internal/services/cloud_boundary.go:13-44`). Shared ancestors are not memoized, even though the observed package had only four unique source parents.
5. Pan115 `Stat` calls `GetFile` through `listRate` (`server/pkg/cloud/pan115/client.go:970-985`), and `listRate` is one request every two seconds (`client.go:225`). Move, copy, rename, and recycle each have independent two-second lanes (`client.go:231-233`), but the transfer loop is sequential, so separate lanes do not create useful overlap.
6. `maxInFlightCalls=2` (`client.go:32`, `:234`) protects the account but does not improve this sequential worker.
7. Directory creation itself is no longer artificially delayed: `mkdirRate` is infinite and only concurrency-bounded (`client.go:227-233`, `:378-400`). However, the existence/list checks around directory creation still use the two-second list lane.
8. The 115 SDK abstraction already exposes variadic batch-capable primitives internally: `mutationSDK.Move`, `Copy`, and `Delete` accept multiple IDs (`client.go:64-70`), and `115driver v1.3.5` encodes every ID into one request (`$GOPATH/pkg/mod/github.com/!shelton!zhu/115driver@v1.3.5/pkg/driver/op.go:9-26`, `:49-94`). OhMyCine's public wrapper discards this capability by exposing only one item at a time (`client.go:465-508`, `:552-564`).

The resulting complexity is approximately `O(files * ancestry_depth + files)` provider calls for move, rather than `O(unique_parent_directories + batches)`. A two-second floor on repeated `Stat` requests alone is enough to dominate runtime.

### Cleanup adds a second item-by-item slow path

After a successful transfer, `finishCompletedTransfer` performs staging cleanup before the queue job is allowed to finish (`server/internal/services/transfer_cleanup.go:22-81`). For 115:

- It first proves the package root is inside the configured Storage (`transfer_cleanup.go:261-280`).
- It then walks ancestry for each removable file (`transfer_cleanup.go:282-295`).
- It recycles every item separately and may stat again to reconcile an error (`transfer_cleanup.go:297-304`).

The observed 10-item cleanup taking about 175 seconds is consistent with this `O(extras * depth)` validation plus one recycle per item. The strict package-root boundary is correct and must remain; the expensive part is re-proving shared ancestry and issuing singleton deletes.

### Risk-control behavior is not the root cause of this successful run

All Pan115 calls share an error-driven account recovery state. A 405/429/rate/frequent/risk response creates exponential backoff from two seconds up to two minutes, and three risk failures open a five-minute circuit (`server/pkg/cloud/pan115/client.go:1064-1116`). That is appropriate as a failure response, but it was not evidenced in the observed task.

The fixed two-second healthy lanes are separate from that adaptive risk circuit. Current healthy pacing is applied per item, which turns safety policy into linear wall time. The safer optimization is fewer batch requests plus adaptive backoff on actual risk responses—not blindly increasing per-item concurrency.

### MoviePilot-Plugins reference implementation

Reference revision inspected: public `main` at commit `574db20b03ec67d930a8753ca25c8695f3c3fe6f` (2026-08-26).

The repository's own batch-transfer design explicitly states that it intercepts 115-to-115 organization to replace per-file API operations with batch operations (`docs/p115strmhelper/BATCH_TRANSFER_DESIGN.md:18-34`). Its useful patterns are:

- Collect tasks for ten seconds or until the batch reaches 100 (`BATCH_TRANSFER_DESIGN.md:199-213`, `:751-757`). This amortizes a burst of MoviePilot tasks into provider batches.
- Deduplicate target directories to leaf directories and let 115 recursively create the path (`plugins.v2/p115strmhelper/helper/transfer/handler.py:403-452`). `_get_folder` checks a cache first, then uses one recursive `fs_makedirs_app` call and immediately updates the folder cache (`handler.py:236-266`).
- Group operations by `(target_directory, transfer_type)` (`handler.py:470-498`) and list existing target files once per group (`handler.py:546-553`).
- Batch-delete conflicts (`handler.py:710-749`).
- Submit all file IDs for a target group in one `fs_move` request (`handler.py:756-817`) or one `fs_copy` request (`handler.py:818-829`). The linked subtitle/audio variant follows the same grouped batch shape (`handler_linked_batch.py:132-161`, `:452-551`).
- Batch-rename with `p115client.tool.edit.update_name` (`handler.py:6-8`, `:1122-1158`).
- Update folder/file/rename caches after mutations rather than immediately re-querying every item (`helper/transfer/cache_updater.py:51-107`, `:125-158`).

No unconditional sleep/rate limiter exists in these move/copy/mkdir code paths. That does not mean OhMyCine should remove all safety controls; it demonstrates that healthy performance comes from batch request shape and cache coherence, not one fixed delay per file.

### Recommended OhMyCine design

#### P0: remove repeated ancestry walks without weakening boundaries

Add a provider-specific, optional batch preflight used only by the 115 worker:

- Prove the immutable `provider_output_id` package root is below the Storage root once.
- Reconcile selected source files from one complete `ListTree(packageRoot)` snapshot, or list each unique manifest parent once and prove each unique parent below the package root with a memoized ancestry cache.
- Match each manifest item by stable ID, exact parent, size, and optional SHA1 before mutation.
- Reuse target parent listings across directory creation, conflict detection, and post-operation reconciliation within one attempt.

Keep generic `providerItemWithinRoot` for one-off callers and destructive boundaries, but do not invoke it independently for every item sharing the same proven package tree.

#### P0: expose batch mutation as an optional capability

Introduce an optional provider interface such as `BatchMutationDriver` rather than changing all drivers:

```text
MoveMany(ctx, itemIDs, targetParentID)
CopyMany(ctx, itemIDs, targetParentID)
RenameMany(ctx, map[itemID]targetName)
RecycleMany(ctx, itemIDs)
```

- Implement 115 `MoveMany`, `CopyMany`, and `RecycleMany` through the existing variadic `115driver` methods.
- Implement `RenameMany` only after validating the 115 batch rename response contract; the MoviePilot plugin's `update_name` proves such an endpoint shape exists, but OhMyCine should write its own adapter and tests.
- Group selected items by target parent and operation. Use bounded chunks (for example at most 50–100 IDs and a request-size limit) so one huge package cannot create an oversized request.
- Apply endpoint pacing once per batch request. Retain the shared adaptive 405/429 circuit and bounded in-flight calls.

For the observed 28 files in four target parents, move calls should fall from 28 to at most four (or the number of bounded chunks), while source validation should scale with four unique source parents rather than 28 files times ancestry depth.

#### P0: preserve restart safety around batch ambiguity

Batching must not weaken the current restart guarantees:

1. Persist a batch intent containing only private IDs/target parents before the provider call.
2. Execute the provider batch outside a database transaction.
3. Reconcile the target parent once after the call and independently classify each ID as applied, unchanged, missing, or ambiguous.
4. Persist per-item states in one short transaction. On retry, reconcile the same intent before resubmitting.
5. For move, stable IDs make convergence straightforward. For copy, keep a task-specific temporary directory, list it once, and match copied results by name+size+SHA1 before grouped rename/move. Multiple candidates must continue to fail closed.

#### P1: batch cleanup within the exact same safety boundary

- Use the same package-root snapshot/unique-parent proof to validate all 10 extras.
- Only after every deletion candidate has passed exact ID/parent/size/SHA1 checks, call `RecycleMany` in bounded chunks.
- If validation is partial or ambiguous, delete nothing from that chunk. Missing IDs remain idempotent.
- Preserve protected video/subtitle logic and the immutable package-root boundary from `.trellis/spec/backend/transfer-organization.md`; batching is not permission to scan or guess extra content.

#### P1: reduce persistence amplification

- Persist batch intent before mutation and one per-batch result after reconciliation, not the entire growing JSON state and summary after every file.
- Heartbeat by batch and by elapsed-time threshold so long provider calls still renew the lease.
- Keep per-item states in the private checkpoint for precise retry and UI counts.

#### P1: add provider-call observability

Add safe counters/timers to the final operation log, without paths or provider IDs:

```text
source_preflight_calls, target_list_calls, mkdir_calls,
move_batch_calls, rename_batch_calls, cleanup_batch_calls,
provider_wait_ms, provider_call_ms, db_checkpoint_ms
```

This distinguishes risk backoff, upstream latency, database persistence, and excess calls. The current start/end log proves the symptom but not which provider stage consumed time.

### Suggested acceptance tests

- A 28-file, four-source-parent, four-target-parent move uses call counts proportional to unique parents and chunks, not file count times ancestry depth.
- The fake 115 adapter asserts at most four move batches for that fixture and no per-file ancestry walk.
- A crash after provider batch success but before checkpoint converges without submitting the move twice.
- Partial/ambiguous batch response reconciles each item and never marks an unverified item complete.
- Ten removable extras are validated from a shared snapshot and recycled in bounded batch calls; one changed item prevents its chunk from being deleted.
- Actual 405/429 responses still trigger exponential backoff/circuit behavior, while healthy mkdir/move/copy paths have no per-item fixed sleep.
- Logs expose only aggregate counts/durations and stable safe codes.

## Files Found

- `server/internal/services/transfer_cloud.go` — 115 cloud transfer orchestration, sequential per-item move/copy/reconcile, directory and conflict listing.
- `server/internal/services/cloud_boundary.go` — generic ancestry validator that issues one `Stat` per parent depth.
- `server/internal/services/transfer_cleanup.go` — manifest-difference cleanup and singleton 115 recycle loop.
- `server/pkg/cloud/pan115/client.go` — Pan115 SDK wrapper, endpoint limiters, adaptive circuit breaker, and currently singleton mutation surface.
- `server/.runtime/windows/logs/runtime.jsonl` — observed 28-file task timings and cleanup duration.
- `server/.runtime/windows/data/ohmycine.db` — read-only aggregate inspection of the reported task; no raw names or IDs copied into this report.
- `DDSRem-Dev/MoviePilot-Plugins/docs/p115strmhelper/BATCH_TRANSFER_DESIGN.md` — external design rationale and batch workflow.
- `DDSRem-Dev/MoviePilot-Plugins/plugins.v2/p115strmhelper/helper/transfer/handler.py` — external grouped mkdir/move/copy/rename implementation.
- `DDSRem-Dev/MoviePilot-Plugins/plugins.v2/p115strmhelper/helper/transfer/handler_linked_batch.py` — external batch handling including linked subtitle/audio items.
- `DDSRem-Dev/MoviePilot-Plugins/plugins.v2/p115strmhelper/helper/transfer/cache_updater.py` — external mutation-aware path/ID cache updates.
- `github.com/SheltonZhu/115driver@v1.3.5/pkg/driver/op.go` — current Go dependency's variadic move/copy/delete request encoding and file-stat ancestry data.

## External References

- Repository: https://github.com/DDSRem-Dev/MoviePilot-Plugins
- Revision inspected: https://github.com/DDSRem-Dev/MoviePilot-Plugins/commit/574db20b03ec67d930a8753ca25c8695f3c3fe6f
- Batch design: https://github.com/DDSRem-Dev/MoviePilot-Plugins/blob/574db20b03ec67d930a8753ca25c8695f3c3fe6f/docs/p115strmhelper/BATCH_TRANSFER_DESIGN.md
- Batch handler: https://github.com/DDSRem-Dev/MoviePilot-Plugins/blob/574db20b03ec67d930a8753ca25c8695f3c3fe6f/plugins.v2/p115strmhelper/helper/transfer/handler.py
- Linked batch handler: https://github.com/DDSRem-Dev/MoviePilot-Plugins/blob/574db20b03ec67d930a8753ca25c8695f3c3fe6f/plugins.v2/p115strmhelper/helper/transfer/handler_linked_batch.py
- Cache updater: https://github.com/DDSRem-Dev/MoviePilot-Plugins/blob/574db20b03ec67d930a8753ca25c8695f3c3fe6f/plugins.v2/p115strmhelper/helper/transfer/cache_updater.py
- External repository license: GNU GPL v3 (`LICENSE`, version line 1-2). Reuse architecture and independently implement it; do not copy source into OhMyCine without confirming license compatibility.

## Related Specs

- `.trellis/spec/backend/transfer-organization.md` — same-connection 115 transfer, ancestry proof, restart safety, copy ambiguity, cleanup boundary, and private state requirements.
- `.trellis/spec/backend/media-library-foundation.md` — Storage-relative roots, reconciliation, provider concurrency/rate boundaries, and watcher separation.
- `.trellis/spec/backend/error-handling.md` — bounded retries, partial failure, safe upstream error reporting.
- `.trellis/spec/backend/quality-guidelines.md` — context-aware external calls, interface-based drivers, tests, and secret-free logging.

## Caveats / Not Found

- The current logs do not emit per-provider-call timings, so the exact split among list-limiter wait, HTTP latency, mutation wait, and database checkpoint time cannot be reconstructed after the fact. The source-level amplification and observed scaling are nevertheless strong evidence.
- The reported task is a package with 28 selected files, not a literal one-file manifest. A true one-file transfer should be benchmarked separately after instrumentation.
- MoviePilot-Plugins has different safety and persistence guarantees. Its code is evidence for batching/caching request shape, not a drop-in implementation for OhMyCine.
- 115 batch rename and maximum IDs/request need integration tests against the chosen SDK/API before fixing a production chunk size.
- `ListTree` may itself return a partial result at configured caps; a partial snapshot must never authorize move or deletion. The unique-parent-list fallback should remain available.
