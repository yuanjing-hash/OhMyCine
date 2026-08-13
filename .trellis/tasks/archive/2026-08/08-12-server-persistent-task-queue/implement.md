# Implementation Plan

1. Add versioned queue migration, Job/Attempt/ActionRequest/Policy models, stable permissions/error codes and migration/RBAC tests.
2. Implement queue service state machine, safe DTOs, actor scoping, audit records and typed worker registry.
3. Implement transactional lane ordering, optimistic conflicts, sparse reindexing and claim-vs-reorder concurrency tests.
4. Implement Scheduler claim, type/resource capacity, leases/heartbeats, expired-lease recovery, retry scheduling, graceful shutdown and fake clock/worker tests.
5. Implement checkpointed ActionRequest wait/respond flow, slot release, stale response rejection and idempotent resume tests.
6. Implement coalescing generation and follow-up semantics; add explicit boundary tests proving MediaLibrary supervisors never create/consume queue work.
7. Add paginated/filterable REST APIs, queue policy/control/reorder endpoints and strict request/RBAC integration tests.
8. Add authenticated, permission-filtered and throttled WebSocket job events with REST recovery tests.
9. Implement `/automation/tasks` global task center, lane filters, drag plus keyboard ordering, task detail/attempt timeline, action response and safe controls in both themes.
10. Update architecture, security, roadmap, Web UI design and Trellis queue code-spec; run full Windows quality gate, embedded build, module verification, race/stress-focused tests and isolated browser acceptance.

## Validation Commands

```powershell
cd server
.\test.ps1
go test ./internal/services -run Queue -count=10
go test ./internal/httpserver -run Queue -count=5
go build -tags webui ./cmd/server
go mod verify

cd webui
npm run permissions:check
npm run test
npm run typecheck
npm run lint
npm run build
```

## Review Gates

- No queue transaction performs network calls or waits for rate tokens.
- Only queued same-lane jobs can reorder; claim/reorder races return stable conflicts.
- Waiting/retry/paused jobs hold no lease or capacity slot.
- Cancellation never implies deleting real files.
- Arbitrary payloads, credentials, absolute paths and token URLs never enter public DTOs/events/logs.
- MediaLibrary watcher/reconciliation remains independent of the queue.
