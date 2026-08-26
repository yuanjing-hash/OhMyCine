# Media Library Foundation

## 1. Scope / Trigger

Apply this contract when changing `MediaLibrary`, local or provider scanning, scan runs, file-tree entries, watchers, Profile/Storage references, or `/api/v1/media-libraries` and its Server Web UI.

Media libraries define both a read-only scan/index boundary over a registered Storage and the durable import policy for new downloads. Scanners and watchers never rename, move, upload, or delete source files. Only the separate `TransferService`, after an explicit download target has been snapshotted, may write inside the selected library root.

## 2. Signatures

REST routes:

```text
GET    /api/v1/media-libraries
GET    /api/v1/media-libraries/:id
POST   /api/v1/media-libraries
PUT    /api/v1/media-libraries/:id
PUT    /api/v1/media-libraries/order
DELETE /api/v1/media-libraries/:id
POST   /api/v1/media-libraries/:id/scan
POST   /api/v1/media-libraries/:id/retry
GET    /api/v1/media-libraries/:id/entries
GET    /api/v1/media-libraries/:id/catalog
GET    /api/v1/media-libraries/:id/catalog/:work
GET    /api/v1/media-libraries/:id/runs
GET    /api/v1/media-libraries/:id/recognitions
POST   /api/v1/media-libraries/:id/recognitions/:token/retry
GET    /api/v1/media-libraries/:id/recognitions/:token/tmdb-candidates
PUT    /api/v1/media-libraries/:id/recognitions/:token/override
DELETE /api/v1/media-libraries/:id/recognitions/:token/override
```

Database ownership:

```text
media_libraries
  -> media_library_scan_runs (ON DELETE CASCADE)
  -> media_library_entries   (ON DELETE CASCADE)
  -> media_library_recognitions (ON DELETE CASCADE)

media_libraries.storage_id -> storages.id (ON DELETE RESTRICT)
media_libraries.profile_id -> media_classification_profiles.id (ON DELETE RESTRICT)

media_libraries.provider_root_id                         # private stable provider directory ID
media_libraries.ingest_enabled                          # 115 share/manual-transfer intake switch
media_libraries.ingest_downloader_id                    # same-Connection pan115_offline downloader
media_libraries.ingest_owner_id                         # owner of internally adopted tasks
media_libraries.ingest_provider_root_id                 # private stable intake directory ID
media_libraries.ingest_relative_root                    # safe Storage-relative display path
media_library_entries.recognition_id                     # nullable fact -> recognition projection
media_library_entries.work_key + series_title            # indexed catalog projection keys
media_recognition_cache.lookup_key                       # credential/path/provider-free SHA-256 key
INDEX media_library_entries(library_id, work_key)
INDEX media_library_entries(library_id, media_type, title)
```

Shared recognition boundary:

```text
provider/local enumeration -> provider-neutral file facts
  -> deterministic recognition units -> mediarecognition.Parse(InputFacts)
  -> built-in packs -> Profile user rules -> structured ParsedFacts/QueryVariant
  -> bounded TMDB movie+tv recall -> top-3 detail enrichment -> mediarecognition.Rank
  -> winning GetByID validation -> Profile classification
  -> recognition + entry/catalog projection
```

Pure domain signatures:

```go
mediarecognition.Parse(InputFacts) (ParsedFacts, error)
mediarecognition.Rank(ParsedFacts, []RemoteCandidate) Decision
tmdb.Client.EnrichCandidates(ctx, candidates, language, limit) ([]tmdb.Candidate, error)
```

Runtime lifecycle:

```text
disabled -> initializing -> attaching_listener
         -> catch_up_reconciliation -> listening
         -> initialization_failed -> bounded retry -> initializing
```

## 3. Contracts

