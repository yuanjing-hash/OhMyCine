# Backend Logging Guidelines

> Executable contracts for OhMyCine Server runtime and audit logs.

## Scenario: Structured runtime logging and log center

### 1. Scope / Trigger

- Trigger: adding a Server log call, module, plugin logger, HTTP request logging, runtime-log file handling, log query/export/configuration API, or Web log-center UI.
- Runtime logs diagnose process and pipeline behavior. SQLite audit logs record security/configuration mutations. They are separate stores, permissions, and retention domains.

### 2. Signatures

- Logger: zerolog through the application-owned `internal/logging.Manager`; required fields are `timestamp`, `level`, `message`, `module`, and `component`.
- User-visible pipeline events additionally require stable `operation` plus localized `operation_label`. Messages start with the same `【operation_label】` so stdout, JSONL, export, and the Web UI remain readable without a separate lookup table.
- Optional stable fields: `request_id`, `user_id`, `plugin_id`, `task_id`, `library_id`, `scan_run_id`, `connection_id`, `storage_id`, `downloader_id`, `status`, `duration_ms`.
- Active file: `runtime.jsonl`; managed history uses application timestamp/sequence names ending in `.jsonl` or `.jsonl.gz`.
- Defaults: 20 MiB active file, 10 compressed backups, 30 days, 500 MiB total; meeting any limit removes oldest history.
- Permissions: `logs.read`, `logs.export`, `logs.configure`; audit remains `audit.read`.
- Routes live under `/api/v1/runtime-logs`; all install no-store before authentication and permission middleware.

### 3. Contracts

- Redact and normalize before stdout/file fan-out, so files, APIs, UI, and exports can never see a less-sanitized copy than stdout.
- Never log Authorization, cookies, passwords, API keys, passkeys, JWT/session tokens, signed proxy parameters, upstream CDN token URLs, raw request/response bodies, or local absolute paths.
- HTTP logs use normalized Gin routes, never raw query strings. Media/storage/scanner logs use provider-relative paths or stable resource IDs.
- No business module opens its own log file. Receive a scoped Logger from composition or a service constructor.
- Every implemented asynchronous business operation logs `start` and exactly one terminal `finish` or `fail` event. Retry, skip, fallback, user-wait, cancellation, and important state transitions are logged when they occur. Terminal events include safe counts, duration, stable error code, and applicable correlation IDs; heartbeat/progress loops do not emit noisy info logs.
- Runtime operation names are centralized in `internal/logging/operation.go`. Do not invent one-off labels or put a Chinese prefix only in free-form message text.
- 115 cloud imports use the stable operation `pan115_cloud_transfer` / `115云端整理`. Events may include internal task/library IDs, transfer mode, safe counts, duration, retry time and stable error code; they never include provider item IDs, provider paths, copy checkpoint contents or raw provider errors.
- 115 share receive and intake reconciliation use `pan115_share_ingest` / `115分享摄取`. Log one bounded sweep start and terminal event per library and safe task lifecycle transitions using only task/library/Connection IDs, counts, duration and stable error codes. Never log share URLs, extraction codes, provider item IDs, provider paths, Cookie, encrypted source blobs or raw upstream responses.
- Media recognition uses the stable operation `media_recognition` / `媒体识别`. Each scan batch logs one start and exactly one finish/fail with `library_id`, `scan_run_id`, unit/matched/unrecognized/cache-hit/recognition-failed counts, duration and a stable error code where applicable. Do not emit one info event per file or recognition rule.
- Recognition processor errors may include only a built-in pack code and source line plus the stable code (`invalid_rule`, `regex_compile`, `input_too_long`, `match_timeout`, `apply_limit`, `invalid_direct_hint`, or `context_canceled`). They never include the processed title, local/provider path, provider item ID, TMDB credential/query or raw upstream response.
- Every implemented HTTP API route maps through `OperationForHTTPRoute` to a user-visible business module. Adding a new route family requires adding its operation mapping and route-coverage test; falling back to generic `HTTP请求` is allowed only for unmatched framework/static routes.
- Configuration and security mutations remain audit events. Runtime logs describe execution; do not duplicate secret-bearing request/config payloads into runtime logs.
- Future plugin loggers bind canonical `plugin_id` in a host hook. Plugin payload cannot override another plugin identity.
- Rotation serializes writes, never removes the active file during retention, recognizes only exact managed names, and recovers uncompressed rotations after restart.
- File initialization/write/rotate/compress/cleanup failure keeps stdout alive, emits bounded stdout-only diagnostics, marks health degraded, and does not terminate Server.
- Query ranges, keywords, page size, decompression, scanned bytes, response size, and wall time are bounded. Cursors bind the normalized filter and never encode a usable file path.
- Export uses the same sanitized query model, has explicit limits, creates no world-readable temporary file, and records a safe audit summary. Routine queries are not audited.
- Runtime retention never deletes or rewrites SQLite audit events.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Sensitive key or URL query appears in event | Persist and return only `***redacted***` |
| Local absolute path is supplied as a path field | Persist only a safe redacted/relative summary |
| Log directory cannot be opened or later becomes unwritable | Continue stdout, set degraded health, do not crash |
| Windows temporarily locks a rotation target | Preserve writable state where possible and retry/degrade safely |
| Unknown/unmanaged file exists in log directory | Ignore it for query, compression, and cleanup |
| Query lacks `logs.read` | Return 403 with `Cache-Control: no-store` and no path/file metadata |
| Cursor belongs to another filter | Reject as an invalid filter |
| Some managed history is malformed/unreadable | Return bounded readable results with partial/malformed indicators |
| Plugin attempts to set a different `plugin_id` | Host-bound identity wins |
| 115 mutation fails or becomes ambiguous | Log only `task_id`, `library_id`, stable `error_code` and retry/terminal state; keep raw identities and responses private |
| 115 share/intake event wakes a sweep | Log the library/Connection correlation plus safe discovered/created/skipped counts; do not log child names or provider IDs |
| One media unit is unrecognized | Count it in the batch terminal event and persist its stable error code; do not log its source title/path at info level |
| Built-in word processing times out/fails | Log the stable processor code and aggregate failure count; never log the input string or regex-expanded source name |

