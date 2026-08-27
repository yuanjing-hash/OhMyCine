# Media Identity Discovery and Library Coverage

> Executable contracts for TMDB-first poster search, identity-aware multi-name resource search, and actor-scoped library coverage.

## Scenario: Stable Media Identity Discovery

### 1. Scope / Trigger

- Trigger: changing Server global/explore search, TMDB poster results, discovery detail navigation, alternative-title generation, identity-aware PT/BT search, or the matching resource UI.
- This flow complements the advanced raw keyword torrent search; it does not replace or silently widen that endpoint.

### 2. Signatures

```text
GET /api/v1/discovery/media-search
  query=<1..160 chars>
  media_type=all|movie|tv
  page=<bounded positive integer>

GET /api/v1/discovery/media/:mediaType/:tmdbID/torrent-search
GET /api/v1/discovery/media/:mediaType/:tmdbID/torrent-search/stream
  site_id=<optional configured Site ID>
  season=<optional non-negative integer>

stable identity = { media_type: movie|tv, tmdb_id: positive integer }
```

The JSON and SSE identity-search routes share the same service implementation. SSE first emits the verified media identity/query-name summary, then emits completed Site groups progressively.

### 3. Contracts

- Global search, Explore default search, recommendation cards, related media and similar media all resolve to the same TMDB-backed detail route and stable identity.
- The Server revalidates `media_type + tmdb_id` with TMDB `GetByID`; browser titles, aliases, years and season counts are display hints only.
- Search names are ordered as localized title, supported Chinese-region aliases, original title, English title/translation and other bounded alternatives. Normalize whitespace/Unicode/case for deduplication while preserving the safe display value.
- At most six names enter Site search and every name obeys the Site query limit of 160 characters. Enrichment failure retains already verified localized/original names rather than converting an identity search to raw keyword search.
- Search uses bounded Site concurrency and serial alias queries within each Site, preserving existing rate limits, cancellation and partial-success behavior.
- Dedupe the same private torrent identity across aliases before returning results. Ordering must use match quality, configured Site order, seeders, publication time and a stable identity tie-break, never goroutine completion order.
- When `season` is requested, a candidate is eligible only when parsing proves the exact same season. Missing or different season information is not a match.
- Public JSON/SSE/browser state contains only safe result facts and the actor-bound opaque result claim. It never contains torrent/magnet URLs, passkeys, Cookies, provider-private identity or upstream bodies.
- Raw keyword search remains an explicit advanced mode and searches exactly the user-supplied query without alias expansion or a fabricated TMDB identity.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Empty/overlong query, invalid media type/page/TMDB ID/season | Return the stable invalid-request error before provider or Site I/O |
| TMDB identity cannot be verified | Return a safe media-identity error; do not search Sites |
| Alias enrichment partially fails | Search the bounded verified names and expose a safe partial summary |
| One alias or Site fails | Preserve successful Site groups and their claims; expose only the Site's safe error |
| Context is cancelled | Stop outstanding provider/Site work and do not launch a fallback duplicate request |
| Same torrent is returned for several aliases | Return one result and one valid opaque claim |
| Requested season is missing/different in a candidate | Exclude it from automatic/identity-scoped results |
| SSE has not completed every alias | Emit identity then each completed Site group; do not wait for the entire matrix |

### 5. Good / Base / Bad Cases

- Good: searching a Chinese TV title returns TMDB posters; selecting one searches its Chinese, regional, original and English names, dedupes the same torrent and keeps one opaque claim.
- Base: a title has only one verified name; identity search performs one bounded query and still uses the stable detail/search route.
- Good: one Site times out while two Sites succeed; the two successful groups render and remain downloadable.
- Bad: Vue loops over aliases, uses the displayed title as trusted identity, accepts a seasonless result for Season 2, or stores a magnet/torrent URL in router/session state.

### 6. Tests Required

- TMDB tests assert poster pagination, media-type filtering, ordered aliases, Chinese-region/original/English coverage, Unicode dedupe, 160-character rejection, six-name budget and partial enrichment fallback.
- Service tests assert bounded Site concurrency, per-Site alias serialization, cancellation, partial failure, exact-season filtering, cross-alias private-identity dedupe, stable ordering and actor-bound claims.
- JSON/SSE integration tests assert shared results, identity-first/progressive Site events, auth/permission checks and absence of torrent URL, magnet, Cookie, passkey, provider identity and upstream body fields.
- Web tests assert Ctrl/Cmd+K and Explore default to poster search, recommendation/search/related cards share a detail route, mode switches abort hidden requests, advanced raw keyword mode does not expand aliases, and tabs expose accessible selected state.
- Run `go test ./...`, `go vet ./...`, Web UI tests, typecheck, lint, production build and `git diff --check`.

### 7. Wrong vs Correct