- Source identity is `storage_id + relative_root`; `relative_root` is provider-relative and starts with `/`. For 115, the private `provider_root_id` is the scan identity while `relative_root` is display/overlap data.
- Changing `storage_id`, normalized `relative_root`, or the private 115 `provider_root_id` replaces the library source identity. The update transaction must delete the old `media_library_entries`, `media_library_recognitions` (including manual overrides), and `media_library_scan_runs`, reset baseline/dirty generations plus scan timestamps, and save the new configuration atomically. If the library remains enabled, its supervisor then performs a new `initial -> catch_up` baseline. Never wait for a later reconciliation to remove old-root entries: a partial provider scan intentionally preserves unseen entries and therefore cannot prove they belong to the new source.
- List order is `sort_order,id`. Reordering submits the complete visible ID set and updates it transactionally; download submission with target `0` selects the first enabled library whose Storage, root and Profile are currently usable.
- Each library owns its classification Profile reference plus destination import policy: `move|copy|symlink` and `ask|overwrite|skip|rename`. Recognition preprocessing and movie/TV naming templates belong to the selected Profile. Legacy template columns remain readable for migration/API compatibility but are not the new UI editing source. Local `symlink` intentionally keeps the staging source as a permanent dependency. A 115 library exposes only `move|copy`; the Web UI hides symlink and the service rejects it independently.
- Download enqueue snapshots the library name, Storage/root and transfer/conflict policy together with Profile revision/rules/recognition/naming. Later library or Profile edits never redirect or rename an already queued task.
- Download completion creates a separate idempotent `transfer` Job. Conflict `ask` creates an ActionRequest and releases the worker slot; a successful transfer increments `dirty_generation` so the library supervisor reconciles the new files.
- A `pan115_offline` download may target only an enabled 115 library on the same Connection. Download enqueue revalidates both Storage identities, the target provider-root ancestry, and mutation capabilities, then snapshots storage type, Connection ID and provider root ID privately. Automatic target `0` skips incompatible libraries in `sort_order,id`; an explicit incompatible target fails instead of redirecting.
- A 115 library may independently enable share/manual-transfer intake. It binds one enabled `pan115_offline` downloader on the same Connection plus one Storage-contained intake directory. The intake root must neither contain nor be contained by the final library root, and enabled intake roots on the same Connection must not overlap each other. Disabling intake clears the binding and stops only new adoption; already-created DownloadTasks continue from their immutable snapshots.
- 115 life events only wake intake reconciliation. After a per-library quiet debounce, the Server lists the authoritative direct children of the intake root, skips system-owned `omc-*` directories, and adopts other children through the ordinary DownloadTask pipeline. Startup plus periodic incremental/full reconciliation run the same bounded sweep so delayed or lost events converge without a second watcher queue.
- Intake adoption never identifies, renames, or moves media inline. The adopted DownloadTask snapshots the Profile, naming, transfer and conflict policy, verifies the provider item remains below the snapshotted intake root, then reuses the same package filter, TMDB gate and TransferService as qBittorrent and 115 native offline downloads.
- For seeding-capable downloaders, a completed `copy|symlink` transfer hands off to a separate durable SeedingTask using the download-time policy snapshot. `copy` may eventually delete the staging source; `symlink` must retain it permanently. `move` removes only the stale provider task after import and never enters long-running seeding management.
- Web clients select local and 115 directories through `DirectoryPickerDialog` and submit `relative_root_token`. A 115 token is bound to actor, Connection, Storage, Storage root, provider item, purpose, and expiry; each browse/select operation revalidates that the item remains below the Storage root.
- Enabled creation automatically starts the first full baseline. Disabled creation persists configuration without scanning; enabling later starts initialization.
- A successful baseline is committed before the watcher attaches. An immediate `catch_up` reconciliation must succeed before status becomes `listening`.
- Each enabled library owns an independent supervisor and single-library scan mutex. Supervisors do not consume persistent job-queue slots.
- Provider scanners may implement the optional `cloud.BulkTreeDriver` contract for full recursive scans. The 115 adapter uses a dedicated recursive file stream plus a descendant-folder map to reconstruct provider-relative paths locally; it must not issue one `Stat` per item or one interactive `List` per directory. Interactive directory browsing keeps its conservative limiter, while the bulk lane uses bounded pages, at most two in-flight calls, shared risk backoff/circuit state, cancellation, and partial-result semantics.
- Scanner/provider adapters only enumerate file facts and structural hints. They never own Profile preprocessing, title cleanup, TMDB calls, final classification, `work_key`, or manual correction behavior. Local, 115 and future providers feed the same `GroupRecognitionUnits` plus shared recognizer boundary.
- Recognition units are deterministic: root-level movie files stay separate; one series/season directory is recognized once rather than once per episode; `BDMV` and `VIDEO_TS` use the outer release directory; stable provider IDs may participate only inside a hashed private source anchor. The source key, input fingerprint and provider ID never enter public DTOs or runtime logs.
- Built-in Profile packs run before user Profile RE2 rules and structured parsing. `InputFacts` contains only a bounded package name, provider-root-relative file facts, source kind and explicit hints; no provider ID, absolute root, credential or URL may cross this boundary. Media-library entries use one leading `/` as a logical provider-root marker, and only the validated library-scan adapter may remove that marker before `Parse`; unknown/downloader input with an absolute, UNC or drive path is rejected.
- `ParsedFacts` keeps canonical/title variants, year/season/episode, resource specifications, release group, directory/file-set structure, type evidence and query reasons. Parsing uses stable 1888–2200 year validation and must not depend on the host clock. Chinese `第N集/话` and `第N季` expressions are structural facts and are removed from a larger work-title surface, but an entire legitimate title such as `第八集` must remain searchable. Destructive bracket stripping is forbidden because `[REC]` and similar text can be a legal title; explicit `[tmdbid=123]` / `{tmdb-123}` markers are parsed separately and still require `GetByID` verification.
- TMDB recall covers movie and TV with a maximum of ten search requests while reserving exact-year, `±1`, no-year and cross-type attempts. Within that budget, canonical variants from distinct filename/parent/package sources receive an opportunity before noisy fallback stages from one source, so a dirty primary filename cannot crowd out a clean parent title. Search summaries include localized/original titles; only the initial top three may be enriched with alternative titles, translations and season structure before final ranking. A single enrichment failure degrades to the original safe summary; caller cancellation remains terminal.
- Romanized/Pinyin and official English release names use the same bounded recall and enrichment path as localized titles. Ranking compares enriched alternative titles/translations after normalization; it does not hard-code individual works. If TMDB indexes a romanized query only through a related cross-type result, the recognizer may spend the remaining ten-request budget on that candidate's bounded localized/original titles in the structurally preferred type, then apply the ordinary enrichment and ranking gate. Candidate order is stable and context cancellation terminates partial recall immediately. For a TV release that contains an explicit season, a nearby four-digit year is season-scoped but ambiguous across release-group conventions: compare it with both the season TMDB `air_date` and the series premiere year when known, use the stronger consistent interpretation, and reject only when every known interpretation conflicts. Missing year evidence remains neutral.
- `mediarecognition.Rank` is the only automatic scoring/threshold owner. Services must not reintroduce first-result selection or local `.98/.82/.62` similarity constants. It compares Unicode NFC/case/punctuation/space/Han-equivalent titles plus year/type/season/structure/consistency/uniqueness and weak popularity evidence, then returns exactly one of `matched|no_match|low_confidence|candidate_conflict`. A matched winner is fetched again through `GetByID` before persistence.
- TMDB enrichment's bounded `number_of_episodes` must survive `tmdb.Candidate -> mediarecognition.RemoteCandidate`. For a structurally strong TV input with parsed maximum episode `E`, a candidate with known `EpisodeCount >= E` receives bounded support and a candidate with known `EpisodeCount < E` receives a conflict penalty; a missing count is neutral. Episode evidence cannot establish identity by itself or override a bad title, media type, or year. Manual-search `98%` remains lexical query/title similarity and is never presented to the automatic ranker as final confidence.
- An exact normalized official title, original title, alternative title, or translation is identity-strength evidence: unrelated approximate candidates must not lower that unique exact winner below the automatic threshold, while two distinct exact identities remain a conflict. Latin typo recovery is retrieval-only and bounded: only a multi-token Latin title of sufficient length may contribute at most two distinctive token searches, the global ten-search budget still applies, and the final decision must compare the complete title with at most one edit/transposition plus ordinary year/type/conflict evidence. A token hit alone never establishes identity.
- Normal recognition remains fully automatic; internal Top-k exists only for ranking, diagnostics and benchmark metrics. Bounded TMDB keyword search and direct ID entry are recovery tools shown only after automatic recognition failed, and browser-provided title/year/category/artwork is never trusted.
- TMDB network calls happen outside SQLite transactions. Before committing recognized results, the transaction reloads and verifies source identity, Profile ID/revision and dirty generation so stale network results cannot overwrite a changed library.
- Recognition cache keys bind `mediarecognition.EngineVersion`, unit fingerprint, Profile ID/revision, metadata language and region. An engine-version change must bypass old positive and negative decisions. Matched results default to 30 days, ordinary no-match to 30 minutes, and transient network failures to five minutes. Missing credentials and authentication failures are not retained as durable lookup answers. Cache JSON contains only canonical match/classification fields, never credentials, URLs, paths, provider IDs or raw upstream responses.
- A single recognition failure preserves the enumerated file facts and produces `status=unrecognized` plus a stable `error_code`; it does not fail the scan. Enumerator, database, source-boundary or recognition-configuration failures still fail the scan run. Scan runs expose `matched`, `unrecognized`, `cache_hits`, and `recognition_failed` counters.
- Manual override accepts only TMDB ID and `movie|tv`. The Server fetches and validates that TMDB item, reclassifies it with the current Profile, and atomically updates the recognition plus linked entry projections. Normal reconciliation retains manual overrides; clearing one immediately re-enters automatic recognition. Client-provided title, category, confidence or metadata is never trusted.
- Recognition endpoints use library-scoped non-semantic tokens, enforce read/scan permissions in router and service layers, and return `Cache-Control: no-store`. Responses may include a relative basename summary, but never absolute paths, complete provider paths, provider IDs, cache keys or stored metadata JSON.
- Enabled 115 Connections own independent life-event polling loops outside the persistent Job scheduler. A new cursor anchors at the newest upstream event after the baseline; subsequent allowlisted `created|moved|renamed|deleted` events are durably inserted with the numeric `(update_time,event_id)` cursor before notification. Cookie, pickcode, temporary URLs and raw upstream responses never enter the inbox. Connection refresh starts/stops loops dynamically, failures remain connection-local, and periodic incremental/full reconciliation remains the consistency backstop.
- Local filesystem events and 115 life events are debounced/coalesced per library and route through the same read-only reconciliation path; fingerprints and persistent cache prevent unchanged units from repeating TMDB calls. Periodic incremental/full reconciliation remains the final consistency backstop. A future provider may narrow enumeration to proven affected units, but it must preserve the same grouping, partial-delete and generation-commit contracts rather than add private recognition logic.
- API entries contain provider-relative paths and opaque provider IDs only. Physical roots and raw OS errors are excluded from API responses, ordinary logs, exports, and AI fields.
- File entries remain the reconciliation fact layer. User-facing media uses the catalog read model: movies keep a per-file work key; TV entries share a normalized series work key and expose `Series -> Season -> Episode` details on demand.
- A work/package recognition may expose representative season/episode hints, but those hints never overwrite per-file facts in a multi-file unit. For legacy rows affected by an older package-level `E01` projection, catalog reads may recover season/episode only from the validated provider-relative basename using the shared parser; absent filename evidence preserves the stored value rather than guessing.
- Player series detail projects optional per-episode `title`, `overview`, `still_path`, `air_date`, `runtime_minutes` and `rating`. TMDB season reads run outside SQLite transactions with the request context plus a bounded timeout, fetch only seasons represented by the catalog, and persist a credential-free cache in the recognition snapshot. The cache is bound to TMDB identity and metadata language, stores a complete fetched season within the global bound so later-arriving episodes remain resolvable, and does not mark an over-capacity season complete. Missing episode metadata falls back to the safe provider-relative filename and must not copy the series overview, poster or backdrop into every episode.
- `/entries` and `/catalog` accept `page`, `page_size=20|50|100`, optional `query`, and `media_type=movie|series`. They return database `COUNT`, `LIMIT`, and `OFFSET` results with `list,total,page,page_size`; an out-of-range page is empty with the real total. Legacy `limit` on `/entries` is capped into the supported page sizes.
- Catalog list paging groups by indexed `work_key` in SQLite before applying offset. Never load all entries into Go or group episodes in Vue, because either approach splits a series across pages or scales with the whole library.
- `partial=true` means enumeration was incomplete. Unseen old entries must be preserved because absence was not proven.
- GORM boolean fields that accept explicit `false` must not carry `default:true` model tags. Creation must write explicit zero values rather than allowing ORM defaults to silently enable a disabled library.
- Local Storage rejects STRM fields but may generate managed NFO/JPG beside media. Cloud STRM libraries require signed proxy plus a Server-selected local projection root and generate managed STRM/NFO/JPG only in that projection.
- Template rendering is constrained below the selected library root. Transfer manifests are private provider-relative facts; API DTOs, Job payloads, checkpoints, audits and logs never expose staging or target absolute paths.

