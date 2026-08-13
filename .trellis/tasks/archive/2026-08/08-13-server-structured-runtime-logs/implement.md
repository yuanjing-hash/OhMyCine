# Server 结构化运行日志与日志中心 — Implementation Plan

## Preconditions

- Work only in the Server, Server Web UI, this task directory, relevant backend specs, and Server architecture docs.
- Preserve unrelated dirty Player files and other untracked Trellis tasks; never use broad staging.
- Before product edits, run `trellis-before-dev` and load the backend logging, security, API, database, Web administration, quality, directory and cross-layer guides selected by its routing.

## Ordered checklist

### 1. Lock contracts and tests first

- Add unit fixtures for sanitized structured events, URL credentials, signed proxy queries, Windows/UNC/Unix paths, oversized payloads and plugin reserved-field spoofing.
- Add rotation test seams for clock, filesystem operations and cleanup scheduling; avoid sleeps in tests.
- Define API request/response types, stable error codes, cursor/filter normalization and settings validation bounds.

### 2. Configuration, persistence and RBAC

- Extend bootstrap config with a canonical runtime log directory and safe environment overrides; ensure launcher defaults live under ignored `.runtime/logs`.
- Add the next explicit SQLite migration for the singleton runtime log policy and safe seed defaults.
- Add `logs.read`, `logs.export`, `logs.configure` to the canonical permission catalog and regenerate TypeScript constants.
- Grant only `logs.read` to the operator seed; do not grant log permissions to viewer.

### 3. Logging core

- Implement `internal/logging` manager, scoped logger factories, dynamic level, redaction/normalization and stdout + JSONL fan-out.
- Implement serialized size rotation, exact managed filename parsing, gzip worker, startup recovery, cleanup rules and graceful close.
- Implement rate-limited stdout-only degraded diagnostics and observable health state without recursion.
- Replace bootstrap logger construction and scope HTTP request logs; remove raw database absolute path from the startup event.

### 4. Query, facets, export and settings services

- Implement bounded newest-first JSONL/gzip reader with cancellation, scan/time/decompression/result limits and opaque filter-bound cursors.
- Implement facets including observed historical plugin IDs and an adapter boundary for future installed-plugin names.
- Implement policy read/update with transactionally coupled audit record, runtime apply and safe errors.
- Implement bounded streaming gzip export and an audit event containing only safe summary/count/size.

### 5. HTTP integration

- Add thin handlers and `/api/v1/runtime-logs*` routes with `NoStore` before authentication/permission checks.
- Enforce separate query/export/configure permissions and standard response envelopes except the explicit streamed export response.
- Add integration tests for authentication, RBAC, no-store on success/denial, filter validation, cursor tampering, range/scan limits, partial read warnings and no path leakage.

### 6. Web log center

- Add typed runtime-log API client/model helpers and `/logs/runtime` route metadata.
- Replace the planned runtime-log row in the topbar log center with a permission-filtered real link.
- Build bounded/paginated filters, URL synchronization, plugin facets, expandable fields, export action, policy panel and degraded/partial/error/empty states.
- Add component/service tests for permission visibility, filter serialization, stale request cancellation, cursor pagination, dangerous content rendering, large fields and white/dark/reduced-motion/responsive states.

### 7. Documentation and durable spec

- Update `docs/architecture/02-server-design.md`, `07-security-design.md`, `08-server-web-ui-design.md`, roadmap status and any runtime/start documentation affected by `OMC_LOG_DIR`.
- Upgrade `.trellis/spec/backend/logging-guidelines.md` from conventions to executable rotation/query/redaction/plugin contracts; update relevant indexes only if needed.

## Validation

From `server/`:

```powershell
go mod tidy
go mod verify
go test ./...
go vet ./...
go build ./cmd/server
go build -tags webui ./cmd/server
go list ./...
```

From `server/webui/`:

```powershell
npm run permissions:check
npm test
npm run typecheck
npm run lint
npm run build
go test .
go mod verify
```

Windows-native smoke:

- Start through `server/test.ps1` with an isolated runtime directory and database.
- Generate concurrent structured events until at least two rotations occur.
- Verify active JSONL, valid gzip history, 20 MiB default threshold tolerance, retention/count/500 MiB cleanup behavior and restart recovery.
- Exercise log query/filter/export through authenticated APIs and the embedded Web UI in light/dark themes.
- Confirm generated absolute paths, credentials and signed URLs do not appear with an `rg` scan over stdout capture, JSONL, gzip-expanded test output and API/export responses.
- Simulate an occupied/unwritable rotation target and verify stdout continuation plus non-crashing degraded state.

## Review gates

- Security review: redaction before fan-out, no arbitrary file read, cursor non-capability, plugin identity binding, no-store ordering and separate RBAC.
- Cross-layer review: catalog → generated permission → navigation/route/action → API middleware → service enforcement.
- Operational review: restart recovery, Windows locks, disk exhaustion, cleanup exactness, bounded goroutines and graceful shutdown.
- Dirty-tree review: stage and commit only exact Server/log-task/spec/docs files owned by this task.

## Rollback points

- Logging manager can fall back to stdout-only without database rollback.
- Query/UI routes can be disabled while keeping the policy row and retained local files.
- Never delete runtime logs or audit records as part of source rollback.
