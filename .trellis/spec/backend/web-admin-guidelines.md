# Server Web Administration Guidelines

> Executable contracts for the embedded Server administration UI, browser sessions, and RBAC.

---

## Scenario: Same-Origin Web Administration and RBAC

### 1. Scope / Trigger

- Trigger: adding or changing Server login/setup, users, roles, permissions, sessions, audit events, protected management APIs, Vue route/navigation/button authorization, or the embedded Web UI build.
- Applies across SQLite migrations, GORM models, services, Gin middleware/handlers, the permission catalog, Vue Router, Pinia auth state, action controls, and production asset embedding.

### 2. Signatures

- Permission code: lowercase stable `<resource>.<action>` string such as `users.read`, `roles.assign`, or `connections.test`.
- Canonical catalog: `server/internal/authz/catalog.json`; TypeScript constants are generated into `server/webui/src/auth/generated-permissions.ts`.
- Core tables: `users`, `roles`, `permissions`, `user_roles`, `role_permissions`, `sessions`, and `audit_logs`.
- Browser session cookie: high-entropy opaque token; SQLite stores only its SHA-256 hash.
- Session fields include idle and absolute expiry plus revocation state.
- Setup/auth APIs:
  - `GET /api/v1/setup/status`
  - `POST /api/v1/setup/owner`
  - `POST /api/v1/auth/login`
  - `POST /api/v1/auth/logout`
  - `GET /api/v1/auth/me`
  - `GET /api/v1/auth/csrf`
- Administration APIs use the standard response envelope and stable application error codes.
- `server/webui` is a nested Go module referenced by the root Server module with a local `require` + `replace`; this prevents `go test ./...` from traversing frontend `node_modules`.
- Production SPA fallback runs only for extensionless `GET`/`HEAD` browser navigations whose `Accept` header explicitly includes `text/html`; it serves `index.html` with `Cache-Control: no-store` so a rebuilt embedded Server cannot retain an older application shell. Content-hashed assets remain immutable.

### 3. Contracts

- The first owner is created only when no user exists. Owner creation, administrator-role assignment, initial session creation, and audit recording are one transaction and are guarded against concurrent setup attempts.
- Owner identity is distinct from a role. The owner cannot be deleted, disabled, or implicitly transferred. The protected `administrator` role cannot be deleted.
- User role replacement, role permission replacement, last-administrator checks, privilege-escalation checks, session revocation, and audit writes occur inside the same transaction as the mutation.
- A non-owner may grant only permissions already present in the actor's effective permission set. Multiple roles are additive; MVP has no deny, inheritance, arbitrary permission creation, or resource-instance scope.
- Vue route meta, navigation filtering, and buttons reuse generated permission codes. They improve UX only; every protected API and sensitive service operation enforces authorization again on the server.
- Browser management uses a revocable server-side session in an HttpOnly, host-only, SameSite cookie. Do not store browser JWT/session tokens in localStorage, Pinia persistence, URLs, logs, or audit metadata.
- State-changing cookie-authenticated requests require a session-bound CSRF header, allowed Origin/Referer validation, safe Fetch Metadata, and an exact JSON media type. Setup and login are also Origin-checked and rate-limited.
- The shared Web UI API client may recover a stale session-bound CSRF token only when a mutating request receives HTTP 403 with stable `error_code=CSRF_INVALID`. It clears the token only if it is still the value used by that failed request, singleflights `GET /api/v1/auth/csrf`, and replays the same mutation exactly once. Other 403 responses are never replayed and continue through the normal forbidden/permission refresh path.
- Authentication/setup responses use `Cache-Control: no-store`. Password reset requires the actor's current password; password changes and account disablement revoke affected sessions.
- Production Web UI assets are built before `go build -tags webui`. Default Go builds/tests must not require `dist` to exist. SPA fallback applies only to explicit HTML navigation. Exact or nested `/api`, `/ws`, `/proxy`, and `/assets` paths, file-like paths with extensions, non-HTML clients, and non-`GET`/`HEAD` requests return real 404 responses instead of `index.html`.
- The root Server module and `server/webui` module must each keep a tidy `go.mod`/`go.sum`. The root module's Go directive must satisfy dependency minimums; currently this is Go 1.23+.
- AI Provider 模型读取成功后使用可搜索的管理端模态选择器展示全部规范化 ID/显示名称；整行选择只回填表单，不自动保存。列表读取失败必须保持当前模型和关闭状态，列表过大/响应无效显示列表语义的安全错误，并保留手动输入能力。

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Setup requested after any user exists | Reject with a stable conflict/setup-closed error |
| Two setup requests race | At most one owner transaction succeeds |
| Unknown username login | Perform a dummy password hash comparison before returning the same safe login error |
| Missing/expired/revoked session | Return 401 and clear frontend auth state |
| Permission changed while a session is active | Next authorization uses current database permissions; frontend refreshes `/auth/me` after 403 |
| A mutation receives 403 + `CSRF_INVALID` for an expired token | Singleflight one fresh-token request and replay once; do not surface the first recoverable 403 as a permission failure |
| The replay again receives `CSRF_INVALID`, or a 403 has another stable error code | Stop without another replay and surface the normal safe error/forbidden event |
| User lacks a page/button permission | Hide or disable the UI affordance; direct API access still returns 403 |
| Actor grants a permission they do not hold | Reject inside the mutation transaction |
| Mutation would leave zero active administrators | Reject with `LAST_ADMIN_REQUIRED` or equivalent stable error |
| Mutation targets the owner for disable/delete/implicit transfer | Reject server-side even if the UI hides the action |
| Mutation has `application/jsonp` or another non-JSON media type | Return 415/400; do not accept prefix matches |
| Origin contains credentials, paths, query, fragment, or unsupported scheme | Reject configuration/startup validation |
| `go test ./...` discovers packages under `webui/node_modules` | Treat as a module-boundary regression; restore the nested Web UI module |
| Browser refreshes `/system/users/accounts` with `Accept: text/html` | Return the SPA `index.html` shell with HTTP 200 and `no-store` |
| Generic/JSON client requests the same extensionless path | Return 404; `*/*`, omitted `Accept`, and `application/json` are not browser HTML navigation |
| Missing `/assets/*`, file-like path, `/api*`, `/ws*`, or `/proxy*` is requested | Return 404 and never mask the missing route/resource with SPA HTML |

