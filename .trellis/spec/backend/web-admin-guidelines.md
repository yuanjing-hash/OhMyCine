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

### 3. Contracts

- The first owner is created only when no user exists. Owner creation, administrator-role assignment, initial session creation, and audit recording are one transaction and are guarded against concurrent setup attempts.
- Owner identity is distinct from a role. The owner cannot be deleted, disabled, or implicitly transferred. The protected `administrator` role cannot be deleted.
- User role replacement, role permission replacement, last-administrator checks, privilege-escalation checks, session revocation, and audit writes occur inside the same transaction as the mutation.
- A non-owner may grant only permissions already present in the actor's effective permission set. Multiple roles are additive; MVP has no deny, inheritance, arbitrary permission creation, or resource-instance scope.
- Vue route meta, navigation filtering, and buttons reuse generated permission codes. They improve UX only; every protected API and sensitive service operation enforces authorization again on the server.
- Browser management uses a revocable server-side session in an HttpOnly, host-only, SameSite cookie. Do not store browser JWT/session tokens in localStorage, Pinia persistence, URLs, logs, or audit metadata.
- State-changing cookie-authenticated requests require a session-bound CSRF header, allowed Origin/Referer validation, safe Fetch Metadata, and an exact JSON media type. Setup and login are also Origin-checked and rate-limited.
- Authentication/setup responses use `Cache-Control: no-store`. Password reset requires the actor's current password; password changes and account disablement revoke affected sessions.
- Production Web UI assets are built before `go build -tags webui`. Default Go builds/tests must not require `dist` to exist. SPA fallback applies only to HTML navigation; missing assets and API routes return real 404 responses.
- The root Server module and `server/webui` module must each keep a tidy `go.mod`/`go.sum`. The root module's Go directive must satisfy dependency minimums; currently this is Go 1.23+.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Setup requested after any user exists | Reject with a stable conflict/setup-closed error |
| Two setup requests race | At most one owner transaction succeeds |
| Unknown username login | Perform a dummy password hash comparison before returning the same safe login error |
| Missing/expired/revoked session | Return 401 and clear frontend auth state |
| Permission changed while a session is active | Next authorization uses current database permissions; frontend refreshes `/auth/me` after 403 |
| User lacks a page/button permission | Hide or disable the UI affordance; direct API access still returns 403 |
| Actor grants a permission they do not hold | Reject inside the mutation transaction |
| Mutation would leave zero active administrators | Reject with `LAST_ADMIN_REQUIRED` or equivalent stable error |
| Mutation targets the owner for disable/delete/implicit transfer | Reject server-side even if the UI hides the action |
| Mutation has `application/jsonp` or another non-JSON media type | Return 415/400; do not accept prefix matches |
| Origin contains credentials, paths, query, fragment, or unsupported scheme | Reject configuration/startup validation |
| `go test ./...` discovers packages under `webui/node_modules` | Treat as a module-boundary regression; restore the nested Web UI module |

### 5. Good/Base/Bad Cases

- Good: a custom operator sees users but not role editing; the users page loads without requiring `roles.read`, role controls remain unavailable, and direct role API access returns 403.
- Good: an administrator edits a custom role only within their own permission set; the transaction updates permissions, authorization revision/session state, and audit metadata atomically.
- Base: planned media modules appear as clearly unavailable navigation states until their real APIs exist; no placeholder endpoint reports fake success.
- Bad: a button is hidden with `v-if`, but the matching POST/DELETE route has no permission middleware or service policy.
- Bad: last-admin or privilege-escalation checks run before the transaction, allowing concurrent requests to invalidate the decision.
- Bad: the Web UI stores a JWT in localStorage or commits built `node_modules`/session material.

### 6. Tests Required

- Backend integration tests must cover first-owner setup, concurrent/closed setup, login/session/CSRF, viewer authorization, permission refresh, self/owner protection, last-administrator protection, and privilege-escalation rejection.
- Run from `server/`: `go test ./...`, `go vet ./...`, `go build ./cmd/server`, `go build -tags webui ./cmd/server`, and `go mod verify`.
- Run from `server/webui/`: `go test .`, `go mod verify`, `npm run permissions:check`, `npm run typecheck`, `npm run lint`, and `npm run build`.
- `go list ./...` from the root Server module must list only OhMyCine Server packages and must not include packages from `node_modules`.
- The permission generation check must fail on duplicate/invalid permission codes or generated TypeScript identifier collisions.
- Review browser responses for cookie flags, `Cache-Control: no-store`, stable error codes, CSP/security headers, and no credential/session leakage.

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
- Default startup uses `OMC_ENV=production`, an embedded Web UI, loopback listening, and one foreground Go process. The launcher must use `exec` so Ctrl+C, systemd, and container signals reach the Server directly.
- Runtime data is persistent. The launcher must never delete, reset, replace, or silently migrate an existing database outside normal Server migrations.
- `server/.runtime/`, `webui/dist/`, `webui/node_modules/`, generated binaries, and SQLite journal/WAL/SHM files are Git-ignored.
- Run `npm ci` only when `node_modules` is absent or the committed lockfile differs from the stored dependency stamp. Never use an untrusted Windows `node.exe`/`npm` from WSL.
- `--skip-build` requires an existing executable binary and reuses it without modifying the binary or database.
- User-provided `OMC_*` values take precedence. Wildcard listen addresses map to loopback only for the default browser origin; LAN/domain/reverse-proxy use requires an explicit exact `OMC_PUBLIC_ORIGIN`.
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
