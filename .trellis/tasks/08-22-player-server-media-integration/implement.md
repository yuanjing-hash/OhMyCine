# Implementation plan

## 1. Server device authentication foundation

- Add migration v32 and `DeviceToken` model/indexes.
- Extend Server auth/config with bounded device-token issue, authenticate, touch, list and revoke operations.
- Reuse password verification/rate limiting without creating a browser Cookie session.
- Revoke device tokens with password reset, user disable/delete and explicit logout.
- Add strict Bearer middleware and keep it out of the browser management route group.
- Add handlers/routes for Player login/logout/bootstrap/device list/revoke.
- Add audit and module logs with redaction tests.

Validation checkpoint:

```powershell
cd server
$env:CGO_ENABLED='0'
go test ./internal/database ./internal/services ./internal/httpserver
```

## 2. Server Player catalog and Emby identity summaries

- Add Player-specific DTO/service methods over existing catalog and recognition snapshot data.
- Add safe Server media-library/catalog/detail/search handlers.
- Expose safe Emby instance fingerprints from enabled tested Connections; extend the Emby client only where safe identity/library summaries are required.
- Add safe artwork projection without exposing credentials or physical paths.
- Add pagination, empty-state and permission tests.

Validation checkpoint:

```powershell
cd server
$env:CGO_ENABLED='0'
go test ./internal/services ./internal/handlers ./internal/httpserver ./pkg/mediaserver/emby
```

## 3. Server authenticated 115 direct stream

- Resolve a requested media entry to its active managed completed STRM artifact by `SourceIdentity`.
- Repeat actor/library/storage/artifact validation before calling `ResolveArtifactForClient`.
- Add GET/HEAD stream handler returning 302 with no-store and safe errors.
- Add tests for wrong library, disabled library, inactive/unmanaged/non-STRM artifact, non-115 storage, permission denial and valid redirect.
- Add a cross-origin test proving Player Authorization is never present at the final CDN when exercised through the native Player bridge in step 5.

Validation checkpoint:

```powershell
cd server
$env:CGO_ENABLED='0'
go test ./internal/services ./internal/httpserver -run 'Player|Device|Direct|Proxy'
```

## 4. Player Server connection and ServerDataSource

- Add the Server credential envelope and safe config sanitization.
- Add a bounded/redacted native Server JSON command and register it in Tauri.
- Add Server to the existing source option/edit/login flow.
- Implement `ServerDataSource` listLibraries/list/getHomeSections/search/getDetail/getStreamRequest and safe error handling.
- Register the source factory and preserve independent behavior when disconnected.
- Add settings/data-source unit or static regression tests.

Validation checkpoint:

```powershell
cd player
npm run typecheck
npm run lint
cargo check --manifest-path src-tauri/Cargo.toml --all-targets
```

## 5. Emby fingerprint, exact identity and cross-source aggregation

- Capture the normalized Emby instance fingerprint after successful Player Emby authentication/test.
- Add typed work/exact identity and alternate playback-target fields without placing credentials or full paths in `MediaItem`.
- Parse trusted OhMyCine STRM artifact identity transiently from Emby media sources.
- Implement one shared merge helper for aggregate home/search/continue preparation; Server wins default, versions and Emby targets remain available.
- Keep source-specific Emby library pages unmerged.
- Wire detail version/line selection to delegate to the selected configured DataSource.
- Add tests for same/different SystemId, same-name libraries, exact artifact merge, TMDB work grouping, version preservation and uncertain non-merge.

Validation checkpoint:

```powershell
cd player
npm run typecheck
npm run lint
npm run build
cargo test --manifest-path src-tauri/Cargo.toml --lib
cargo clippy --manifest-path src-tauri/Cargo.toml --all-targets -- -D warnings
```

## 6. Cross-layer verification and documentation

- Start an isolated Server database/profile; never touch the owner's normal Player profile.
- Exercise first login → token restore → bootstrap → library/detail → 115 stream redirect.
- Exercise Player direct Emby + Server source against the same fixture `SystemId` and assert one aggregate card plus two preserved targets.
- Exercise a different `SystemId` with the same name and assert no false merge.
- Exercise revoked token and prove subsequent catalog/stream requests fail.
- Verify cross-origin redirect strips Server Authorization at the final upstream.
- Update `server/api/openapi.yaml` if present and update architecture/roadmap status consistently.
- Run `git diff --check` and review all changed DTO/log output for secrets and absolute paths.

Final commands:

```powershell
cd server
$env:CGO_ENABLED='0'
go test ./...
go vet ./...

cd ..\player
npm run typecheck
npm run lint
npm run build
cargo test --manifest-path src-tauri/Cargo.toml --lib
cargo clippy --manifest-path src-tauri/Cargo.toml --all-targets -- -D warnings

cd ..
git diff --check
```

## Risky files and rollback points

- `server/internal/middleware/context.go` and `server/internal/httpserver/router.go`: never broaden Bearer acceptance to management routes.
- `server/internal/services/auth.go` / admin user mutation paths: device revocation must remain transactional with password/user state changes.
- `server/internal/services/signed_proxy.go`: reuse internal resolver; do not weaken signed STRM validation or log upstream URLs.
- `player/src/services/datasource/types.ts`: identity fields affect every source; keep them optional and non-breaking.
- `player/src/services/datasource/homeAggregation.ts`, `searchAggregation.ts`, and continue-watching merge: do not mutate source-specific data or discard versions.
- `player/src-tauri` redirect handling: a failure can leak Bearer credentials cross-origin; the real redirect test is a release blocker.
- Preserve unrelated dirty files under `player/src-tauri/gen/schemas/` and `.trellis/tasks/08-19-player-subtitle-danmaku-regressions/`.