### 5. Good/Base/Bad Cases

- Good: a custom operator sees users but not role editing; the users page loads without requiring `roles.read`, role controls remain unavailable, and direct role API access returns 403.
- Good: an administrator edits a custom role only within their own permission set; the transaction updates permissions, authorization revision/session state, and audit metadata atomically.
- Base: planned media modules appear as clearly unavailable navigation states until their real APIs exist; no placeholder endpoint reports fake success.
- Good: a browser refreshes a nested Vue route and receives `index.html`, while a missing JavaScript asset and an unknown API route still return 404.
- Good: two concurrent mutations fail with the same stale CSRF token, share one refresh request, and each replay once with the fresh token.
- Bad: a button is hidden with `v-if`, but the matching POST/DELETE route has no permission middleware or service policy.
- Bad: last-admin or privilege-escalation checks run before the transaction, allowing concurrent requests to invalidate the decision.
- Bad: the Web UI stores a JWT in localStorage or commits built `node_modules`/session material.
- Bad: the NoRoute handler returns `index.html` for every missing `GET`, causing mistyped API paths, JSON clients, and missing assets to look successful.
- Bad: every 403 refreshes CSRF or recursively retries, which can replay denied mutations and hide real permission errors.

### 6. Tests Required

- Backend integration tests must cover first-owner setup, concurrent/closed setup, login/session/CSRF, viewer authorization, permission refresh, self/owner protection, last-administrator protection, and privilege-escalation rejection.
- Web API client tests must cover stale-token refresh plus one replay, concurrent refresh singleflight, and a non-CSRF 403 with zero replay.
- Run from `server/`: `go test ./...`, `go vet ./...`, `go build ./cmd/server`, `go build -tags webui ./cmd/server`, and `go mod verify`.
- Run from `server/webui/`: `go test .`, `go mod verify`, `npm run permissions:check`, `npm run typecheck`, `npm run lint`, and `npm run build`.
- `go list ./...` from the root Server module must list only OhMyCine Server packages and must not include packages from `node_modules`.
- The permission generation check must fail on duplicate/invalid permission codes or generated TypeScript identifier collisions.
- Review browser responses for cookie flags, `Cache-Control: no-store`, stable error codes, CSP/security headers, and no credential/session leakage.
- Web UI Go tests must cover a real HTML deep link, exact and nested reserved prefixes, missing assets/files, `*/*` and JSON clients, non-safe methods, missing index, and immutable asset versus `no-store` SPA-shell caching.
- An embedded-binary smoke must request `/`, one nested route with `Accept: text/html`, the generated JavaScript asset, a missing asset, and a missing API route; assert `200/200/200/404/404` respectively.

