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
POST   /api/v1/discovery/pt-results/tmdb-candidates
PUT    /api/v1/discovery/pt-results/recognition-override
POST   /api/v1/discovery/downloads
```

## Contracts

- Keep provider paths, category mappings, login-page detection, HTML parsing, and torrent quirks inside the concrete adapter. Services depend only on `pkg/site.Adapter`.
- Site Cookie/passkey are encrypted with AES-256-GCM using purpose/AAD bound to site ID and adapter kind. Ordinary DTOs return only exact `cookie_configured` / `passkey_configured` booleans; ciphertext and plaintext never enter search REST, SSE, logs, audit details, or job payloads. Plaintext is available only through the dedicated authenticated, CSRF-protected, permission-gated, `no-store`, audited credential reveal action after an explicit administrator gesture.
- Candidate create/update performs network validation outside a database transaction. Update replaces the stored credential and policy with revision CAS only after validation succeeds; failure preserves the old record.
- A disable-only update is the deliberate exception to candidate probing: it uses revision CAS and audit locally so an expired Cookie or unavailable tracker cannot prevent an administrator from disabling the site. Re-enabling still requires a successful probe.
- User-configured site roots require HTTPS with no userinfo, query, or fragment. Adapter clients set bounded timeout, redirect count, response size, and strict same-origin checks including port.
- Multi-site search has a global concurrency bound and per-site rate limiter. One site failure produces only that site's error group. SSE group writes are serialized, and JSON fallback returns the same DTO.
- Browser results contain a 256-bit opaque claim, not a torrent URL or provider identity. The claim binds actor, site, torrent identity, and a short expiry. Its private recognition facts may additionally retain one bounded adapter subtitle and the normalized search media type (`movie|tv`); neither field is a trusted identity.
- The Explore UI may restore the latest search within one browser tab through bounded `sessionStorage` only: at most 24 groups, 300 results and 512 KiB for 30 minutes. It stores search fields, safe result DTOs and quick-recognition summaries only; expired opaque claims are removed on restore, and Cookie/passkey/source URLs are never persisted. Recognition summaries bind `engine_version`: restoring an older version keeps the safe search groups but discards stale matched/unrecognized decisions. A new search clears all recognition summaries. `localStorage` is not used for this transient state.
- Quick recognition accepts only that opaque claim, resolves its server-side title without reserving or consuming it, and never downloads the torrent. It reuses the shared media-recognition parser before optional TMDB enrichment. A bounded subtitle enters the same provider-neutral parser as an auxiliary query fact after URL/path/credential/direct-identity-like values are rejected; the search media type is structure guidance only. Neither may replace the package title or force an identity, and no site-specific parser is allowed. Its safe DTO includes `engine_version` and optional structured episode facts (`season`, `season_year`, `episode_min`, `episode_max`, `count`); the UI derives single/range/multi labels from these facts and never reparses the release name. Missing/unavailable/no-match metadata returns HTTP 200 with `status=unrecognized`, a stable error code, and safe parsed title/year/specifications; only permission and invalid/expired claims are request errors.
- Manual recognition is an explicit user-triggered recovery tool available even after quick recognition fails. Candidate search accepts the opaque claim plus a bounded editable keyword/type/year, returns only safe TMDB summaries through the same-origin image gateway, and never returns provider/torrent identity. Selecting a candidate submits only `tmdb_id + movie|tv`; Server must call TMDB `GetByID` before atomically binding that identity to the still-valid, actor-bound, non-reserved claim. Download handoff copies the verified identity into `DownloadTask` so the worker revalidates it and uses the ordinary Profile classification/transfer pipeline; changing browser display state alone is never sufficient.
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
| Stored recognition uses an older engine version | Keep the bounded search results, discard only the stale recognition summary, and allow a fresh recognition request |
| Adapter subtitle is empty, oversized, URL/path-like, or contains a NUL/backslash | Ignore it as recognition context; keep the primary release title and perform no outbound request derived from that value |
| Search media type is not `movie` or `tv` | Drop the hint; the shared recognizer must infer type and may reject an unresolved conflict |

## Tests Required

- Adapter fixtures cover authenticated page, login page, result parsing, malformed-row skip count, next page, FREE promotion, size/peer counts, valid torrent, and HTML/error response.
- Service tests cover ciphertext/redaction, candidate-update preservation, site failure isolation, actor/expiry binding, atomic concurrent claim reservation, retry restoration, and normal `DownloadService` task creation.
- Quick-recognition tests cover engine-version invalidation, single episode, episode range, multi-episode count, season-only/season-year display, and absence of episode labels for movies or untrusted facts.
- A cross-layer quick-recognition test must prove `adapter result subtitle + search media type -> private actor-bound claim -> shared MediaRecognitionRequest -> bounded TMDB recall`, while URL/path-like subtitles and unsupported type hints are ignored. The same test must not expose those private claim facts as provider identity.
- Manual-recognition tests cover automatic-failure recovery, editable keyword search, safe poster candidates, cross-actor/expired/reserved claims, `GetByID` verification, and verified identity propagation from opaque claim to `DownloadTask` without exposing provider/torrent identity.
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

## Scenario: Address-Driven Public BT and Per-Site Search

### 1. Scope / Trigger

- Trigger: adding/changing a built-in public BT adapter, resolving an administrator-entered official URL, exposing Site capabilities, or navigating from one Site card into fixed single-site search.

### 2. Signatures

```text
POST /api/v1/sites/resolve
  request:  { base_url: string }
  response: { kind, name, base_url, site_type, credential_kind, capabilities }

