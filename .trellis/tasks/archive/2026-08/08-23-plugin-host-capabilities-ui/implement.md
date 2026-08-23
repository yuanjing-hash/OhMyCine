# Implementation Plan

- [x] Extend and test Plugin Manifest/SDK contracts for `media.metadata` and the bounded declarative `settingsPage` tree.
- [x] Expose validated settings schemas in installed-plugin summaries and add thin connection update APIs without exposing credentials.
- [x] Build the Server Web UI declarative plugin settings renderer and replace the raw JSON connection editor for plugins that declare a settings page.
- [x] Implement Bilibili's settings schema and metadata operation, including opaque poster/backdrop asset refs.
- [x] Persist plugin provenance and immutable provider metadata snapshots on download tasks; invoke metadata only through the owning plugin connection.
- [x] Generate provider NFO/JPG sidecars through Server-owned artifact helpers and attach them to the managed import manifest.
- [x] Add the general cloud UploadDriver and implement rate-limited 115 local-file upload using the existing SDK.
- [x] Route plugin local staging to 115 through TransferService, reusing target snapshot, directory, conflict, queue, progress, retry and audit behavior.
- [x] Update architecture, security, plugin SDK and official-plugin documentation.
- [x] Run focused tests, then full Server Go tests, Web UI checks, SDK contract checks and Bilibili Rust tests; perform a final cross-layer/security review.

## Validation

```powershell
cd server
go test ./...
go vet ./...
go build ./cmd/server
go build -tags webui ./cmd/server
go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 run

cd server/webui
npm test -- --run
npm run typecheck
npm run lint
npm run build

cd plugin-sdk
npm run typecheck
npm run verify

cd plugins/official/bilibili
cargo fmt -- --check
cargo test
cargo clippy --all-targets -- -D warnings
.\build.ps1
```

## Risk and Rollback Points

- Keep existing local and 115-native-offline transfer branches covered before adding the upload branch.
- Do not delete or rewrite existing download/transfer records; additive fields only.
- Preserve staging files on every upload ambiguity or reconciliation failure.
- Treat Manifest validation and the UI renderer allowlist as one contract; tests must fail when either side accepts an unknown component.
- Do not include the unrelated `.trellis/tasks/08-19-player-subtitle-danmaku-regressions/` directory in this task.