## 4. Validation & Error Matrix

| Condition | Stable error / behavior |
|---|---|
| Empty or duplicate name | `media_library_name_required` / `media_library_name_conflict` |
| Traversal, symlink/Reparse Point escape, missing root, or token outside Storage | `media_library_path_invalid` |
| 115 token belongs to another actor/Connection/Storage/root, is tampered, or expired | Reject with the safe directory-token error; expose no provider path or credential |
| Disabled/missing/non-local source in the local slice | `media_library_storage_unavailable` |
| Missing Profile | `media_library_profile_unavailable` |
| Same-Storage roots overlap in either direction | `media_library_overlap` |
| Local source submits STRM configuration | `invalid_request` |
| Full/incremental interval or rate/concurrency exceeds service bounds | `invalid_request` |
| Initial or catch-up scan fails | `initialization_failed`, safe error code, `next_retry_at`; no listener |
| Storage/Profile is referenced | deletion returns conflict / `media_classification_profile_in_use` |
| Import mode, conflict policy, or template is unsupported/unsafe | `invalid_request`; persist no partial update |
| 115 target uses symlink, another Connection, a non-115 downloader, a moved-out provider root, or lacks required mutation capability | Reject as unavailable/invalid before enqueue; never fall back to local upload or another library |
| Intake is enabled for a non-115 library, with a disabled/wrong-Connection downloader, or without a valid intake token | Reject the whole create/update; persist no partial intake configuration |
| Intake root overlaps the final library root or another enabled intake root on the same Connection | `media_library_overlap`; do not start a sweep |
| A replayed life event or periodic sweep sees an already adopted provider child | Treat the unique ingest identity as an idempotent no-op; create no duplicate DownloadTask |
| Automatic target's earlier library is unusable | Skip it and try the next ordered enabled library |
| Explicit target is missing, disabled, or unusable | Reject; never silently redirect to another library |
| Actor lacks a media-library permission | `permission_denied`, even if UI control is hidden |
| Invalid page/page size/query length/media type | `invalid_request`; run no unbounded query |
| Catalog token is invalid or does not belong to the requested library | `invalid_request` or `not_found`; never search another library |
| Recognition status is not `matched|unrecognized`, or manual filter is malformed | `invalid_request`; run no unbounded query |
| TMDB is missing, unauthorized, unavailable, no-match, or below confidence threshold | Preserve facts and return an `unrecognized` item with a stable safe code |
| Best candidate is below the corpus threshold | `tmdb_low_confidence`; do not call the winning `GetByID` path |
| Top candidates remain inside the corpus conflict margin | `tmdb_candidate_conflict`; do not silently select the first result |
| Two exact-title TV candidates differ by known total episode range | Use the parsed episode and enriched bounded `EpisodeCount`; reject the candidate whose known range cannot contain the episode, while missing counts stay neutral |
| Only a high episode count supports an otherwise wrong title/type/year | Keep the item unrecognized; structural range evidence cannot create identity |
| Provider-neutral facts contain an absolute/UNC/drive path, URL or credential marker | `tmdb_invalid_request`; run no TMDB request |
| One top-three enrichment detail fails | Keep the original bounded candidate and continue ranking; never expand beyond the request budget |
| Retry targets a manual override | Reject with conflict until the override is cleared |
| Override submits an invalid type/ID or TMDB does not confirm it | Reject safely; preserve the previous recognition and entry projection |
| Profile/source/generation changes during TMDB work | Reject the stale commit and let the supervisor reconcile the current configuration |
| Storage/root/provider directory identity changes | Atomically clear source-bound entries, recognitions/overrides and scan runs, reset scan state, then automatically build a fresh baseline when enabled; never modify source files |

