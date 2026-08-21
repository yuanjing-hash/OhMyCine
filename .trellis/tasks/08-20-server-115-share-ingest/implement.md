# 115 分享转存与中转目录自动摄取实施计划

## 1. Schema and contracts

- [x] Add v26 additive migration, models and migration tests for library intake + adopted idempotency snapshots.
- [x] Add cloud share capability/types and downloader source/directory override contracts.
- [x] Add stable errors and `pan115_share_ingest` operation mapping/tests.

## 2. 115 provider and downloader

- [x] Implement bounded share link parsing, share snap and share receive in `pkg/cloud/pan115` with fake SDK tests.
- [x] Extend `pan115offline` for `115_share` and internal provider-item adoption using stable per-task folders.
- [x] Cover ambiguous success/retry adoption, invalid/expired link, root boundary, manifest and destructive cancel semantics.

## 3. MediaLibrary and Download orchestration

- [x] Add media-library intake validation, overlap rules, safe detail projection and source replacement/update behavior.
- [x] Add share-aware target selection and immutable staging provider directory snapshot.
- [x] Add internal adopted task enqueue with unique source key and existing Profile/target/seeding-safe snapshots.
- [x] Add debounced life-event/periodic intake sweep and composition wiring without adding a watcher queue worker.

## 4. HTTP and Web UI

- [x] Extend strict media-library payload and 115 directory picker flow for intake settings.
- [x] Extend strict download payload for `115_share`; reject internal source kinds.
- [x] Add media-library 115 intake controls and share source mode in Downloads UI.
- [x] Update TS contracts and focused frontend tests.

## 5. Documentation and verification

- [x] Update backend specs and Server architecture/roadmap with the implemented slice and metadata-sidecar follow-up boundary.
- [x] Run focused Go/provider/service/router tests.
- [x] Run `go test ./...`, `go vet ./...`, root/embedded builds and `go mod verify`.
- [x] Run Web UI permissions/test/typecheck/lint/build and nested Go verify/test.
- [x] Run `git diff --check` and `server/test.ps1`; confirm no leftover Server process.

## Risky files / rollback points

- `server/internal/services/download.go`: preserve existing qBittorrent/offline retry and downstream-stage semantics.
- `server/pkg/cloud/pan115/client.go`: never log raw share response/link and reuse shared risk-control lanes.
- `server/internal/services/media_library.go`: intake sweep must not become read-only scanner mutation logic.
- `server/internal/database/migrations.go`: v26 must be additive and idempotent.
- `server/webui/src/views/MediaLibrariesView.vue`: retain stale-request cancellation and current source picker behavior.
