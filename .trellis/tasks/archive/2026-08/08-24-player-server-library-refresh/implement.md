# Implementation Plan

## Phase 1 — Player change API

- [x] Add safe change DTO/query service over the parent outbox contract.
- [x] Add bounded waiter hub, heartbeat, cancellation and post-wake revalidation.
- [x] Register Bearer-only route with no-store and strict cursor/wait validation.
- [x] Add auth, permission, revocation, retention/resync and leak tests.

## Phase 2 — Player controller

- [x] Add runtime-validated change response types.
- [x] Implement one cancellable long-poll loop per enabled ServerDataSource.
- [x] Add source-scoped cursor persistence, retry/backoff and old-Server capability handling.
- [x] Wire lifecycle to config load/change/disable/remove and app teardown.

## Phase 3 — Cache invalidation and UX

- [x] Add source/library revision invalidation and coalesced background Home refresh.
- [x] Add typed local change event/state for affected Server views.
- [x] Add “媒体库已更新” prompt and explicit current-level refresh.
- [x] Preserve scroll/selection/navigation; reject stale request application.
- [x] Prove active playback and unrelated DataSources remain untouched by source-scoped invalidation.

## Phase 4 — Verification and docs

- [x] Add Server router/service and Player verification coverage.
- [x] Update frontend/backend specs and Player/server/security/roadmap docs.
- [x] Run Server focused/full gates and Player verify/typecheck/lint/build; Rust/Cargo and profile smoke are not applicable because this slice changes no Tauri/Rust code or profile schema.

## Validation

```powershell
cd server
go test ./internal/services ./internal/httpserver
go test ./...
go vet ./...
cd ..\player
npm run verify:server-datasource
npm run typecheck
npm run lint
npm run build
cd src-tauri
cargo test
cargo clippy --all-targets --all-features -- -D warnings
cd ..\..
git diff --check
```

## Rollback points

- Disable controller startup to return to existing TTL/manual refresh without altering credentials or content.
- Remove route registration while leaving additive change history for media-server refresh use.
- Never recover by clearing normal/portable Player profiles, removing ServerDataSource credentials or resetting media libraries.

## 2026-08-26 independent acceptance

- Replaced the single-consumer wake channel with a broadcast generation channel and re-authenticate the device/user after every long-poll wake or timeout before filtering currently visible enabled libraries.
- Player now preserves HTTP status, stops on `401/403/404`, validates bounded monotonic cursor/change DTOs, isolates cursors by source and Server origin, and uses cancellable bounded jittered retry for transient failures.
- Pending updates are merged by Server source, library and content revision; Home refresh is coalesced, current unrelated libraries are not prompted, and explicit refresh preserves scroll, focus, navigation and playback context.
- Added revocation, user/storage disablement, multiple waiter, retention/resync, malformed/overflow/stale cursor, source lifecycle and cache-isolation regression coverage.
- Server full tests/vet/builds and Player ServerDataSource/online-library/navigation/Home/playback verification, typecheck, ESLint, production build and `git diff --check` passed. `verify:view-architecture` still reports only the pre-existing oversized `SettingsView.vue`, which this task did not modify.