POST /api/v1/sites
  kind="auto_bt" is a browser intent only; Server resolves base_url again.

GET /api/v1/discovery/torrent-search?site_id=<configured-site-id>&...
GET /api/v1/discovery/torrent-search/stream?site_id=<configured-site-id>&...
```

- Catalog and Site DTOs expose stable `site_type=pt|bt`, `credential_kind=cookie|api_key|none`, and Server-derived `capabilities.search/download`.
- Built-in URL registry keys are `nyaa`, `animetosho`, `tokyotosho`, `mikan`, `anidex`, `dmhy`, `acgrip`, `yts`, `eztv`, `1337x`, `thepiratebay`, `extto`, and `limetorrents`; Torznab remains the explicit Jackett/Prowlarr option.

### 3. Contracts

- Public BT adapter code may ship with Server, but unconfigured adapters are absent from the public add catalog and produce zero probes/searches. A Site exists only after an administrator enters a supported official HTTPS origin, Server resolves it, tests it, and saves it.
- URL resolution accepts an HTTPS root origin only: no userinfo, query, fragment, non-root path, abnormal port, similar-domain suffix, or unregistered subdomain. IDNA-normalized hosts match the versioned registry exactly; unknown mirrors are not trusted automatically.
- `kind="auto_bt"` and any browser resolution preview are untrusted. Create resolves the submitted origin again and persists the registry's stable kind/canonical origin, never a client-selected adapter kind.
- RSS/API/HTML parsing remains per-site and fixture-backed. Shared bounded HTTP machinery may be reused, but CSS/HTML selectors and download-host allowlists may not be generalized across unrelated sites.
- Fixed single-site mode carries `site_id` through initial search, retry, pagination, recognition, download, and bounded session restoration. If that Site fails, return only that Site's error; never silently broaden to aggregate search.
- Torznab API Key uses the Site AES-GCM envelope and same-origin API contract. CookieCloud considers only cookie-authenticated catalog entries and never creates or updates public BT/Torznab credentials.
- A `SourceResolver` converts server-private result identity to one bounded torrent or canonical BTIH magnet. Browser/API/SSE/session storage/log/audit/Job payload never contains magnet, torrent URL/body, upstream HTML, passkey, API Key, or provider identity.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Supported exact HTTPS root origin | Return safe definition preview; create still repeats resolution and connection test |
| HTTP, userinfo, path, query, fragment, abnormal port | Reject as invalid Site URL; persist nothing and send no adapter request |
| Similar hostname, attacker suffix, unregistered subdomain/mirror | Return unsupported Site; never fall through to another parser |
| Browser posts a forged stable kind with another origin | Ignore/reject the kind and resolve origin server-side |
| Configured single Site is disabled/offline/not searchable | Disable the card action or return only that Site's stable error; never aggregate |
| HTML/API structure changes or download source crosses allowlisted host | Fail only that Site and expose no upstream body/source URL |

### 5. Good / Base / Bad Cases

- Good: administrator enters `https://nyaa.si`, Server identifies and probes Nyaa, saves one Site, then card search keeps `site_id` through page 2 and one-click download.
- Base: an unconfigured built-in adapter remains invisible and performs no network I/O; global PT/BT search continues over configured enabled Sites only.
- Bad: display all public BT names by default, trust `kind="nyaa"` from the browser, accept `https://nyaa.si.attacker.example`, or drop `site_id` on retry and search every Site.

### 6. Tests Required

- Registry tests cover every official origin/alias and reject HTTP, userinfo, path/query/fragment, abnormal ports, similar domains, subdomain spoofing and unknown mirrors.
- Adapter fixtures cover normal/empty/next-page/malformed/challenge responses, media-only result facts, and cross-host source/redirect rejection for every RSS/API/HTML implementation.
- Service/HTTP tests prove create re-resolves `auto_bt`, unconfigured definitions make zero requests, PT/CookieCloud/Torznab remain compatible, capabilities are Server-derived, and public DTO/log/audit state is source-free.
- Web tests prove the add form does not list concrete public BT Sites and single-site route/search/retry/pagination/session restoration retain the same `site_id`.

### 7. Wrong vs Correct

Wrong:

```go
adapter := registry[request.Kind]
return adapter.Search(ctx, request.BaseURL, query) // client selects code + target
```

Correct:

```go
definition, err := builtin.ResolveOfficialOrigin(request.BaseURL)
if err != nil { return ErrUnsupportedSite }
// Create repeats this resolution, probes definition.CanonicalOrigin, then saves definition.Kind.
```

