# Database Guidelines

> Database patterns and conventions for OhMyCine Server.

---

## Overview

SQLite is the default database for local development and self-hosted deployments. PostgreSQL may be mentioned only as a future optional deployment shape and must not drive MVP schema or query choices.

Backend persistence uses GORM. Database code must be written with SQLite compatibility as the primary target.

---

## Default Strategy

- Default database: SQLite.
- Default DSN in examples: local app data path or `./data/ohmycine.db`.
- Use PostgreSQL only when a task explicitly targets optional future deployment support.
- Avoid raw SQL that depends on PostgreSQL-only syntax unless a SQLite-compatible alternative exists.
- Keep schema names stable because Player/Server sync and CLI tooling will depend on them.

---

## Core Tables

The planned Server schema includes these domains:

- `connections`: external service configs, encrypted sensitive fields, status/quota metadata.
- `storage_destinations`: final local/cloud storage targets and STRM settings.
- `category_rules`: media classification, naming templates, transfer mode, match rules.
- `sites`: PT site configs, encrypted credentials, status/user info.
- `downloaders`: qBittorrent/Transmission configs and status.
- `download_tasks`: user-owned download records and client task references.
- `transfer_tasks`: source/target paths, transfer status, STRM/notification state.
- `follow_tasks`: user-owned TV follow tasks.
- `media`: local Server media records.
- `users`: authentication and permissions.
- `strm_schedules`: per-destination STRM cron configuration.
- `search_history` and `settings`.

---

## Model Rules

- Store timestamps with `created_at` and `updated_at` semantics.
- Use snake_case table and column names.
- Do not expose password hashes or encrypted secret blobs in JSON responses.
- Store flexible config as JSON text only after sensitive fields are encrypted.
- Use foreign keys for relationships where SQLite supports them.
- Keep transfer modes explicit: `move`, `hardlink`, `copy`, `symlink`.

---

## Transactions

Use a transaction when an operation changes related records or must be atomic:

- Creating a connection and initial status/settings.
- Creating storage destinations and STRM schedules.
- Creating a download task and associated discovery metadata.
- Marking transfer completion, updating media records, and recording notification state.
- Updating user permissions and related task visibility.

Do not hold database transactions while making slow external network calls. Persist intent, perform external work with `context.Context`, then persist result.

---

## Query Patterns

- Keep queries in services or repository-like helpers under `internal/`, not handlers.
- Always scope user-owned data by authenticated user unless the caller is admin.
- Paginate list endpoints that can grow large.
- For path-based file operations, store canonical paths and validate roots in service code before writing records.
- For task polling, update only changed fields to reduce unnecessary writes.

---

## Migrations

- MVP may use GORM `AutoMigrate`, but schema changes must still be reviewed for destructive behavior.
- Never drop or rename columns automatically without an explicit migration plan.
- Add indexes for common filters: media type/year/rating, task status, owner user ID, destination ID.
- Seed only safe defaults. The initial admin account must require password change or explicit secure configuration.

---

## Sensitive Data Storage

- Credentials in `connections`, `sites`, `downloaders`, and settings must be encrypted at rest using AES-GCM or an equivalent approved mechanism.
- Master keys come from environment/secret file or a generated local key file, never from hard-coded constants.
- Configuration export is redacted by default. Full credential export requires explicit user confirmation.

---

## Common Mistakes

- Changing the default target from SQLite to PostgreSQL.
- Returning encrypted config blobs or password hashes to clients.
- Performing network calls inside a DB transaction.
- Auto-deleting STRM or media records without dry-run/confirmation where required.
- Forgetting user scoping for download and follow tasks.

## Scenario: Windows-native Server SQLite runtime

### 1. Scope / Trigger

- Trigger: Server database bootstrap, local Windows startup/test scripts, SQLite dependency upgrades, or tests that create file-backed SQLite databases.
- The Windows-native Server must build and run without Docker, WSL, CGO, GCC, Clang, or MSVC C compilation.

### 2. Signatures

- Database entry point: `database.Open(path string) (*gorm.DB, error)`.
- Windows launch: `server/start.ps1 [-SkipBuild]`.
- Windows quality gate: `server/test.ps1 [-CheckDependenciesOnly] [-SkipWebUi] [-SkipGoQuality] [-SkipHealthCheck]`.
- System Go bootstrap package: exact winget ID `GoLang.Go`.

### 3. Contracts

