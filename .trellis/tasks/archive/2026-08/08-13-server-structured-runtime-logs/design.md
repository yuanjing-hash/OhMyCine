# Server 结构化运行日志与日志中心 — Technical Design

## 1. Architecture and boundaries

The feature is split into four explicit layers:

```text
Server code / future plugin host
        ↓ scoped zerolog.Logger
redaction + normalization hook
        ↓
stdout sink + managed JSONL rotating sink
        ↓
bounded runtime-log query/export service
        ↓
/api/v1/runtime-logs/* + Web log center
```

Runtime logs remain append-only files. Audit logs remain SQLite records and are never fed into the runtime-log rotation manager. Task state, notifications, and metrics are not reconstructed from log files.

## 2. Package ownership

- `internal/logging`: logger manager, field taxonomy, redaction, JSON normalization, rotating file sink, gzip compression, retention cleanup, query reader, plugin-scoped logger factory, health/degraded state.
- `internal/config`: bootstrap-only physical log directory and safe environment overrides.
- `internal/models` + `internal/database`: singleton mutable runtime log policy. The absolute directory is not persisted here.
- `internal/services`: authorization-aware query, facets, export and policy update orchestration; policy changes and exports call `AuditService`.
- `internal/handlers` + `internal/httpserver`: thin request parsing, no-store responses and permission-gated routes.
- `server/webui`: runtime log view, filters, details, export and policy form.

No business package may open its own log file. It receives a scoped zerolog logger from the application composition root.

## 3. Bootstrap and policy lifecycle

1. `config.Load` resolves `OMC_LOG_DIR` against the process/server runtime context and validates it. The launcher defaults it under `server/.runtime/logs`; direct development defaults beside the configured database data directory rather than the Git source tree.
2. A logger manager starts with safe compiled defaults so database-open and migration failures still reach stdout and, when possible, the file sink.
3. After migrations, it loads the singleton policy row and atomically applies the effective level and rotation policy.
4. A policy API update validates bounds, commits the row and audit record in one database transaction, then applies the new in-memory policy. If application fails, the API reports a degraded/restart-safe state and the persisted policy is reloaded on the next start.
5. Shutdown flushes/closes the managed file sink without closing stdout.

Defaults:

| Setting | Default | Proposed safe range |
|---|---:|---:|
| level | `info` in production, `debug` in development | debug/info/warn/error |
| active file | 20 MiB | 1–256 MiB |
| compressed backups | 10 | 1–100 |
| retention | 30 days | 1–365 days |
| total size | 500 MiB | 32 MiB–10 GiB |

The effective total-size minimum must be at least the configured active-file limit. Invalid combinations are rejected rather than silently coerced.

## 4. File format, rotation and recovery

- Active file: `runtime.jsonl` with one valid JSON object per line.
- Historical files: application-owned timestamp + monotonic suffix names only; compressed history ends in `.jsonl.gz`.
- Rotate before a write would exceed the threshold. The writer serializes write/rotate transitions with a mutex; it never permits two goroutines to rotate the same file.
- Rename active file, immediately reopen a fresh active file, and compress the closed rotation asynchronously through a single bounded worker. A temporary suffix is used during compression and atomically renamed only on success.
- Startup recovery handles a final partial JSON line, uncompressed managed rotations, and stale compression temporary files without reading or deleting unrelated files.
- Cleanup runs after startup recovery, after rotation, after policy change, and on a bounded periodic ticker. Only exact managed filenames under the configured canonical directory are eligible.
- Retention considers compressed and recoverable uncompressed rotations. It removes oldest history until age, count and total-byte constraints all hold. The active file is never removed by retention.
- Windows sharing/rename failures leave the current file writable when possible and retry later with bounded backoff. Failure emits one rate-limited diagnostic to stdout and marks the sink degraded; it does not recursively log through the failed sink.

Use a small, well-maintained rotation dependency only if it satisfies Windows, compression and recovery contracts. Otherwise keep the custom surface limited to a zerolog `io.Writer`; total-cap cleanup and secure querying remain project-owned in either case.

## 5. Structured event contract and redaction

Required persisted fields:

```text
timestamp, level, message, module, component
```

Optional correlation fields use stable snake_case names:

```text
request_id, user_id, plugin_id, task_id, library_id, scan_run_id,
connection_id, storage_id, downloader_id, status, duration_ms
```

