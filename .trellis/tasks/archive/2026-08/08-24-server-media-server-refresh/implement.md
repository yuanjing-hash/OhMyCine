# Implementation Plan

## Phase 1 — Contracts and persistence

- [x] Add media-server interface and stable provider error types.
- [x] Add additive migration/models for content revision, media changes, refresh targets and runs.
- [x] Add migration, uniqueness, foreign-key and safe serialization tests.

## Phase 2 — Authoritative change producer

- [x] Implement transactional change/revision service and after-commit wake hub.
- [x] Integrate scan reconciliation and manual recognition mutation paths.
- [x] Integrate artifact completion/removal readiness barriers for 115+STRM and local policies.
- [x] Add partial/failed/superseded/no-op/generation-race tests across media-change and artifact suites.

## Phase 3 — Emby/Jellyfin adapters

- [x] Extend Emby with safe library enumeration and refresh.
- [x] Add Jellyfin Connection/provider envelope, adapter and tests.
- [x] Cover endpoint prefixes, auth differences, redirect rejection, bounded responses and stable errors.

## Phase 4 — Targets and persistent jobs

- [x] Implement target CRUD/test/list and revision CAS.
- [x] Register queue policy/worker and target-scoped coalescing.
- [x] Implement desired/successful revision reconciliation, restart recovery and retry classification.
- [x] Add failure-isolation, concurrent-generation and idempotency tests.

## Phase 5 — Management API and Web UI

- [x] Register routes/handlers with thin request parsing and service authorization.
- [x] Extend generated permission use and operation logging/audit.
- [x] Add typed API helpers and Player Management target cards/actions.
- [x] Verify generated permission gates, revision/error/Toast paths and both-theme semantic-token compliance in the existing Web UI suite.

## Phase 6 — Documentation and quality gate

- [x] Update backend specs and Server/Player/security/roadmap docs; no OpenAPI file exists yet.
- [x] Run focused Go tests throughout, then full Server/Web UI gates.
- [x] Run `git diff --check` and inspect safe DTO/log/Job persistence.

## Validation

```powershell
cd server
go test ./pkg/mediaserver/... ./internal/services ./internal/httpserver ./internal/database
go test ./...
go vet ./...
go build ./cmd/server
go build -tags webui ./cmd/server
cd webui
npm test -- --run
npm run typecheck
npm run lint
npm run build
cd ..\..
git diff --check
```

## Rollback points

- Migration is additive; route/worker registration can be disabled while leaving rows unused.
- Producer integration must remain behind one service boundary so it can be disabled without modifying media/artifacts.
- Target deletion/disable never calls deletion APIs on Emby/Jellyfin and never changes media files.

## 2026-08-26 independent acceptance

- Fixed artifact generation carry-forward so a newer no-op generation cannot discard an older pending ready change; partial generations never publish or supersede pending changes.
- Refresh target creation and re-enable reconcile to the latest ready revision, while terminal authentication/configuration failures stay failed until an explicit retry or configuration correction.
- Transient refresh failures honor the queue maximum attempts with bounded backoff and expose `retry_wait` truthfully.
- Added explicit target test/retry operations, joint UI permission gating, safe `CONNECTION_IN_USE` handling, v39 upgrade coverage, and metadata-only projection change coverage.
- Full `go test ./...`, `go vet ./...`, normal/WebUI-tag builds, WebUI 25-file/133-test suite, typecheck, ESLint, Vite build, and `git diff --check` passed in isolated Windows test directories.
