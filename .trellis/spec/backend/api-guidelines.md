# API Guidelines

> REST, WebSocket, and proxy API rules for OhMyCine Server.

---

## Overview

Server APIs use REST under `/api/v1/`, WebSocket events for real-time updates, and a separate secured `/proxy/*` route for 302 playback. Server API design must preserve Player independence: Player can connect to Server for enhanced features but basic playback must remain usable without Server.

---

## Standard REST Envelope

Use this response shape for normal REST endpoints:

```json
{
  "code": 0,
  "message": "success",
  "data": {}
}
```

Rules:

- `code = 0` means success.
- Non-zero `code` means application error.
- `message` must be client-safe and must not include secrets, raw SQL, stack traces, cookies, proxy URLs with tokens, or upstream credential details.
- Validation errors should identify fields without echoing sensitive values.

Exceptions:

- `/api/v1/health` may return minimal status without the full envelope if documented.
- `/proxy/*` is a playback route, not a JSON REST endpoint.
- WebSocket messages use event envelopes.

---

## Authentication Defaults

- `/api/v1/auth/login`: anonymous, rate limited.
- `/api/v1/health`: anonymous, minimal information only.
- `/api/v1/*`: authenticated by default.
- `/ws/events`: authenticated.
- `/proxy/*`: signed URL or authenticated by default; trusted-LAN mode must be explicit configuration.

Never add an unauthenticated management endpoint without an explicit security rationale.

---

## Route Conventions

Use resource-oriented endpoints:

- `GET /api/v1/connections`
- `POST /api/v1/connections`
- `PUT /api/v1/connections/{id}`
- `DELETE /api/v1/connections/{id}`
- `POST /api/v1/connections/{id}/test`

Use action routes only when the action is not a simple CRUD update, for example:

- `POST /api/v1/strm/sync/incremental`
- `POST /api/v1/follows/{id}/pause`
- `POST /api/v1/downloads/{id}/resume`

---

## Permission Rules

- Connection, destination, category, downloader, PT site, and system settings APIs are admin-only unless a task explicitly designs delegated permissions.
- Regular users can access shared media but only see and control their own download and follow tasks.
- Readonly users can browse/play allowed media but cannot mutate configuration, files, downloads, or follows.
- API-level permission checks are mandatory; page visibility alone is not security.

---

## WebSocket Events

Use event messages shaped as:

```json
{"type":"download.progress","data":{"task_id":"...","progress":45.2}}
```

Rules:

- Authenticate connections.
- Filter events by user permissions.
- Do not push credentials or signed proxy secrets.
- Rate-limit high-frequency progress events.
- Use stable event names such as `download.progress`, `transfer.completed`, `strm.progress`, `media.added`, `follow.new_episode`, `site.status_changed`.

---

## Config Sync API

Player ↔ Server sync is high risk.

Default mode is structural sync only:

- Sync names, types, URLs, paths, media library IDs.
- Do not sync API keys, cookies, passwords, AI keys, downloader credentials, or PT passkeys unless the user explicitly chooses full sync.
- Do not overwrite same-URL different credentials automatically.
- Deletion sync must not cascade silently.

---

## Proxy Route Rules

`GET /proxy/{driver}/{path...}` must not be publicly open by default.

- Prefer signed URLs in STRM files.
- Validate expiration and HMAC signature before looking up upstream URLs.
- Normalize paths and reject traversal, repeated encoding tricks, and symlink escape for local paths.
- Cache upstream URLs only within their expiry and with permission context in the cache key.
- Do not log upstream CDN token URLs.

---

## OpenAPI

When `server/api/openapi.yaml` exists, update it in the same task as API behavior changes. Keep examples redacted and aligned with SQLite default configuration.