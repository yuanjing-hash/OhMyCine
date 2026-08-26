# Parent Implementation Plan

## Task order

1. Complete and verify `08-24-server-media-server-refresh`.
2. Complete and verify `08-24-player-server-library-refresh` against the frozen change contract.
3. Run the parent cross-child end-to-end and security review.

## Cross-child contract gate

- [x] Freeze `MediaLibraryChange` readiness, revision, cursor and safe DTO semantics before either child changes consumers.
- [x] Confirm local, 115+STRM, metadata correction and removal producer barriers use committed authoritative state.
- [x] Confirm media-server and Player consumers advance independently from the same ready revision.
- [x] Confirm outbox retention and `resync_required` behavior cannot lose eventual convergence.

## Cross-child integration

- [x] Wire ready change wake-ups to both refresh-job scheduling and Player long-poll waiters only after commit.
- [x] Verify repeated scans/imports advance desired revisions without one external refresh/reload per file.
- [x] Verify target failure, Player offline state and Server restart remain isolated and recoverable.
- [x] Verify Player refresh UX preserves active playback, current list scroll/selection and unrelated DataSources.

## Documentation and specification

- [x] Update Server and Player architecture/security/roadmap status without removing later PT, follow, sync or provider scope.
- [x] Add executable backend and frontend spec contracts for the final notification boundary.
- [x] Confirm no `api/openapi.yaml` exists, so no OpenAPI artifact requires updating.

## Parent validation gate

```powershell
cd server
go test ./...
go vet ./...
go build ./cmd/server
go build -tags webui ./cmd/server
cd webui
npm test -- --run
npm run typecheck
npm run lint
npm run build
cd ..\..\player
npm run typecheck
npm run lint
npm run build
npm run verify:server-datasource
cd src-tauri
cargo test
cargo clippy --all-targets --all-features -- -D warnings
cd ..\..
git diff --check
```

- [ ] Run a Windows isolated-profile Server + Player smoke test; do not touch the owner’s normal or portable Player profiles.
- [x] Exercise one local watcher reconciliation and one fake/isolated 115+STRM flow through media-server refresh and Player update.
- [x] Inspect REST/events/logs/audit/SQLite safe columns for token, credential, absolute path, provider ID and signed/upstream URL leaks.
- [x] Confirm all test processes and isolated runtime directories are cleaned up without touching `server/.tmp` owned by another task.

## Rollback points

- Child 1 can be disabled at route/worker registration while leaving additive schema unused.
- Child 2 can be disabled at Player notification-controller startup while retaining normal TTL/manual refresh.
- Never roll back by deleting media, STRM artifacts, user profiles, credentials or existing Connection records.

## Completion criteria

- Both child acceptance suites pass.
- Parent PRD acceptance criteria pass end to end.
- Final spec/roadmap status matches actual behavior.
- A fresh user approval is required before activating either child and starting implementation.

## 2026-08-26 parent acceptance

- Both child tasks were independently implemented, checked, committed and archived before the parent review.
- Added parent-level local watcher catalog/removal and fake 115 pending-to-ready artifact regressions, proving the same committed revision advances media-server target state and broadcasts to Player waiters only after the readiness barrier.
- Full Server tests/vet/builds, WebUI 133 tests/typecheck/ESLint/build, Player ServerDataSource and related verification/typecheck/ESLint/build, Rust 92 tests, Clippy `-D warnings`, and `git diff --check` passed.
- Automated fakes cover Emby, Jellyfin, multiple Player waiters, revocation, user/storage disablement, retention/resync, batch coalescing and retry isolation. Real qBittorrent/115 credentials, real Emby/Jellyfin endpoints and multiple physical Player GUIs remain operator smoke-test items because this isolated run intentionally did not use the owner's accounts or profiles.