- Keep GORM as the ORM and `gorm.io/driver/sqlite` as the dialect layer, but register the pure-Go `modernc.org/sqlite` database/sql driver with `DriverName: "sqlite"`.
- Build and test Server with `CGO_ENABLED=0`.
- File-backed databases enable `foreign_keys(1)`, `busy_timeout(5000)`, `journal_mode(WAL)`, and `_txlock=immediate` through the modernc DSN. In-memory tests enable foreign keys, busy timeout, and the same immediate transaction mode without WAL. Immediate write reservation prevents a GORM read-then-write transaction from failing instantly with `SQLITE_BUSY_SNAPSHOT` when a scheduler write commits between its reads and first mutation; normal writer contention still waits under the bounded busy timeout.
- Windows persistent runtime state defaults to `server/.runtime/windows/{bin,data,logs}`. Test databases and process output use unique children of `server/.runtime/windows/tests/`.
- If compatible Go is absent, `start.ps1` installs the official system package with `winget install --id GoLang.Go --exact`; it must not download a repository-local portable Go or install a C compiler.
- Default Server binding remains `127.0.0.1`; tests use a dynamically allocated loopback port.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Empty database path | Return `database path is required`; do not create files. |
| Parent directory missing | Create it with owner-restricted intent before opening SQLite. |
| Go absent and winget present | Run the exact official `GoLang.Go` install flow, refresh the current process PATH, then revalidate the minimum `go.mod` version. |
| Go absent and winget absent | Fail with an actionable installation message; do not silently download another toolchain. |
| Test cleanup target equals the tests root or is a sibling | Reject cleanup and retain data. |
| Health probe/build fails | Exit non-zero and print the unique retained diagnostics directory. |
| SQLite connection remains open at test cleanup | Treat as a test bug; explicitly close the underlying `*sql.DB`. |
| A background scheduler writes while an API transaction reads and then mutates | Reserve the writer at transaction start and wait under `busy_timeout`; never surface the upgrade race as an immediate HTTP 500. |

### 5. Good/Base/Bad Cases

- Good: `CGO_ENABLED=0 go test ./...` passes on Windows, the embedded EXE answers `/api/v1/health`, and its unique successful test directory is removed.
- Base: an existing compatible system/PATH Go and unchanged `package-lock.json` are reused without reinstalling Go or frontend dependencies.
- Bad: switching back to `go-sqlite3`, requiring a C compiler, sharing the persistent database with health tests, or deleting an unverified path after a failure.

### 6. Tests Required

- Database tests assert `PRAGMA foreign_keys = 1`, `PRAGMA journal_mode = wal`, and that a read-then-write transaction retains its snapshot while a competing writer waits and resumes after commit.
- HTTP integration tests close the GORM underlying `*sql.DB` so Windows can remove `t.TempDir()` files.
- PowerShell contract test asserts the exact winget arguments and rejects cleanup of the tests root and sibling paths.
- Full `server/test.ps1` runs frontend permission/test/typecheck/lint/build, `CGO_ENABLED=0` Go test/vet/build, and a real isolated health probe.
- `git check-ignore` must cover Windows EXE, DB/WAL/SHM, logs, unique test data, `webui/node_modules`, and `webui/dist`.

### 7. Wrong vs Correct

#### Wrong

```go
db, err := gorm.Open(sqlite.Open(dsn), config) // default go-sqlite3 requires CGO
```

#### Correct

```go
import _ "modernc.org/sqlite"

db, err := gorm.Open(sqlite.New(sqlite.Config{
    DriverName: "sqlite",
    DSN:        dsn,
}), config)
```

---

## Scenario: v25 Media Recognition Persistence

### 1. Scope / Trigger

- Trigger: changing media-library recognition units, recognition cache, entry/catalog match projections, scan recognition counters, Profile built-in pack selection, or source-replacement cleanup.
- The recognition store is a derived read model over source facts. It never authorizes or performs a source file mutation.

### 2. Signatures

```text
media_classification_profiles.builtin_recognition_packs_json
download_tasks.profile_builtin_recognition_packs_json

media_library_recognitions
  UNIQUE(library_id, source_key)
  profile_id, profile_revision, status, error_code
  media_type, title, release_year, tmdb_id, confidence
  category_name, matched_rule_id, metadata_json, manual_override
  input_fingerprint, last_generation

media_recognition_cache
  lookup_key PRIMARY KEY, status, error_code, result_json, expires_at

media_library_entries
  recognition_id, tmdb_id, release_year, match_confidence, recognition_error_code

media_library_scan_runs
  matched, unrecognized, cache_hits, recognition_failed
```

### 3. Contracts

- v25 is additive. It creates new tables/indexes and adds nullable/defaulted columns; it does not delete or reinterpret a v24 entry as a successful TMDB match.
- `media_library_recognitions` cascades with its library. Entry `recognition_id` may be null during migration/reconciliation and is cleared safely when a recognition disappears.
- Source replacement deletes entries, recognitions (including manual overrides), and scan runs inside the same transaction that saves the new source identity and resets generations/timestamps.
- TMDB and provider calls happen before the short commit transaction. The commit reloads the library/Profile and verifies source identity, Profile revision and dirty generation before upserting projections.
- Complete reconciliation deletes proven-missing entries and orphan recognitions. Partial enumeration preserves unseen entries and recognitions.
- `media_recognition_cache.result_json` contains only canonical credential-free result fields. Its SHA-256 key binds input fingerprint, Profile ID/revision, language and region; no path, provider ID, URL, credential or upstream body is stored.
- Profile `builtin_recognition_packs_json` defaults to `["tv-v1","anime-v1"]`; an explicit `[]` remains distinct and must survive reads, copies and download snapshots.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Fresh database | Create all v25 tables, columns, indexes, defaults and cascades exactly once |
| v24 database with entries | Preserve entries as pending facts; create no fabricated matched recognition |
| Migration runs repeatedly | Remain idempotent with one schema version and no duplicate seed/data rewrite |
| Source identity changes | Atomically remove old entries/recognitions/overrides/runs and reset scan state |
| Source/Profile/generation changes during lookup | Abort stale projection commit; preserve the newer configuration |
| Enumeration is partial | Do not delete unseen entry or recognition rows |
| Cache result contains non-canonical/private data | Do not persist it; tests must reject observable path/provider/credential leakage |

