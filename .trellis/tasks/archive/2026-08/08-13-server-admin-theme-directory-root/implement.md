# Implementation Plan

## 1. Theme Foundation

- Add typed theme preference/bootstrap logic with safe localStorage parsing and light fallback.
- Add unit tests for fallback, persistence and root-attribute application where practical without browser-global leakage.
- Add an accessible reusable theme toggle and initialize the selected theme before Vue mount.
- Replace global hard-coded dark canvas, focus and status styles with semantic light/dark tokens.
- Refactor UnoCSS shortcuts for panels, buttons, inputs and labels to use semantic styles.

## 2. Global Shell and Authentication

- Restyle `AppLayout` sidebar, topbar, navigation, panels, overlay and responsive drawer as opaque traditional admin surfaces.
- Add the theme toggle to the authenticated topbar while preserving search/log/notification/account behavior and mobile responsiveness.
- Update Login, Setup and Forbidden views to use the shared authentication/page shell and expose the same theme toggle.

## 3. Page Migration

- Migrate Dashboard cards and metrics to semantic surfaces and compact spacing.
- Migrate User Management, Users, Roles, Audit, Storage and Planned views away from dark-only color utilities.
- Remove decorative English eyebrow text and redundant visual copy while preserving technical names and business behavior.
- Check all loading, empty, permission-denied, disabled, success, warning and error states in both themes.

## 4. Directory Root Navigation

- Add platform-aware “此电脑” / “文件系统” root navigation to `DirectoryPickerDialog`.
- Route the root action through the existing abortable `/api/v1/filesystem/roots` request; keep breadcrumb and parent token navigation unchanged.
- Restyle the dialog with theme tokens and preserve focus trap, Escape close, focus restoration, loading/error/truncation behavior.
- Add regression coverage for platform label/root behavior and confirm no backend contract change is necessary.

## 5. Verification

- Run from `server/webui`: `npm run test`, `npm run typecheck`, `npm run lint`, `npm run build`.
- Run related backend checks: `go test ./internal/services ./internal/httpserver` and then the repository's full `server/test.ps1` gate.
- Start the Windows test server and manually smoke both themes on login/setup (as available), dashboard, account/role/audit/storage/planned pages, responsive sidebar and directory dialog.
- Verify Windows navigation: roots -> `D:\` -> child -> `此电脑` -> roots; confirm breadcrumb returns to the drive root and Storage selection still saves through an opaque selection token.
- Inspect git diff/status and ensure unrelated Player/Trellis worktree changes are not modified, staged or reverted.

## Risk and Rollback Points

- UnoCSS dynamic token syntax can silently fail generation. Prefer stable authored semantic classes or statically discoverable variable utilities, then verify the production CSS through `npm run build`.
- Theme migration can leave isolated dark-only utilities. Use repository-wide searches for `bg-white/`, `border-white/`, `text-slate-`, gradients, blur and glow after migration, then visually inspect intentional exceptions.
- Do not change directory service/token code unless a failing regression proves a backend defect. Root navigation should remain a client call to the existing endpoint.