### 7. Wrong vs Correct

#### Wrong

```go
// Checked before the transaction; another request may change the role meanwhile.
if !actor.HasAll(requestedPermissions) {
    return ErrPrivilegeEscalation
}
return db.Transaction(func(tx *gorm.DB) error {
    return replaceRolePermissions(tx, roleID, requestedPermissions)
})
```

```ts
// UI-only string drifts from the backend contract.
const canDelete = permissions.includes('button.deleteUser')
```

```ts
// A generic retry turns a real permission denial into a replayed mutation.
if (response.status === 403) return api(path, options)
```

```go
// Every unknown GET becomes HTML, masking missing APIs and static assets.
if request.Method == http.MethodGet {
    serveIndex()
}
```

#### Correct

```go
return db.Transaction(func(tx *gorm.DB) error {
    effective, err := resolveEffectivePermissions(tx, actorID)
    if err != nil {
        return err
    }
    if !effective.ContainsAll(requestedPermissions) {
        return ErrPrivilegeEscalation
    }
    return replaceRolePermissions(tx, roleID, requestedPermissions)
})
```

```ts
import { Permissions } from '@/auth/generated-permissions'

const canDelete = auth.can(Permissions.UsersDelete)
// The DELETE API also requires users.delete and enforces owner/last-admin policy.
```

```go
// Correct: an explicit HTML navigation may fall back, but generic clients,
// reserved service prefixes, and file-like paths keep their real 404.
if (request.Method == http.MethodGet || request.Method == http.MethodHead) &&
    strings.Contains(request.Header.Get("Accept"), "text/html") &&
    !isReservedPath(requestPath) && path.Ext(path.Base(requestPath)) == "" {
    serveIndex()
}
```

```ts
if (response.status === 403 && errorCode === 'CSRF_INVALID' && mutating && !alreadyRetried) {
    await refreshCSRFSingleflight()
    return api(path, options, { alreadyRetried: true })
}
```

---

## Scenario: Server Filesystem Directory Picker

### 1. Scope / Trigger

- Trigger: selecting a Server-local directory for Storage or a future Server-owned local path field.
- This is a Server filesystem read model, never a browser-native picker and never a general file manager.

### 2. Signatures

- Permission: `storages.browse`.
- Routes: `GET /api/v1/filesystem/roots`, `GET /api/v1/filesystem/directories?token=...`, and `GET /api/v1/storages/{id}/directory`.
- Storage create/update primary Web UI field: `picker_token`; raw `root_path` remains API-compatible but is not a free-text Web UI control.

### 3. Contracts

- The reusable Vue dialog shows Server identity, roots, breadcrumbs, parent, refresh, current-directory selection, and loading/empty/error/truncated states.
- The dialog traps focus, closes with Escape, restores focus, and exposes disabled-directory reasons to assistive technology.
- The client sends only server-issued navigation/selection tokens and displays the authorized path summary read-only.
- Tokens are short-lived, purpose/platform/adapter-version bound, AES-GCM sealed, and HMAC authenticated. Signing alone is insufficient because a base64-encoded signed payload still exposes absolute paths.
- Root and child responses are bounded and no-store. Rate-limit state is bounded/expired by actor + IP, and a timed-out adapter call retains its concurrency slot until the adapter actually exits.
- Storage save repeats canonicalization, Reparse Point/symlink rejection, uniqueness, and read-only probe; successful browsing is not proof that registration will succeed.
- Revalidation checks every path component at use time because a safe directory can be replaced by a symlink/junction after token issuance. Re-selecting the unchanged stored root still repeats canonicalization and probe.
- Apply `Cache-Control: no-store` before permission middleware so both successful and denied picker responses remain non-cacheable.
- No create-directory, rename, move, delete, upload, download, preview, recursive scan, ACL, owner, content, or subtree-stat operation belongs in the picker.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Missing/invalid/tampered/wrong-purpose/wrong-platform token | Reject with `directory_token_invalid`; do not reveal claims or path |
| Expired token | Reject with `directory_token_expired` and require a fresh selection |
| Any ancestor becomes symlink/junction/Reparse Point | Reject browse/select/save with safe unavailable error |
| Directory disappeared | Return `directory_not_found`; do not echo the path |
| Directory is unreadable | Return `directory_unreadable`; do not return raw OS error |
| Per actor+IP rate exceeded | Return `directory_rate_limited`; keep rate-key state bounded and expiring |
| Concurrency limit reached | Return `directory_busy` without starting another adapter call |
| Adapter exceeds timeout | Return `directory_unavailable` promptly, but release its concurrency slot only after the adapter exits |
| Actor lacks `storages.browse` | Return 403 with `Cache-Control: no-store` and no root/path data |
| Listing contains more than 500 directories | Return the bounded sorted prefix plus `truncated=true` |
| Storage saves the same selected root again | Repeat canonicalization and read-only probe; do not skip because the string is unchanged |

