# MediaLibrary Web UI Acceptance

## Implemented

- `/system/media-libraries` uses generated `media_libraries.*` permissions for navigation, route access and actions.
- Creation selects an enabled Storage, a classification Profile and a source directory through `DirectoryPickerDialog`; the request submits `relative_root_token` and never reconstructs an absolute source path.
- Local source capabilities hide STRM controls and the request boundary strips `strm_enabled` / `strm_local_root_token` even if stale local UI state exists.
- Enabled creation explains and observes automatic initialization. Active states poll until stable; initialization failures show a safe error code, next retry time and an immediate retry action.
- Status, scan runs, provider-relative media entries, manual follow-up scan, edit/disable and delete-config-only UX are available with permission gating.
- Profile revision drift is visible and links to the independent rule-management page.
- New surfaces use semantic Server admin tokens and remain compatible with both light and dark palettes.

## Automated evidence

- `src/media-libraries.test.ts`: opaque token request boundary, local STRM stripping, capability gate and failure presentation.
- `src/navigation.test.ts`: generated read permission visibility.
- `src/router/contracts.test.ts`: canonical route/navigation mapping.
- Web UI quality commands are recorded in the implementation handoff.

Final local results: `npm run typecheck`, `npm run lint`, `npm run test -- --reporter=verbose` (9 files / 34 tests), `npm run build`, generated permission check, semantic-theme anti-pattern search, and `git diff --check` all pass.

## Completed live acceptance

- The opt-in `OMC_LIVE_LIBRARY_ROOT` acceptance discovered the expected four real MP4 files and compared the complete source tree before/after. File names, directory structure, sizes, modes and nanosecond timestamps were unchanged; the real path was injected only through the process environment and is absent from Git.
- An isolated Server process and fresh database were used for authenticated browser smoke. Owner setup, permission-gated `/system/media-libraries` navigation, empty state and light/dark theme switching rendered correctly without touching the owner's normal Server profile.