Wrong:

```go
for _, name := range aliases {
    groups = append(groups, sites.Search(ctx, actor, SiteSearchInput{Query: name})...)
} // repeats all-Site scheduling, delays SSE, and preserves duplicate private results
```

Correct:

```go
identity := tmdb.GetByID(ctx, mediaType, tmdbID, "zh-CN")
names := boundedOrderedSearchNames(identity)
groups := sites.SearchMediaEach(ctx, actor, identity, names, emitCompletedSite)
// one bounded Site scheduler; aliases are serial per Site; claims remain opaque
```

## Scenario: Actor-Scoped Media Library Coverage

### 1. Scope / Trigger

- Trigger: changing discovery detail library status, cross-library catalog aggregation, TV season/episode status, coverage freshness, or any subscriber/follow missing-episode calculation.

### 2. Signatures

```text
GET /api/v1/discovery/media/:mediaType/:tmdbID/coverage

movie status  = present|missing|unknown
episode status = present|missing|future|unknown
season status  = present|partial|missing|future_or_incomplete|unknown
```

Coverage is a safe read model over TMDB season facts and the current actor's readable, enabled Server media libraries. Subscription workers must reuse the same service rather than maintain a second missing-episode algorithm.

### 3. Contracts

- Revalidate TMDB identity and obtain bounded season/episode snapshots outside database transactions.
- Aggregate only libraries the actor can read. The response may expose library ID/name summaries, but never root paths, provider item IDs, source keys or relative media paths.
- A logical episode key is `(tmdb_id, season_number, episode_number)`. Multiple files/libraries add library references without incrementing the logical present count.
- Only verified catalog identity facts prove presence. Provisional/unrecognized entries and title similarity do not prove a work or episode exists.
- A clearly aired episode absent from every complete readable library scan is `missing`. A future air date is `future`; absent/invalid air date, unavailable TMDB detail, unscanned libraries or an absence observed only through partial enumeration is `unknown`.
- A partial scan may still prove `present` for entries it returned; it cannot prove absence. Missing declaration gaps in a TMDB season response are counted as `unknown` and mark TMDB freshness `partial` rather than silently reducing the season total.
- Season 0 remains visible with `special=true`, but its counts are excluded from normal show missing totals. It participates in automation only when a later subscription explicitly selects it.
- Coverage caches, when added, must bind actor visibility and library scan generation/freshness; never share a privileged projection with another actor.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Actor lacks discovery or required library-read permission | Reject or omit that library according to the service permission contract; never enumerate it |
| No readable library has completed a scan | Return `unknown`, not `missing` |
| Partial scan contains the exact episode | Return `present` for that episode; other unseen episodes remain `unknown` |
| TMDB declares 12 episodes but returns 10 unique episode rows | Preserve total 12, count two `unknown`, set TMDB freshness `partial` |
| TMDB returns duplicate episode numbers | Deduplicate before totals and status projection |
| Episode air date is later than today | Return `future`; do not include it in missing or subscription targets |
| Episode air date is absent/invalid | Return `unknown`; do not infer release from episode number |
| Same episode exists in several libraries/files | Count one logical present episode and return bounded safe library references |

### 5. Good / Base / Bad Cases

- Good: two readable libraries both contain S01E01 and only one contains S01E02; coverage reports two logical present episodes with the right library summaries.
- Base: a movie has a verified catalog entry in one readable library and returns `present`; a fully scanned empty set returns `missing`.
- Good: a partial scan proves S01E01 exists while S01E02 is unseen; statuses are `present` and `unknown`.
- Bad: subtract catalog count from TMDB total, treat every unseen partial-scan item as missing, include Season 0 in normal totals, or return `relative_path` in coverage JSON.

### 6. Tests Required

- Service tests cover movie present/missing/unknown, actor-readable library filtering, cross-library duplicate files, exact verified identity, complete/partial/unscanned states and stable freshness.
- TV tests cover present/missing/future/unknown, absent dates, Season 0, duplicate TMDB rows, declared episode gaps, TMDB season failure and all season/show totals.
- Router/serialization tests assert discovery plus library permission behavior and reject private path/provider/source fields in successful and error responses.
- Cross-layer tests assert the detail UI uses the same statuses and that future/unknown episodes are never represented as auto-downloadable missing episodes.

### 7. Wrong vs Correct

Wrong:

```go
missing := tmdbSeason.EpisodeCount - catalogEpisodeCount
// duplicates, partial scans, future episodes and missing air dates become false missing facts
```

Correct:

```go
for _, episode := range dedupeTMDBEpisodes(tmdbSeason) {
    status := projectEpisodeStatus(episode, verifiedCatalogFacts, scanFreshness)
    totals.AddLogical(episode.Number, status)
}
// only definitely aired + definitely absent becomes missing
```