### 5. Good/Base/Bad Cases

- Good: an authorized remote administrator opens the picker, browses Server-visible Windows drives or container mounts through opaque tokens, and saves a directory that is revalidated at commit time.
- Good: a directory is replaced with a junction after listing; the next browse/select/save rejects it before traversal.
- Base: a mount disappears or token expires while the dialog is open; the UI shows a recoverable reselect state and returns to roots.
- Bad: Vue joins `currentPath + '/' + childName`, accepts a free-text absolute path, or persists picker tokens in SQLite.
- Bad: a signed JSON token is merely base64-encoded, allowing anyone holding it to decode the Server absolute path.
- Bad: permission middleware returns a cacheable 403 because the no-store middleware was registered after RBAC.

### 6. Tests Required

- Cover middleware and service authorization, operator/viewer seed behavior, token purpose/tamper/expiry, bounded sorted listing, files excluded, symlink/Reparse Point disabled, safe errors/no-store, and picker-token Storage round trip.
- Assert tokens do not contain or decode to the selected absolute path, are never persisted, and cannot be reused across browse/select purposes or adapter/platform versions.
- Cover ancestor-link replacement, stale saved roots, unchanged-root reprobe, adapter timeout/concurrency retention, bounded actor+IP rate state, denied-response no-store, Unix mount escape decoding and Windows logical-drive smoke.
- Frontend tests cover stale-request aborts, loading/empty/error/truncated states, Escape and all close-path focus restoration, collision-safe ARIA IDs, disabled reasons and absence of free-text path inputs/path concatenation.
- Run Web UI permission drift, tests, typecheck, lint, build, Server Go test/vet/build, `go build -tags webui`, module verifies, and a Windows native root/list smoke.

### 7. Wrong vs Correct

#### Wrong

```go
// Integrity only: anyone can base64-decode payload and read an absolute path.
payload, _ := json.Marshal(claims)
token := base64.RawURLEncoding.EncodeToString(append(payload, hmacSum(payload)...))
```

```ts
// The client invents a Server path and bypasses token purpose/boundary rules.
await api(`/api/v1/filesystem/directories?path=${currentPath}/${child.name}`)
```

#### Correct

```go
// Seal path claims, authenticate them, bind purpose/platform/version/expiry,
// then revalidate every component when the token is consumed.
token := sealAndAuthenticate(directoryClaims)
path := resolveAndValidate(token, expectedPurpose)
```

```ts
// Server-issued tokens are the only navigation and selection capability.
await api(`/api/v1/filesystem/directories?token=${encodeURIComponent(item.token)}`)
```

---

## Scenario: One-Command Local and Production Launcher

### 1. Scope / Trigger

- Trigger: changing `server/start.sh`, runtime directories, embedded Web UI startup, local production defaults, or startup documentation.
- Applies to Bash dependency discovery, npm/Go builds, SQLite placement, environment overrides, process signals, and Git ignore rules.

### 2. Signatures

- Command: `server/start.sh [--skip-build|--help]`.
- Default runtime root: `server/.runtime/`.
- Default binary: `server/.runtime/bin/ohmycine-server`.
- Default database: `server/.runtime/data/ohmycine.db`.
- Runtime overrides: `OMC_RUNTIME_DIR`, `OMC_BINARY_PATH`, `OMC_DATABASE_PATH`, `OMC_ENV`, `OMC_SERVER_HOST`, `OMC_SERVER_PORT`, `OMC_PUBLIC_ORIGIN`, and `OMC_COOKIE_SECURE`.
- Default build order: Web UI dependency check → `npm run build` → `go build -tags webui` → foreground `exec`.

