---
name: security-design
description: Use this skill when designing, reviewing, or implementing OhMyCine security-sensitive features, including authentication, authorization, credential storage, Player secure storage, Server encrypted config, 302 proxy, signed STRM URLs, config sync, file operations, plugin security, AI provider handling, WebSocket permissions, Docker exposure, or audit logging.
---

# Security Design Skill

## When to use

Use this skill whenever a task touches security-sensitive areas:

- login, sessions, JWT, device authorization
- roles, permissions, multi-user isolation
- API keys, cookies, passwords, passkeys, downloader credentials, AI keys
- Player secure storage or config export/import
- Server encrypted config or database credential storage
- Player ↔ Server config sync
- `/proxy/*`, 302 direct playback, signed STRM URLs, URL cache
- local file operations: move, copy, hardlink, symlink, delete, STRM cleanup
- external HTTP clients, SSRF risk, custom Base URL handling
- plugin install/update/runtime permissions
- WebSocket events and user scoping
- Docker port exposure, mounted volumes, public deployment
- audit logs and log redaction

This skill should also be used during security review of architecture docs or implementation plans.

## Core security model

OhMyCine is self-hosted, but self-hosted does not mean everything is trusted. Treat these as sensitive assets:

- Emby/Jellyfin API keys
- 115 cookies
- OpenList/Alist tokens or passwords
- CloudDrive2/WebDAV credentials
- PT cookies, passkeys, user IDs
- qBittorrent/Transmission credentials
- AI provider API keys
- JWT/session tokens
- 302 proxy URLs and upstream CDN URLs
- local config files and SQLite databases
- plugin code and plugin manifests

Trust boundaries:

- Player and Server are user-trusted components, but saved external credentials still need secure storage.
- External data sources, PT sites, cloud APIs, plugins, and AI providers are not trusted by default.
- Server management API requires auth by default.
- 302 proxy must not be publicly open by default.
- Config sync must not copy sensitive fields unless explicitly confirmed.

## Authentication and authorization

Server expectations:

- Login endpoint may be anonymous but must be rate-limited.
- `/api/v1/health` may be anonymous but should return only basic status.
- `/api/v1/*` requires auth by default.
- `/ws/events` requires auth and user scoping.
- `/proxy/*` requires signed URL, authentication, or explicit trusted-LAN mode.

Roles:

- `admin`: manage all connections, users, downloaders, sites, cloud drives, settings
- `user`: use media library, create downloads/follows, view own tasks
- `readonly`: browse/play only

Permissions must be enforced server-side at API/service level, not just in UI.

## Credential storage

### Server

Sensitive fields should be encrypted at rest.

Recommended pattern:

- AES-256-GCM
- master key from environment, secret file, or first-start generated local key
- never log or return master key
- default config export redacts secrets
- explicit full export requires user confirmation

Sensitive fields include:

- `api_key`
- `cookie`
- `passkey`
- `password`
- `token`
- `jwt_secret`
- downloader credentials
- AI provider keys

### Player

Use OS secure storage where available:

- Windows: Credential Manager / DPAPI
- macOS: Keychain
- Linux: Secret Service / libsecret, otherwise warn about risk
- Android: Android Keystore
- iOS: Keychain

Plain config should store non-sensitive settings plus credential references, not raw credentials.

## Config sync rules

Player ↔ Server sync is high-risk.

Default sync should include:

- data source name
- type
- URL/base URL
- path/library IDs
- non-sensitive UI/config metadata

Default sync should not include:

- API keys
- cookies
- passwords
- PT passkeys
- downloader credentials
- AI API keys

Full credential sync requires explicit user confirmation and should be auditable.

Conflict handling:

- same name but different URL: ask user
- same URL but different credentials: do not auto-overwrite secrets
- deletes should not cascade by default

## 302 proxy and STRM security

`/proxy/{driver}/{path...}` must not be anonymously public by default.

Preferred modes:

