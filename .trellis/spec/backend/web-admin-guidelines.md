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
