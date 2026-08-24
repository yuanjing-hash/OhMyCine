# PT Discovery Contract

## Scope / Trigger

Apply this contract when changing built-in PT adapters, site persistence/management, aggregated search, SSE search output, result claims, or PT-to-download handoff. PT sites are Server-native integrations; non-PT content sites remain plugin capabilities.

## Signatures

```text
pkg/site.Adapter
  Kind() string
  Test(context.Context, Config) (Health, error)
  Search(context.Context, Config, Query) (Page, error)
  Download(context.Context, Config, torrentID) ([]byte, filename, error)

GET    /api/v1/sites
POST   /api/v1/sites
PATCH  /api/v1/sites/:id
DELETE /api/v1/sites/:id
POST   /api/v1/sites/:id/test
GET    /api/v1/discovery/pt-search
GET    /api/v1/discovery/pt-search/stream
POST   /api/v1/discovery/downloads
```

## Contracts

- Keep provider paths, category mappings, login-page detection, HTML parsing, and torrent quirks inside the concrete adapter. Services depend only on `pkg/site.Adapter`.
- Site Cookie/passkey are encrypted with AES-256-GCM using purpose/AAD bound to site ID and adapter kind. DTOs return only `credential_configured`; ciphertext and plaintext never enter REST, SSE, logs, audit details, or job payloads.
- Candidate create/update performs network validation outside a database transaction. Update replaces the stored credential and policy with revision CAS only after validation succeeds; failure preserves the old record.
- A disable-only update is the deliberate exception to candidate probing: it uses revision CAS and audit locally so an expired Cookie or unavailable tracker cannot prevent an administrator from disabling the site. Re-enabling still requires a successful probe.
- User-configured site roots require HTTPS with no userinfo, query, or fragment. Adapter clients set bounded timeout, redirect count, response size, and strict same-origin checks including port.
- Multi-site search has a global concurrency bound and per-site rate limiter. One site failure produces only that site's error group. SSE group writes are serialized, and JSON fallback returns the same DTO.
- Browser results contain a 256-bit opaque claim, not a torrent URL or provider identity. The claim binds actor, site, torrent identity, and a short expiry.
- Download confirmation atomically reserves a claim. Concurrent consumers receive the generic expired/unavailable result. Provider or `DownloadService` failure releases the reservation only until its original expiry; success permanently consumes it.
- PT downloads call the existing `DownloadService.Submit` with a bounded torrent source. They do not create a parallel queue, transfer path, media-library selector, or naming implementation.
- Management remains system-admin-only in the first release even though stable `sites.*` permission codes exist for future delegation. Search requires `discovery.read`; download requires `downloads.create`.

## Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Wrong/expired Cookie | Stable authentication error; candidate config is not saved |
| Enabled site is unavailable and administrator disables it | Disable locally with revision CAS; do not send the broken credential again |
| One site times out | Other site groups continue and remain downloadable |
| HTML/login page returned for torrent | Reject as invalid response; never pass it to downloader |
| Claim used by another actor, expired, or in-flight | Return the same generic expired result without revealing which check failed |
| Provider/download handoff fails | Restore the claim only if its original TTL remains valid |
| SSE client disconnects | Stop emitting and do not write provider errors or sensitive context |

## Tests Required

- Adapter fixtures cover authenticated page, login page, result parsing, malformed-row skip count, next page, FREE promotion, size/peer counts, valid torrent, and HTML/error response.
- Service tests cover ciphertext/redaction, candidate-update preservation, site failure isolation, actor/expiry binding, atomic concurrent claim reservation, retry restoration, and normal `DownloadService` task creation.
- HTTP tests cover authentication, CSRF, no-store, redacted CRUD responses, JSON/SSE result parity, and absence of torrent IDs/credentials.
- Frontend tests cover public query construction, grouped result replacement, and explicit page append; run test, typecheck, lint, and build.

## Wrong vs Correct

Wrong: send a passkey-bearing `download.php` URL to the browser and let the UI POST it back.

Correct: return an opaque actor-bound claim, resolve and fetch the torrent inside Server, then submit the bytes through `DownloadService`.