### 5. Good / Base / Bad Cases

- Good: enumerate and recognize without a transaction, then commit only after verifying the same source generation and Profile revision.
- Good: an unchanged second scan reads a non-expired cache projection and performs no TMDB request.
- Base: v24 entries remain queryable until the next reconciliation creates and links their v25 recognition rows.
- Bad: hold a SQLite transaction open across TMDB, delete unseen rows after a partial 115 page, or leave a manual override attached after the library root changes.

### 6. Tests Required

- Fresh v25, v24-to-v25 and repeated migration tests assert every table, column, index, default and foreign-key action.
- Source-replacement tests pre-populate entries, recognitions, manual overrides and scan runs, then assert same-transaction cleanup and zeroed generation/timestamps.
- Reconciliation tests assert stale source/Profile/generation rejection, full deletion, partial preservation, stable-provider rename convergence and cache reuse.
- Cache tests assert positive/no-match/network TTL classes and no durable cache for missing credentials/authentication failures.

### 7. Wrong vs Correct

#### Wrong

```go
return db.Transaction(func(tx *gorm.DB) error {
    match, err := tmdb.Search(ctx, title) // slow network while holding SQLite writer
    if err != nil { return err }
    return tx.Save(&match).Error
})
```

#### Correct

```go
match := recognizer.Recognize(ctx, facts, profileSnapshot) // outside transaction
return db.Transaction(func(tx *gorm.DB) error {
    if err := verifyCurrentSourceProfileGeneration(tx, snapshot); err != nil {
        return err
    }
    return upsertRecognitionProjection(tx, match)
})
```

## Scenario: v26 115 Share Intake Persistence

### 1. Scope / Trigger

- Trigger: changing 115 media-library intake configuration, share/adopted download source snapshots, or intake deduplication.

### 2. Signatures

```text
media_libraries:
  ingest_enabled BOOLEAN NOT NULL DEFAULT false
  ingest_downloader_id TEXT NULL
  ingest_owner_id INTEGER NULL
  ingest_provider_root_id TEXT NOT NULL DEFAULT ''
  ingest_relative_root TEXT NOT NULL DEFAULT ''

download_tasks:
  staging_provider_directory_id TEXT NOT NULL DEFAULT ''
  ingest_source_key TEXT NOT NULL DEFAULT ''
  source_origin TEXT NOT NULL DEFAULT 'user'

CREATE UNIQUE INDEX idx_download_tasks_ingest_source_key
ON download_tasks(ingest_source_key)
WHERE ingest_source_key <> '';
```

### 3. Contracts

- v26 is additive and idempotent. Existing libraries remain intake-disabled and existing tasks remain `source_origin=user`.
- The stable provider intake directory, adopted provider item and share source remain private. Only the safe Storage-relative intake display path and configured downloader reference may appear in the administrative DTO.
- `ingest_source_key` is a SHA-256 identity derived from Connection, library and provider item. Preflight lookup is an optimization; the partial unique index is the concurrent sweep authority.
- Provider network calls occur before/after short transactions, never while holding the SQLite writer. Job creation and DownloadTask insertion remain one transaction.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Fresh, v25 upgrade, or repeated migration | Produce the same v26 columns/indexes once without rewriting existing rows |
| Two sweeps insert the same non-empty ingest key | One succeeds; the other maps the unique conflict to an idempotent no-op |
| Ordinary user/share task has an empty ingest key | Partial index permits it; no false uniqueness conflict |
| Intake is disabled | Clear downloader/owner/provider-root/display-path fields atomically |

### 5. Good / Base / Bad Cases

- Good: one provider child observed by event and periodic sweep yields one DownloadTask despite concurrent insertion.
- Base: a pre-v26 task upgrades as `source_origin=user` with empty staging-provider and ingest-key fields.
- Bad: rely only on `COUNT` before insert, store a raw provider item ID as the ingest key, or hold a transaction across 115 API calls.

### 6. Tests Required

- Fresh v26, v25-to-v26 and repeated migration tests assert every column/default plus both intake lookup indexes and the partial unique predicate.
- Concurrency/idempotency tests assert one adopted task for the same identity and multiple ordinary tasks with empty keys.
- Raw-row tests assert share URLs, receive codes and provider item IDs are absent outside encrypted source ciphertext/private provider snapshot fields.

### 7. Wrong vs Correct

#### Wrong

```go
if count == 0 { db.Create(&task) } // event and timer can race
task.IngestSourceKey = providerItemID
```

#### Correct

```go
task.IngestSourceKey = sha256Hex(connectionID, libraryID, providerItemID)
err := tx.Create(&task).Error // partial unique index is authoritative
if isIngestUniqueConflict(err) { return nil }
```
