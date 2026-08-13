# Backend Security Guidelines

> Mandatory security rules for OhMyCine Server, CLI integrations, and backend-adjacent design.

---

## Overview

OhMyCine handles sensitive media-server keys, cloud-drive credentials, PT cookies/passkeys, downloader passwords, AI API keys, JWTs, and proxy URLs. Security defaults must be safe for self-hosted users and local development.

Consult this guide before implementing credentials, 302 proxy, config sync, plugins, file operations, external HTTP clients, AI integrations, or deployment examples.

---

## Authentication and Sessions

- Server management APIs require authentication by default.
- Use bcrypt or argon2id for password hashes.
- Reject default weak JWT secrets such as `change-me` in production mode.
- Access tokens should be short-lived; refresh/device tokens should be revocable.
- Login must be rate-limited.
- Initial admin setup must force a secure password path; never ship a silently usable default admin password.

---

## Credential Storage

Sensitive fields include:

- Emby/Jellyfin API keys.
- OpenList/Alist tokens, usernames, and passwords.
- CloudDrive2 API Tokens.
- Generic WebDAV usernames and passwords.
- 115 cookies and API proxy credentials.
- PT cookies, passkeys, and user IDs.
- qBittorrent/Transmission credentials.
- AI provider API keys.
- JWT/session secrets.

Rules:

- Encrypt sensitive config at rest with AES-256-GCM or an approved equivalent.
- Master keys come from environment, secret file, or generated local key file.
- Master keys are never logged, returned by APIs, or committed.
- Exported configs are redacted by default. Full export requires explicit confirmation.
- API responses must not include sensitive plaintext or encrypted blobs unless explicitly designed as backup export.
- Player Rust storage must use the shared storage layout. Windows standard mode stores app databases under LocalAppData and DPAPI-wraps the credential master key; portable mode uses EXE-adjacent data with an explicit reduced-protection warning.
- Legacy Player storage migration is file/key allowlisted and never overwrites newer target data. It runs only in standard mode; portable mode never imports standard-profile, legacy Roaming, or shared WebView localStorage data automatically.

---

## 302 Proxy Security

`/proxy/*` is high risk and must not be naked public access by default.

Supported modes:

1. `signed-url` for STRM playback.
2. `authenticated` for Player direct playback through Server.
3. `trusted-lan` only when explicitly configured.

Signed URL requirements:

- Include expiration.
- Sign method + normalized path + expiration + optional scope using HMAC-SHA256 or equivalent.
- Reject expired or invalid signatures.
- Normalize paths before signature verification and upstream lookup.
- Optionally bind to library/user/client scope when available.

URL cache requirements:

- TTL must not exceed upstream URL expiry.
- Cache key includes driver, path, and permission context.
- Cached CDN URLs and token query params are never logged.
- Cache hit still requires proxy authorization.

---

## File and Path Safety

All local file operations must:

- Operate under configured roots only.
- Canonicalize paths before use.
- Reject `..`, repeated-encoding traversal, and symlink escape.
- Handle Windows separators, drive letters, and UNC paths when implementing cross-platform behavior.
- Avoid overwriting existing targets by default.

Transfer modes:

- `move`: default safe behavior; do not overwrite unless configured.
- `copy`: check destination space where feasible.
- `hardlink`: do not silently fall back to copy across filesystems without user consent.
- `symlink`: admin-only by default because of escape risk.
- delete/cleanup: require confirmation or dry-run when destructive.

STRM cleanup must:

- Delete only `.strm` files under configured STRM roots.
- Not follow symlinks outside roots.
- Support dry-run preview.
- Record the files considered/deleted without exposing credentials.

### Server directory picker

- Server Web administration must browse the Server process filesystem through authenticated APIs; browser-native file pickers represent the client device and are not a substitute.
- Protect root and child enumeration with the independent sensitive permission `storages.browse` at both route middleware and service policy. Owner, administrator, and operator receive it by default; viewer does not.
- Enumerate only one directory level per request, return directories only, cap and sort results, apply cancellation/timeouts plus per-actor rate/concurrency limits, and use `Cache-Control: no-store`.
- Windows roots are process-visible logical/mapped drives. Unix/NAS/Docker roots and mounts are only those visible in the process namespace. Never fabricate an unmounted host path.
- Navigation and selection use short-lived signed opaque tokens bound to purpose, platform, and adapter version. Clients never join separators, `..`, drive letters, hostnames, or shares to create the next request.
- Reject symlinks, junctions, mount-point Reparse Points, and other Reparse Point children for entry and selection. Saving a selected root always repeats canonicalization, uniqueness, and the existing read-only probe.
- Browse logs, audit metadata, and safe errors must not contain absolute paths, child names, or raw OS errors. A picker response may include only the current interaction's displayed paths and names.

---

## External HTTP and SSRF Defense

Use a controlled HTTP client for external calls:

- Set timeouts.
- Limit redirects.
- Limit response size for metadata/probe calls.
- Allow only expected schemes (`http`, `https`, WebDAV equivalents).
- Reject `file://`, `gopher://`, and unexpected schemes.
- Treat user-configured URLs as privileged admin configuration; ordinary user inputs must not be able to probe internal management addresses.
- Plugins and site/cloud adapters should route network calls through the same controlled client when plugin architecture exists.
- Player subtitle providers use provider-specific controlled native clients. OpenSubtitles API Keys and optional account passwords stay in the credential boundary; account sessions stay in process memory only. Shooter/Xunlei local file paths are used only by Rust to compute bounded content hashes and are never sent externally. Xunlei name search sends only the selected media/file/custom keyword to the fixed HTTPS `api-shoulei-ssl.xunlei.com/oracle/subtitle` endpoint; remote playback URLs and headers remain inside Rust.
- Subtitle downloads allow only fixed trusted provider domains, bounded redirects/response sizes, allowlisted subtitle extensions, generated cache filenames, and the shared Player `cache/subtitles` directory. Shooter/Xunlei download URLs remain in a short-lived Rust map while Vue holds opaque references. Xunlei downloads are restricted to the allowlisted HTTPS `subtitle.v.geilijiasu.com` host.
- Player updates use the Tauri updater minisign trust root. Commit only the public key; keep the private key outside the repository and in GitHub Actions Secret storage, require signed artifacts, and fail release builds when the secret is absent.
- Update discovery is pinned to the HTTPS `yuanjing-hash/OhMyCine` GitHub Releases API and exact release-asset manifest path. Do not expose custom updater URLs. Portable updates may target the current executable directory but must not delete `portable.flag` or portable data directories.

---

## Config Sync Security

Default Player ↔ Server sync is structural only.

Do sync by default:

- Data source name/type.
- URL/base URL.
- paths, media library IDs, ordering, display metadata.

Do not sync by default:

- API keys, cookies, passwords, AI keys, PT passkeys, downloader passwords.

Full credential sync requires explicit user confirmation and clear destination disclosure.

---

## Plugin and Hub Security

Hub is a distribution site, not a trusted runtime backend. Third-party plugins are untrusted by default.

Rules:

- Do not auto-install or auto-update plugins by default.
- Show plugin permissions before install/update.
- Do not grant plugins global credential access.
- Record plugin install/enable/update/delete in audit logs.
- Prefer WASM or external-process isolation for long-term plugin runtime. Go plugin loading may remain a candidate but must not be treated as a settled safe default.
- High-risk permissions include arbitrary network access, file deletion, credential read, system command execution, and user/permission mutation.

---

## AI Data Boundary

AI features are primarily Player-side unless explicitly designed otherwise.

Server-side AI work, if introduced, must:

- Store AI keys as credentials.
- Avoid sending local absolute paths, credentials, proxy URLs, cookies, or passkeys to LLM providers by default.
- Keep recommendations constrained to media the user owns/has indexed.
- Never allow AI to directly delete files, submit downloads, or change configuration without explicit user action.

---

## Logs and Audit

Security-relevant events should be auditable:

- Login success/failure.
- User and permission changes.
- Connection/downloader/site/storage/category changes.
- Download/follow creation and deletion.
- File delete/move/rename.
- STRM cleanup.
- Plugin install/enable/update/delete.
- Proxy authorization failures.

Audit logs must not include sensitive field values.

---

## Scenario: Registered Local Storage Roots

### 1. Scope / Trigger

- Trigger: adding or changing local Storage registration, probing, path-based media access, Windows filesystem handling, or the Storage administration API/UI.
- Applies across the explicit SQLite migration, Storage model/service, local driver, Gin routes, permission catalog, audit metadata, and Web UI controls.
- A registered Storage is a physical access boundary. It is not a MediaLibrary, StorageDestination, scan request, or authorization to modify files beneath the root.

### 2. Signatures

- Table: `storages`; `type` is currently restricted to `local`; nullable `connection_id` is reserved for the future Connection migration and has no premature foreign key.
- Management APIs:
  - `GET /api/v1/storages`
  - `POST /api/v1/storages`
  - `PATCH /api/v1/storages/{id}`
  - `DELETE /api/v1/storages/{id}`
  - `POST /api/v1/storages/{id}/test`
- Stable permissions: `storages.read`, `storages.create`, `storages.update`, `storages.delete`, and `storages.test`.
- Driver entry points: `LocalDriver.CanonicalizeRoot(input string)`, `LocalDriver.ProbeRoot(root string)`, and `storage.Constrain(root, candidate string)`.
- Stable path errors: `storage_path_not_absolute`, `storage_path_not_found`, `storage_path_not_directory`, `storage_path_reparse_point`, `storage_path_outside_root`, and `storage_unreadable`.

### 3. Contracts