## 5. Good / Base / Bad Cases

- Good: select a Storage-contained directory, submit its opaque selection token, create enabled, observe `initial` then `catch_up`, and enter `listening` with relative entries.
- Good: select `/Media/TV` below a 115 Storage rooted at `/Media`; persist its stable provider ID privately, scan only descendants, and list one Series row whose detail groups all seasons and episodes.
- Good: a 115 offline downloader and target library share one Connection; enqueue snapshots that identity and later library edits cannot redirect the cloud import.
- Good: a user transfers a release into a configured 115 intake directory with the 115 App; a life event wakes a direct-child sweep, one internal DownloadTask is created, and the existing recognition/Transfer pipeline organizes it into the final library.
- Good: change an enabled library from `/Old` to `/New`, observe the old catalog disappear with the update, then observe only `/New` entries after the automatic initial and catch-up scans.
- Good: local and 115 scans of the same release facts produce the same TMDB identity/category, and the second unchanged scan uses the persisted recognition cache without another TMDB request.
- Good: an unrecognized release remains visible, an administrator searches bounded TMDB candidates and saves an ID-only override, and a later scan retains the verified override.
- Good: `Ming Dynasty in 1566 HQ -BlackTV` with 49 episode files parses to the numeric-preserving title, strong TV evidence and the correct TMDB identity without user interaction.
- Good: `Ai qing gong yu 2012 S03 ...`, `Ipartment S05 2020 ...`, and `Apartment of Love ...` recall the same series through TMDB aliases/translations; 2012 and 2020 validate the corresponding seasons instead of conflicting with the series premiere year.
- Good: `迪迦·奥特曼` matches the unique punctuation-normalized TV identity; `The Final Odyssey` may match a movie only after its enriched official alias is compared; `ULRAMAN TIGA 1996` may use bounded token retrieval but still wins only by complete-title typo, year and type evidence.
- Good: `[银色子弹字幕组][名侦探柯南][第1206集 摔落的男人][WEBRIP][简日双语MP4][1080P]` selects the exact-title TMDB candidate whose enriched series range contains episode 1206 and rejects an exact-title candidate with a known 24-episode range; no title or ID dictionary is involved.
- Base: create disabled, save the explicit `false`, and start no scan until a later update enables it.
- Bad: accept `../`, reuse a token across Storages, start every 115 library at the Storage root, recognize every episode separately, let a provider adapter call TMDB, accept TMDB result zero, copy fuzzy thresholds into services, expose normal-flow Top-k selection, group episodes after pagination, persist an absolute source path in an entry, log `scanErr` containing a filesystem path, mark listening after failed catch-up, delete unseen entries from a partial scan, or retain recognitions/overrides from the old source identity.

