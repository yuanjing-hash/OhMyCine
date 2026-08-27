# Research: Transfer deletion preview stalls on 115 source cleanup

- Query: Why does the Organization deletion dialog remain on “正在核对任务、来源和媒体库托管清单…” for the cancelled 23/28 `屌丝男士` 115 transfer, especially when the provider task/files were already removed manually?
- Scope: internal
- Date: 2026-08-27

## Findings

### 1. Exact UI/API path

- The screenshot's focused top-right option is `record_and_source` (“删除转移记录和源文件”), defined at `server/webui/src/components/TransferDeletionDialog.vue:12-16`.
- Clicking an option sets `loading=true` and awaits `previewTransferDeletion`; the loading copy in the screenshot is rendered while the promise is pending (`server/webui/src/components/TransferDeletionDialog.vue:19-25`, `:85`).
- The request is `POST /api/v1/transfers/:id/deletion-preview` (`server/webui/src/transfers.ts:185`). The shared API client performs a bare `fetch` with no deadline or default `AbortController` (`server/webui/src/api/client.ts:27-36`). While it is pending, the dialog disables every option and its close button (`TransferDeletionDialog.vue:59`, `:65`), so the user has no escape hatch.
- The route invokes the service synchronously in the request goroutine (`server/internal/handlers/transfers.go:96-112`; route registration at `server/internal/httpserver/router.go:162`). No progress is returned during provider reconciliation.

### 2. The synchronous 115 preview does an unbounded number of serial provider calls

- `PreviewDeletion` calls `loadTransferDeletionBoundary` before it creates the preview token (`server/internal/services/transfer_deletion.go:105-139`). The absence of a `transfer_deletion_previews` row therefore means the request is still in boundary validation or failed before token creation.
- Source scopes require `validateSourceDeletionBoundary` (`transfer_deletion.go:283-302`). For `pan115_offline`, this calls `pan115DeletionBoundary` (`transfer_deletion.go:354-358`).
- `pan115DeletionBoundary` first validates the provider output root and then loops over every file in the complete source manifest (`transfer_deletion.go:442-479`). Each file is passed to `providerItemWithinRoot`.
- `providerItemWithinRoot` repeatedly calls `driver.Stat` for the file and every parent until it reaches the requested root; it has no cache for parents shared by adjacent files (`server/internal/services/cloud_boundary.go:13-44`).
- 115 `Stat` calls `GetFile` through the shared read/list limiter (`server/pkg/cloud/pan115/client.go:970-988` in the current worktree; the runtime binary was built from the equivalent pre-refactor `Stat` at former lines 953-970). The runtime client uses one `listRate` token every two seconds with burst one and a 15-second per-provider-call HTTP timeout (`server/pkg/cloud/pan115/client.go:25-33,216-225`; the running binary was started before the in-progress limiter refactor).
- The HTTP server has a 60-second `WriteTimeout` (`server/cmd/server/main.go:254-257`), but the deletion handler has no shorter operation deadline. A handler can spend minutes validating and then be unable to deliver a useful JSON response.

### 3. Runtime evidence makes the timeout deterministic for this task

Read-only inspection of `server/.runtime/windows/data/ohmycine.db` found:

- TransferTask `5dd1cac0-aef0-4027-9877-5efeb677ef5a` is the screenshot row. Its domain phase is stale `transferring`, progress is `23/28`, and `finished_at` is null.
- Its transfer Job `d429a6b6-6e8b-4e76-94bc-0d38e4592be9` is terminal `cancelled` (revision 3), and its Download Job `d2f2aaf8-f6c5-4b24-bb65-fc482e0043b7` is also terminal `cancelled` (revision 4). Both were cancelled at 2026-08-27 23:34:38 Asia/Shanghai. Thus the terminal Job gate correctly permits opening deletion even though the stale domain phase still says `transferring`.
- The DownloadTask is `pan115_offline`, `transfer_mode=move`, `provider_status=completed`; it still stores a provider task identity and provider output identity.
- The selected import manifest contains 28 videos, but the immutable complete source manifest used by destructive source deletion contains 38 files under five distinct provider parents. There are no `media_managed_items` because the transfer was cancelled before final managed-item capture.
- Only two of the 38 source items are direct children of the package root. Even in the best possible ancestry layout, validating the root plus 2 direct files and 36 nested files requires at least `1 + 2*2 + 36*3 = 113` serialized `Stat` calls. With one list token every two seconds, the lower bound is about 224 seconds before API latency, deeper ancestry, concurrent 115 reads, or retry/backoff. This exceeds the 60-second server response-write budget by almost four times.
- No preview row existed after the screenshot, consistent with the handler being stuck before `PreviewDeletion` reaches `tx.Create(&preview)`.