### 3. Contracts

- Resolve every default/relative path against the physical `server/` directory, not the caller's current directory.
- Default startup uses `OMC_ENV=production`, an embedded Web UI, wildcard listening on port 3000, a loopback advertised origin, and one foreground Go process. The launcher must use `exec` so Ctrl+C, systemd, and container signals reach the Server directly.
- Runtime data is persistent. The launcher must never delete, reset, replace, or silently migrate an existing database outside normal Server migrations.
- `server/.runtime/`, `webui/dist/`, `webui/node_modules/`, generated binaries, and SQLite journal/WAL/SHM files are Git-ignored.
- Run `npm ci` only when `node_modules` is absent or the committed lockfile differs from the stored dependency stamp. Never use an untrusted Windows `node.exe`/`npm` from WSL.
- `--skip-build` requires an existing executable binary and reuses it without modifying the binary or database.
- User-provided `OMC_*` values take precedence. Wildcard listen addresses map to loopback only for the default browser origin; LAN/domain/reverse-proxy use requires an explicit exact `OMC_PUBLIC_ORIGIN`.
- Windows may read `.runtime/windows/config/server.json` between defaults and environment overrides. It accepts only `listen_host`, `port`, and `public_origin`; credentials and unknown fields fail closed.
- `OMC_PUBLIC_ORIGIN` is the only advertised origin for Web UI CSRF, STRM, and Emby gateway URLs. Never persist `0.0.0.0` or `::`, and never add per-library/per-player origin or port settings.
- Missing Go/npm/Node dependencies fail before mutating runtime data and return a clear actionable message.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Launcher invoked outside `server/` | Resolve the same runtime, Web UI, binary, and database paths |
| Path contains spaces | Quote paths and complete dependency/build/start steps normally |
| `--help` | Print usage and create no `.runtime` files |
| `--skip-build` without a binary | Fail clearly; do not create a database |
| Lockfile unchanged | Reuse `node_modules`; do not run `npm ci` |
| Lockfile changed | Run `npm ci`, then refresh the dependency stamp |
| `OMC_SERVER_HOST=0.0.0.0` and no public origin | Listen on wildcard but default browser origin to `127.0.0.1` |
| `OMC_SERVER_HOST=::` and no public origin | Use bracketed IPv6 listen syntax and default browser origin to `[::1]` |
| Explicit external host/domain | Require the operator to set the exact browser `OMC_PUBLIC_ORIGIN` |
| SIGINT/SIGTERM | Reach the Go process and allow graceful HTTP shutdown |

### 5. Good/Base/Bad Cases

- Good: `./server/start.sh` from the repository root builds once, stores the binary/database under `server/.runtime`, serves the UI and API on one port, and stops cleanly with Ctrl+C.
- Good: a later `./start.sh --skip-build` preserves the database inode and binary modification time.
- Base: an operator overrides port/database paths through environment variables while retaining safe loopback defaults.
- Bad: the script starts the Server in the background, writes a PID file, and leaves an orphan process after the shell exits.
- Bad: each start deletes the SQLite database, unconditionally reinstalls npm dependencies, or writes generated binaries into tracked source paths.

### 6. Tests Required

- Run `bash -n server/start.sh` and `server/start.sh --help`; assert help creates no runtime directory.
- Invoke from a different current directory and from a path containing spaces.
- Perform a full startup on an isolated port, poll `/api/v1/health`, request `/`, and terminate the exact launcher/Server PID.
- Repeat with `--skip-build`; assert binary modification time and database identity are unchanged.
- Test IPv4 wildcard and IPv6 loopback startup plus generated public-origin output.
- Verify `git status --ignored`/`git check-ignore` covers every runtime/build/database artifact.
- Re-run the Server and Web UI Go/npm quality gates from the primary Web administration scenario.

### 7. Wrong vs Correct

#### Wrong

```bash
cd server
rm -f data/ohmycine.db
npm install
go run ./cmd/server &
```

#### Correct

```bash
server_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
runtime_dir="${OMC_RUNTIME_DIR:-${server_dir}/.runtime}"
database_path="${OMC_DATABASE_PATH:-${runtime_dir}/data/ohmycine.db}"

# Build only when requested, never remove the database, and hand signal ownership
# to the Go process.
exec "${runtime_dir}/bin/ohmycine-server"
```

---