## 6. Tests Required

- Migration: v5 is idempotent; all tables, foreign-key restrictions/cascades, and operator/viewer permission seeds are asserted.
- Service: CRUD/RBAC, overlap, local STRM rejection, explicit disabled persistence, automatic baseline plus catch-up, retry state, Storage/Profile references, and Profile revision dirtying.
- Source replacement: pre-populate entries and successful scan runs for one root, change Storage/root/provider identity, assert entries/runs/generations/timestamps are reset in the same update, then enable and assert only the new root is indexed through `initial -> catch_up`.
- Watcher: create/update/move/delete converge; repeated runs cover debounce timing; separate libraries do not share a global lock.
- Provider watcher: same-second numeric event IDs, duplicate/replayed events, restart recovery, unknown event types, connection failure isolation, dynamic disable, cancellation, and zero persistent Job consumption are asserted with fake streams.
- 115 intake: same-Connection downloader validation, final-root/intake-root and cross-library overlap rejection, direct-child-only sweep, `omc-*` exclusion, per-library debounce, startup/periodic fallback, duplicate-event idempotency, immutable intake snapshot, and zero watcher Job consumption are asserted.
- Directory token: root and nested selections resolve to `/`-based relative roots; outside selections fail.
- Provider directory: cross-actor, cross-Connection, cross-Storage, moved-outside-root, tampered, paginated, and expired 115 tokens fail safely; old 115 libraries backfill to the Storage provider root.
- Catalog: parser fixtures cover `S01E02`, `1x02`, `EP02`, Chinese episode/season labels and season folders; 12,099 entries return a true second page and total; Series grouping occurs before pagination; search/filter totals, out-of-range pages, and season/episode order are asserted.
- Player episode projection: multi-file package hints preserve E01/E02/... file facts; legacy all-E01 rows recover from safe relative basenames; TMDB season calls inherit cancellation and stay bounded; full-season metadata is cached by TMDB identity plus metadata language; a later episode in an already-fetched season reuses that cache; missing episode metadata exposes neither copied series text nor copied series artwork.
- Recognition grouping: root movies remain separate, season episodes share one unit, disc structures use the outer release folder, and stable provider identity survives a rename without exposing the raw ID.
- Recognition integration: local and fake provider facts share the exact recognizer; `Seven.Samurai.1954...` queries `Seven Samurai` and matches a localized TMDB response through original title plus year; a repeated unchanged scan asserts zero additional TMDB requests.
- Next-generation corpus: offline fixtures cover numeric titles, legal hyphens/brackets, 49-episode TV structure, BDMV/VIDEO_TS, Chinese simplified/traditional, English/Japanese/Korean names, release/audio suffixes, close candidates and strong year conflicts. Golden baseline/candidate reports must be reproducible without live TMDB and must label synthetic/reference behavior honestly.
- Ranking/retrieval: candidate input order does not change the decision; a unique exact normalized identity survives nearby approximate franchise candidates, duplicate exact identities conflict, and one-character Latin typo recovery cannot promote a token-only candidate. Exact/±1/no-year/cross-type/token requests remain within ten searches; detail enrichment remains within three candidates; alternative/translated titles can change the winner; no-match, low-confidence and conflict return distinct reason codes and never call `GetByID` for a rejected winner.
- Episode-range ranking: the untouched E1206 release is covered at parser, shared service and frozen corpus layers; known containing/insufficient totals resolve the exact-title conflict in either candidate order, missing totals remain neutral, ordinary low episodes do not fabricate uniqueness, and a high total cannot promote an unrelated title/type/year.
- Multilingual/season-year regression: Pinyin/Romanized, official English and localized aliases share one generic ranking path; a wrong-type romanized hit can bridge through its authoritative title within ten total searches; `SeasonYear` is separated from series `Year`, TMDB season air dates affect only the matching season, series-premiere-year naming remains accepted, unrelated year conflicts still reject unsafe matches, and context cancellation terminates partial recall.
- Recognition failures/correction: missing credential, auth, network, no-match and low confidence preserve facts; retry, bounded candidate search, verified override, retained override and clear-then-auto behavior are covered with RBAC, strict JSON, no-store and safe DTO assertions.
- v25 migration: fresh creation, v24 upgrade and repeated migration assert recognition/cache tables, entry/run additive columns, indexes/cascades, old entries retained as pending facts, and source replacement removing recognitions plus manual overrides.
- API: automatic initialization reaches `listening`, entries/catalog contain no physical root, invalid paging returns 400, strict JSON rejects unknown fields, reorder persists, and deletion leaves source files untouched.
- Import: target selection order/manual override, immutable download snapshots, local copy/move/symlink and 115 move/copy behavior, sidecars, conflict options, restart idempotency including move-before-rename, provider root `0`, root confinement, redacted checkpoints, copy ambiguity preservation, and dirty-generation handoff.
- Live acceptance is opt-in through `OMC_LIVE_LIBRARY_ROOT`; compare file list, size, mode, and nanosecond timestamps before/after and never hard-code the real root.

## 7. Wrong vs Correct

Wrong:

```go
Enabled bool `gorm:"default:true"`
log.Error().Err(scanErr).Msg("scan failed")
for _, unseen := range oldEntries { tx.Delete(&unseen) } // even on partial scan
page := allEntries[offset:end] // full-library load and post-query grouping
if library.StorageID != downloader.StorageID { useFirstLibrary() } // silently redirects a cloud download
```

Correct:

