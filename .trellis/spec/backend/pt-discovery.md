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
POST   /api/v1/discovery/pt-results/recognize
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
- Quick recognition accepts only that opaque claim, resolves its server-side title without reserving or consuming it, and never downloads the torrent. It reuses the shared media-recognition parser before optional TMDB enrichment. Missing/unavailable/no-match metadata returns HTTP 200 with `status=unrecognized`, a stable error code, and safe parsed title/year/specifications; only permission and invalid/expired claims are request errors.
- Recognition posters use the existing authenticated same-origin Discovery image gateway. Raw TMDB image URLs are not a new browser contract.
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

## Scenario: CookieCloud Site Discovery

### 1. Scope / Trigger

- Trigger: changing CookieCloud domain matching, supported-site discovery, credential probing, sync summaries, runtime logs, or the Site-management sync UI.

### 2. Signatures

```text
POST /api/v1/settings/sites/cookiecloud/sync

CookieCloudSyncSummary {
  status: "success" | "partial"
  created: integer
  updated: integer
  skipped: integer
  skipped_unsupported_domains: integer
  skipped_missing_login_cookies: integer
  failed: integer
  issues?: [{ action: "create" | "update", site_id?: integer, kind: string, error_code: string }]
}
```

### 3. Contracts

- Each built-in discovery candidate uses the provider's canonical HTTPS origin, including the canonical host. Do not depend on an apex-to-`www` redirect because the PT client deliberately rejects cross-origin redirects.
- Cookie selection reproduces browser domain delivery for the fixed candidate host: merge every matching parent and exact-host domain, then let the more-specific domain override duplicate cookie names. Reject public-suffix-like single-label domains and return no candidate when the merged set contains only `cf_clearance`.
- Candidate validation happens before encrypted Site creation/update. Failure leaves existing credentials unchanged and returns only stable issue fields; Cookie values, domains, Base URLs, usernames, and upstream bodies never enter issues, logs, or audit metadata.
- A partial sync persists its first stable issue code in `last_sync_error_code`, records audit outcome `partial`, and emits the same safe code in the runtime terminal event. It must not call `ErrorCode(nil)` or collapse a known candidate failure into `INTERNAL_ERROR`.
- Sync summaries may count CookieCloud domains that do not match a configured or built-in supported host, but must label them as other CookieCloud domains rather than PT sites. They also count supported candidates without a usable authentication Cookie. API, audit, logs and UI never list the domains or Cookie names/values.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Canonical host accepts merged Cookie | Create/update the encrypted Site and increment the matching success count |
| Apex origin redirects to canonical host | Use the canonical host in the discovery registry; do not weaken strict redirect checks |
| Parent and exact-host domains contain the same cookie name | Exact-host value wins; emit one cookie pair |
| Candidate probe rejects authentication | Return `site_authentication_failed`, persist partial status, create no Site |
| Candidate probe is unavailable/rate-limited/malformed | Return the matching stable Site code without raw provider details |
| CookieCloud contains only Cloudflare clearance state | Do not probe or create a Site |

### 5. Good / Base / Bad Cases

- Good: `.pttime.org` authentication cookies are merged for `www.pttime.org`, probed against `https://www.pttime.org`, and create one encrypted PTTime Site.
- Base: one supported candidate fails while another succeeds; counts and safe issues describe the partial result without aborting successful work.
- Bad: probe `https://pttime.org`, reject its expected redirect, report only `failed=1`, and leave the UI showing an unknown error.

### 6. Tests Required

- Service tests cover canonical discovered Base URLs, parent/exact-host merge precedence, Cloudflare-only rejection, successful creation, authentication failure issues, persisted `last_sync_error_code`, partial audit outcome, and absence of Cookie values in serialized/audited output.
- Web tests cover stable-code localization and unknown future-code fallback; test, typecheck, lint, and production build remain required.

### 7. Wrong vs Correct

Wrong:

```go
candidate := discoveryCandidate{baseURL: "https://pttime.org", cookieHost: "www.pttime.org"}
cookie := cookies[longestMatchingDomain] // drops matching parent-domain cookies
```

Correct:

```go
candidate := discoveryCandidate{baseURL: "https://www.pttime.org", cookieHost: "www.pttime.org"}
cookie := mergeMatchingDomainsParentFirst(cookies, candidate.cookieHost)
```

## Scenario: Built-in Multi-Site Catalog

### Scope / Trigger

- Trigger: adding a built-in PT site, changing a shared tracker parser, exposing the supported-site directory, or changing CookieCloud multi-site discovery.

### Contracts