## Scenario: Explicit Multi-Site Scope and Controlled Rendered Fetch

### 1. Scope / Trigger

- Trigger: changing aggregate title search, TMDB identity resource search, retry/pagination/session restoration, safe search options, or Cloudflare-challenged public BT adapters.

### 2. Signatures

```text
GET /api/v1/discovery/search-options
  -> { list: [{ id, name, site_type, health_status, searchable, reason }], total }

GET /api/v1/discovery/torrent-search[ /stream ]
GET /api/v1/discovery/media/{movie|tv}/{tmdb_id}/torrent-search[ /stream ]
  query: site_id=<id> OR repeated/comma-separated site_ids=<id>, 1..64 unique IDs

pkg/site.RenderedFetcher
  Fetch(context.Context, RenderedFetchRequest) (RenderedPage, error)
  Health(context.Context) error
```

- `RenderedFetchRequest` contains Server-selected `ProfileID`, exact URL, exact allowed hosts, bounded user agent, timeout and maximum bytes. It contains no Cookie, passkey or client-selected host.

### 3. Contracts

- `site_id` is the locked fixed-single-site contract. `site_ids` is the explicit aggregate scope; sending both is invalid. Parse repeated and comma-separated values, trim, validate positive integers, deduplicate in first-seen order and reject more than 64 raw or unique entries.
- The service reloads every requested Site and rechecks actor visibility, enabled/search capability and current safe configuration. An absent, forbidden or non-searchable requested ID fails the request; it never widens to all Sites.
- JSON, SSE, TMDB multilingual query variants, per-site retry, pagination and restored sessions carry the exact same ordered scope. A cache/session entry created without an explicit modern scope must not be restored as implicit all-site search.
- `/discovery/search-options` requires `discovery.read` and returns only the safe summary above. It never returns Base URL, adapter internals, Cookie/passkey/API-key flags, solver URL, profile state or management-only fields.
- Native HTTP remains the default. Only explicitly registered challenge profiles currently allowed for 1337x and EXT.to may invoke rendered fetch. A healthy configured CloakBrowser loopback companion is preferred; `ErrUnavailable` may fall back only to that Site's configured FlareSolverr endpoint.
- ACG.RIP uses `/.xml?term=...`; EZTV accepts bounded non-negative integer or decimal-string sizes; LimeTorrents uses the exact registered `.fun` hosts and redirects only within the profile host set. AniDex/YTS/The Pirate Bay external failures remain per-site diagnostics rather than parser relaxations.
- Rendered fetch failure is isolated to its Site group. It must not cancel successful Sites, disclose upstream bodies, or reinterpret a challenge page as an empty successful result.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Both `site_id` and `site_ids` | Return safe `400`; perform no Site search |
| Empty/zero/malformed/over-64 `site_ids` | Return safe `400`; never fall back to all Sites |
| Duplicate selected IDs | Search each requested Site once, preserving first-seen order |
| Requested Site becomes disabled/forbidden | Fail scope validation; do not silently remove it or widen scope |
| JSON succeeds and caller retries through SSE | Reuse the exact scope and return only selected Site groups |
| Cloak companion unavailable | Fall back only to configured FlareSolverr for the same Site; otherwise emit its site-level unavailable error |
| Challenge adapter returns off-profile final URL/body too large | Reject the rendered page and expose no body or target URL |
| One selected Site fails remotely | Keep other selected groups usable and attach only a safe error to the failed Site |

### 5. Good / Base / Bad Cases

- Good: the browser selects Mikan, Nyaa and ACG.RIP; title and TMDB multilingual searches, page 2 and retry touch exactly those three Sites.
- Base: a fixed Site-card search supplies one `site_id` and bypasses the selector while retaining that ID through the full flow.
- Bad: drop `site_ids` after the first SSE request, restore an unscoped cache as all Sites, or send PT Cookie/passkey to FlareSolverr/CloakBrowser.

### 6. Tests Required

- Handler/service tests cover repeated/comma-separated parsing, de-duplication, mutual exclusion, 1..64 bounds, permission/capability revalidation and no implicit scope widening.
- Cross-layer tests assert identical scope for JSON/SSE/TMDB multilingual variants/retry/page changes plus safe `search-options` redaction.
- Adapter fixtures cover ACG.RIP current RSS, EZTV numeric strings, LimeTorrents controlled host redirects, challenge detection and the external-failure diagnostic classes.
- Rendered-fetch tests cover native routing, Cloak preference, same-Site Flare fallback, no fallback for validation errors, PT credential absence, response limits and final-host rejection.

### 7. Wrong vs Correct

#### Wrong

```go
if len(input.SiteIDs) == 0 { sites = allEnabledSites }
page := solver.Fetch(ctx, clientURL, site.Cookie)
```

#### Correct

```go
scope, err := authorizeExactSearchScope(actor, input.SiteID, input.SiteIDs)
page, err := site.FetchRendered(ctx, configWithoutCredentials, registeredProfileRequest)
```
