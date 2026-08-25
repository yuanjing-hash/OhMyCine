# Frontend Directory Structure

> How OhMyCine Player frontend and Tauri code are organized.

---

## Overview

Player uses Tauri v2 + Vue 3 + TypeScript + UnoCSS. The web UI lives under `player/src/`; Rust/Tauri integration lives under `player/src-tauri/`. Windows-native development and runtime verification are the local default. Linux/WSL cross-built artifacts remain supplementary and must not be treated as Windows runtime proof.

The repository is design-first. Use existing directories when present and do not create broad rewrites to match a planned tree unless the task explicitly asks for it.

---

## Target Directory Layout

```text
player/
├── src-tauri/
│   ├── src/
│   │   ├── commands/       # Tauri commands exposed to Vue
│   │   ├── mpv/            # libmpv FFI/player integration
│   │   └── render/         # platform rendering contexts
│   ├── Cargo.toml
│   └── tauri.conf.json
├── src/
│   ├── assets/             # fonts, images, icons
│   ├── components/
│   │   ├── ui/             # reusable Cinema OS UI primitives
│   │   ├── layout/         # app layout, sidebar, window chrome
│   │   ├── player/         # playback UI, controls, track menus
│   │   ├── media/          # cards, grids, hero, poster wall
│   │   └── common/         # search, settings, server status
│   ├── views/              # route-level pages
│   ├── stores/             # Pinia stores
│   ├── composables/        # reusable Composition API logic
│   ├── services/           # datasource, scraper, AI, sync clients
│   ├── router/             # Vue Router
│   ├── i18n/               # translations
│   ├── styles/             # CSS variables, glass, animations, global
│   └── utils/              # pure utilities
└── scripts/                # build/download helpers
```

---

## Module Organization

### Views

Views are route-level orchestration components. They may compose stores, services, and UI components, but should not contain reusable business logic that belongs in composables/services.

Important views:

- `HomeView`: aggregated home from all configured data sources.
- `SourceLibraryView`: one data source's library/file view.
- `PlayerView`: video surface and controls.
- `SettingsView`: local settings and data source management.
- Server-enhanced views such as downloads/discovery/follow must degrade when Server is disconnected.

### Components

- `components/ui`: generic primitives such as buttons, cards, dialogs, inputs, sliders.
- `components/layout`: shell, dynamic data-source sidebar, window chrome.
- `components/player`: playback controls, progress, subtitle/audio menus, playlist, danmaku overlay.
- `components/media`: MediaCard, MediaGrid, MediaRow, HeroCarousel, PosterWall, details.

### Services

- `services/datasource`: DataSource types, manager, credential helpers, safe error/redaction helpers, and Emby/Jellyfin/OpenList/Alist/CloudDrive2/local/server implementations.
- `services/scraper`: filename parser, TMDB, metadata DB/cache.
- `services/ai`: Player-side RAG recommendations using user-provided keys.
- `services/sync`: Player ↔ Server structural/full sync flows.
- `services/danmaku`: parsers, sources, renderer.

### Raw File Source Scraping and Classification

Use this contract when adding Player-side scraping, metadata cache, classification, or poster-wall views for raw file sources such as OpenList/Alist, CloudDrive2, local files, or future custom file sources. Emby/Jellyfin already provide server-side library metadata and must not be forced through this local classification flow by default.

#### 1. Scope / Trigger
- Trigger: adding or changing local scraping, scan jobs, filename/path parsing, TMDB matching, poster/backdrop cache, scan logs, logical classification rules, or poster-wall rendering for raw file sources.
- Applies to `services/scraper`, source-scoped local metadata stores, Tauri SQLite/cache commands, source library views, settings for scraping/classification rules, and any DataSource method that exposes scraped results.

#### 2. Signatures
- Source root input: `DataSourceConfig.extra.rootPath` or equivalent user-selected root. For OpenList/Alist it is a provider path scoped by the DataSource, defaulting to `/`.
- Scan input: source id, source type, selected root path, scan mode preference (`auto`, `standard`, `nonStandard`), and optional rule-set id/version.
- Scan outputs: raw file records, parsed media candidates, matched metadata records, logical category assignment, unresolved records, scan log entries, and local artwork cache references.
- Rule-set shape: logical groups may be separated by media type internally, but the physical remote folder tree must not be required to contain fixed top-level names such as `movie`, `tv`, `Movies`, or `TV`.
- Visible source-page MVP: raw sources may expose a local `media-library` view alongside the original `folders` view. The media-library view can be powered by a source/root-scoped local scan cache until the full SQLite scraper DB exists.
- Recognition candidate contract: `RecognitionRemoteCandidate` carries bounded `id`, `mediaType`, localized/original title, `originalLanguage`, alternative titles, translations, release year, season/episode counts, popularity, vote count, and poster presence. Player `player-nextgen-v4` implements shared `media-recognition-contract-v3`; changing either constant requires an automatic-cache invalidation review.

