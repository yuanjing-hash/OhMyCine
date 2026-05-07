# Backend Directory Structure

> How OhMyCine Server and Go backend code are organized.

---

## Overview

Backend code is Go 1.22+ and follows a layered layout:

- `cmd/` contains executable entry points only.
- `internal/` contains private application code: config, database, models, handlers, services, middleware, scheduler.
- `pkg/` contains reusable drivers/clients that may be shared by Server and CLI.
- `api/` contains OpenAPI when present.
- `configs/` contains example configuration, never real secrets.

The repository is design-first and some directories may still be placeholders. Do not assume a planned path exists until it is present in the working tree.

---

## Directory Layout

Target Server layout:

```text
server/
├── cmd/
│   └── server/              # main.go and process bootstrap only
├── internal/
│   ├── config/              # Viper/YAML config loading and validation
│   ├── database/            # SQLite connection and migrations
│   ├── models/              # GORM models
│   ├── handlers/            # thin Gin HTTP handlers
│   ├── services/            # business logic and orchestration
│   ├── middleware/          # auth, CORS, logging, recovery
│   └── scheduler/           # cron jobs for follow and STRM sync
├── pkg/
│   ├── cloud/               # cloud drive drivers and registry
│   ├── mediaserver/         # Emby/Jellyfin clients
│   ├── downloader/          # qBittorrent/Transmission clients
│   ├── scraper/             # PT site adapters
│   ├── metadata/            # TMDB and filename parsing
│   ├── proxy/               # secured 302 proxy engine
│   └── strm/                # STRM generation and cleanup
├── api/
├── configs/
└── go.mod
```

CLI code follows the same Go conventions and may reuse safe code from shared `pkg/` packages.

---

## Module Organization

### Handlers

Handlers must be thin. They may:

- Bind and validate request input.
- Get authenticated user context.
- Call a service method.
- Convert service results into the standard response envelope.

Handlers must not:

- Talk directly to external services.
- Implement media transfer, STRM generation, downloader polling, or metadata matching logic.
- Log secrets or raw request bodies containing credentials.

### Services

Services own business behavior:

- Connection management.
- Storage destinations and category rules.
- Discovery, download, transfer, STRM, metadata, follow tasks, notification.
- User permission decisions that handlers enforce.

Use transactions for multi-record changes and accept `context.Context` for all operations that may block.

### Drivers and clients

Reusable integrations live under `pkg/`:

- Cloud drivers implement a common `Driver` interface.
- Downloader clients implement a common `DownloadClient` interface.
- Media-server clients implement a common `MediaServerClient` interface.
- PT adapters implement a common `Site` interface.

Keep external API quirks inside the concrete driver/client. Services should depend on interfaces.

---

## Naming Conventions

- Package names are lowercase, short, and singular when possible: `proxy`, `strm`, `metadata`.
- Handler files are named by resource: `connection.go`, `destination.go`, `category.go`.
- Service files are named by domain: `transfer.go`, `follow.go`, `notify.go`.
- Use user-facing `OpenList/Alist` in docs and UI strings. Code may use `alist` for OpenList (Alist-compatible API) drivers/routes.
- API route resource names are plural where applicable: `/connections`, `/destinations`, `/downloads`.

---

## Component Boundaries

- Server is an enhancement/automation layer. Do not add Server-only requirements to Player's local playback path.
- Do not place Player-side AI recommendation logic in Server unless a task explicitly designs a Server-side AI feature.
- Hub is a static plugin distribution site, not a runtime backend.
- Docker files are deployment/CI artifacts, not local development prerequisites.

---

## Common Mistakes

- Putting business logic in Gin handlers instead of services.
- Creating driver-specific branches throughout services instead of using common interfaces.
- Treating Docker Compose as required for local WSL development.
- Using PostgreSQL-specific behavior in MVP code; SQLite is the default target.
- Describing OpenList support as only `Alist` in user-facing docs.