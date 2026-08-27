# Technical design

## 1. Boundaries

This work keeps the existing Server pipeline and adds no parallel download path.

```text
Explore query
  -> safe searchable-site options
  -> selected site_ids
  -> JSON/SSE search session
  -> per-site adapter
       -> native HTTP
       -> controlled rendered fetch (CloakBrowser or FlareSolverr)
  -> opaque result claim
  -> existing DownloadService

115 completed manifest
  -> package identity + per-file episode facts
  -> transfer plan
  -> 115 mutation scheduler
  -> managed items / catalog
  -> optional safe reorganization
```

## 2. Multi-site search contract

- Extend search inputs with `SiteIDs []string` while retaining singular `SiteID` for the fixed single-site route. Supplying both is invalid.
- Normalize, deduplicate and cap repeated `site_ids` before service use; JSON and SSE share the same parser.
- The service reloads every site and applies actor visibility, enabled state and capability checks. Missing/forbidden IDs fail rather than silently widening scope.
- Add a DiscoveryRead endpoint returning only `{id,name,site_type,health_status,searchable,reason}`.
- Include the ordered site scope in the WebUI session identity. Retry/page requests reuse it exactly.
- Use one reusable dialog for direct torrent search and TMDB identity search. Locked single-site routes bypass it.
- Persist the last selected site IDs in browser-local storage under a versioned WebUI key. On first use, select every currently selectable site. On later use, intersect the saved IDs with the current safe options; newly added/re-enabled sites are not silently selected, while “select all” replaces the saved scope with all current selectable IDs.

## 3. Public BT adapter repairs

- ACG.RIP: update the exact RSS profile to `/.xml?term=...` and preserve strict content type/body limits.
- EZTV: implement a bounded integer-or-decimal-string JSON type; reject negatives, overflow, fractions and arbitrary strings.
- LimeTorrents: add the exact current `.fun` host and allow redirects only when both hosts belong to the profile's explicit host set.
- External failures remain diagnostics with stable error codes; no forced save-as-online behavior.

## 4. Rendered fetch providers

Introduce a provider-neutral `RenderedFetcher` contract. Its request contains a server-resolved profile ID and exact URL, never an arbitrary client URL. The service revalidates HTTPS, host set, redirect chain, response limit and site ownership before dispatch.

Providers:

1. `FlareSolverrFetcher`: external `/v1` client, optional short-lived session for proxy authentication, bounded solution HTML/cookies/UA, guaranteed session destroy.
2. `CloakBrowserFetcher`: separate companion process through narrow loopback IPC/CDP. OhMyCine owns configuration and health but does not bundle the proprietary browser binary. Installation is an explicit admin action that downloads from the official channel after a license notice.

Routing:

- Native HTTP for profiles that do not require rendering.
- For known challenge profiles, prefer a healthy configured CloakBrowser companion; fall back to configured FlareSolverr; otherwise return a site-level solver-unavailable error.
- Initial use is limited to public BT profiles without private Cookie/passkey.

Controls include one bounded browser session by default, per-site isolated ephemeral contexts, no cross-site Cookie jar, page/context/process cleanup, redacted bounded logs, isolated cache, and no non-loopback listener.

## 5. Per-file season and episode precedence

Build one shared resolver for transfer validation, planning and reorganization:

```text
explicit manual per-file correction
  > validated identity_snapshot.episodes[path]
  > deterministic ResolvePackageEpisodes(file path)
  > task-level automatic season/episode only when there is one video
    or every structured file agrees with that same season
  > no fact / needs action
```

`RecognitionOverrideSeason` remains an explicit user action and is tested separately from automatic `ScrapeSeason`. The resolver validates identity revision and requires each snapshot path to exist in the selected manifest; duplicate or conflicting facts fail closed.

## 6. Existing-task repair

