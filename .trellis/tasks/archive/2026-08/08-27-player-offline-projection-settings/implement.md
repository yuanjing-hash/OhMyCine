# Implementation Plan

1. Exclude `OFFLINE_SOURCE_CONFIG` from user-visible ordered source configuration while retaining manager registration.
2. Add deterministic sanitization/pruning for legacy offline sections, items, and source snapshots; invoke it after offline index deletion.
3. Route download-history detail actions to the original source/item identity.
4. Change frontend and Rust default concurrent tasks to 1; keep segment default at 1 and add regression assertions.
5. Add Rust segment-topology reprojection that preserves only entity-validated continuous prefixes and apply it when requested thread count changes.
6. Extend TypeScript and Rust regression tests for projection removal, badge cleanup, defaults, and segment count changes.
7. Run Player lint/typecheck/build, targeted verification scripts, Rust fmt/check/clippy/test, `git diff --check`, and Server scope check.
8. Update the download/offline code spec, commit, archive, push `develop`, then publish and monitor the next Player Beta.

## Reviewer fixes

- Hardened visible data-source configuration so legacy or malformed `__offline__` / `offline` entries cannot reappear in navigation, home aggregation, or search.
- Rewrote sanitized display caches during startup (and removes invalid cache JSON) so legacy offline projections are deleted from persistent storage rather than only hidden in memory.
- Made completed-file deletion optimistically remove its exact offline index row and downloaded badge before best-effort index reconciliation, preventing stale badges after a successful delete.
- Added segment-topology regression coverage for increasing worker count and for discontinuous completed ranges; only continuous prefixes are retained.

## Reviewer verification

- `verify-download-planning.ts`: passed.
- Vue TypeScript check: passed.
- ESLint (`src/**/*.ts`, `src/**/*.vue`): passed.
- Full build/Rust suite: delegated to the main task's final sequential quality gate to avoid competing Cargo locks with concurrent Server work.
- Server scope: no changed files under `server/**`.