## Scenario: Administration Navigation and Mixed Dashboard

### 1. Scope / Trigger

- Trigger: adding or changing Server administration navigation, dashboard cards, top-bar tools, user-management information architecture, or responsive shell behavior.
- Applies across Vue routes/meta, permission catalog usage, dashboard summary APIs, job/storage/subscription read models, notification/log drawers, and personal-account actions.

### 2. Signatures

- Primary navigation groups:
  - standalone `dashboard`;
  - `discovery`: recommendations and exploration;
  - `subscriptions`: subscriptions, workflows, calendar;
  - `automation`: task center, downloads, organization, STRM/import, files;
  - `system`: connections/storage, sites, plugins, user management, settings.
- User-management subviews: accounts, roles/permissions, and sessions.
- Top-bar tools: global search, logs, notifications, and authenticated-user menu.
- Dashboard layout: responsive 12-column grid on desktop; ordered single-column sections on compact screens.
- Canonical product design: `docs/architecture/08-server-web-ui-design.md`.

### 3. Contracts

- Do not flatten every route into one side-navigation level. A group is visible only when the actor can access at least one implemented child route; an empty group is omitted.
- Account administration and self-service are separate. Admins manage other users under User Management; every authenticated user edits their own profile, password, and sessions from the avatar menu.
- Runtime, task, and audit logs share the top-bar log center. Notifications contain actionable events such as connection expiry, job failure, subscription matches, and low storage; they are not duplicate navigation pages.
- The dashboard is a mixed content/operations surface. Media, storage/connection health, active jobs, and pipeline state come before subscription/calendar and metadata discovery content. The discovery hero is always after operational cards.
- Dashboard cards render only real backend data or an explicit unconfigured/planned state. Never fabricate successful connections, media counts, tasks, capacities, or recommendations.
- Every card documents a future data owner/API boundary. Frontend views do not query providers, SQLite, filesystems, or job engines directly.
- Navigation, route meta, action buttons, cards containing protected data, log tools, and APIs reuse stable permission codes. UI filtering is UX; API/service authorization remains the security boundary.
- The design may learn grouped navigation and card density from comparable tools, but OhMyCine keeps its own Cinema OS palette, media-pipeline semantics, terminology, and layout proportions.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Actor has no permission for any group child | Hide the group and its heading |
| Actor can read users but not roles | Show User Management with Accounts only; do not fail the page by loading roles |
| Actor edits their own profile | Route through avatar self-service, not admin user mutation UI |
| Card API is unavailable or feature is not configured | Show a truthful empty/error/configuration state; preserve the rest of the dashboard |
| One dashboard data owner fails | Isolate that card failure; do not replace other successful cards |
| Discovery metadata is available | Render it below status, pipeline, recent-import, and subscription sections |
| Compact/mobile layout | Preserve all dashboard sections in desktop priority order; move navigation to a drawer/sheet and keep top tools accessible |
| Protected log/audit data requested without permission | Hide the corresponding tool/tab and return 403 from the API |

### 5. Good/Base/Bad Cases

- Good: an operator opens the dashboard and sees real destination health, active pipeline runs, recent imports, then subscription and TMDB/Douban discovery content below.
- Good: a viewer sees only readable navigation children and no empty System group or destructive quick actions.
- Base: no source is configured, so the media/storage cards explain what to configure and provide an authorized add-connection action instead of showing misleading zero values.
- Bad: Users, Roles, Audit, Connections, STRM, and every planned capability appear as peer menu items in one flat list.
- Bad: the dashboard is dominated by user/role counts or an artwork hero while failed storage and pipeline tasks are hidden below the fold.
- Bad: the UI fills planned cards with static demo numbers in a real Server session.

### 6. Tests Required

- Route/navigation tests cover every permission combination needed to hide empty groups and show only allowed User Management tabs.
- Component tests assert the desktop 12-column order and compact single-column order keep the discovery hero after operational sections.
- Dashboard service/API tests return explicit configured/empty/error states and isolate partial failures by data owner.
- Browser checks cover log, notification, and avatar drawers plus keyboard/focus behavior.
- Visual checks compare desktop and compact layouts against the documented wireframes without requiring exact screenshot duplication.
- Frontend typecheck, lint, build, permission-catalog drift check, and backend authorization tests must pass.

### 7. Wrong vs Correct

#### Wrong

