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
- Use Windows-native PowerShell commands for local development; treat Linux/WSL commands as explicit compatibility, CI, or deployment paths rather than the default.

## Scenario: Server Beta Release Packaging

### 1. Scope / Trigger

Apply this contract whenever `.github/workflows/server-beta-release.yml`, Server release assets, tags, or the official TMDB build credential change. Server Beta is independent from Player Release and must never create, require, update, or trigger a Player `v*.*.*` tag/release.

### 2. Signatures

```text
workflow_dispatch.version = X.Y.Z | vX.Y.Z
normalized tag            = server-vX.Y.Z
release title             = OhMyCine Server vX.Y.Z Beta
required secret           = OHMYCINE_TMDB_READ_ACCESS_TOKEN
```

Assets are exactly `OhMyCine-Server-vX.Y.Z-windows-x64.zip`, `OhMyCine-Server-vX.Y.Z-linux-x64.tar.gz`, and `OhMyCine-Server-vX.Y.Z-SHA256SUMS.txt`.

### 3. Contracts

- Dispatch only from `develop`; fetch `origin/develop` before validation and again immediately before the first remote mutation. `GITHUB_SHA` must equal the fetched tip both times.
- A pre-existing Server tag must resolve to that exact commit. A pre-existing Release must use the exact tag/title, be published, and be a prerelease. The same tag/commit may be rerun idempotently.
- Build the WebUI first and compile both binaries with `CGO_ENABLED=0` and `-tags webui`; a plain backend binary is not an official release artifact.
- Scope `contents: write` to the publishing job. Inject only the official read-only TMDB token through its typed Secret and linker variable; reject missing, oversized, or linker-unsafe values without printing them.
- Run permission drift, all 158 WebUI tests, typecheck, ESLint, WebUI build, Go module verification, build, vet, tests, and `golangci-lint` v2.4.0 through `golangci-lint-action@v7` or a newer v2-compatible action before packaging.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Version is not strict `X.Y.Z`/`vX.Y.Z` | Fail before building or writing remote state. |
| Ref is not `develop`, or fetched tip differs | Fail without creating a tag/release. |
| Existing tag points elsewhere | Fail; never move or overwrite the tag. |
| Existing release has wrong tag/title, is draft, or is not prerelease | Fail; never rewrite it. |
| TMDB Secret is missing or unsafe for linker injection | Fail without echoing the value. |
| Same version and commit are rerun | Reuse the prerelease and replace only the three same-name Server assets. |

### 5. Good / Base / Bad Cases

- Good: latest `origin/develop`, missing `server-vX.Y.Z`, all gates pass -> create the namespaced prerelease and upload the three Server assets.
- Base: exact tag/release already exist at the same commit -> verify them and idempotently refresh the same assets.
- Bad: use a Player `vX.Y.Z` release, an older develop commit, or a plain non-WebUI binary -> reject before publication.

### 6. Tests Required

- `server_beta_release_guard.py` tests strict normalization and rejects Player-style input outside the accepted version field.
- Static workflow guard asserts manual-only dispatch, two develop-tip checks, exact tag/release identity and title, scoped write permission, pinned linter, embedded WebUI builds, and only Server asset names.
- `actionlint` validates both Server workflows. Local/CI quality gates must build `-tags webui` for Windows amd64 and Linux amd64.
- Read-only preflight verifies the selected namespaced tag/release does not already point elsewhere before the main session dispatches the release.

### 7. Wrong vs Correct

```yaml
# Wrong: couples Server to Player and can trigger Player Release.
tag: v1.2.3
release: existing-player-prerelease

# Correct: isolated Server namespace and prerelease.
tag: server-v1.2.3
release: OhMyCine Server v1.2.3 Beta
```