### 5. Good/Base/Bad Cases

- Good: `OperationLibraryIncrementalScan.Event(logger.Info()).Uint("library_id", id).Int("added", n).Msg(OperationLibraryIncrementalScan.Message("完成"))`.
- Good: a cloud error records driver, stable error code, duration, and request ID without response body or signed URL.
- Good: `OperationPan115CloudTransfer` logs an `ask` conflict count and releases the worker without logging names or provider IDs.
- Good: `OperationMediaRecognition` logs a 30-unit batch with 24 matched, 4 unrecognized, 18 cache hits and 2 stable processing failures without naming any source file.
- Base: an old gzip fragment is unreadable; the UI shows a partial result warning while other history remains queryable.
- Bad: logging a full connection config, raw `Location`, local Storage root, or request query for convenience.
- Bad: a plugin constructs its own file logger or chooses its `plugin_id` field.
- Bad: UI filters a broad server directory by accepting a filename/path from the browser.

### 6. Tests Required

- Unit tests cover nested sensitive keys, bearer text, credentialed/signed URLs, Windows/UNC/Unix paths, event size/depth bounds, plugin identity binding, concurrent rotation, gzip validity, retention, restart recovery, and degraded writes.
- API integration tests cover auth/RBAC separation, no-store ordering, filter/range limits, cursor tampering, partial reads, export audit, and absence of absolute paths/file names.
- Web tests cover filter serialization, permission-gated actions, stale requests, pagination, safe text rendering, large fields, responsive layout, and light/dark themes.
- Business-operation tests cover operation code/label persistence, facet/filter behavior, start/finish/fail coverage for critical pipelines including `pan115_cloud_transfer`, provider-ID/path absence, and preservation through event-size compaction.
- Share-ingest tests cover `pan115_share_ingest` mapping, per-sweep start/terminal pairing, safe counters, and absence of links, receive codes, provider IDs/paths, Cookie and upstream bodies.
- Recognition tests cover `media_recognition` route/operation mapping, start/terminal pairing, batch counters, stable processor error codes, and absence of filenames, absolute/provider paths, provider IDs, credentials and upstream payloads.
- Run Server Go test/vet/build, Web UI permission check/test/typecheck/lint/build, and a Windows-native isolated runtime smoke.

### 7. Wrong vs Correct

Wrong:

```go
log.Error().Err(providerErr).Str("item_id", item.ID).Str("path", item.Path).Msg("115 move failed")
```

Correct:

```go
OperationPan115CloudTransfer.Event(log.Error()).
    Str("task_id", task.ID).
    Uint("library_id", task.LibraryID).
    Str("error_code", safeCode).
    Msg(OperationPan115CloudTransfer.Message("失败"))
```