#### 3. Contracts
- The scanner starts from the user's selected root and infers the structure below that root. Never reject or downgrade a library only because the root or first child is not named `movie`/`tv`/`Movies`/`TV`.
- Standard directory mode is a detection result, not a hard-coded path requirement. It may use folder depth, title-year folders, `Season NN`, episode patterns, known category-name hints, and media-file distribution to infer movie/series/season/episode structure.
- Path-aware recognition for raw files must follow a MoviePilot-like merge order: parse the file stem first, then the parent segment, then the grandparent segment, and merge missing identity fields from later segments. A parent folder named `Season 01`, `S01`, or `第1季` is a season signal, not a title. A file stem such as `S01E01` is an episode signal, not a title. In `剧名/Season 01/S01E01.mkv`, the grandparent `剧名` supplies the series-title candidates.
- Clear standalone movie filenames must become movie candidates even when they have no year and are placed directly under the selected root, for example `阿凡达.mp4`. If the first parent segment is a known explicit category such as `电影` or `华语电影`, `电影/阿凡达.mp4` and `电影/阿凡达/阿凡达.mp4` must keep that path category hint. A likely work folder such as `阿凡达/阿凡达.mp4` must not create a fake category hint from `阿凡达`; classification should come from TMDB metadata rules or media-kind fallback. Obvious non-work filenames such as `sample.mp4`, `4K.mp4`, `video.mp4`, trailers, clips, extras, and test/demo files must remain unresolved until manually identified or matched by a stronger signal.
- Non-standard mode treats directory names as unreliable and primarily uses filename parsing plus TMDB matching. It must preserve every playable video as a file/fallback item even when metadata matching fails.
- Path-derived category hints from standard directory structures are final library categories before TMDB rule assignment for matched items. The final media-library grouping priority for matched raw-source items is explicit path/category-folder hints such as `综艺` / `动漫` / `国产剧`, then TMDB metadata rule assignment when no clear path category exists, then media-kind fallback (`电影`, `剧集`, `未识别`). TMDB metadata may enrich title, poster, overview, and detail fields, but it must not duplicate the same path-scoped work into another TMDB-derived category. Never create a category card from a likely work folder such as `机械之声的传奇 The Legend of Vox Machina AMZN GrassTV`.
- Any raw scraped item whose TMDB match status is missing or not `matched` (`notConfigured`, `notFound`, `failed`, `skipped`, or future non-matched states) must use visible category `未识别` in media-library category cards, regardless of parsed media kind, path category hint, or path-derived movie/TV structure. Keep the parsed title, series title, season number, episode number, provider path, and fallback media type so the `未识别` category can still aggregate playable files by work/season/episode and support later manual identification.
- Title extraction for TMDB matching must strip release/source/subtitle noise such as platform tags (`AMZN`, `Netflix`), release groups (`GrassTV`, `NTb`, `PTerWEB`), resolution/codec tokens, and subtitle tags while preserving real title tokens. A folder named `机械之声的传奇 The Legend of Vox Machina AMZN GrassTV` should yield search candidates including `机械之声的传奇` and `The Legend of Vox Machina`.
- Raw-title parsing is Unicode-first: normalize to NFC and preserve Unicode letters, numbers, and marks without maintaining a script allowlist. Natural-language season/episode tokens may be removed only when a meaningful work title remains; machine tokens such as `S01E02` stay structural, while legal whole titles such as `第八集`, `[REC]`, `Spider-Man`, and `Tinker-Tailor-Soldier-Spy` must not be emptied or truncated. Keep fixtures for Chinese, English, Japanese, Korean, Latin diacritics, Cyrillic, Arabic, and Thai.
- Automatic TMDB recall must give canonical file, parent, and grandparent sources an opportunity before consuming same-source fallback variants. Keep the order deterministic and bounded to at most 10 search requests and 3 detail enrichments per work. Rank the merged movie/TV identities using localized, original, alternative, and translated titles plus year, parsed type, season structure, and uniqueness; reject low-confidence and close-conflict decisions instead of accepting the provider's first result.
- Player and Server recognition implementations must execute the same provider-neutral redacted corpus. A Player recognition engine version and `automatic` / `manual` match source belong in the local scan cache. Engine drift may invalidate and recompute automatic results, but valid manual identity and metadata/artwork overrides must be retained. Legacy matched records with no reliable source marker are preserved conservatively; legacy failures may be recomputed.
- Provider authority is only a bounded same-identity tie-break. It may contribute at most `0.03`, and only when both candidates are exact-title, same-media-type, conflict-free matches. Releasing a close-candidate conflict additionally requires the winner to expose at least six independent completeness dimensions and to lead materially over the runner-up. Popularity and vote count may refine an already established identity, but must never establish identity or override a title, media-type, year, or known episode-count conflict. Missing provider fields are neutral.
- TMDB search-to-detail mapping must preserve the complete safe authority field chain. Search summaries provide fallback `originalLanguage`, year, popularity, vote count, and poster presence; detail payloads enrich original/alternative/translated titles, season/episode counts, popularity, vote count, and poster presence before final ranking. Local scan cache sanitization must whitelist the same non-secret metadata fields so a cache round-trip cannot change the decision surface.
- A non-fatal top-candidate detail failure must not silently remove that identity from final conflict evaluation. Keep its bounded search summary in the candidate set; if it remains the winner but its detail metadata is unavailable, return an unresolved result instead of selecting a lower-quality shell or fabricating detail metadata.
- This local recognizer applies only to Player-scanned raw sources such as local files, OpenList/Alist, and CloudDrive2. `ServerDataSource`, Emby, and Jellyfin metadata remain authoritative and must not be routed through Player local recognition or reclassified by its engine version.
- Classification rules are local logical grouping rules for poster walls, filters, library sections, AI/recommendation context, and future suggested organization. They must not mutate, validate, rename, move, delete, or upload files on the remote provider.
- Classification matching may use TMDB detail fields such as `original_language`, `production_countries`, `origin_country`, `genre_ids`, `release_year`, and later top-level detail fields. Multiple fields are ANDed, comma values are ORed, and `!value` excludes a value.
- User-provided TMDB credentials are an optional enhancement, not the only usable metadata path. If no user token/key is configured, raw-source scanning must still keep playable candidates, path recognition, fallback categories, and folder browsing available; future built-in/public metadata channels must not be represented by hard-coded secret keys in the client.
- Movie and TV rule groups both default to fallback category `未分类`. `外语电影` may be an explicit editable default/example movie category, but fallback defaults, placeholders, and migrated legacy fallback values must not use it.
- Raw provider paths and provider names are untrusted. Normalize paths before scan parsing, reject plain or URL-encoded `.` / `..` segments, reject provider names containing `/` or `\` when joining with a parent path, and skip invalid provider records instead of throwing through the whole scan.
- Manual raw-source scans must go through the active `DataSource.list()` boundary, not provider-specific API clients in the view. The folder/file view remains available if scan fails or no scan cache exists.
- MVP recursive scans must enforce conservative limits such as max depth, max folders, and max entries. Hitting a limit should mark the scan partial and log a local warning, not hang the UI or break folder browsing.
- Local scan cache writes are best-effort. If persistence fails, keep the current in-memory scan result visible and record a local warning; do not fail the scan solely because cache persistence failed.
- All scan logs, match decisions, metadata records, posters, backdrops, and derived category assignments are stored locally in Player app data. Do not write them back to OpenList/Alist, CloudDrive2, local source roots, or any provider unless a later task explicitly designs a write-capable workflow.
- Future right-click identify, manual TMDB result selection, poster/still upload, and metadata editing for raw-source items are a local override layer only. Persist those overrides under source/root-scoped Player app data/cache and never rename, move, upload, delete, or otherwise mutate OpenList/Alist or other raw providers from that override flow.
- Local scan cache must store only whitelisted raw-file fields and derived TMDB/category metadata. Reject or skip URL-like provider paths and paths containing signed/token query keys before caching so tokenized playback URLs are not persisted as media identities.
- Do not store OpenList/Alist credentials, tokens, tokenized stream URLs, full sensitive provider URLs, or local absolute paths in metadata records intended for display, AI prompts, export, or logs.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| User-selected root has no `movie`/`tv` named child | Continue auto detection from the selected root; do not mark it invalid for this reason |
| Directory has strong title/year or season/episode structure | Prefer standard-mode parsing and use TMDB to confirm/enrich metadata |
| Directory is mixed or ambiguous | Fall back to non-standard parsing, keep unresolved items playable, and record a local diagnostic |
| Root contains a clear standalone movie file such as `阿凡达.mp4` | Parse it as a partial movie candidate and attempt TMDB matching without requiring a movie folder or year token |
| Path starts with a known manual category such as `电影/阿凡达.mp4` or `电影/阿凡达/阿凡达.mp4` | Preserve the explicit category path hint for matched display grouping |
| Path uses a work folder without a category such as `阿凡达/阿凡达.mp4` | Parse the movie candidate but do not use the work folder as a visible category |
| Filename is obvious non-work noise such as `sample.mp4`, `4K.mp4`, or `video.mp4` | Keep it unresolved rather than creating a noisy movie candidate |
| Standard/non-standard confidence is low | Keep scan usable and expose a user confirmation/switch point rather than failing the scan |
| Recursive scan exceeds depth/folder/file limits | Stop that branch or scan, mark partial, log a warning, and keep already collected results usable |
| TMDB is unavailable, rate limited, or key is missing | Keep file browsing/playback available, keep unresolved candidates, and show a user-safe metadata-unavailable state |
| TMDB matches a work to genre/country/language metadata | Use an explicit standard-directory path category first; when no clear path category exists, run configured classification rules on TMDB fields for poster-wall grouping |
| Raw scraped item has missing match result or a non-`matched` TMDB status | Show it under visible category `未识别` while preserving parsed title/series/season/episode structure and playable provider path |
| Exact same-title/same-type TMDB candidates include one complete work and one nearly empty shell | Use bounded authority only after title/type/structure establish the identity; the materially complete candidate may resolve the close conflict |
| Candidate has more votes/popularity but a wrong title, type, year, or too-small known episode count | Keep the strong conflict and reject or choose the structurally compatible candidate; authority must not rescue it |
| Authority fields are missing, zero, malformed, or outside configured bounds | Treat them as absent/neutral and keep deterministic title/structure ranking |
| A shortlisted TMDB detail request fails transiently | Retain its safe search summary for conflict evaluation; never let the failed fetch delete the candidate and promote an empty duplicate |
| Source is `server`, Emby, or Jellyfin | Trust source metadata; do not invoke Player raw-source recognition or invalidate it through Player engine drift |
| Folder name looks like a work title plus release/source tags | Use it as a search-title source, not as a visible category name |
| File path contains traversal, encoded traversal, unsafe joined name, or escapes the selected root | Reject/skip the path before browse/detail/stream/scrape work |
| Provider item path is an HTTP/WebDAV/file URL or contains token/signature/password query keys | Reject/skip it before writing local scan cache |
| Local scan cache write fails | Keep the current scan result in memory, log a local warning, and leave folder browsing/playback usable |
| Classification rule has no explicit conditions | Treat it as a fallback rule within its media-type scope/order |
| Classification rule references an unsupported field | Ignore that rule with a local validation error; do not break the whole scan |
| Scan cache is cleared for a source | Clear only that source's local scrape metadata/artwork/log cache; keep DataSource config and credentials intact |
| Source is Emby/Jellyfin | Use provider metadata by default; do not apply raw-file scraping/classification unless a future explicit import mode says so |

#### 5. Good/Base/Bad Cases
- Good: the user selects `/影视库`; the scanner detects `华语电影/片名 (2024)/片名.mkv` as a movie structure and `综艺/剧名/Season 01/S01E01.mkv` as a TV structure without requiring a `Movies` or `TV` parent.
- Good: the selected root contains both `阿凡达.mp4` and `动漫/灵笼/Season 01/S01E01.mkv`; the movie is matched and displayed through movie/TMDB classification while the series keeps the `动漫` path category.
- Good: `电影/阿凡达.mp4`, `华语电影/阿凡达.mp4`, and `电影/阿凡达/阿凡达.mp4` preserve the explicit category folder, but `阿凡达/阿凡达.mp4` does not show a category card named `阿凡达`.
- Good: `机械之声的传奇 The Legend of Vox Machina AMZN GrassTV/Season 01/S01E01.mkv` is searched as `机械之声的传奇` / `The Legend of Vox Machina`, TMDB confirms TV animation from the US, and the poster wall groups it under the configured animation category such as `动漫`, not under the work-folder name.
- Good: two exact `名侦探柯南` TV candidates tie on title, but the real work has year, Japanese original language, 1212 episodes, aliases/translations, poster, and votes while the duplicate is empty; the real work wins within the `0.03` authority bound.
- Base: a messy folder of mixed files is scanned as non-standard, creates playable unresolved rows for misses, and groups successful TMDB matches through logical categories such as `华语电影`, `外语电影`, `综艺`, or `未分类`.
- Base: a provider omits vote count, poster, language, or episode count; missing values neither reward nor penalize the candidate, so the existing title/type/year result remains stable.
- Base: `sample.mp4`, `4K.mp4`, and `video.mp4` stay under unresolved/manual-identification flows instead of creating low-quality movie matches.
- Bad: scanner code checks `root.children.Movies` and `root.children.TV`, then treats every other selected root as non-standard or invalid.
- Bad: classification code creates or renames remote directories to match category names.
- Bad: the media-library root shows a category card named after a release folder such as `The Legend of Vox Machina AMZN GrassTV`.
- Bad: all matched items in a mixed root are displayed only under the first matched path category, so a root-level movie disappears when a sibling `动漫/...` series exists.
- Bad: select the globally most popular same-name result, or add enough vote/popularity score to overturn a mismatched media type, year, or parsed episode number.

#### 6. Tests Required
- Unit tests for path normalization, selected-root containment, standard/non-standard detection, filename parsing, rule matching, and fallback classification.
- Integration or service tests with representative OpenList/Alist-like trees: movie folders with title/year, TV series with seasons/episodes, mixed flat folders, Chinese category names, missing years, and unresolved files.
- Parser/display regression tests must cover `阿凡达.mp4`, `阿凡达/阿凡达.mp4`, `电影/阿凡达.mp4`, `华语电影/阿凡达.mp4`, `电影/阿凡达/阿凡达.mp4`, sibling anime series folders, and noise files such as `sample.mp4`, `4K.mp4`, and `video.mp4`.
- Shared Player/Server corpus tests must assert the same match/reject disposition for every redacted release fixture and verify the declared recognition contract versions stay synchronized.
- Ranker tests must cover candidate-order invariance, three-candidate ties, empty/malformed authority fields, authority disabled, popularity/vote-only differences, wrong title/type/year, and known episode-count conflicts.
- A real provider-payload-shaped test must exercise TMDB search -> detail -> rank for the complete and empty `名侦探柯南` candidates, assert the canonical identity wins for E1200/E1201/E1204/E1206, and assert search/detail request budgets remain bounded.
- The provider-payload test must also fail the authoritative candidate's detail request and assert the result stays unresolved rather than returning the empty-shell candidate.
- Cache tests must round-trip `originalLanguage`, season/episode counts, popularity, vote count, poster/artwork fields, engine version, and match source without persisting credentials or tokenized URLs.
- Persistence tests verify local metadata/artwork/log cache is source-scoped and clearing it does not remove config or credentials.
- UI review verifies poster-wall mode, folder-view fallback, unresolved items, scan status/logs, missing poster fallbacks, and no remote-write affordances.

#### 7. Wrong vs Correct

Wrong:
```ts
if (!children.some((entry) => entry.name === 'Movies') || !children.some((entry) => entry.name === 'TV')) {
  return { mode: 'nonStandard', reason: 'missing fixed top-level folders' }
}
```

Correct:
```ts
const detected = detectMediaStructure({
  rootPath: selectedRoot,
  entries,
  hints: ['title-year-folder', 'season-folder', 'episode-number', 'known-category-name'],
})
```

Wrong:
```ts
await alist.mkdir(`/Movies/${categoryName}`)
await alist.move(file.path, targetPath)
```

Correct:
```ts
await localScrapeDb.saveCategoryAssignment({
  sourceId,
  itemPath: file.path,
  categoryName,
  ruleSetVersion,
})
```

Wrong:
```ts
const best = candidates.sort((a, b) => (b.voteCount ?? 0) - (a.voteCount ?? 0))[0]
```

Correct:
```ts
const decision = decideRecognitionCandidate(parsed, titleVariants, candidates)
// Authority is bounded and can resolve only an already-established exact
// same-title/same-type identity with no structural conflict.
```

### DataSource Implementations

When implementing concrete sources such as Emby, keep provider-specific protocol logic under `services/datasource/` and expose it through a manager/factory rather than directly from views.

#### 1. Scope / Trigger
- Trigger: adding or changing a concrete media source (`emby`, `jellyfin`, `alist`, `clouddrive2`, `local`, `server`, or future cloud sources).
- Applies to source clients, DataSource classes, manager/factory wiring, settings forms, source library views, home aggregation, and playback URL routing.

#### 2. Signatures
- DataSource class: `class EmbyDataSource implements DataSource` or equivalent provider-specific implementation.
- Factory: `createDataSource(config: DataSourceConfig): DataSource` validates `config.type` before constructing.
- Manager: `DataSourceManager` owns instantiated sources and exposes source lookup/refresh methods by id.
- View boundary: `SettingsView`, `SourceLibraryView`, and `HomeView` call store/manager/DataSource methods, not provider HTTP endpoints.

#### 3. Contracts
- `init(config)` normalizes non-sensitive config, loads credentials through a credential reference when available, and does not log tokens.
- Emby add/edit flows authenticate with account/password through `/Users/AuthenticateByName` or equivalent, then store the returned access token behind `credentialRef`; normal Emby setup must not ask the user to manually paste an access token.
- OpenList/Alist add/edit flows authenticate with account/password through `/api/auth/login`, then store the returned token behind `credentialRef`; the first Player MVP must not expose manual token entry, public/shared directory access, path passwords, or WebDAV mode in the normal OpenList/Alist setup flow.
- OpenList/Alist add/edit flows allow selecting a library root after authenticated login by browsing directories from `/`; persist the chosen root as non-sensitive `extra.rootPath`, default to `/`, and do not persist credentials until final Add/Save succeeds.
- `test()` returns connection/auth success without exposing raw provider errors or credentials.
- `listLibraries()` returns `MediaLibrary[]` for source-level library cards and should be fetched after successful add/login so the source is known usable before it appears as connected.
- `list(path?)`, `search(keyword)`, and `getDetail(id)` map provider responses into shared media types.
- Top-level aggregate and source-library searches are work-level surfaces: keep `movie` and `series`, preserve raw-provider `file`/`folder` fallbacks, and exclude `season`/`episode` cards. Apply this normalization before per-source result limits so episode-heavy provider responses cannot crowd out the matching Series entity. Emby/Jellyfin top-level search requests should ask for `Movie,Series`; hierarchy browsing still fetches seasons and episodes normally.
- OpenList/Alist `listLibraries()` exposes the selected `extra.rootPath` as the source library, `list()` treats that path as the source root, `search()` scopes provider search/fallback listing to that root when practical, and `getStreamURL()` rejects unsafe paths or paths outside the selected root before building `/d{path}` URLs.
- `getDetail(id)` may include provider-derived media source options, audio/subtitle tracks, stills, collections, similar items, and media info, but must not expose local filesystem paths, STRM paths, credentials, or tokenized playback URLs as display fields.
- `getStreamURL(id)` returns a playable URL for mpv/player loading and must be treated as sensitive when tokenized; for STRM/remote-provider items, inspect provider playback metadata before falling back to static stream URLs, and return a user-safe error if no real playable URL is exposed.
- Emby hierarchy browsing must preserve views/libraries at the root, direct children for libraries/folders, series → seasons, and season → episodes; only search/home/recent aggregation should use recursive queries by default.
- `exportConfig()` returns non-sensitive fields and credential references only.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| Unsupported `DataSourceConfig.type` | Reject construction with a user-safe unsupported-source error |
| Missing server URL or required source identifier | Fail `init`/`test` with a user-safe validation error |
| Missing credential for a source that requires auth | Show an auth-required state; do not create a fake connected source |
| Emby account/password auth fails | Do not persist the source; show a redacted, user-safe login failure |
| Emby auth succeeds but library fetch fails | Do not mark setup as complete unless the UI explicitly supports a connected-but-empty/error state |
| Provider API returns unexpected shape | Treat as invalid external data and show a safe error/empty state |
| Provider returns tokenized image/stream URLs | Redact tokens in UI/log/error output; pass real URL only to the component/service that needs it |
| OpenList/Alist setup login succeeds but root browsing fails | Keep credentials unpersisted for new/changed login input and show a user-safe root browsing error |
| OpenList/Alist `extra.rootPath` is missing | Default to `/` and expose `/` as the library root |
| OpenList/Alist path contains `.` / `..` or escapes `extra.rootPath` | Reject before provider stream URL construction or nested browse |
| Source is disabled | Manager must skip initialization and route views must show disabled state instead of browsing |
| Source is offline/auth fails | Keep local files and other sources usable; show source-specific error state |

#### 5. Good/Base/Bad Cases
- Good: `SourceLibraryView` asks the manager/store for the active source, then calls `listLibraries()`/`list()` and renders generic `MediaCard` data.
- Base: a first concrete source supports libraries/items/stream URLs while advanced playback negotiation remains future work.
- Bad: a route view imports an Emby HTTP client and builds `/Users/{id}/Items` URLs directly.

#### 6. Tests Required
- `npm run typecheck`, `npm run lint`, and `npm run build` pass after frontend source changes.
- For desktop package confidence, `npm run tauri:build:windows` must still produce the Windows executable/installer when Player packaging is in scope.
- Review Settings add/edit/test/remove, source sidebar appearance, library loading, media item loading, play routing, loading/empty/error states, and token redaction.
- For OpenList/Alist, review the visible source-type selector, account-login-only flow, root directory picker from `/`, persisted `extra.rootPath`, scoped list/search/playback behavior, and cleanup when final config save or root validation fails.

#### 7. Wrong vs Correct

Wrong:
```ts
// SourceLibraryView.vue
const items = await $fetch(`${config.url}/Users/${userId}/Items`, { query: { api_key: token } })
```

Correct:
```ts
const source = dataSourceStore.getSource(sourceId)
const items = await source.list(libraryId)
```

---

## Naming Conventions

- Vue components use PascalCase file names: `HeroCarousel.vue`.
- Composables use `useX.ts`: `useMpv.ts`, `useServer.ts`, `useKeyboard.ts`.
- Pinia stores use domain names: `player.ts`, `media.ts`, `server.ts`, `settings.ts`, `ui.ts`.
- DataSource classes use PascalCase and end with `DataSource`.
- User-facing text should say `OpenList/Alist` or `OpenList (Alist-compatible API)`; code may use `alist` identifiers for API compatibility.

---

## Tauri/Rust Boundaries

Tauri commands expose platform capabilities only:

- Player commands for libmpv control.
- File commands for safe local file access under allowed roots.
- System/window commands.
- Danmaku load/fetch commands when implemented.

Rust internals should use structured errors and return strings safe for frontend display. Keep libmpv integration in dedicated modules.

### libmpv FFI Contract

When wrapping libmpv in `player/src-tauri/src/mpv/`, keep the FFI surface small and explicit:

#### 1. Scope / Trigger
- Trigger: Rust code creates, owns, or controls a native `mpv_handle`.
- Applies to player lifecycle, command execution, property get/set, event forwarding, and future render context wiring.

#### 2. Signatures
- Owner type: `MpvPlayer { handle: *mut mpv_handle, ... }` or an equivalent single-owner wrapper.
- State sharing: expose the owner through `Arc<Mutex<MpvPlayer>>` or another explicit serialization primitive.
- Frontend boundary: Tauri commands return user-safe `Result<T, String>` and do not expose raw pointers.

#### 3. Contracts
- `mpv_create()` returning null is an initialization error.
- Native strings passed into libmpv are `CString`; interior NUL bytes must become command/property errors.
- `mpv_command()` argument arrays are null-terminated and all C strings outlive the call.
- Strings returned by libmpv, such as `mpv_get_property_string()`, are freed with `mpv_free()` exactly once.
- The owner releases the handle with `mpv_terminate_destroy()` in `Drop`.
- Only implement unsafe auto-trait promises that are required by the actual state container; prefer `Send` with mutex serialization and do not add manual `Sync` unless there is a proven concurrent access contract.
- Do not enable visible video output (`vo=gpu` or equivalent) unless a native window/surface/render context is actually bound. Without `wid`/native surface/`MpvRenderContext`, libmpv may create an uncontrolled external mpv window.
- Until embedded video rendering is implemented, initialize libmpv in a non-windowing mode such as `force-window=no`, `vo=null`, and `video=no`, and make UI/docs state that backend loading/control works while in-window video is pending.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| `mpv_create()` returns null | Return an initialization error before storing state |
| `mpv_initialize()` returns non-zero | Destroy/avoid leaking the handle and return a safe error string |
| Rust string contains interior NUL | Return an argument error; do not call libmpv |
| libmpv command/property call returns non-zero | Convert to a safe error string for Tauri |
| `vo=gpu` is set without `wid`/native surface/render context | Treat as a bug: either bind a real embedded render target or suppress video output so no external mpv window appears |
| UI loads media but embedded render target is not implemented | Show an honest in-app placeholder; do not claim video is rendered in-window |
| WSL/WSLg EGL/Mesa warnings during an optional compatibility check | Record the result as supplementary and partial; use Windows-native runtime verification for completion |

#### 5. Good/Base/Bad Cases
- Good: direct libmpv FFI wrapper owns the handle, frees returned strings, binds an explicit render target before enabling visible video, and is accessed through a mutex-backed Tauri state.
- Base: backend media loading/control works with visible video suppressed while embedded rendering is still pending and documented.
- Bad: high-level wrapper or global state hides the native handle lifecycle, or `vo=gpu` opens an external mpv window that OhMyCine cannot close/control.

#### 6. Tests Required
- Rust check/build must compile the Tauri crate after any libmpv wrapper change.
- Frontend typecheck/lint/build must still pass because Tauri command contracts are frontend-facing.
- `tauri dev` must be attempted for runtime/libmpv changes; assert whether the app launches fully or is only partially verified due to graphics environment limits.
- Manually verify that local file playback does not create an external mpv video window unless an explicit sidecar prototype was requested.

#### 7. Wrong vs Correct

Wrong:
```rust
unsafe impl Send for MpvPlayer {}
unsafe impl Sync for MpvPlayer {}
```

Correct:
```rust
unsafe impl Send for MpvPlayer {}
// Access is serialized by Arc<Mutex<MpvPlayer>>; do not promise Sync without a separate contract.
```

Wrong:
```rust
set_option("vo", "gpu")?;
set_option("osc", "no")?;
// No `wid`, native surface, or render context is bound.
```

Correct:
```rust
set_option("force-window", "no")?;
set_option("vo", "null")?;
set_option("video", "no")?;
// Enable visible video only after a real embedded render target exists.
```

### True libmpv Render API Backend Contract

When implementing embedded video rendering through libmpv's render API, keep the architecture cross-platform even if the first concrete backend is Windows.

#### 1. Scope / Trigger
- Trigger: adding or changing `mpv_render_context`, native render surfaces, GL/WGL/EGL/Metal/Android/iOS surfaces, render-thread callbacks, or render-status commands.
- Applies to `player/src-tauri/src/mpv/render.rs`, platform-specific render modules, `MpvPlayer` handle access, Tauri render commands, and frontend render status UI.

#### 2. Signatures
- Render status command: `mpv_render_status() -> Result<MpvRenderState, String>`.
- Render state fields: `status: 'idle' | 'initializing' | 'ready' | 'unsupported' | 'error'`, `backend`, and optional user-safe `message`.
- Rust render boundary: `MpvRenderContext` owns `*mut mpv_render_context` and frees it with `mpv_render_context_free()`.
- OpenGL backend input: valid initialized `*mut mpv_handle`, current app-owned GL context, `mpv_opengl_init_params`, and per-frame `mpv_opengl_fbo` physical dimensions.

#### 3. Contracts
- `MpvPlayer` remains the single owner of `mpv_handle`; render modules may access the raw handle only through mpv-internal APIs.
- Do not use mpv `wid` or `tauri-plugin-libmpv` as the primary implementation when the task calls for True render API.
- Do not remove `force-window=no`, `vo=null`, or `video=no` fallback until an app-owned render context/surface is successfully initialized.
- Keep unsafe render API calls inside `mpv/render.rs` or narrow mpv-internal platform modules.
- `mpv_render_context_set_update_callback()` must only wake/signaling the render loop; it must not render directly, block on UI locks, or emit high-frequency Tauri events.
- `mpv_render_context_render()` runs only on the render thread with the same GL context current and physical-pixel FBO dimensions.
- Non-first backend platforms remain planned: return explicit unsupported/fallback states rather than deleting Linux/macOS/Android/iOS scope.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| `mpv_render_context_create()` fails | Keep video suppressed, return/render `status: 'error'`, and show an in-app fallback |
| Platform backend is not implemented | Return/render `status: 'unsupported'` with a clear message; do not crash |
| Render surface is missing or zero-sized | Skip frame rendering and keep state truthful; do not enable external mpv output |
| mpv update callback fires | Wake the render loop only; no direct GL, Tauri UI, or blocking work inside callback |
| Resize/fullscreen/DPI changes | Update native surface bounds and FBO width/height in physical pixels |
| WSL/WSLg cannot visually verify GL/WebView2 during an optional compatibility check | Mark the result partial; require Windows-native visual verification |

#### 5. Good/Base/Bad Cases
- Good: cross-platform `RenderBackend` boundary exists, Windows OpenGL backend owns the first native surface, unsupported platforms return explicit states, and Vue displays render status truthfully.
- Base: compile-proof scaffold confirms libmpv render bindings and exposes `mpv_render_status()` while visible video remains suppressed until a real surface exists.
- Bad: setting `vo=gpu` or passing `wid` as a shortcut while claiming True render API, or treating Windows-first implementation as removal of Linux/macOS/mobile product scope.

#### 6. Tests Required
- `cargo check` must compile render API imports and platform cfg paths.
- Frontend `npm run typecheck`, `npm run lint`, and `npm run build` must pass after command/status changes.
- Windows package build must pass when platform libraries or native render code change.
- Runtime visual checks must cover local file playback, Emby URL playback, no external mpv window, resize/maximize/fullscreen, and close-during-playback.

#### 7. Wrong vs Correct

Wrong:
```rust
set_option("vo", "gpu")?;
// No app-owned GL surface or mpv_render_context exists yet.
```

Correct:
```rust
set_option("force-window", "no")?;
set_option("vo", "null")?;
set_option("video", "no")?;
// Switch visible video on only after MpvRenderContext is active.
```

Wrong:
```rust
unsafe extern "C" fn update(_: *mut c_void) {
    mpv_render_context_render(ctx, params);
}
```

Correct:
```rust
unsafe extern "C" fn update(ctx: *mut c_void) {
    signal_render_thread(ctx);
}
```

### Managed FSR 1 Playback Shader Contract

#### 1. Scope / Trigger
- Trigger: changing Player video scaling, `glsl-shaders`, playback-engine settings, native render bounds, Windows libmpv diagnostics, Android mpv assets, or the Windows/Android playback settings UI.
- Applies to `playerInteractionSettings.ts`, `useMpv.ts`, the Tauri `MpvEngineSettings` bridge, Windows `mpv/player.rs`, Android `MpvPlugin.kt` / `MpvSurfaceHost.kt`, and the application-owned Shader resources.

#### 2. Signatures
- Persisted/request fields: `fsrMode: 'off' | 'auto' | 'force'`, `fsrSharpness: number`, `fsrDenoise: boolean`, and `fsrTarget: 'auto' | '1080p' | '1440p' | '2160p'`.
- Defaults: `auto`, `35`, `true`, and `auto`. Normalize sharpness to an integer in `0..100`; higher UI values mean a sharper result.
- Diagnostics add only bounded `fsrStatus` and `fsrReason` strings. They must not contain media URLs, headers, provider paths, or credentials.

#### 3. Contracts
- Use FSR 1.x EASU + RCAS through libmpv user Shader hooks. Do not upscale video in Vue/WebView, and do not describe this as FSR 2/3, frame generation, or temporal upscaling.
- The only accepted Shader is `src-tauri/resources/shaders/ohmycine-fsr-v1.glsl`. Windows materializes its compile-time bytes under the active profile's `cache/mpv/shaders`; Android setup copies it into APK `assets/mpv` and runtime code installs it into private `filesDir/mpv`. Never accept a user-provided Shader path through Tauri or Kotlin commands.
- The Shader compares the effective capped intermediate target (`min(OUTPUT, target cap)`) with the source, so EASU and RCAS run only while that intermediate picture is enlarged. Testing only raw `OUTPUT > source` is insufficient because a numeric cap can be smaller than the source. `force` may bypass conservative capability prechecks but must not bypass the upscale-only condition or failure fallback.
- `fsrSharpness` maps to RCAS stops with `2 * (1 - value / 100)`. Numeric target labels are output-picture shorter-edge caps that preserve the current render-surface aspect ratio; they are not display-mode switches and do not force the app window or screen resolution.
- Runtime switching clears the managed list with `change-list glsl-shaders clr ""`, sets bounded `glsl-shader-opts`, and appends only the managed Shader. Render-surface size changes recompute target width/height without reloading the media.
- Any resource installation, Shader load, compile, or parameter-update failure clears FSR and keeps ordinary mpv scaling active. Such a failure must not abort mpv initialization, media loading, hardware decoding, subtitles, danmaku, or navigation.
- Windows controls live in the playback settings panel. Android controls live in a child panel of the right-top three-dot menu and must not consume or replace the mobile long-press speed gesture.
- Preserve the AMD MIT notice, mpv-port provenance, and packaged notice resource whenever the Shader changes.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| Stored mode, target, or sharpness is invalid | Restore the documented default or clamp the finite sharpness value |
| Output is the same size as or smaller than the source | Keep the managed Shader harmlessly armed but skip EASU/RCAS passes |
| Numeric target is smaller than the current output short edge | Cap the FSR intermediate dimensions and preserve aspect ratio |
| Windows cache materialization fails | Pass no Shader path, report a generic fallback reason, and continue normal playback |
| Android asset/private-file installation fails | Keep mpv initialization running, record a generic fallback, and continue normal playback |
| mpv reports Shader/GLSL/hook load or compile failure | Clear `glsl-shaders`, set fallback diagnostics, and never expose the raw log line |
| User selects `off` at runtime | Clear the managed Shader immediately without reloading media |
| Render surface changes size or orientation | Recompute target width/height from physical surface bounds |

#### 5. Good/Base/Bad Cases
- Good: a 720p video displayed on a 4K surface uses the packaged EASU/RCAS Shader, the user can select a 1440p shorter-edge cap, and changing sharpness applies during playback.
- Base: a native GPU/Shader incompatibility produces `fallback` diagnostics while the same video continues through ordinary mpv scaling.
- Bad: accepting an arbitrary `.glsl` path, treating `2160p` as a command to resize the monitor/window, applying RCAS while downscaling, or propagating a Shader cache error as a fatal playback error.

#### 6. Tests Required
- `npm run verify:fsr-upscaling` must assert defaults/normalization, all TypeScript-Rust-Kotlin fields, EASU/RCAS/upscale condition, fixed resource paths, target mapping, clear-and-fallback behavior, and desktop/mobile UI placement.
- Run `npm run typecheck`, `npm run lint`, and `npm run build`.
- Run Windows-native `cargo test --target x86_64-pc-windows-msvc` and `cargo clippy --all-targets --target x86_64-pc-windows-msvc -- -D warnings`.
- Run `npm run setup:libmpv:android` before packaging and verify the APK assets contain both the Shader and notice. Build the Android preview APK with the Windows-native toolchain.
- Device/runtime testing must cover low-resolution upscale, same-size/downscale bypass, off/auto/force switching, all target caps, runtime resize/fullscreen/orientation, hardware decode, subtitles, danmaku, and deliberately broken Shader fallback.

#### 7. Wrong vs Correct

Wrong:
```rust
let shader_path = Some(materialize_fsr_shader(&app)?);
player.apply_engine_settings(settings, shader_path)?;
```

Correct:
```rust
let shader_path = materialize_fsr_shader(&app).ok();
player.apply_engine_settings(settings, shader_path)?;
// A missing managed Shader becomes FSR fallback, not playback failure.
```

### Windows MSVC/GNU libmpv Build Contract

When Windows-native development builds Player for `x86_64-pc-windows-msvc`, or CI and explicit Linux/WSL compatibility work builds it for `x86_64-pc-windows-gnu`, treat libmpv as both a link-time and runtime dependency. Windows MSVC is the default local path; Linux/WSL cross-building remains supplementary:

#### 1. Scope / Trigger
- Trigger: `npm run tauri:build:windows:native`, `npm run tauri:build:windows`, either Windows Rust target, or any change to libmpv setup/bundling.
- Applies to `player/scripts/setup-libmpv.mjs`, `player/src-tauri/build.rs`, `player/src-tauri/tauri.conf.json`, and `.gitignore`.

#### 2. Signatures
- Setup script command: `npm run setup:libmpv -- windows` installs Windows libmpv artifacts under `player/src-tauri/lib/`.
- Build targets: `TARGET=x86_64-pc-windows-msvc` and `TARGET=x86_64-pc-windows-gnu` add `player/src-tauri/lib` as a native link-search path.
- Bundle resource mapping: runtime DLLs are copied to the Windows app install root.

#### 3. Contracts
- Install `libmpv-2.dll` for Windows runtime loading.
- Install `libmpv.dll.a` for GNU link-time resolution of `-lmpv`.
- Install the same COFF import archive as `mpv.lib` for MSVC link-time resolution of `-lmpv`; this keeps setup independent from Visual Studio command-line tools.
- Do not bundle `libmpv.dll.a` or `mpv.lib`; they are import libraries, not runtime files.
- Do not commit generated import libraries, downloaded DLLs, installers, or `target/` outputs.
- Node ESM setup scripts must convert `import.meta.url` with `fileURLToPath()` before using `node:path`; `.pathname` produces malformed drive-prefixed paths on Windows. Use `basename()` rather than splitting paths on `/` when passing archive names to extraction tools.
- Keep native Linux builds using system libmpv/pkg-config; only add the explicit link-search path for the two Windows targets.
- Linux/WSL cross-build success proves executable/installer generation only; Windows installation, signing, launch, and playback require the Windows-native environment.
- In the Windows transparent WebView + mpv underlay model, WebView-reported surface bounds are the sole size authority. A pure native owner `Moved` event must synchronously reposition the mpv HWND from the cached physical surface bounds plus the owner's current client origin; it must not wait for `ResizeObserver` or derive a replacement size from the native client rect. Owner `Resized` and `ScaleFactorChanged` events may synchronize visibility/z-order but must wait for `ResizeObserver` plus the Tauri resized/scale report before resizing the mpv HWND, so video never leads the WebView layout frame.

#### 4. Validation & Error Matrix
| Condition | Required behavior |
|-----------|-------------------|
| Linker says `cannot find -lmpv` | Ensure `libmpv.dll.a` exists in `player/src-tauri/lib/` and the Windows GNU target has that directory in `rustc-link-search` |
| MSVC linker says it cannot open `mpv.lib` | Run `npm run setup:libmpv -- windows`; ensure `mpv.lib` exists and the Windows MSVC target has the vendored link-search path |
| `libmpv-2.dll` missing from bundle | Add it as a Tauri resource at the Windows app root |
| `libmpv.dll.a` or `mpv.lib` appears in git status as tracked/addable | Ignore it; regenerate via the setup script instead of committing it |
| Native Linux `cargo check` starts using vendored Windows lib path | Scope link-search by `TARGET`, not unconditionally |
| Linux/WSL build produces an NSIS installer | Mark cross-build as passed but keep Windows-native runtime/signing/playback unverified |

#### 5. Good/Base/Bad Cases
- Good: setup installs `libmpv-2.dll`, `libmpv.dll.a`, and `mpv.lib`; build.rs exposes the lib directory only for Windows MSVC/GNU, and Tauri bundles only runtime DLLs.
- Base: a Linux/WSL cross-build creates `.exe` and an NSIS installer while signing/runtime playback remain unverified.
- Bad: relying on Linux `libmpv.so` for a Windows GNU build, or committing generated import libraries/installer artifacts.

#### 6. Tests Required
- `npm run setup:libmpv -- windows` installs `libmpv-2.dll`, `libmpv.dll.a`, and `mpv.lib`.
- Native Windows `cargo test`/`cargo check` resolves `mpv.lib` for `x86_64-pc-windows-msvc`.
- Setup-script path tests must confirm a Windows file URL resolves to one valid drive path and archive extraction receives only the basename.
- `npm run tauri:build:windows` resolves `-lmpv` and produces the Windows executable/installer.
- `cargo check` without a Windows target still passes on Linux.
- Inspect git status to confirm generated DLL/import-library/target outputs are not staged.

#### 7. Wrong vs Correct

Wrong:
```rust
println!("cargo:rustc-link-search=native=lib");
```

Correct:
```rust
if std::env::var("TARGET").as_deref() == Ok("x86_64-pc-windows-gnu") {
    println!("cargo:rustc-link-search=native={}", lib_dir.display());
}
```

---

## Common Mistakes

- Putting data-source-specific logic directly in views/components instead of DataSource implementations.
- Treating ServerDataSource as mandatory.
- Writing secrets into `config.json` instead of secure storage.
- Replacing existing Player work during Trellis migration instead of adopting current state.
- Assuming mobile support is as mature as desktop without implementation proof.
