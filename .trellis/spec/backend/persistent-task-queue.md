# Persistent Task Queue

## Scope

Use this contract for Server jobs that perform discrete, restart-safe automation work such as download, transfer, media-artifact generation/upload, scrape and media-server refresh. MediaLibrary watchers and provider event listeners remain parallel supervisor work and never consume queue slots; after a scan commits one authoritative generation, its file-producing STRM/NFO/JPG work runs as a coalesced `media_artifact` Job.

## Durable contracts

- SQLite is authoritative for job state, lane order, attempts, timeline events, actions, policies, checkpoints and leases. Memory coordinates goroutines only.
- A scheduling lane is exactly `(job_type, priority)`. Only the complete set of `queued` rows in one lane may be reordered, with every expected revision supplied.
- Claim and reorder use short transactions and revision predicates. A claimed or changed row returns `queue_order_conflict`; the client refreshes instead of overwriting.
- Every running mutation requires the current random lease token. Persist only its SHA-256 hash. Heartbeats extend leases; stale tokens cannot checkpoint, wait, retry, fail or complete.
- Scheduler owns a quiet lease keepalive for every actively running in-process Worker. Its bounded interval is derived from the claimed lease and adapts after renewal, so one provider call may safely exceed a nominal lease without relying on worker progress boundaries. The keepalive must stop and join before Wait, RetryLater, Fail, Complete, or interrupt acknowledgement releases the lease; a renewal failure cancels the Worker context and the stale execution must not persist further results. Lease-only renewals do not publish progress events. Explicit worker heartbeats remain responsible for progress metrics and provider polling checkpoints.
- Running pause/cancel first persists interrupt intent, then cancels the worker after commit. The active lease continues to consume capacity until the worker acknowledges or the lease expires.
- A non-running provider-backed download or seeding pause/cancel is never finalized directly. Control moves the Job to `queued` with durable `interrupt_status`; Scheduler claims it normally, calls the `InterruptibleWorker` before `Run`, and acknowledges the target state only after provider success. Provider failure clears the intent and safely continues the claimed work. Fake/non-provider Jobs keep direct control semantics.
- Download pipeline cancellation is provider-first and file-retaining: after UI confirmation the worker calls provider `Cancel(..., false)`, then marks the DownloadTask and related Jobs cancelled while preserving history. Provider task-not-found is idempotent success; provider failure restores/retains the prior local facts. Terminal-record DELETE defaults to the same `false` cleanup and removes local facts only afterward; `delete_data=true` is a separate explicit destructive opt-in.
- Cancellation may race an in-flight Submit. If the provider identity arrives after local cancellation, persist that identity and immediately remove the provider task with `deleteData=false` through a fresh bounded context. Cleanup failure remains a safe diagnostic on the cancelled history so deletion can retry it.
- Lease recovery preserves a pending provider interrupt by returning the Job to `queued` with the intent intact. It must not infer provider success from process loss. Any unanswered ActionRequest closed by a control intent stays closed so stale prompts cannot reappear on queued/paused/cancelled DTOs.
- `waiting_user_action`, `retry_wait` and `paused` hold no lease. Action responses require the current version and an allowlisted option.
- A feature page may reuse `POST /api/v1/jobs/:id/retry` for its own failed child Job when its allowlisted DTO exposes that Job ID/status. Labels must identify the exact stage (for example, retry download, retry import, or retry seeding) so a completed prerequisite is never accidentally rerun.
- Worker retry persists `next_attempt_at` and returns its slot. Do not sleep for rate limits or retry while holding capacity.
- Type capacity and optional resource-key capacity derive from unexpired running leases, so provider A cannot exhaust provider B's slots.
- `QueueService.Claim(nil)` may scan every policy for maintenance and deterministic service tests. `Scheduler` must therefore return without claiming when `WorkerRegistry.Types()` is empty; an empty runtime registry means no executable work, not "all job types". This keeps production jobs queued until their real adapters are registered.
- `media_artifact` payload contains only `artifact_run_id`. Its private run policy binds library/generation/output root and target kind; one coalescing key per library points the active Job at the newest queued generation, and an older running generation stops at the next file boundary without blocking watchers.

## Private state and public DTOs

- Payload and checkpoint are private typed-worker JSON objects, limited to 64 KiB and recursively rejected when keys imply authorization, cookies, passwords, secrets, tokens, passkeys, credentials, signed URLs or local/absolute paths.
- Public DTOs and timeline events are allowlists. Never return payload/checkpoint, lease hashes, signed URLs, arbitrary worker JSON or operating-system errors.
- Unknown progress, counts, speed and ETA remain `null`; UI renders “未知” and does not convert missing data to zero.
- User jobs have a real `owner_id`; system jobs use explicit `created_by_kind=system` and `owner_id=NULL`. Own-scope permissions never expose system jobs.

## Required verification

- Exercise concurrency caps, resource fairness, complete-lane reorder conflicts, stale leases, checkpoint persistence, action-version rejection, retry promotion, expired-lease recovery, coalescing generation and running interrupt acknowledgement.
- Exercise an empty Scheduler registry and assert that queued jobs remain queued with zero attempts. Also place more than 64 jobs for a capacity-blocked resource ahead of another runnable resource to prevent bounded candidate scans from reintroducing provider starvation.
- Verify operator grants read/control/respond/reorder; viewer receives no task-center permission; policy mutation remains administrator-only by default.
- Run repeated queue service tests, HTTP RBAC tests, Web UI test/typecheck/lint/build, embedded build and the Windows `server/test.ps1` gate.

## Scheduler registry example

Wrong:

```go
claimed, err := queue.Claim(registry.Types()) // empty slice means all policies
```

Correct:

```go
jobTypes := registry.Types()
if len(jobTypes) == 0 {
    return
}
claimed, err := queue.Claim(jobTypes)
```