- Persist a canonical absolute root plus a comparison form. Windows comparison is case-insensitive and must deliberately support drive-letter and UNC roots.
- Registration validates only the configured root. Reject a root that is a symlink, junction, mount point, or other Reparse Point. Future traversal must still re-check every traversed component before any file operation.
- Probe is bounded and read-only: open the root, read at most one directory entry, close it, and query capacity. Never create a sentinel file to infer writability and never recursively enumerate media during Storage registration/test.
- On Windows, capacity reports bytes available to the current caller (`GetDiskFreeSpaceEx` caller-available value), not volume-wide free bytes that may ignore quotas.
- Driver capabilities are generated by the driver and stored as a snapshot. The client cannot claim unsupported cloud, offline-download, direct-URL, signed-proxy, cursor, or watch capabilities.
- Create/update/test/delete are independently permission-protected in the router and service. The Web UI reuses generated permission constants, but hiding a control never replaces API authorization.
- Delete removes the Storage configuration only. It must not enumerate, rename, move, overwrite, or delete any child path.
- Audit metadata may record resource ID, type, enabled state, outcome, and stable error code. It must not contain the configured absolute root, child names, raw OS errors, ACLs, or usernames.
- Database unique constraints are the concurrency authority. Preflight checks improve UX, but create/update races must map SQLite uniqueness failures to stable `storage_name_conflict` or `storage_path_conflict` responses instead of leaking SQL or returning a generic 500.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Empty or relative root | Reject with `storage_path_not_absolute`; create no record |
| Missing root | Reject with `storage_path_not_found` |
| Root resolves to a file | Reject with `storage_path_not_directory` |
| Root is a symlink/junction/Reparse Point | Reject with `storage_path_reparse_point` |
| Candidate is the root itself or a normalized descendant | Accept from `Constrain` |
| Candidate is a sibling, another volume/share, or escapes with `..` | Reject with `storage_path_outside_root` |
| Directory can be opened but capacity is unavailable | Preserve readable/available state and return `storage_capacity_unknown` with null capacity values |
| Duplicate normalized name/root is submitted concurrently | Return stable conflict code and HTTP 409 |
| Actor lacks any one Storage action permission | The matching API returns 403 even if another Storage permission is present |
| Storage configuration is deleted | Remove only the database row; filesystem contents and timestamps remain unchanged |

### 5. Good/Base/Bad Cases

- Good: an authorized operator registers an existing Windows or UNC directory, sees a bounded health/capacity summary, and the audit event contains no absolute path.
- Good: a second concurrent request loses the unique race and receives `storage_path_conflict`, while the Server log retains internal diagnostic context without exposing it through the API.
- Base: capacity lookup fails after the directory was read successfully; the Storage remains registered/readable and reports an explicit unknown-capacity state.
- Bad: the probe creates and deletes a temporary file in the user's real media root to test write access.
- Bad: deleting a Storage record recursively cleans the root, or audit metadata stores `root_path`/a child filename.
- Bad: Windows path comparison is case-sensitive, or a sibling prefix such as `D:\\media-old` is accepted beneath `D:\\media`.

### 6. Tests Required

- Migration tests cover fresh install, idempotency, and an actual previous-version database upgrading to the Storage migration.
- Local driver tests cover relative/missing/file roots, root symlink/Reparse Point rejection, empty and non-empty read-only probes, root/descendant/sibling traversal, and no file mutation.
- Windows tests cover drive-letter and UNC acceptance, case-insensitive comparison, caller-available capacity behavior where injectable, and explicit skip reporting when the test process cannot create a symlink.
- Service tests cover name/root normalization, duplicate preflight and unique-race mapping, stable safe errors, capabilities, audit redaction, and delete-config-only behavior.
- Router tests cover authentication plus independent denial for list/create/update/delete/test permissions; frontend tests cover route/navigation/action visibility from generated constants.
- Windows PowerShell 5 live/API tests that send non-ASCII names or paths must encode `ConvertTo-Json` output with `[Text.Encoding]::UTF8.GetBytes(...)` and declare `application/json; charset=utf-8`. Passing a Unicode JSON `String` directly to `Invoke-RestMethod -Body` may use the system code page and falsely surface a valid directory as `storage_unreadable`.
- Run `server/test.ps1`, `go build -tags webui ./cmd/server`, root and Web UI `go mod verify`, and `git diff --check`.

### 7. Wrong vs Correct

#### Wrong

```go
// Mutates the user's media root and still does not prove future writes are safe.
probe := filepath.Join(root, ".ohmycine-write-test")
if err := os.WriteFile(probe, nil, 0o600); err != nil {
    return err
}
_ = os.Remove(probe)
```

```go
// Prefix checks accept siblings such as D:\\media-old.
if !strings.HasPrefix(candidate, root) {
    return ErrOutsideRoot
}
```

#### Correct

```go
canonical, err := localDriver.CanonicalizeRoot(input)
if err != nil {
    return mapSafeStoragePathError(err)
}
probe := localDriver.ProbeRoot(canonical) // bounded read + capacity only
```

```go
relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
if err != nil || relative == ".." ||
    strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
    filepath.IsAbs(relative) {
    return ErrOutsideRoot
}
```
