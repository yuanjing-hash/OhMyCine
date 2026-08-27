# Automatic TV Follow Contract

> Executable contracts for MoviePilot-style TV subscriptions without bypassing OhMyCine identity, coverage, discovery, download, transfer, or authorization boundaries.

## 1. Scope / Trigger

Apply this contract when changing TV follow subscriptions, season selection, follow scheduling, missing-episode reconciliation, automatic resource selection, follow episode claims, follow events, or the discovery-to-download handoff used by follows.

The supported flow is:

```text
TMDB TV identity + selected seasons
→ immutable execution snapshot
→ persistent follow-search Job
→ actor-scoped MediaCoverage
→ identity-aware SiteService search
→ deterministic filtering/set cover
→ episode claims
→ SiteService opaque result resolution
→ DownloadService
→ Transfer → Import → Notify
```

Follow orchestration must never fetch torrent URLs directly, call a downloader client, move files, or create a second import pipeline.

## 2. Signatures

REST routes under `/api/v1`:

```text
GET    /follows/defaults?media_type=tv&tmdb_id=<id>
GET    /follows
POST   /follows
GET    /follows/{id}
PUT    /follows/{id}
DELETE /follows/{id}
POST   /follows/{id}/pause
POST   /follows/{id}/resume
POST   /follows/{id}/search
GET    /follows/{id}/runs
```

Persistent queue signature:

```text
job_type       = follow-search
resource_key   = follow:<subscription_id>
coalescing_key = search
payload        = run_id + subscription_id + subscription_revision + trigger
```

Database signature:

```text
follow_subscriptions
follow_subscription_seasons
follow_runs
follow_episode_claims

follow_subscriptions.lifecycle_revision
follow_runs.lifecycle_revision

download_tasks.follow_subscription_id
download_tasks.follow_resource_fingerprint
UNIQUE(download_tasks.follow_subscription_id,
       download_tasks.follow_resource_fingerprint)
  WHERE both values are non-empty
```

Permissions:

```text
follows.read_own / follows.read_all / follows.create
follows.update_own / follows.update_all
follows.delete_own / follows.delete_all
follows.execute_own / follows.execute_all
```

## 3. Contracts

- A subscription is bound to `media_type=tv`, a positive TMDB ID, one to twenty explicit seasons, ordered site IDs, one enabled downloader, and one enabled target MediaLibrary.
- Season `0` participates only when explicitly present in the snapshot. Do not infer specials from an ordinary season subscription.
- The execution snapshot is versioned and contains stable IDs plus bounded non-sensitive filters only. Never persist site credentials, downloader credentials, torrent/magnet URLs, temporary tokens, upstream bodies, or absolute paths in follow tables, jobs, events, or API DTOs.
- Global defaults prefill a new form only. Creating a subscription freezes the snapshot. Updating uses expected revision/CAS and affects only later runs; a run always reads its copied `execution_snapshot_json`.
- Pause and resume both advance `lifecycle_revision`. A queued or running job may execute only while its copied lifecycle revision still matches, so a quick pause/resume cannot revive work that was invalidated by the pause.
- `(owner_id, tmdb_id, season_number)` is unique across live subscriptions, including paused and completed subscriptions. Multi-season creation and season replacement are transactional.
- HTTP create and manual search only enqueue work. External TMDB/site/downloader operations never run in the request handler.
- Due scanning includes `active` and `completed`. `completed` means all currently aired targets are present, not permanently stopped.
- Coverage is authoritative. Only explicit `missing` episodes are eligible. `present`, `future`, and `unknown` are never downloaded. Any unknown fact in a selected season blocks the run safely.
- Search must call the shared identity-aware SiteService path so localized, regional, original, English, and translated names retain the same bounded/deduplicated behavior as manual media resource search.
- Candidates must prove the selected TMDB work, season, and episode coverage before filters are applied. Stable selection prefers coverage count, configured site order, quality preference, seeders, publish time, and resource fingerprint.
- Episode claims prevent duplicate concurrent work. An active download claim removes that episode from the missing set. A failed/cancelled download clears the claim's task reference and may be retried; the old terminal DownloadTask releases its follow idempotency key before a replacement is created.
- SiteService resolves the private result and calls DownloadService. It must execute an internal `BeforeSubmit` guard after site retrieval but immediately before DownloadService handoff, so pause/delete during a slow external call cannot submit a download.
- DownloadService must also execute the follow lifecycle/status guard as `BeforePersist` inside the same database write transaction immediately before creating `DownloadTask`. The earlier handoff guard is not sufficient to close a pause/delete time-of-check/time-of-use race.
- A run may project final status back to the subscription only while both configuration revision and lifecycle revision still match; stale runs must never overwrite a paused subscription.
- Pausing or deleting does not cancel already submitted downloads or remove imported media.
- Follow WebSocket events use `JobTypeFollowSearch`, carry owner identity internally, and are visible only to `follows.read_all` or the matching owner with `follows.read_own`. Jobs-only permission must not reveal follow events.