```go
Enabled bool `gorm:"not null"`
log.Error().Str("error_code", CodeMediaLibraryScanFailed).
    Uint("library_id", id).Uint("scan_run_id", run.ID).Msg("scan failed")
if !result.Partial {
    for _, unseen := range oldEntries { tx.Delete(&unseen) }
}
page := db.Where("library_id = ?", id).
    Group("work_key").Offset(offset).Limit(pageSize)
target, err := snapshotDownloadTarget(ctx, downloader, library) // proves same Connection and root before enqueue
```

Wrong:

```go
for _, file := range providerFiles {
    title := pan115.GuessTitle(file.Name)
    match := tmdb.Search(ctx, title) // provider-private recognizer and one lookup per episode
    tx.Save(&match)                  // network-derived result committed without generation validation
}
```

Correct:

```go
facts := provider.Enumerate(ctx, root)                  // no TMDB
units := medialibrary.GroupRecognitionUnits(facts)
results := recognizer.Recognize(ctx, profile, units)   // outside DB transaction
commitIfCurrent(tx, librarySource, profile.Revision, generation, results)
```

Wrong:

```go
match := tmdb.Search(ctx, title) // accepts result zero and owns hidden thresholds
if match.Confidence >= .82 { persist(match) }
```

Correct:

```go
parsed, err := mediarecognition.Parse(facts)
candidates := recallWithinBudget(ctx, parsed.Queries)
decision := mediarecognition.Rank(parsed, enrichTopThree(ctx, candidates))
if decision.Status == mediarecognition.DecisionMatched {
    verified := tmdb.GetByID(ctx, decision.Match.MediaType, decision.Match.ID)
    persist(verified)
}
```

Wrong:

```go
remote := mediarecognition.RemoteCandidate{Title: candidate.Title} // drops enriched EpisodeCount
if candidate.Confidence == .98 { persist(candidate) }              // lexical search similarity is not final confidence
```

Correct:

```go
remote := remoteRecognitionCandidate(candidate) // preserves bounded EpisodeCount
decision := mediarecognition.Rank(parsed, []mediarecognition.RemoteCandidate{remote})
```

Wrong:

```go
record.BaselineGeneration = existing.BaselineGeneration // even after root change
tx.Save(&record)                                         // old catalog remains attached to the same library ID
```

Correct:

```go
sourceChanged := existing.StorageID != record.StorageID ||
    existing.RelativeRoot != record.RelativeRoot ||
    existing.ProviderRootID != record.ProviderRootID
if sourceChanged {
    tx.Where("library_id = ?", id).Delete(&models.MediaLibraryEntry{})
    tx.Where("library_id = ?", id).Delete(&models.MediaLibraryScanRun{})
    // Keep the new record's zero generations/timestamps so the supervisor builds a fresh baseline.
}
```

## Scenario: Metadata Snapshot and Managed Media Artifacts

### 1. Scope / Trigger

- Trigger: changing shared recognition output, TMDB detail mapping, `media_artifact` Jobs, NFO/JPG/STRM generation, local adjacent output, or cloud STRM projection.

### 2. Signatures

```text
media_library_recognitions.metadata_json = {
  "version": 1,
  "classification": { ... },
  "snapshot": { "version": 1, "tmdb_id": ..., "media_type": ..., ... }
}

media_artifact Job payload = { "artifact_run_id": "<uuid>" }
media_artifact_runs.policy_json = private immutable generation/root/target/scan snapshot
media_artifact_runs.cleanup_status = pending|running|completed|failed|skipped
media_artifacts(library_id, target_kind, relative_path) = managed ownership manifest
```

Relevant service boundaries:

```go
ScheduleGeneration(libraryID uint, generation uint64) error
tmdb.Client.DownloadJPEG(ctx, imageIdentity, size string, maxBytes int64) ([]byte, error)
nfo.Render(snapshot tmdb.Snapshot) ([]byte, error)
```

### 3. Contracts

