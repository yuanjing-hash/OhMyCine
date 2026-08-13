# Design: Persistent Task Queue and Task Center

## Boundaries

SQLite is the queue fact source. `QueueService` owns state transitions, ordering, leases, checkpoints, ActionRequests and authorization policy. `Scheduler` owns claim/capacity decisions. Typed workers implement isolated business handlers. Gin handlers remain request/response adapters; `/automation/tasks` consumes only REST/WebSocket contracts.

MediaLibrary supervisors remain outside this system. They never register queue workers or write queue rows.

## Persistence Model

- `jobs`: public ID, owner, type, priority, lane position, revision, status, safe display fields, resource keys, coalescing key/generation, progress/telemetry, due/retry/lease/checkpoint metadata, cancellation flag and timestamps.
- `job_attempts`: job, attempt number, lease token hash/reference, start/finish/status, safe error code and telemetry summary.
- `job_action_requests`: job, version, action type, safe prompt/options/preview, expiry, response and responding actor.
- `queue_policies`: per-type concurrency and optional resource limits with revision.

Payload/checkpoint storage is typed and private to the worker registry. API DTOs expose allowlisted summaries, never arbitrary JSON blobs.

## Lane Ordering

A lane is `(job_type, priority)`. Claim order is:

```text
priority policy -> lane_position ASC -> created_at ASC -> id ASC
```

Only queued jobs are reorderable. The reorder request contains lane identity, ordered job IDs, and expected revisions. One SQLite transaction verifies every row is still queued and in the lane, then assigns sparse monotonically increasing positions and bumps revisions. Claim and reorder serialize through short SQLite write transactions; if claim wins, reorder returns `queue_order_conflict`.

The UI must filter to one lane before enabling drag handles. Keyboard up/down actions use the same endpoint and contract. Other status filters are read-only.

## Claim, Lease and Recovery

Scheduler wakeups are event-driven plus a bounded ticker. A short transaction selects a due queued job whose type/resource capacity is available, atomically marks it running, creates an attempt and lease, then dispatches after commit. Heartbeats extend leases. Completion, retry, wait, pause and failure require the current lease token.

On startup and periodically, expired running leases are reclaimed to retry/failed according to policy. No worker sleeps while holding a slot for retry or rate limits.

## Action Requests

Workers return a structured wait result with checkpoint and versioned safe options. The service transaction stores the checkpoint/action, moves the job to `waiting_user_action`, closes the attempt and releases capacity. A response requires permission, current action version and an allowed option; it marks the action responded and requeues the job. The worker revalidates external state on resume.

## Concurrency and Coalescing

Capacity has two layers: global job type and resource keys such as provider/downloader/storage/library. Capacity accounting derives from active leases in SQLite plus the scheduler's current claim transaction; it must survive restarts.

Coalescing uses active uniqueness over type/resource/coalescing key and a dirty generation. A worker records its start generation. If completion observes a newer generation, the transaction preserves or creates one follow-up queued job.

## APIs and Events

Planned REST surface:

```text
GET  /api/v1/jobs
GET  /api/v1/jobs/:id
GET  /api/v1/jobs/:id/attempts
POST /api/v1/jobs/:id/pause
POST /api/v1/jobs/:id/resume
POST /api/v1/jobs/:id/cancel
POST /api/v1/jobs/:id/retry
POST /api/v1/jobs/:id/actions/:version/respond
PUT  /api/v1/job-lanes/:jobType/:priority/order
GET  /api/v1/queue/policies
PUT  /api/v1/queue/policies/:jobType
```

List endpoints are paginated and filterable. Stable WebSocket events include `job.created`, `job.status_changed`, `job.progress`, `job.action_required`, `job.order_changed` and `queue.policy_changed`; progress is throttled and every event is permission-scoped.

## Permissions

Add stable job permissions for read-all/read-own, control-all/control-own, action response and reorder/policy administration. MVP owner/admin receives all; operator receives global read/control/respond/reorder but not necessarily policy mutation; viewer receives none. Service policy remains authoritative.

## Task Center UI

`/automation/tasks` becomes implemented navigation. The page contains summary counts, saved-in-page filters, a lane-aware queue table, drag/keyboard ordering for queued items, status/telemetry cells, action-required emphasis, details/attempt timeline drawer, and control confirmations. It supports both Server themes and responsive non-drag keyboard controls.

## Failure and Rollback

- Unknown telemetry remains null.
- Worker errors persist only safe codes/messages.
- Queue migrations are additive and versioned.
- Scheduler startup failure stops queue execution but does not corrupt stored jobs or block unrelated management/media-library APIs.
- Feature rollback can disable Scheduler startup while retaining read-only queue records.