- Decode the original Transfer source manifest and the download identity snapshot.
- Correlate managed items to original files using stable provider item ID, with size/SHA1 consistency checks, not the already-wrong final S02 name.
- Rebuild target paths with the shared resolver.
- Keep the existing preview token, rule fingerprint, managed manifest digest and root checks as the confirmation boundary.
- Do not rewrite the affected task automatically. The user previews and confirms S01–S04 moves/renames; missing or ambiguous identity blocks the action.

## 7. 115 pacing redesign

Replace `mutationRate` with independent lanes for list/path, mkdir, move, copy, rename, recycle/purge and offline/share, while retaining one shared risk controller.

Start with MoviePilot-derived conservative spacing for move/copy/rename/delete (one call per two seconds per operation). Path lookup/list has its own read pacing and caches. Healthy mkdir calls are deduplicated and bounded but do not inherit a fixed two-second delay from unrelated mutations. Explicit 405/429/provider risk responses trigger jittered exponential backoff and a bounded circuit across the affected 115 account.

Before mutation, deduplicate target directories into a DAG, list each parent once, cache conflict listings, persist resolved IDs and resume from checkpoints. Group files by target directory and prefer a bounded batch move/copy capability matching MoviePilot-Plugins when provider identity and post-call listing allow deterministic reconciliation after partial success. Batch rename and delete remain separately gated because their conflict and destructive semantics differ.

## 8. UI status

Expose safe phases such as `checking_directories`, `creating_directories`, `checking_conflicts`, `moving`, `renaming`, `risk_backoff` and `reconciling`, plus processed/total and optional retry time. A normal transfer is not labelled “风控中”; only real backoff uses that label and includes the bounded retry time. The current blanket notice about “按风控限速逐个准备目录” is removed.

## 9. Compatibility and rollback

- Singular `site_id` remains compatible.
- Old search sessions without a site scope prompt for scope rather than silently restoring an all-site search.
- Old identity snapshots remain readable; only planning precedence changes.
- Existing transfer state is never rewritten automatically.
- Disabling rendered fetching restores native HTTP/Torznab behavior without changing site credentials.
- Site profiles, selector/API, rendered providers, episode resolver/reorganization and 115 pacing/UI remain independently revertible.

## 10. Convergent transfer deletion preview

- Split preview validation by scope. `record_only` uses database state only and never initializes a downloader/cloud driver.
- Treat a missing external provider task, missing 115 output root or missing manifest item as an already-satisfied deletion outcome. Preserve counts/warnings so the user can see what was already absent; only identity conflicts (an ID resolves to a different item inside the claimed boundary) fail closed.
- Distinguish an actually live local worker from a stale persisted provider state. A live worker must be cancelled and observed terminal before its ownership rows are removed; provider-side absence alone is not a live-worker signal.
- For source/library scopes, group manifest items by stable parent ID and reconcile from bounded directory listings or a provider bulk-stat capability. Apply an overall context timeout and return an actionable timeout error rather than leaving the modal pending indefinitely.
- For `record_and_source`, the immutable `provider_output_id` package root is the deletion boundary. Items still proven beneath it are residual source candidates; historical manifest IDs now outside it are detached and preserved. A missing package root means the source side is already complete, not boundary corruption.
- Confirmation rechecks digests/revisions and remains idempotent: already-missing items count as completed, while no unselected source/library object is mutated.

## 11. Server-only beta release contract

- Decouple Server Release from the historical Player `v*.*.*` prerelease prerequisite. The workflow input remains a semantic Server version, while the release tag is namespaced as `server-vMAJOR.MINOR.PATCH`; this tag does not match Player's tag trigger.
- Dispatch remains restricted to `develop`; CI fetches `origin/develop`, requires `GITHUB_SHA` to equal its tip, and requires an existing namespaced tag to point to the same commit or creates it through the workflow's scoped contents permission.
- Create or reuse a prerelease titled `OhMyCine Server vMAJOR.MINOR.PATCH Beta`, then upload only embedded-WebUI Server Windows/Linux archives and their checksum manifest. Existing historical Server assets attached to Player releases are untouched.
- The workflow is idempotent for the same version/commit and refuses a tag or release that points elsewhere, is not a prerelease, or lacks the official TMDB build secret.
