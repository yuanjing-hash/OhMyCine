# Implementation Plan

## 1. Schema and Pure Contract

- Add `internal/classification` version 1 Go types, allowlists, empty/default constructors, strict validation, canonical clone/copy helpers and the pure matcher.
- Add Player-v1-derived contract fixture/tests for all matching semantics and exact default category order.
- Add cryptographically random category ID generation with deterministic IDs only inside tests.

## 2. Persistence, Permissions and Service

- Add explicit migration v3, profile model and idempotent `default-v1` seed; extend fresh/idempotent and v2→v3 migration tests.
- Add four canonical permissions, generated frontend constants and operator/viewer seed matrix tests.
- Implement profile service list/get/create/copy/update/delete, strict payload validation, normalized uniqueness, copy-name allocation, revision compare-and-swap, protected constraints, extensible reference checker and audit summaries.
- Cover duplicate races, stale revision, deep-copy independence, protected behavior, policy denial and log/audit redaction.

## 3. REST API

- Add thin handlers and the six `/api/v1/media-classification-profiles` routes with standard response envelopes and stable HTTP mappings.
- Add router integration tests for permission matrix, validation, lifecycle, copy, revision conflict, protected rejection and audit safety.
- Update API/security/Server architecture docs and roadmap status where current implementation state changes; do not relabel pipeline `/categories` as this feature.

## 4. Server Web UI

- Add Profile DTOs/API calls and generated permission checks.
- Add `系统 → 规则管理` navigation item, `/system/media-rules` route and navigation/route authorization tests.
- Implement Profile master/detail page and controlled reusable rule-editor components for group selection, category order, add/delete, fallback, include/exclude/unconstrained options and year bounds.
- Implement read-only built-in state, copy/create flows, draft preservation, delete confirmation and stable error/revision conflict feedback.
- Verify responsive layout, keyboard access and white/dark semantic tokens without dark-only utilities or glass effects.

## 5. Verification

- Run focused classification, migration, service and router Go tests, then `go test ./...`, `go vet ./...`, normal/webui-tag builds and module verification.
- Run Web UI permission drift, unit tests, typecheck, lint and production build.
- Run full `server/test.ps1` in the Windows-native environment.
- Use an isolated SQLite/runtime and browser-smoke owner/operator/viewer visibility, default Profile read-only behavior, copy/edit/delete flow, revision error handling and both themes.
- Run `git diff --check`, scan logs/audits for `rules_json`, and confirm unrelated Player/parallel task files remain untouched.

## Risk and Rollback Points

- Do not reuse `categories.*`, `/categories`, Storage Destination models or Player runtime code.
- Strict validation must reject unknown/invalid input instead of inheriting Player's local sanitize-and-fallback behavior; API writes need predictable errors.
- SQLite uniqueness and optimistic update checks must map races to stable conflict codes rather than 500.
- Do not build fake MediaLibrary records for reference tests. The next child task must attach the real reference checker before libraries can reference Profiles.
