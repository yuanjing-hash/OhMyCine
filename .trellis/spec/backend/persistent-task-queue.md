# Persistent Task Queue

## Scope

Use this contract for Server jobs that perform discrete, restart-safe automation work such as download, transfer, upload, scrape and media-server refresh. MediaLibrary watcher, scheduled reconciliation, full/incremental scans and STRM projection remain supervisor work and must never register a queue worker or consume a queue slot.

## Durable contracts

- SQLite is authoritative for job state, lane order, attempts, timeline events, actions, policies, checkpoints and leases. Memory coordinates goroutines only.
- A scheduling lane is exactly `(job_type, priority)`. Only the complete set of `queued` rows in one lane may be reordered, with every expected revision supplied.
- Claim and reorder use short transactions and revision predicates. A claimed or changed row returns `queue_order_conflict`; the client refreshes instead of overwriting.
- Every running mutation requires the current random lease token. Persist only its SHA-256 hash. Heartbeats extend leases; stale tokens cannot checkpoint, wait, retry, fail or complete.
- Running pause/cancel first persists interrupt intent, then cancels the worker after commit. The active lease continues to consume capacity until the worker acknowledges or the lease expires.
- `waiting_user_action`, `retry_wait` and `paused` hold no lease. Action responses require the current version and an allowlisted option.
- Worker retry persists `next_attempt_at` and returns its slot. Do not sleep for rate limits or retry while holding capacity.
- Type capacity and optional resource-key capacity derive from unexpired running leases, so provider A cannot exhaust provider B's slots.
- `QueueService.Claim(nil)` may scan every policy for maintenance and deterministic service tests. `Scheduler` must therefore return without claiming when `WorkerRegistry.Types()` is empty; an empty runtime registry means no executable work, not "all job types". This keeps production jobs queued until their real adapters are registered.

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
