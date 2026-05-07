# Backend Logging Guidelines

> Logging conventions for OhMyCine backend code.

---

## Overview

Use structured logging with zerolog for Server backend code. Logs must help diagnose automation flows without exposing credentials or private proxy URLs.

---

## Log Levels

- `debug`: detailed development diagnostics, disabled or minimized in production.
- `info`: lifecycle and successful high-level operations, such as server start, task scheduled, sync completed.
- `warn`: recoverable failures, skipped items, upstream temporary errors, partial sync failures.
- `error`: failed requests/jobs that require user attention or retry handling.

Do not use fatal exits outside process bootstrap. Return errors from libraries/services instead.

---

## Structured Logging

Prefer structured fields over formatted strings:

- `request_id`
- `user_id`
- `task_id`
- `destination_id`
- `connection_type`
- `driver`
- `site`
- `status`
- `duration_ms`

Use stable field names so logs can be filtered by CLI/diagnostic tools.

---

## What to Log

Log these events with safe metadata:

- Server startup/shutdown and configuration source.
- Login success/failure without passwords.
- Connection/downloader/site test results without credentials.
- Download task lifecycle changes.
- Transfer task lifecycle changes.
- STRM sync and cleanup counts.
- Emby/Jellyfin refresh attempts and results.
- Proxy authorization failures without full signed/CDN URLs.
- Plugin install/enable/update/delete when implemented.
- Security-sensitive configuration changes with actor and resource ID.

---

## What NOT to Log

Never log plaintext or unredacted:

- `Authorization` headers.
- Cookies.
- API keys.
- PT passkeys.
- JWTs or refresh tokens.
- Downloader passwords.
- AI API keys.
- 115/OpenList/CloudDrive2 credentials.
- Signed proxy URLs with `sig`/`exp` when the signature can be reused.
- Upstream CDN URLs containing token query parameters.
- Full request/response bodies that may contain credentials.

Use redaction like `sk-***redacted***` or `***redacted***`.

---

## Request Logging

Request logs should include method, normalized route, status, duration, request ID, and authenticated user ID if available. Avoid logging raw query strings for routes that may carry signatures or tokens.

---

## Common Mistakes

- Logging full connection config JSON.
- Logging upstream redirect `Location` headers for cloud media.
- Emitting too many progress logs for downloader/STRM loops.
- Logging local absolute paths in AI-related telemetry.
- Using unstructured `fmt.Println` in services.