- Keep tracker identity separate from parser engine. A catalog definition owns a stable key, display name, engine, canonical HTTPS origins and auto-discovery policy; adapters remain registered by stable key.
- Standard NexusPHP definitions reuse one bounded request/parser implementation. A non-NexusPHP tracker must receive an explicit adapter override instead of being declared compatible only to make discovery counts look larger.
- The Site table accepts versioned catalog keys rather than a single PTTime check constraint. Existing `pttime` rows and credential AAD remain unchanged during migration.
- CookieCloud discovery iterates every supported definition, validates each candidate independently and may create multiple Sites in one sync. A configured key or canonical host suppresses only the same tracker, not every tracker sharing an engine.
- Unknown CookieCloud domains remain count-only. Do not probe arbitrary browser-cookie domains or return their names merely to auto-detect an unsupported tracker.
- Generic NexusPHP is a manual administrator option with no automatic CookieCloud domain matching.
- Nested result markup is grouped by torrent ID and parsed from the ancestor torrent row with the richest direct-cell structure. Outer layout rows must not duplicate a nested result, while inner title rows must not discard outer size or peer fields.
- The initial verified catalog includes `pttime`, `sewerpt` (`https://sewerpt.com`), and `panda` (`https://pandapt.net`). Panda audio-only `special.php` is not advertised until it has its own tested query contract; ordinary video search remains `torrents.php`.
- A shared NexusPHP health probe requires positive authenticated evidence such as a `logout.php` link or the tracker welcome marker. Absence of a login form alone is not proof that a Cookie is valid.
- For nested title tables, group anchors by torrent ID and choose the ancestor torrent row with the richest direct-cell structure. This must preserve outer size/peer/completion fields while still emitting one item. Title text is the bounded fallback when the anchor has no `title`; `span[title]` timestamps and `pro_free*` promotion classes are supported.

### Tests Required

- Catalog keys are unique and each has a registered adapter.
- A CookieCloud payload containing three supported domains creates three encrypted Site rows with their own names and keys.
- Migration preserves existing PTTime rows and allows additional catalog keys.
- A nested NexusPHP result fixture produces one result, not duplicate outer/inner rows.
- Adapter fixtures cover positive login proof. SewerPT and PandaPT result fixtures cover standard and nested rows, Panda title-text fallback, outer peer statistics, `span[title]` publication time, and `pro_free*` promotion markers.

## Scenario: Public BT RSS and Torznab

### Contracts

- Catalog and DTOs expose stable `site_type=pt|bt` and `credential_kind=cookie|api_key|none`. Existing PT rows derive these values from their catalog key without a destructive migration.
- Built-in public BT RSS entries are Nyaa, AnimeTosho, Tokyo Toshokan, Mikan and AniDex. Their feed origin, query parameter and torrent download host/path are compile-time profiles; an administrator cannot turn a built-in key into an arbitrary HTTPS fetcher.
- Fixed public RSS download origins accept only normal HTTPS ports unless the exact configured test origin is injected; matching a hostname alone must not authorize an arbitrary alternate port.
- Torznab is the explicit custom Jackett/Prowlarr connection. Its API Key uses the Site AES-GCM envelope and is sent only to the configured same-origin HTTPS API. Cross-origin torrent links and redirects are rejected, and a feed-provided download URL cannot override the encrypted configured API Key.
- CookieCloud considers only catalog entries whose credential kind is `cookie`. Public RSS and Torznab hosts remain unsupported CookieCloud domains and their encrypted envelopes are never updated from browser cookies.
- RSS/Torznab adapters may implement `site.SourceResolver`. The resolver converts a server-only identity to a bounded torrent or canonical BTIH magnet, then `SiteService` reuses `DownloadService.Submit` and the existing classification/transfer pipeline.
- REST/SSE results contain only actor-bound opaque tokens. API Keys, magnets, torrent URLs and provider response bodies never appear in DTOs, logs, audit metadata or job payloads.
- `/discovery/torrent-search`, `/discovery/torrent-search/stream` and `/discovery/torrent-results/recognize` are the primary generic routes. Existing PT routes remain compatible aliases.

### Tests Required

- Each built-in profile asserts its feed path/query key and rejects foreign feed/download hosts.
- Nyaa fixture covers title, size, publication time, seed/leech/completion counts and controlled torrent resolution.
- AnimeTosho compatibility covers `torrent_url` and `magnet_uri`; Torznab covers caps, namespaced attrs, encrypted API Key and same-origin download.
- Cross-layer tests prove a resolved magnet/torrent enters the normal DownloadService while public JSON/SSE contains no source or credential.