This is not an SQLite lock: all database reads before provider validation are small, provider work deliberately happens outside a writer transaction, and the stall occurs before preview insertion. It is synchronous N-times-ancestry provider I/O plus an absent request/UI deadline.

### 4. A partial `move` can never satisfy the current full-source boundary

- The transfer moved 23 of 28 selected items before cancellation. A moved item's stable ID now belongs below the target library, not below the immutable source package root.
- `providerItemWithinRoot` correctly refuses to prove such an item is still under the source root (`cloud_boundary.go:39-44`), but `pan115DeletionBoundary` converts that outcome into a hard `transfer_deletion_boundary_changed` (`transfer_deletion.go:467-476`).
- Therefore, after the slow scan reaches the first moved item, `record_and_source` cannot preview, even though the safe semantic result is “this item is no longer source data; retain it where it is.”
- This is especially visible for partial moves because `media_managed_items` are captured only on successful final reconciliation. The task has no complete library-ownership manifest, so library deletion must remain unavailable, but local history plus remaining source cleanup can still converge safely.

### 5. Already-missing 115 state is handled inconsistently

- Missing individual manifest items are counted as `sourceMissing` (`transfer_deletion.go:467-471`), which is an idempotent/convergent behavior.
- A missing provider output/package root is not handled the same way. Any root lookup error, including provider `not_found`, is mapped to “115 来源边界已变化” (`transfer_deletion.go:454-460`). If the user already deleted the whole package directory, the safest and most useful conclusion is that source removal is already complete, not that local history must be retained forever.
- The normal DownloadService already treats downloader `task_not_found` as idempotent success (`server/internal/services/download.go:1097-1126`, especially `:1117-1120`). The transfer deletion implementation bypasses that helper for 115.
- Specifically, `deleteTransferSource` has a special `pan115_offline` branch that revalidates the whole manifest, recycles every file ID one by one, and only clears `provider_task_id` locally (`server/internal/services/transfer_deletion.go:482-506`). It never calls the downloader client's `Cancel`/115 `DeleteOfflineTasks`, although `pan115offline.Client.Cancel` already delegates ordinary offline tasks to `driver.CancelOffline(ctx,id,deleteData)` (`server/pkg/downloader/pan115offline/client.go:439-451`) and the cloud client calls the actual offline deletion endpoint (`server/pkg/cloud/pan115/client.go:705-714` in the pre-refactor layout).
- Consequently, even a successful source-scope confirmation can leave the real 115 offline task behind, causing the “already downloaded/task exists” problem on a later submission. It also spends at least one paced recycle call per manifest item and repeats the full slow validation during confirmation.

### 6. Job terminal state is not quite the same as worker quiescence

- `CancelPipeline` removes the provider task first, then directly writes running downstream jobs to terminal `cancelled`, releases their leases, and finally calls `interruptLocally` (`server/internal/services/download.go:928-1055`).
- For this runtime row the provider task identity remains stored, while both Jobs are already terminal. That can be legitimate retained history, but deletion must not infer from stale `TransferTask.phase=transferring` that a worker is active, nor infer from Job `cancelled` alone that an old goroutine cannot still be unwinding.
- The deletion barrier should use queue/lease/worker ownership facts. Only a genuinely active local worker/lease should block or require cancellation acknowledgement. Stale domain phases must be reconciled for display, not used as the authoritative gate.

## Root cause

The immediate stall is caused by doing a complete, per-file, per-ancestor 115 proof synchronously inside a preview HTTP request while every `Stat` is serialized by a two-second read limiter and neither the frontend request nor the service operation has a bounded deadline. The affected task requires at least 113 `Stat` calls, so it cannot fit the Server's 60-second response budget.

Two semantic bugs then prevent eventual success:

1. a partial move is validated as if all original source IDs must still remain under the source package root; and
2. a missing provider task/package root is treated as boundary corruption rather than idempotent completion.

The 115-specific confirmation path also deletes file IDs instead of deleting/reconciling the actual offline task, so it does not satisfy the product contract that provider task removal accompanies local deletion.

