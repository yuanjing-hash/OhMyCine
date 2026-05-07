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