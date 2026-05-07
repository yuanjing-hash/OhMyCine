# Backend Quality Guidelines

> Code quality standards for Go backend development in OhMyCine.

---

## Overview

Backend code should be simple, testable, and secure. Implement only the task requirements; preserve planned product scope in documentation and do not over-engineer future capabilities before their task exists.

---

## Required Patterns

- Use Go 1.22+.
- Use Gin for HTTP routing when implementing Server APIs.
- Use GORM for persistence.
- Use SQLite-compatible schema and queries by default.
- Use `context.Context` in external calls and long-running services.
- Keep handlers thin and services responsible for business logic.
- Use interfaces for cloud drivers, media-server clients, downloader clients, PT sites, and metadata providers.
- Use transactions for related database writes.
- Use structured zerolog logging with redaction.
- Validate and canonicalize local paths before file operations.
- Preserve Player independent-first and Server enhancement-layer boundaries.

---

## Forbidden Patterns

- Requiring Docker for local development.
- Making Player basic playback depend on Server.
- Returning raw internal errors or upstream secrets to API clients.
- Storing credentials in plaintext.
- Exposing `/proxy/*` without signed/authenticated/trusted-LAN protection.
- Writing file operations outside configured roots.
- Auto-installing/updating plugins.
- Granting plugins global credential access.
- Sending credentials/local absolute paths to AI providers by default.
- Adding PostgreSQL-only behavior to MVP code.
- Omitting roadmap updates when implementation status changes.

---

## Testing Requirements

When the relevant component exists:

- Run `go test ./...` for Server/CLI changes.
- Run `golangci-lint run` for Server when configured.
- Add unit tests for path safety, proxy signature validation, credential encryption/redaction, and classification/template logic when implemented.
- Add integration-style tests for drivers/clients using fakes or local test servers where possible.

Do not require Docker to pass local unit tests.

---

## Code Review Checklist

Reviewers and check agents should verify:

- API routes use `/api/v1/` and standard response envelopes.
- Auth defaults are safe.
- User-owned resources are scoped by user/role.
- Sensitive fields are encrypted at rest and redacted in logs/API responses.
- File paths are root-constrained and symlink/traversal safe.
- STRM/proxy behavior is signed or authenticated by default.
- External calls have timeouts and use contexts.
- Long-running jobs handle partial failures cleanly.
- Docs/spec/roadmap are updated when architecture or status changes.

---

## Documentation Rules

- Use `OpenList/Alist` or `OpenList (Alist-compatible API)` in user-facing docs.
- State that SQLite is the default database; PostgreSQL is future optional only.
- Keep PT search, follow tasks, AI, plugins, and multi-user permissions documented as planned scope even if phased later.
- Distinguish WSL/Linux development commands from Windows-native Tauri packaging/runtime concerns.