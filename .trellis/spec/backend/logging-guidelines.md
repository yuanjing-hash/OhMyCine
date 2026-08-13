# Backend Logging Guidelines

> Executable contracts for OhMyCine Server runtime and audit logs.

## Scenario: Structured runtime logging and log center

### 1. Scope / Trigger

- Trigger: adding a Server log call, module, plugin logger, HTTP request logging, runtime-log file handling, log query/export/configuration API, or Web log-center UI.
- Runtime logs diagnose process and pipeline behavior. SQLite audit logs record security/configuration mutations. They are separate stores, permissions, and retention domains.

### 2. Signatures

- Logger: zerolog through the application-owned `internal/logging.Manager`; required fields are `timestamp`, `level`, `message`, `module`, and `component`.
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

### 5. Good/Base/Bad Cases

- Good: `logger.Info().Uint("library_id", id).Int("added", n).Msg("Media library reconciliation completed")`.
- Good: a cloud error records driver, stable error code, duration, and request ID without response body or signed URL.
- Base: an old gzip fragment is unreadable; the UI shows a partial result warning while other history remains queryable.
- Bad: logging a full connection config, raw `Location`, local Storage root, or request query for convenience.
- Bad: a plugin constructs its own file logger or chooses its `plugin_id` field.
- Bad: UI filters a broad server directory by accepting a filename/path from the browser.

### 6. Tests Required

- Unit tests cover nested sensitive keys, bearer text, credentialed/signed URLs, Windows/UNC/Unix paths, event size/depth bounds, plugin identity binding, concurrent rotation, gzip validity, retention, restart recovery, and degraded writes.
- API integration tests cover auth/RBAC separation, no-store ordering, filter/range limits, cursor tampering, partial reads, export audit, and absence of absolute paths/file names.
- Web tests cover filter serialization, permission-gated actions, stale requests, pagination, safe text rendering, large fields, responsive layout, and light/dark themes.
- Run Server Go test/vet/build, Web UI permission check/test/typecheck/lint/build, and a Windows-native isolated runtime smoke.