Logger constructors bind `module` and `component`; future plugin constructors also bind a canonical `plugin_id` supplied by the host. Plugin payload fields with reserved names are dropped/overwritten by the host binding.

Redaction occurs before the fan-out writer, so stdout, files, API responses and exports share the same sanitized event. The normalizer:

- matches sensitive keys case-insensitively and recursively within bounded object depth/count;
- strips credentials and sensitive query parameters from URL-like values;
- replaces local absolute Windows/UNC/Unix paths in path-designated fields with a safe relative/resource summary;
- bounds message, string, collection, object depth and total encoded event size;
- never records HTTP raw queries or bodies in the request middleware.

Callers must still avoid secret-bearing data. The central layer is defense in depth, not permission to log arbitrary configs.

## 6. Query and export contracts

Routes:

```text
GET   /api/v1/runtime-logs
GET   /api/v1/runtime-logs/facets
GET   /api/v1/runtime-logs/settings
PATCH /api/v1/runtime-logs/settings
POST  /api/v1/runtime-logs/export
```

Permissions:

- list/facets/settings read: `logs.read`
- export: `logs.export`
- settings update: `logs.configure`
- audit page remains `audit.read`

All runtime-log routes install `NoStore` before permission middleware. Filters include UTC time range, multi-level/module/component/plugin values, keyword and exact correlation IDs. Default range is 24 hours; maximum interactive range is 31 days. Keyword length is capped at 128 Unicode code points. Page size defaults to 100 and caps at 200.

The reader receives only the logger manager's canonical directory and enumerates only managed filenames. It scans newest-first with cancellation, decompression byte limits, wall-clock deadline and total scanned-byte budget. It parses complete JSONL records, skips and counts malformed lines, and returns an opaque cursor that is authenticated or validated against the normalized filter hash so clients cannot use it as a filename/path capability.

Facets are derived from bounded retained-log scanning and, when the plugin registry exists, enriched with installed plugin display names. Historical unknown/uninstalled `plugin_id` values remain selectable.

Export is a server-generated `.jsonl.gz` stream of the same sanitized query model. It has a shorter maximum range/explicit row and uncompressed-byte limits, uses no temporary world-readable file, and records one audit event containing actor, filter hash/summary, result count and output bytes. It never exposes managed filenames.

Partial unreadability returns successful safe data plus warnings/counts when some managed files remain readable. A wholly unavailable sink/query returns a stable application error without raw OS errors or paths.

## 7. Web UI

`/logs/runtime` is linked from the existing topbar log center only when `logs.read` is present. `/logs/audit` remains independent.

The page contains:

- compact filter bar with time preset/custom range, level, module, component, plugin and advanced correlation IDs;
- debounced keyword input and explicit apply/reset behavior;
- virtualized or bounded paginated result list showing time, level, module/component and message;
- expandable sanitized field detail using semantic `<dl>` markup;
- URL query synchronization for non-sensitive filters only;
- export action gated by `logs.export`;
- settings panel gated by `logs.configure`, with units, bounds, effective policy and degraded-state explanation.

Facet labels use observed IDs immediately. A future plugin catalog adapter can add display name and installation state without changing the query schema.

## 8. Compatibility, rollout and rollback

- Existing zerolog call sites continue compiling; the composition root begins passing scoped loggers incrementally, while request middleware receives the `http/request` scope immediately.
- Migration adds only a runtime policy table/row plus permission catalog entries. Existing `audit_logs` are unchanged.
- Operator receives `logs.read` by default; viewer receives none. Administrator continues to receive the complete catalog through existing administrator semantics. Export/configuration are not granted to operator by default.
- stdout remains available for external collection. JSON stdout in production and human console output in development remain supported, but both receive the same sanitized event fields.
- Rollback may stop using the file sink and UI while leaving the harmless policy row and permission entries. Runtime files are local operational data and are not deleted automatically during rollback.

## 9. Risks

- Querying compressed flat files is intentionally bounded and suited to a self-hosted log volume; it is not a general indexing engine.
- Windows antivirus or backup software may transiently lock rotations; retry/degraded behavior must be tested natively.
- Perfect free-text secret detection is impossible. Typed/scoped logging APIs, strict HTTP logging and tests with representative credentials are required alongside redaction.
- Future out-of-process plugins must send structured events through an authenticated host channel; this task defines the identity contract but does not implement the plugin runtime.
