---
name: server-pipeline
description: Use this skill when developing, designing, reviewing, or debugging OhMyCine Server media pipeline features, including 115/OpenList/CloudDrive2/local files, connections, storage destinations, category rules, STRM generation, 302 proxy, downloader integration, PT discovery, transfer, metadata, follow/subscribe, WebSocket events, or Emby/Jellyfin refresh.
---

# Server Pipeline Skill

## When to use

Use this skill for OhMyCine Server tasks involving:

- Server architecture or Go backend implementation
- connections, storage destinations, category rules
- 115, OpenList/Alist, CloudDrive2, local file drivers
- STRM generation, sync, cleanup, NFO/poster generation
- 302 proxy and direct-link playback
- Emby/Jellyfin media server clients and refresh notifications
- qBittorrent/Transmission downloader integration
- PT site management, discovery/search, one-click download
- transfer engine and media organization
- metadata scraping and filename parsing
- follow/subscribe engine for TV series
- REST API, WebSocket events, database models, scheduler jobs

Do not use this skill for Player-only UI/playback tasks unless they change Server integration contracts.

## Project context

OhMyCine Server is the self-hosted automation engine. It is not merely an API backend for Player. Its core pipeline is:

```text
Discover → Download → Transfer → Import → Notify
```

The critical Server abstraction is:

```text
Connections → Storage Destinations → Category Rules
```

## Roadmap and scope rules

Do not remove planned Server capabilities from project docs. Adjust implementation order only.

Initial Server work should prioritize the owner's must-have storage/playback loop:

- 115 cloud drive
- OpenList/Alist
- CloudDrive2
- local files
- STRM generation
- 302 proxy
- Emby/Jellyfin refresh
- basic Player ↔ Server sync surface

Keep these as later planned capabilities, not deleted scope:

- PT aggregation/search
- one-click download
- follow/subscribe
- AI-related integration if needed
- plugin system
- multi-user permissions
- larger cloud-drive driver list

## Three-layer architecture

### 1. Connections

Connections represent the ability to connect to an external service.

Examples:

- Emby/Jellyfin API
- OpenList/Alist
- CloudDrive2/WebDAV
- 115 cloud drive
- local filesystem roots
- qBittorrent/Transmission
- PT sites

Connections should own service credentials and connection health checks. They should not decide where media belongs.

### 2. Storage Destinations

Storage destinations represent final media placement.

Examples:

- movies on OpenList path `/media/movies`
- TV on 115 path `/tv`
- documentaries on local NAS path `/nas/docs`

Destinations may configure:

- local vs cloud type
- associated connection
- remote/local path
- STRM enabled/disabled
- STRM output root
- STRM proxy base URL

### 3. Category Rules

Category rules decide which media type goes to which destination and how it is named/transferred.

Rules include:

- media type: movie, tv, documentary, variety, etc.
- destination ID
- transfer mode: move, hardlink, copy, symlink
- directory template
- naming template
- match rules and priority

## Core flows

### Existing cloud/local media to STRM playback

```text
Cloud/local source → scan/list files → classify/parse metadata → generate STRM → Emby/Jellyfin scans STRM → playback request → 302 proxy → client streams from cloud/CDN
```

### One-click download flow

```text
Discovery result → classify → choose destination/download path → submit to downloader → track task → transfer on completion → STRM if needed → refresh media server → notify Player
```

### Follow/subscribe flow

```text
Follow task → scheduled search → detect missing episodes → apply site/quality/group filters → submit downloads → normal transfer/import pipeline
```

## Implementation workflow

1. Identify which pipeline stage is affected: connection, storage, classification, discovery, download, transfer, STRM, proxy, notify, follow.
2. Check `docs/architecture/02-server-design.md` and `docs/architecture/06-roadmap.md`.
3. If credentials, proxy, file paths, plugins, or user permissions are involved, check `docs/architecture/07-security-design.md`.
4. Preserve the three-layer separation.
5. Keep handlers thin; put business logic in services.
6. Use interfaces for external systems: cloud drivers, media server clients, download clients, site scrapers.
7. Ensure errors are observable and do not leak secrets.
8. Update API docs/OpenAPI when endpoint contracts change.
9. Update architecture docs when module boundaries or flows change.

## Package responsibilities

Planned package boundaries:

- `internal/handlers`: HTTP request parsing and response only
- `internal/services`: business logic and pipeline orchestration
- `internal/models`: GORM models
- `internal/middleware`: auth, CORS, logging, recovery
- `internal/scheduler`: cron jobs for follow and STRM sync
- `pkg/cloud`: cloud/local storage driver abstraction
- `pkg/mediaserver`: Emby/Jellyfin clients
- `pkg/downloader`: qBittorrent/Transmission clients
- `pkg/scraper`: PT site adapters
- `pkg/metadata`: TMDB, filename parsing, NFO helpers
- `pkg/proxy`: 302 proxy and URL cache
- `pkg/strm`: STRM generation and cleanup

## Security reminders

- Credentials in connection configs must be encrypted at rest.
- 115 cookies, PT cookies/passkeys, downloader passwords, and media-server API keys must not appear in logs or plain API responses.
- `/proxy/*` must not be anonymously public by default. Prefer signed STRM URLs or authenticated mode.
- Normalize and constrain local paths before file operations.
- Do not follow symlink escapes during cleanup or transfer.
- Hardlink failure should not silently downgrade to copy unless explicitly designed and user-approved.
- WebSocket events must be filtered by user permissions when multi-user support is active.

## API expectations

REST API routes use `/api/v1/`.

Response envelope:

```json
{
  "code": 0,
  "message": "success",
  "data": {}
}
```

Use WebSocket events for progress/status changes such as:

- download progress/completed
- transfer progress/completed
- STRM progress
- media added
- follow new episode
- site status changes

## Validation commands

Use only after the Server project exists:

```bash
cd server
go mod download
go run ./cmd/server
go test ./...
golangci-lint run
```

Run a single Go test:

```bash
cd server
go test ./internal/services -run TestName
```

Docker when files exist:

```bash
cd server
docker compose up -d
```

## Documentation to consult

- `docs/architecture/02-server-design.md`
- `docs/architecture/01-overview.md`
- `docs/architecture/06-roadmap.md`
- `docs/architecture/07-security-design.md`
- `DEVELOPMENT.md`
- `CLAUDE.md`