## Safe fix boundary

### Backend

1. Keep `record_only` purely local. It must not construct a 115 driver, call `Stat`, inspect the source manifest physically, or wait for downloader/provider state. It should recheck actor ownership, terminal/quiescent Job facts, reorganization dependencies, then create a token immediately.
2. Introduce an explicit source-deletion reconciliation result rather than requiring every immutable manifest item to remain below the old source root. Suggested states per owned source boundary: `present_in_source`, `already_missing`, `detached_from_source`, `ambiguous/unavailable`.
3. Treat provider task `not_found`, package root `not_found`, and individually missing source items as converged success/missing. These cases must still preserve all local identity/digest facts through preview/confirm CAS.
4. Treat a stable item found outside the immutable package root as detached, not as a deletion candidate. Never recycle it by item ID from a source-scope operation. This preserves the 23 files already moved into the media library.
5. When the immutable package root still exists below the configured 115 Storage root, delete only that proven package root (or a bounded bulk snapshot of remaining descendants) instead of walking and recycling 38 entries one by one. Revalidate the exact package-root stable ID immediately before mutation. Unmanaged siblings outside that root are never in scope.
6. Remove/reconcile the actual 115 offline task through the existing downloader `Cancel` contract. `task_not_found` is success. If the provider task exists but the package root is already absent, remove only the task (`deleteData=false`); if the root exists and the user chose source deletion, use the downloader's supported destructive cancellation/root cleanup semantics without touching detached library items.
7. Add a bounded service timeout below the 60-second HTTP write budget and return a stable retryable `provider_timeout/unavailable` error. Do not hide an unbounded provider sweep behind a preview request. For large or slow providers, persist a reconciliation job and let the dialog poll progress instead.
8. Gate deletion on authoritative local worker quiescence (lease/interrupt acknowledgement), not stale `TransferTask.phase`. If a worker is genuinely active, reject with an actionable conflict or run the existing cancellation barrier first.
9. Preserve fail-closed behavior for auth failure, mismatched stable identity, ambiguous package root, configured-root ancestry failure, or provider errors other than explicit not-found. “Missing is complete” must never become “unavailable is missing.”

### Frontend

1. Give preview and confirmation requests an `AbortController` and a bounded UI deadline consistent with the backend contract.
2. Keep Close/Cancel enabled during preview; abort the request on close and component unmount. Disable only the destructive choice currently in flight or show a cancelable progress state.
3. On timeout, invalid response, or abort, always clear loading and show an actionable error. The current `finally` is correct once `fetch` actually settles, but bare `fetch` can remain pending indefinitely.
4. If reconciliation becomes asynchronous, show real counts/phases such as “核对 115 来源任务”“核对包目录”“已缺失 N 项”; do not leave one indefinite generic sentence.

## Reproduction paths

### Deterministic performance reproduction

1. Create a terminal/cancelled `pan115_offline` DownloadTask and TransferTask.
2. Persist a complete source manifest with 38 files under four child folders plus two root files.
3. Use a fake driver whose `Stat` succeeds and record every requested ID; configure the read limiter at one call per two seconds or use a fake clock.
4. Request `record_and_source` deletion preview.
5. Current behavior makes at least 113 `Stat` calls and cannot finish inside 60 seconds.

### Partial-move reproduction

1. Start a 28-video 115 `move` Transfer and stop it after 23 files.
2. Keep stable IDs for the moved files under the target library root and five files under the source package root.
3. Cancel the pipeline, then preview `record_and_source`.
4. Current behavior eventually returns boundary-changed. Correct behavior retains the 23 detached/library items, previews only the proven remaining source boundary, and allows local history convergence.

### Already-deleted reproduction

1. Delete the 115 offline task and package directory manually while retaining the local terminal records.
2. Preview and confirm `record_and_source`.
3. Current behavior rejects the missing root. Correct behavior reports the source as already missing, removes local history, and performs no file mutation.

## Required tests

### Go service tests