## 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Invalid TMDB ID or non-TV media type | Reject with `invalid_request`; enqueue nothing. |
| No seasons/sites/downloader/library, invalid interval, or out-of-range filter | Reject with `follow_configuration_invalid`. |
| Duplicate owner/TMDB/season | Reject with `follow_season_conflict` from the database constraint. |
| Stale update revision | Reject with `follow_revision_conflict`; do not replace seasons. |
| Blocked subscription is edited successfully | Clear safe error, set `active`, and make `next_run_at` immediately due. |
| Paused subscription is manually searched | Reject; do not enqueue. |
| Coverage contains unknown selected episodes | Mark run failed and subscription blocked; download nothing. |
| No definite missing episode | Mark completed when fully present, or remain active/submitted while an active claim is converging. |
| Definite missing episodes but no proven candidate | Mark `no_match`, keep subscription active, and schedule the next retry. |
| Pause/delete occurs during site retrieval or task persistence | `BeforeSubmit` or transactional `BeforePersist` rejects handoff; mark the run cancelled/stale and create no DownloadTask. |
| Job retries after submit but before claim attachment | DownloadService returns the existing active task by stable follow idempotency key; attach the same task. |
| Prior follow download failed/cancelled | Release the old idempotency key and allow exactly one replacement task. |
| Newly aired episode appears after completed | Due reconciliation updates progress and returns subscription to active/no-match or submitted. |
| Unauthorized owner/all access | Return permission denied or scoped not-found behavior; disclose no foreign summary/event. |

## 5. Good / Base / Bad Cases

- Good: select Seasons 1 and 2, review all automatic settings, save revision 1, and let one season pack claim several missing episodes while entering the normal Download/Transfer pipeline once.
- Good: a subscription is completed today; a future episode airs next week; the completed due scan sees the new definite missing episode and resumes search.
- Base: no resource matches filters; the run records a bounded reason-count summary and retries later without storing result URLs.
- Bad: compute missing episodes as `1..TMDB total` without air-date and catalog completeness facts.
- Bad: submit from the follow worker directly to qBittorrent/Transmission or persist a resolved torrent URL in the Job payload.
- Bad: allow `jobs.read_own` alone to receive follow events, or rely only on the WebSocket handshake without per-event owner filtering.

## 6. Tests Required

- Migration: fresh v52, previous-head upgrade, idempotent migration, follow table/index/FK/check constraints, season uniqueness, and deletion that preserves DownloadTask/media facts.
- Snapshot/service: normalization, defaults frozen after creation, revision CAS, blocked-edit reactivation, owner/all CRUD/execute matrix, and enqueue coalescing.
- Coverage/worker: present, missing, future, unknown, explicit Season 0, completed-to-active, no-match, configuration blocked, and partial site failure.
- Selection: include/exclude, resolution, codec, source, groups, seeders, age, size, stable tie-break, episode ranges, bounded complete-season handling, and resource limit.
- Pipeline integration: real test path through TMDB client fixture, MediaCoverageService, SiteService opaque claim, DownloadService, target library snapshot, recognition season/episode fields, and follow claim attachment.
- Race/idempotency: pause/delete while site download is blocked, crash after DownloadService submit, active duplicate suppression, and failed/cancelled task replacement.
- Events/API: follow-own/all event isolation, jobs-only denial, standard response envelope, route permission middleware, and no credential/URL/path leakage.
- Web UI: typed defaults/create/update/actions, multi-season editor, revision conflict, progress/run history, keyboard/dialog behavior, responsive presentation, and both themes.

Run the Server and Web UI quality gates from their component roots after these tests.

## 7. Wrong vs Correct

### Wrong

```go
// The worker bypasses private claims and the normal pipeline.
torrentURL := searchByTitle(subscription.Title)
qbit.AddTorrent(ctx, torrentURL)
```

### Correct

```go
coverage := coverageService.Coverage(ctx, owner, "tv", subscription.TMDBID)
candidates := siteService.SearchMediaIdentity(ctx, owner, identityInput)
selected := selectDeterministically(candidates, definiteMissing)

siteService.Download(ctx, owner, SiteDownloadInput{
    ResultToken: selected.Token,
    FollowSubscriptionID: subscription.ID,
    FollowResourceFingerprint: selected.Fingerprint,
    BeforeSubmit: verifySubscriptionStillActive,
})
```

The correct path preserves media identity, owner scope, site credential isolation, downloader idempotency, transfer routing, and observability.
