# Design: Server Admin Theme and Directory Root Navigation

## Boundaries

This task changes only `server/webui` product presentation and directory-picker navigation behavior. The existing filesystem roots endpoint remains the root-navigation authority. No database, permission catalog, token payload, directory adapter, or storage API contract changes are required.

```text
Browser-local theme preference
  -> theme bootstrap before app.mount()
  -> data-theme on <html> + color-scheme
  -> semantic CSS tokens
  -> shared controls / layout / every Server view

DirectoryPickerDialog
  -> root button
  -> GET /api/v1/filesystem/roots
  -> existing authorized DirectoryBrowserService.Roots
  -> Server-visible drive / mount listing
```

## Theme State and Bootstrap

- Add a small shared theme module/composable with the closed type `light | dark` and a version-stable localStorage key such as `omc:server-theme`.
- `readStoredTheme()` accepts only known values and falls back to `light`; malformed or unavailable storage must not break startup.
- `applyTheme()` sets `document.documentElement.dataset.theme` and `document.documentElement.style.colorScheme`.
- Call theme initialization in `main.ts` before `app.mount('#app')` so the first Vue-rendered frame uses the selected palette.
- A reusable `ThemeToggle` button exposes the current state, target action, icon and accessible label. Use it in authenticated topbar and unauthenticated login/setup/forbidden shells without copying state logic.
- Theme is intentionally browser-local and explicit. It does not use `prefers-color-scheme`, because product intent requires white as the deterministic first default.

## Visual Token System

Define semantic tokens on `:root` for the light palette and override them under `:root[data-theme='dark']`. Required groups include:

- canvas, sidebar, topbar, surface, surface-muted, surface-hover, overlay
- border, border-strong
- text, text-muted, text-subtle, text-on-accent
- accent, accent-hover, accent-soft
- success, warning, danger and their soft surfaces/borders
- focus ring and restrained shadow
- shared radii and density values

Refactor UnoCSS shortcuts (`panel`, buttons, input, label) to consume CSS variables or replace them with stable global semantic classes. Page-specific utility layout classes may remain, but color and elevation must come from semantic tokens. Avoid animated full-root theme transitions; controls may retain short interaction transitions.

## Traditional Admin Layout

- Sidebar and topbar use opaque solid surfaces and 1px borders. Retain collapse, mobile drawer, permission-filtered navigation and focus trapping.
- Cards/forms/tables use compact spacing and approximately 6–10px radii. Shadows are subtle and reserved for overlays.
- Dashboard retains its information architecture but replaces decorative gradients and oversized tile styling with conventional statistic and section cards.
- Authentication surfaces use a centered, bounded form card on a plain canvas; the theme toggle remains reachable without authentication.
- Remove decorative English eyebrows from route headings and replace any necessary context with concise Chinese copy.
- Dialog overlay remains visually distinct in both themes; the dialog uses the same surface/border/control tokens as the rest of the console.

## Directory Root Navigation

- Derive the root label from `listing.platform`: `windows` becomes `此电脑`; every other platform becomes `文件系统`.
- Render the root as a semantic button whenever a listing exists. At roots it represents the current level and may be disabled or remain a refresh-equivalent action; after entering a root/child it is enabled and calls a dedicated `loadRoots()` function.
- `loadRoots()` requests the existing `/api/v1/filesystem/roots` through the same abortable `load()` path. It never generates a path or token.
- Existing breadcrumbs remain token buttons. Existing `parent_token` remains the one-level parent control. `D:\` can therefore be reached by its breadcrumb while `此电脑` returns to all drives.
- Add component-focused regression tests around root label and root endpoint navigation. If direct Vue DOM testing would require a large new dependency, extract pure label/navigation helpers for unit coverage and verify the click flow through the existing browser smoke step; do not add a test stack solely for this small interaction.

## Security and Compatibility

- No backend change is expected. The roots request continues through authentication, `storages.browse` middleware, service authorization, per-actor/IP rate limit, concurrency bound, timeout and no-store middleware.
- Root navigation reveals no information beyond the already authorized roots response and does not log absolute paths.
- Theme storage contains only a non-sensitive enum. Storage access is guarded so privacy mode or blocked localStorage cannot prevent the console from loading.
- Existing route/API contracts and saved sidebar-collapse preference remain compatible.

## Rollback

- Theme work is isolated to a shared module/component, global tokens, Uno shortcuts and Server Vue templates/styles; reverting it restores the former presentation without data migration.
- Root navigation is a client-only call to an existing endpoint. Reverting the button does not affect filesystem APIs or stored Storage records.