- `record_only` on a 10,000-entry 115 manifest performs zero driver/downloader calls and completes immediately.
- Missing offline task + missing package root is idempotent success; local Transfer/Download history is removed only after confirmation.
- Existing offline task + missing package root removes only the provider task and does not claim file removals.
- Missing offline task + existing proven package root deletes/recycles only that root according to the explicit source scope.
- Partial move: files below the target library root are classified detached and never recycled; only source-root remnants are removed.
- Provider `not_found` is distinct from auth-expired, timeout, 429/risk backoff, malformed identity, and ancestry ambiguity. Only explicit not-found converges.
- Provider timeout returns before the HTTP budget, preserves every row/file, and emits a safe error code.
- Root identity or configured Storage ancestry change fails closed with no mutation.
- Confirmation rechecks token revisions/digests and exact root identity; replay and expiry remain rejected.
- Actual pan115 downloader `Cancel` is invoked with the right `deleteData` semantics; task-not-found succeeds and other failures retain local records/provider identity.
- A cancelled Job with stale Transfer phase is deletable when no lease/worker is active; an actually running lease blocks until cancellation acknowledgement.

### HTTP/WebUI tests

- Preview route completes or returns a valid bounded error before `WriteTimeout`; no invalid/empty response after long provider work.
- Closing the dialog during preview aborts the request and immediately restores the page.
- Timeout/error clears loading, re-enables options and Close, and displays the safe server message.
- The 23/28 cancelled row explains that 23 detached/library-side files will be preserved and shows only remaining source deletion scope.
- `record_and_library` stays unavailable when no complete `media_managed_items` ownership manifest exists; source convergence must not weaken library deletion boundaries.

## Files found

- `server/webui/src/components/TransferDeletionDialog.vue` — deletion scope UI and the permanently disabling loading state.
- `server/webui/src/transfers.ts` — preview/confirm endpoint functions and deletion DTOs.
- `server/webui/src/api/client.ts` — shared fetch wrapper without a deadline.
- `server/internal/httpserver/router.go` — deletion route registration and permissions.
- `server/internal/handlers/transfers.go` — synchronous preview/confirm handlers.
- `server/internal/services/transfer_deletion.go` — boundary loading, full manifest validation, provider/file deletion and finalization.
- `server/internal/services/cloud_boundary.go` — uncached parent-by-parent `Stat` ancestry proof.
- `server/pkg/cloud/pan115/client.go` — 115 request timeouts, pacing and offline-task deletion API.
- `server/pkg/downloader/pan115offline/client.go` — provider task cancellation adapter.
- `server/internal/services/download.go` — existing task-not-found convergence and pipeline cancellation behavior.
- `server/cmd/server/main.go` — global HTTP timeouts.
- `server/internal/services/transfer_deletion_test.go` — current coverage; lacks missing-root, partial-move, performance/deadline and provider-task cleanup cases.

## Related specs

- `.trellis/spec/backend/download-route-selection.md:79-80,107-118` already requires provider-first cancellation, provider task-not-found idempotency, retained local facts on other failures, and separation of cancellation from destructive record deletion.
- `.trellis/spec/backend/transfer-organization.md:38-47,67-80` requires stable-ID ancestry, restart-safe moves, missing-item idempotency and strict source/package deletion boundaries.
- `.trellis/spec/backend/error-handling.md:32,56-58` requires context cancellation and distinct timeout/unavailable mapping.
- `.trellis/spec/frontend/server-admin-ui.md` applies to a responsive, recoverable admin modal.

## External references

- None required. This diagnosis is based on the checked-in Go/Vue contracts and the local runtime database; no external service behavior was assumed beyond the adapter semantics already encoded in the repository.

## Caveats / Not Found

- The live database was inspected read-only; no provider API or deletion endpoint was invoked, so the current physical location/existence of each of the 38 provider IDs was intentionally not changed or exhaustively re-queried.
- Current `.trellis/spec/backend/transfer-organization.md:30-31,68` still describes legacy record-only Transfer deletion, while v51 code exposes four destructive scopes. This is spec drift and should be reconciled during the task's spec-update phase; implementation must not use the older wording to silently remove the user-approved destructive options.
- `server/pkg/cloud/pan115/client.go` was being changed concurrently by the main task's implement agent (operation-specific limiters). Line numbers above describe the current worktree where noted; the runtime evidence comes from the already-running binary launched before those edits. Splitting mutation limiters does not fix the deletion preview because its dominant calls are `Stat` through the read/list limiter and repeated ancestry traversal.
- A bulk/root-based implementation must confirm the exact semantics of 115 `DeleteOfflineTasks(deleteFiles=true)` for a partially moved package. If it can affect detached stable IDs, use task removal plus separately proven package-root recycle instead. The safety invariant is outcome-based: source-root remnants may be deleted; detached/library items may not.
