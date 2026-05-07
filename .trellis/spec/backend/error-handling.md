# Backend Error Handling

> How errors are propagated, logged, and returned by OhMyCine backend code.

---

## Overview

Backend errors must preserve diagnostic detail internally while returning safe, stable responses to clients. Do not leak credentials, upstream token URLs, raw SQL, filesystem internals, or stack traces through API messages.

---

## Error Types

Use domain-aware errors where helpful:

- Validation errors for malformed input.
- Authentication errors for missing/invalid credentials.
- Authorization errors for insufficient permission.
- Not-found errors for missing resources.
- Conflict errors for duplicate names, existing target files, or sync conflicts.
- External service errors for Emby/Jellyfin, OpenList/Alist, CloudDrive2, 115, PT sites, downloaders, TMDB, AI providers.
- Internal errors for unexpected failures.

Wrap errors with context using Go `%w`, but sanitize before returning to clients.

---

## Error Handling Patterns

- Functions that call external services or perform file/database work must return `error`.
- Use `context.Context` and handle cancellation/timeouts distinctly when useful.
- Do not `panic` for normal request failures.
- Do not ignore errors from filesystem, database, encryption, or network calls.
- For batch operations such as STRM generation, continue per item where appropriate and collect/report item failures without aborting the entire job unnecessarily.

---

## API Error Responses

Use the standard envelope for REST errors:

```json
{
  "code": 40001,
  "message": "invalid request",
  "data": null
}
```

Rules:

- Map validation to 400.
- Map authentication to 401.
- Map authorization to 403.
- Map missing resources to 404.
- Map conflicts to 409.
- Map upstream timeout/unavailable to 502/503/504 as appropriate.
- Map unexpected failures to 500 with a generic message.
- Include field-level validation details only when they are safe.

---

## Logging Errors

- Log server-side diagnostic details with redaction.
- Include request ID/task ID/resource ID when available.
- Log upstream service name, not credentials.
- Do not log `Authorization`, cookies, passkeys, API keys, JWTs, downloader passwords, AI keys, or CDN URLs containing token query parameters.

---

## Retry and Partial Failure

- Retrying is appropriate for transient external service failures and downloader polling.
- Do not retry authentication failures blindly.
- File transfer retries must avoid duplicate/partial target corruption.
- STRM cleanup should support dry-run and report skipped/deleted counts.

---

## Common Mistakes

- Returning `err.Error()` directly to API clients for upstream or internal errors.
- Logging raw request bodies containing credentials.
- Treating context cancellation as a successful operation.
- Hiding all item-level failures in long-running batch jobs.
- Using panic/recover as normal control flow.