- A successful scan transaction commits source facts, recognition, the versioned credential-free TMDB snapshot and generation before scheduling an artifact run. Recognition cache keys include the snapshot schema version so a pre-snapshot 30-day cache entry cannot suppress detail refresh.
- Snapshot JSON contains stable TMDB/IMDb IDs, metadata text, people, country/language fields and TMDB image file identities. It never contains credentials, API/image origins, absolute paths, provider IDs, temporary URLs or raw responses.
- Local libraries with metadata artifacts enabled target `local_adjacent`; cloud + STRM targets `local_projection`. Cloud + STRM always uses opaque signed URLs and never stores provider paths/pickcodes in STRM content.
- Unrecognized or incomplete snapshot units may still produce STRM, but produce no NFO/JPG. Deterministic NFO/JPG output is derived only from the persisted snapshot.
- Every output first resolves a root-confined target, rejects reparse/symlink escapes, writes a same-directory temporary file, flushes it, then atomically replaces only a manifest-owned artifact. An unmanaged same-name file is skipped and never adopted.
- TMDB image download accepts only a snapshot file identity plus an allowlisted size, rejects redirects and non-JPEG content, checks the JPEG magic bytes, and enforces a caller-selected limit no greater than 20 MiB.
- Cloud STRM source assets always include persisted `srt|ssa|ass|jpg` facts and may include up to 16 per-library extra extensions. Extras are normalized lowercase ASCII alphanumerics without dots, paths, whitespace or wildcards; defaults and the fixed 17 video extensions are rejected. The effective set is snapshotted into the artifact run. Before download the worker re-stats the provider item and rechecks ID/parent/type/size, obtains a temporary URL only in memory, rejects sensitive required headers and redirects, bounds text/JPEG bytes, then writes the original relative path through the same manifest boundary.
- A 115 temporary playback URL is acquired with the exact downstream User-Agent and cached in that User-Agent scope. `115driver` acquisition headers such as Cookie, Content-Type and Referer are provider API request metadata, not CDN requirements; the 115 adapter discards them and exposes only the matching User-Agent requirement. The signed resolver accepts that exact requirement but still fails closed for Cookie, Referer, Authorization, a mismatched User-Agent or any other header that a plain 302 cannot express safely.
- Every artifact policy immutably binds the source scan run ID, scan kind, partial flag, cleanup eligibility and canonical projection-root identity. Only a complete successful non-partial authoritative scan may enable automatic cleanup; failed, partial, superseded, unknown-kind or changed-root runs preserve stale artifacts for manual review.
- Artifact completion deactivates the old manifest, marks the run complete and advances the library applied generation in one transaction guarded by the current generation. Cleanup then follows its own persisted `pending -> running -> completed|failed|skipped` state machine. Replaying a completed artifact Job resumes pending/running/failed cleanup, while a cleanup failure never rolls the already completed artifact generation back.
- Automatic and manual cleanup share one deletion primitive. It holds the per-library scan mutex, revalidates generation/root/manifest snapshot at each file boundary, and atomically claims each candidate as `cleanup` before deleting it. Automatic cleanup may resolve only the current projection root; confirmed manual cleanup may resolve previous roots solely from each artifact owner's immutable policy, with the complete root-identity set hashed into the token. Manifest deletion and the run's cumulative removed count commit together; a crash after file deletion leaves a recoverable claim that converges on replay.
- STRM administration reads only safe library/run/artifact DTOs. Incremental/full requests enqueue durable `strm_reconcile` Jobs; failed artifact runs may be retried. Manual cleanup first issues an operation/actor/library/generation/root-hash/manifest-bound short-lived HMAC confirmation token. Tokens use bounded canonical Base64URL plus strict JSON and never encode the absolute projection root.
- Cleanup deletes only inactive managed `local_projection` artifacts whose kind matches an allowlisted extension: STRM, NFO, generated JPG artwork, subtitles, and policy-snapshotted source companions. It never adopts or deletes unmanaged same-name files. Candidate rows remain tied to their owning run and canonical root.
- Cleanup never follows symlink, junction or other reparse boundaries. Path, ownership, kind/extension, root identity, generation and manifest snapshot are checked before claim and again before deletion; a changed boundary stops the run with a stable safe code. Cleanup audits, logs and API DTOs contain IDs, counts, state and safe codes only, never absolute roots or provider details.
- One active coalesced Job per library advances to the latest queued generation. Older runs become `superseded`; each file boundary rechecks the current generation and heartbeats the persistent queue lease.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Old classification-only metadata JSON | Read as legacy classification with an empty snapshot; refresh on the next non-manual recognition |
| Snapshot missing version/TMDB ID/type/title | Skip NFO/JPG as metadata incomplete; do not guess |
| Local output root cannot be resolved/canonicalized | Reject scheduling or fail the run with `artifact_projection_unavailable` |
| Cloud STRM lacks signed proxy or projection root | Reject media-library policy before a run is created |
| Existing target has no managed manifest | Count as skipped; never overwrite or adopt it |
| Job generation no longer matches the library | Mark the old run superseded at the next artifact boundary |
| Scan is failed, partial, superseded, unknown-kind, or projection root changed | Do not automatically delete; persist `skipped` or a safe cleanup failure for manual review |
| Cleanup process stops after artifact completion | Replay the completed artifact Job and resume from persisted cleanup state/claims without regenerating or double-counting |
| Candidate kind/extension, ownership, root, generation or manifest snapshot changes | Stop cleanup with a stable safe code; never delete the candidate |
| Image identity is absolute, redirects, exceeds limit, is non-JPEG or has wrong magic | Fail the metadata artifact unit safely; persist no URL/body |
| Source asset extension is not allowlisted, provider identity changed, or temporary URL requires headers | Skip unsupported extension or fail safely; never persist/log URL, headers, pickcode or provider ID |
| 115 SDK returns Cookie/Content-Type/Referer acquisition headers with a playback URL | Normalize them inside the 115 adapter to the exact User-Agent binding; never forward, persist or log those headers |

### 5. Good / Base / Bad Cases

- Good: one matched movie scan commits a full snapshot, writes `Movie.strm`, `Movie.nfo`, `Movie-poster.jpg` in a cloud projection and later regenerates identical managed content.
- Base: an unrecognized video writes only its signed STRM and remains visible for correction.
- Bad: save `image.tmdb.org` URLs in SQLite, derive URLs from request Host, overwrite a user NFO because the name matches, or let a 115 adapter implement its own NFO renderer.

### 6. Tests Required

- TMDB fake responses assert detail/credits/external IDs map into a bounded snapshot and serialized snapshot/NFO contains no credential or configured origin.
- Local artifact tests assert adjacent NFO/JPG, no STRM, root confinement and unmanaged-file preservation.
- Cloud tests assert ordinary and ISO STRM naming, opaque signed content, NFO/JPG projection, coalesced latest generation, superseded old run and manifest counts.
- Image tests assert allowlisted size, redirect rejection, MIME/magic/byte limits and invalid absolute identity rejection.
- Source-asset tests assert same-tree subtitle/JPG output, provider identity revalidation, binary subtitle rejection, redirect/header/size rejection and absence of temporary URLs in DB/logs.
- Cleanup tests assert full/incremental success, partial/failed/superseded/root-change preservation, replay recovery, idempotent removed counts, per-file claims, unmanaged preservation, kind/extension allowlists, token tamper/size/strict-JSON rejection, and symlink/junction/reparse confinement.
- Migration tests assert v29 additively introduces cleanup status/summary columns and backfills historical completed/superseded runs as `skipped` so upgrading cannot unexpectedly delete old projections.
- Run the focused packages plus `go test ./...`, `go vet`, `go mod verify` and `git diff --check` on Windows.

### 7. Wrong vs Correct

Wrong:

```go
snapshot.Poster = imageBase + detail.PosterPath // persists deployment URL
os.WriteFile(target, nfo, 0o644)                // overwrites unmanaged content
```

Correct:

```go
snapshot.PosterPath = validatedTMDBFileIdentity(detail.PosterPath)
if !manifestOwns(target) { return skipped }
atomicWriteArtifact(root, target, rendered)
```

## Scenario: Authoritative Media Change and Notify Convergence

### 1. Scope / Trigger

- Trigger: changing catalog reconciliation, manual recognition, managed artifacts, Emby/Jellyfin refresh targets, or Player ServerDataSource change delivery.

### 2. Signatures

```text
media_libraries.content_revision
media_library_changes(sequence, library_id, content_revision, kind, state, generation)
media_server_refresh_targets(desired_revision, successful_revision, manual_generation, successful_manual_generation)
media_server_refresh_runs(target_id, requested_revision, job_id, status, error_code)

GET /api/v1/player/media-changes?cursor=<uint>&wait_seconds=<0..12>
Authorization: Bearer omc_player_...

media_server_refresh Job payload = { "target_id": <uint> }

POST /api/v1/media-server-refresh-targets/:id/test
POST /api/v1/media-server-refresh-targets/:id/retry
```

### 3. Contracts

- A complete authoritative reconciliation increments `content_revision` once and writes its change in the same transaction. No-op, failed, partial, superseded, stale-generation, or conflict-waiting work does not publish a ready change.
- A change that requires STRM/NFO/JPG or cleanup is persisted as pending. The matching current artifact generation and cleanup must succeed before it becomes ready. Manual metadata override uses the same new-generation barrier when sidecars are enabled.
- Artifact coalescing may finish a newer complete no-op generation that has no change row. That generation carries forward the newest older pending change represented by its completed projection and supersedes only the remaining older pending rows. A matching generation with its own change supersedes all older pending rows. Partial artifact generations never publish or discard pending authoritative changes.
- Artifact readiness uses one storage-aware predicate across reconciliation and manual-recognition paths: local libraries wait only when local metadata artifacts are enabled; cloud libraries wait only for an enabled signed STRM projection. Cloud metadata-upload policy without STRM must not create a pending change when no artifact producer can satisfy it.
- Marking a change ready advances every enabled refresh target's desired revision and wakes Player waiters only after commit. Emby/Jellyfin and Player consume the same ready revision independently.
- Refresh jobs coalesce by target and store only `target_id`; workers reload encrypted Connection credentials and the latest desired/manual generation at execution time. Manual refresh at content revision zero still executes once without fabricating a content revision.
- Target creation snapshots the latest ready revision atomically with insertion. Re-enabling a target catches its desired revision up to the latest ready library revision before enqueueing, so changes committed while disabled are not lost. Target testing revalidates the saved stable upstream library ID; retry is available only for an enabled terminally failed target with outstanding work.
- The Player endpoint authenticates every bounded poll with device Bearer, filters libraries through current visibility, and returns only logical library ID, revision, controlled kind, time, cursor, and `resync_required`. It never returns paths, provider/upstream IDs, credentials, signed URLs, or raw upstream bodies.
- Ready history is bounded globally, but pruning always retains the latest ready row for every library. A cursor older than retained history receives `resync_required` and a new safe baseline instead of an unbounded per-device queue.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Artifact generation is pending, failed, or superseded | Keep the change non-ready; do not advance targets or Player visibility |
| A complete no-op artifact generation supersedes an older pending generation | Publish the newest represented pending change once; discard only older pending projections |
| Artifact enumeration is partial | Do not publish or supersede any pending authoritative change |
| Emby/Jellyfin authentication or target configuration is invalid | Persist a safe terminal error; do not retry as transient |
| Upstream is unavailable or rate-limited | Use the queue's bounded retry policy without blocking other targets or Player; after the final attempt keep the target terminally failed until explicit retry/configuration correction |
| A disabled target is re-enabled after ready changes | Atomically catch up desired revision and enqueue the target |
| Target is deleted while an old Job remains | Worker exits as a safe no-op |
| Desired/manual generation advances while a worker runs | Reconcile the latest generation before terminal success |
| Player token is revoked or user loses access | Reject/re-filter the next poll; cursor never authorizes access |
| Player cursor predates retained history | Return `resync_required=true` and a current cursor |

### 5. Good / Base / Bad Cases

- Good: a 115 scan commits catalog state, finishes STRM/sidecar generation and cleanup, then independently refreshes every enabled media-server target and wakes eligible Players.
- Base: no refresh target is configured; ready changes still reach Player and the administration UI shows a truthful empty state.
- Bad: emit from downloader completion, put API keys/upstream library IDs in Job payloads, or mark a manual metadata change ready before its replacement sidecars exist.

### 6. Tests Required

- Migration tests cover fresh/upgrade/repeat application, defaults, indexes, foreign keys, revision zero, and queue policy registration.
- Media-change tests cover ready/pending/no-op carry-forward/partial/stale/superseded transitions, artifact recovery, manual metadata barrier, latest-per-library retention, and `resync_required`.
- Refresh tests cover Emby and Jellyfin prefixes/auth/response bounds/redirect rejection, revision-zero manual refresh, atomic create and re-enable catch-up, explicit target test/retry, bounded transient retry, terminal-failure restart suppression, coalescing generation, target isolation, restart recovery, target-referenced connection deletion, deleted targets, and secret-free payloads/DTOs.
- Router tests use device Bearer, verify revocation/visibility, and assert no path/provider/credential leakage.

### 7. Wrong vs Correct

Wrong:

```go
onDownloadComplete(func() { refreshAllServers(); broadcastToPlayers(entry) })
```

Correct:

```go
change := recordPendingOrReadyInCatalogTransaction(tx, library, generation)
afterCommit(func() { wakeReadyConsumers(change.LibraryID) })
```