```ts
const navigation = [
  'dashboard', 'users', 'roles', 'audit', 'connections', 'destinations', 'strm',
]

const dashboard = {
  mediaCount: 337, // demo value shown even when no provider is configured
}
```

#### Correct

```ts
const groups = buildVisibleNavigation(routeDefinitions, auth.permissions)

const dashboard = await dashboardService.loadSummary()
// Each card receives real data, configured=false, or an isolated safe error.
// Discovery content remains after operational sections in every layout.
```

---

## Scenario: Media Recognition Review and Correction

### 1. Scope / Trigger

- Trigger: changing the media-library catalog/recognition tabs, match filters, retry, TMDB candidate search, manual override, scan recognition counters, or Profile built-in word-pack controls.
- The Web UI reviews Server-owned recognition projections. It never receives credentials, cache keys, provider IDs or physical paths and never writes source files.

### 2. Signatures

- Read permission: `media_libraries.read`; retry/override/clear permission: `media_libraries.scan`.
- Recognition tabs: all, matched, unrecognized, and manual override.
- Profile detail/update field: `builtin_recognition_packs: Array<'tv-v1' | 'anime-v1'>`.
- Recognition APIs are the five `/api/v1/media-libraries/:id/recognitions...` routes documented in `media-library-foundation.md` and always return `Cache-Control: no-store`.

### 3. Contracts

- Media list and recognition tabs remain Server-paginated. Switching library, tab, filter or search invalidates/aborts stale requests so an older response cannot replace the current view.
- Recognition rows display safe title/type/year/TMDB/category/status/error summaries, file count, a relative basename summary and update time. Do not reconstruct a path from that summary.
- Unrecognized rows offer retry and bounded TMDB candidate search. Saving a candidate sends only `tmdb_id` and `media_type`; the Server re-fetches metadata. Manual rows expose clear-override only to actors with scan permission.
- The Profile editor displays the two built-in packs as read-only-content toggles. Users may enable/disable pack codes but cannot edit embedded lines; copy preserves the selection and explicit all-off remains `[]`.
- Scan history displays matched, unrecognized, cache-hit and recognition-failed counts separately from enumerated/added/updated/removed counts.
- Mutation feedback uses the global Toast viewport, preserves the current page/filter, and reloads the changed projection without a full application refresh.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Viewer lacks `media_libraries.scan` | Hide retry/override/clear controls; API still returns 403 if called directly |
| Recognition request becomes stale | Abort/ignore it; do not flash results from the previous library/tab |
| TMDB is unavailable or returns no candidates | Keep the row unrecognized and show a recoverable Toast/empty candidate state |
| Override save fails validation | Keep the dialog selection and existing recognition; show the safe Server message |
| Built-in pack API returns an unknown code | Treat as contract error; do not silently display or submit it |
| Explicit pack selection is empty | Submit `[]` and keep both toggles off after reload/copy |

### 5. Good / Base / Bad Cases

- Good: an operator filters to unrecognized, retries one row, then selects a verified TMDB candidate; the row moves to matched/manual without losing the current library context.
- Base: no TMDB credential exists; file facts and unrecognized rows remain visible with a safe configuration message.
- Bad: load all recognitions and paginate in Vue, submit candidate title/category/confidence, expose a provider path for debugging, or let a viewer invoke a hidden mutation through an unprotected endpoint.

### 6. Tests Required

- Frontend tests cover tab/filter serialization, stale request cancellation, matched/unrecognized/manual rendering, scan counters, retry, candidate search, override, clear override, Toast feedback and permission-gated controls.
- Profile UI tests cover both built-in toggles, explicit empty selection, copy preservation, unknown-code rejection and unchanged read-only source content.
- Router/service tests cover permission matrices, strict override JSON, library-scoped tokens, no-store ordering, safe summaries and absence of path/provider/cache/private metadata fields.
- Run permission generation, Web UI tests/typecheck/lint/build and Server Go router/service tests.

### 7. Wrong vs Correct

#### Wrong

```ts
await api('/override', {
  title: candidate.title,
  category: selectedCategory,
  confidence: 1,
  providerPath: row.path,
})
```

#### Correct

```ts
await api(`/api/v1/media-libraries/${libraryID}/recognitions/${token}/override`, {
  method: 'PUT',
  body: JSON.stringify({ tmdb_id: candidate.id, media_type: candidate.media_type }),
})
// Server re-fetches and verifies metadata, then reclassifies atomically.
```