1. `signed-url`: recommended for STRM files used by Emby/Jellyfin
2. `authenticated`: recommended for Player direct playback through Server
3. `trusted-lan`: optional convenience mode for local-only deployments

Signed STRM URL concept:

```text
/proxy/alist/media/movie.mkv?exp=<unix>&sig=<hmac>
```

Signature should cover:

```text
method + normalized path + exp + optional user/library scope
```

Requirements:

- reject expired signatures
- reject invalid signatures
- normalize paths before signing and lookup
- reject traversal and double-encoding bypasses
- do not cache upstream URLs beyond their real expiry
- still check outer proxy auth/signature on cache hit
- redact upstream CDN token URLs in logs

## File and path safety

All local file operations must:

- use configured root directories
- normalize paths
- prevent `..` traversal
- prevent symlink escape
- handle Windows drive letters, separators, and UNC paths deliberately
- avoid deleting outside configured STRM/library roots

Transfer strategy risks:

- `move`: do not overwrite by default
- `copy`: check disk space where feasible
- `hardlink`: do not silently downgrade to copy without explicit design/user expectation
- `symlink`: admin-only by default because of escape/confusion risk
- `delete`: confirm and audit

STRM cleanup should:

- operate only under configured STRM output roots
- target `.strm` files only
- not follow arbitrary symlinks
- support dry-run once implemented
- record what was removed

## External HTTP and SSRF

Any user-configurable URL or plugin/network request can become SSRF risk.

Use a controlled HTTP client for Server external calls:

- timeout
- redirect limit
- response size limit
- allowed schemes only: normally HTTP/HTTPS/WebDAV equivalents
- no `file://`, `gopher://`, unexpected protocols
- proxy settings only through admin-controlled config

Custom AI provider Base URLs and external plugin URLs should be treated as potentially sensitive user-controlled endpoints.

## Logging and audit

Never log raw:

- Authorization headers
- cookies
- API keys
- passkeys
- JWTs
- downloader passwords
- upstream CDN URLs with token parameters
- AI keys

Audit events should include:

- login success/failure
- user/permission changes
- connection changes
- downloader/site/cloud config changes
- download task creation/deletion
- file move/delete/rename
- STRM cleanup
- plugin install/enable/update/delete
- proxy signature failures and abnormal access

Audit logs must not include sensitive field values.

## Plugin security

Third-party plugins are untrusted by default.

Default policy:

- no automatic install
- no automatic update
- show permissions before install
- audit install/enable/update/delete
- no global credential access by default
- high-risk capabilities require explicit permissions

Preferred long-term isolation is WASM or external process. Go plugin may be considered a candidate but is risky because it runs in-process and has cross-platform limitations.

## AI safety

AI features are primarily Player-side.

- AI API keys belong in secure storage.
- Default LLM payloads should include media metadata, not local absolute paths or credentials.
- User should choose whether to send file names, overviews, watch history, or other sensitive context.
- AI should recommend only media already in the user's library.
- AI must not directly perform destructive operations.

## Docker and deployment

- Do not expose qBittorrent/Transmission Web UI publicly by default.
- Prefer binding admin-only auxiliary services to localhost or LAN.
- Server public deployment should be behind HTTPS reverse proxy.
- Mount only required directories, not host root.
- Separate data, config, logs, downloads, and STRM volumes.

## Review checklist

For any security-sensitive task, check:

- What secret or privileged resource is involved?
- Is the default safe for a self-hosted but internet-exposed deployment?
- Can a normal user access admin-only data?
- Can this leak credentials in logs, API responses, WebSocket events, config export, or AI prompts?
- Can a path escape the configured root?
- Can `/proxy/*` be abused as a public file jump service?
- Does config sync copy secrets unexpectedly?
- Does plugin or custom URL code bypass normal network controls?

## Documentation to consult

- `docs/architecture/07-security-design.md`
- `docs/architecture/02-server-design.md`
- `docs/architecture/03-player-design.md`
- `AGENTS.md`
- `DEVELOPMENT.md`
