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
- File-backed databases enable `foreign_keys(1)`, `busy_timeout(5000)`, and `journal_mode(WAL)` through modernc `_pragma` DSN parameters. In-memory tests enable foreign keys and busy timeout without WAL.
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

### 5. Good/Base/Bad Cases

- Good: `CGO_ENABLED=0 go test ./...` passes on Windows, the embedded EXE answers `/api/v1/health`, and its unique successful test directory is removed.
- Base: an existing compatible system/PATH Go and unchanged `package-lock.json` are reused without reinstalling Go or frontend dependencies.
- Bad: switching back to `go-sqlite3`, requiring a C compiler, sharing the persistent database with health tests, or deleting an unverified path after a failure.

### 6. Tests Required

- Database test asserts `PRAGMA foreign_keys = 1` and `PRAGMA journal_mode = wal` on a file-backed database.
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
