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
- CloudDrive2/WebDAV credentials.
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