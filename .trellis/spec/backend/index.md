# Backend Development Guidelines

> Long-lived implementation rules for OhMyCine Server and Go-based backend packages.

---

## Overview

OhMyCine Server is the optional self-hosted automation layer. It must never become a hard dependency for basic Player playback. Backend work should implement the media pipeline and storage/playback loop while preserving the final planned scope.

Core Server responsibilities:

- Manage Connections → Storage Destinations → Category Rules.
- Drive the Discover → Download → Transfer → Import → Notify pipeline.
- Support 115, OpenList/Alist, CloudDrive2, and local files early.
- Generate STRM files, provide secured 302 playback, and notify Emby/Jellyfin refreshes.
- Expose REST APIs under `/api/v1/` and WebSocket events for real-time updates.

---

## Pre-Development Checklist

Before changing backend code or API documentation:

1. Confirm the change belongs to Server/CLI backend and does not make Player basic playback depend on Server.
2. Read the relevant guide below, especially security rules for credentials, proxy routes, sync, plugins, file operations, and AI.
3. Use SQLite as the default local/self-hosted database target. Any PostgreSQL mention must be future optional deployment only.
4. Use `OpenList/Alist` in user-facing docs; code identifiers may use `alist` for API/ecosystem compatibility.
5. Keep handlers thin and place business logic in services.
6. Add or update OpenAPI when `api/openapi.yaml` exists and endpoint behavior changes.
7. If a feature status changes, update `docs/architecture/06-roadmap.md` in the same task.

---

## Guidelines Index

| Guide | Description | Status |
|-------|-------------|--------|
| [Directory Structure](./directory-structure.md) | Go package layout and ownership boundaries | Active |
| [Database Guidelines](./database-guidelines.md) | SQLite/GORM rules, migrations, transactions | Active |
| [API Guidelines](./api-guidelines.md) | REST, WebSocket, response envelopes, auth defaults | Active |
| [Error Handling](./error-handling.md) | Error propagation and client-safe responses | Active |
| [Security Guidelines](./security-guidelines.md) | Credentials, proxy, paths, sync, plugin, AI boundaries | Active |
| [Logging Guidelines](./logging-guidelines.md) | zerolog conventions and redaction rules | Active |
| [Quality Guidelines](./quality-guidelines.md) | Testing, linting, and forbidden patterns | Active |

---

## Quality Check

A backend change is not complete until:

- `go test ./...` passes for the relevant Go component when it exists.
- `golangci-lint run` passes when configured for the component.
- Security-sensitive paths are checked against [Security Guidelines](./security-guidelines.md).
- API responses follow the standard envelope unless the endpoint is explicitly non-REST (`/proxy/*`, WebSocket, health).
- New code uses `context.Context` for external calls and long-running operations.
- Sensitive values are encrypted at rest and redacted from logs.

---

**Language**: Trellis spec files are written in English. Product-facing architecture docs may remain Chinese.