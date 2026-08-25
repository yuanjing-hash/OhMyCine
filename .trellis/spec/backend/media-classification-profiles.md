# Media Classification Profile Contract

> Executable Server contract for reusable movie/TV classification, filename-recognition preprocessing, and organization naming Profiles. They are not download-target or transfer-policy `CategoryRule` records.

## 1. Scope / Trigger

Apply this contract when changing `MediaClassificationProfile`, the versioned classification schema or matcher, recognition preprocessors, movie/TV naming templates, Profile REST APIs, MediaLibrary Profile references, download snapshots, or the Server rule-management UI.

Do not apply it to download target selection, Storage Destinations, transfer modes, conflict policy, or provider file mutations.

## 2. Signatures

```go
const SchemaVersion = 1

func DecodeStrict(raw []byte) (RulesV1, error)
func CanonicalJSON(rules RulesV1) ([]byte, error)
func DefaultRules() RulesV1
func EmptyRules() RulesV1
func Classify(metadata ClassifiableMetadata, rules RulesV1) ClassificationResult
```

```text
GET    /api/v1/media-classification-profiles
GET    /api/v1/media-classification-profiles/:id
POST   /api/v1/media-classification-profiles
POST   /api/v1/media-classification-profiles/:id/copy
PATCH  /api/v1/media-classification-profiles/:id
DELETE /api/v1/media-classification-profiles/:id
```

Canonical permission prefix:

```text
media_classification_profiles.read
media_classification_profiles.create
media_classification_profiles.update
media_classification_profiles.delete
```

Additive v24 database ownership:

```text
media_classification_profiles.recognition_rules_json
media_classification_profiles.movie_directory_template
media_classification_profiles.movie_filename_template
media_classification_profiles.tv_directory_template
media_classification_profiles.tv_filename_template
download_tasks.profile_recognition_rules_json          # private immutable snapshot
download_tasks.movie_* / tv_*_template                 # private immutable Profile snapshot
```

Additive v25 recognition-pack ownership:

```text
media_classification_profiles.builtin_recognition_packs_json
download_tasks.profile_builtin_recognition_packs_json  # private immutable snapshot
```

Profile detail adds `builtin_recognition_packs`, `recognition_rules` and the four template fields. Each recognition rule is exactly `{enabled, media_type: all|movie|tv, pattern, replacement}`. The only current built-in pack codes are `tv-v1` and `anime-v1`.

## 3. Contracts

- Version 1 contains exactly one `movie` group and one `tv` group. Each group has ordered categories and a non-empty fallback.
- Conditions support allowlisted genre IDs, original languages, movie production countries, TV origin countries, and inclusive release-year bounds.
- A category match uses ordered first-match behavior: excludes win; includes are OR within one dimension; dimensions are AND; strings compare case-insensitively; missing actual metadata cannot satisfy a non-empty include.
- The protected built-in row has stable code `default-v1`, display name `默认分类规则`, revision 1, and rules exactly equivalent to Player v1 defaults. Server owns and seeds it independently; Server never reads or executes Player settings.
- API request envelopes and nested rule JSON reject unknown fields and trailing JSON values. Invalid values are rejected, not silently sanitized.
- Updates require the current revision and execute a database compare-and-swap. A successful update increments revision exactly once; stale or overflowing revisions fail safely.
- Copy is a deep copy: preserve group/category order, conditions and fallbacks while generating new Profile and category IDs. Automatic copy names retry unique suffixes under concurrent conflicts and remain within the name limit.
- A Profile is the single source of truth for ordered recognition preprocessing plus movie/TV directory and filename templates. MediaLibrary owns destination, transfer mode and conflict policy; provider adapters never parse titles or choose naming.
- Built-in recognition packs run in fixed `tv-v1 -> anime-v1` order before user recognition rules, filename parsing and TMDB lookup. A newly created/default Profile enables both packs. An explicitly persisted `[]` disables both; missing legacy storage obtains the default without converting an explicit empty selection back to default.
- The built-in packs are offline `go:embed` snapshots of MoviePilot-Help `Words/TV.txt` at commit `f99c1b0bfd6721a727260e3e41e7d0bca73af8c7` and `Words/anime.txt` at commit `8f26b5b48ac1a863cae97dd67689d05433394349`. Source URL, commit, sync date, SHA-256 and upstream MIT license remain beside the snapshots. Runtime never downloads the mutable GitHub URLs.
- All 322 valid snapshot rules must parse and precompile. Supported semantics include block, replacement, replacement plus episode offset, `EP` arithmetic, captures/backreferences, lookahead/lookbehind and `{[tmdbid=...;type=...;s=...;e=...]}` hints. Direct hints still require a server-side TMDB `GetByID` validation before classification.
- Compatible backtracking regex execution is bounded to 4,096 input runes, 50 ms per match, two seconds per processor invocation and 64 applications per rule. Timeout, apply-limit, invalid-rule and conflicting-direct-hint outcomes are stable recognition errors; no source title/path is included in the processing error.
- User recognition rules run in stored order after the built-in packs. User patterns use Go RE2, replacements use Go capture expansion, an empty replacement deletes the match, and disabled rules are retained but skipped. Runtime never executes arbitrary expressions.
- Download enqueue snapshots Profile revision, canonical classification JSON, canonical built-in pack JSON, canonical user recognition JSON and all four templates. Later Profile edits may mark a MediaLibrary for reclassification but never redirect, re-recognize or rename an in-flight task.
- The v24 migration preserves every v14-v23 MediaLibrary naming combination. When libraries sharing one old Profile have different four-template combinations, migration creates one custom Profile per distinct combination and rebinds them atomically; it never changes the protected default or silently applies new defaults.
- Complete-manifest recognition chooses one provider-neutral anchor before TMDB lookup. Tests use full real release names with mixed `.`, spaces and `-`; simplified fixtures cannot replace this coverage.
- Provider-neutral recognition considers the primary filename, meaningful parent folders and package name. Profile preprocessing runs before built-in media-type/year parsing for each candidate, so generic disc filenames such as `BDMV/STREAM/00000.m2ts` can fall back to the release folder.
- Real PT/Nyaa package titles may use `/`, `／`, or `|` as multilingual title separators. Package-title facts may preserve and split those separators into query variants, but must still reject leading-root forms, URLs, drive paths, every backslash, control data and unsafe file facts. A semantic package title is never reused as a filesystem path.
- Domain parsing runs after the fixed `tv-v1 -> anime-v1 -> user` preprocessing chain and before media-type decision. Season/episode evidence found in release names must therefore participate in type selection instead of being removed after an earlier `unknown` decision.
- Release parsing covers `S02 Complete`, `EP26-52`, `[01-52TV全集]`, `[1-52]`, `[52全]`, bracketed absolute episodes, title-first bracket packs, multilingual aliases and technical brackets before the release group. Technical/audio/language tokens and a defensible trailing group are removed without hard-coding a work title; full valid title counterexamples remain mandatory.
- Router middleware and service policy both enforce permissions. Operator receives all four Profile permissions by default; viewer receives none.
- Mutation audits contain actor, Profile ID, action, result, kind/revision and aggregate category counts only. Never log/audit `rules_json`, complete conditions, or future media paths.
- A MediaLibrary reference checker is injected at the service boundary. Once MediaLibrary exists, referenced custom Profiles cannot be deleted; do not bypass this by querying from handlers.

## 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Missing/duplicate movie or TV group | Stable invalid-rules error |
| Unknown JSON field or trailing JSON | HTTP 400 with safe invalid-request/rules error |
| Genre/language/country outside allowlist | Reject the write |
| Same value in include and exclude | Reject the write |
| Recognition JSON is not one strict array, has unknown fields, exceeds 64 rules/48 KiB, has invalid media type, newline/control data, or an invalid/oversized RE2 pattern | Reject with the Profile validation error; preserve the previous revision |
| Built-in pack list contains an unknown or duplicate code | Reject with the Profile validation error; preserve the previous revision |
| Built-in pack list is explicitly `[]` | Persist `[]`; do not silently restore defaults |
| Embedded snapshot hash/count/manifest disagrees with the compiled rules | Fail tests/build verification; do not silently skip a rule |
| Built-in regex times out, exceeds its application limit, or emits conflicting direct hints | Return a stable unrecognized error for that unit; do not block the supervisor or trust one conflicting hint |
| Directory template is absolute/traversing or filename template contains a separator | Reject with the Profile validation error; persist no partial update |
| Year outside 1888–2200 or `from > to` | Reject the write |
| Duplicate normalized Profile name, including a race | Stable name-conflict response, never raw SQLite/500 |
| Stale revision | Stable revision-conflict response; preserve UI draft |
| Revision at maximum value | Reject before increment |
| Update/delete built-in Profile | Protected-profile error |
| Delete referenced custom Profile | Profile-in-use error |
| Copy without a name under concurrent creation | Retry the next bounded suffix |

## 5. Good / Base / Bad Cases

- Good: MediaLibrary stores a Profile ID, obtains classification through the pure matcher, and marks itself for later reclassification after Profile revision changes without touching files.
- Good: a download snapshots a Profile, applies `tv-v1`, then `anime-v1`, then its ordered user rules, queries TMDB with the clean title/year, classifies once, then lets local or cloud transfer execute the same safe plan.
- Good: a direct ID hint from the embedded anime pack calls TMDB by type and ID, verifies the returned metadata, and only then applies the current classification rules.
- Base: an API client lists or copies the protected default Profile using dedicated Profile permissions.
- Bad: reusing `categories.*`, importing Player TypeScript, silently deleting unknown rule values, updating without revision CAS, retaining copied category IDs, or writing complete rules to audit metadata.
- Bad: downloading word packs from GitHub at runtime, silently ignoring an unsupported line, trusting a direct hint without TMDB verification, or allowing a catastrophic regex to run without bounds.
- Bad: putting recognition or naming inside qBittorrent/115 adapters, reading current Profile settings during a retry, or testing only `Seven Samurai CC MA 2 0 SONYHD` while production uses dots and `0-SONYHD`.

## 6. Tests Required

- Exact default movie/TV category order and condition values against the recorded Player v1 contract.
- Matcher tests for order, include OR, dimension AND, exclude precedence, case normalization, missing metadata, year boundaries and fallback.
- Strict outer/nested JSON, allowlist, duplicate and contradictory-condition validation.
- Fresh migration, v2→v3 upgrade, repeated migration, one exact built-in seed, and preservation of Storage/custom Profile data.
- Service tests for every policy action, protected operations, reference checker, deep-copy ID regeneration, CAS/revision overflow, normalized uniqueness race and copy-name concurrency.
- Router tests for administrator/operator/viewer matrices and safe status/error envelopes.
- Audit assertions that neither `rules_json` nor recognizable full-rule values appear.
- Web UI tests for DTO cloning from Vue reactive proxies, navigation permissions, draft preservation and accessible editor relationships.
- Profile tests cover built-in defaults, explicit empty selection, unknown/duplicate rejection, copy preservation, ordered/all/movie/TV user recognition, invalid RE2, capture replacement, template safety, deep-copy preservation and revision CAS. Download integration asserts the exact TMDB query title/year for a complete real release folder and file name.
- Built-in pack tests verify the manifest and SHA-256, parse/precompile all 322 effective rules, and cover block, replacement, 38 episode offsets, lookaround, backreference, direct TMDB hints, conflicting hints, timeout/application bounds and fixed `tv-v1 -> anime-v1 -> user` order.
- PT/Nyaa regressions use untouched production-shaped titles at both the pure parser and shared `recognizeMedia` service entry. They cover dotted codec/channel tokens, complete seasons, title-first and group-first brackets, Japanese/Chinese/English aliases, episode ranges/counts and release-group placement; a parser-only green test cannot prove the Profile packs and TMDB query budget compose correctly.
- Migration tests preserve legacy library templates, split distinct combinations, reuse identical combinations and remain idempotent.
- Full `server/test.ps1` plus isolated browser smoke for default read-only, copy, edit/revision and both themes.

## 7. Wrong vs Correct

Wrong:

```go
db.Model(&profile).Updates(input) // no revision predicate, unvalidated rules
```

Correct:

```go
validated := classification.DecodeStrict(input.Rules)
result := db.Model(&models.MediaClassificationProfile{}).
    Where("id = ? AND revision = ? AND protected = 0", id, input.Revision).
    Updates(canonicalFields(validated, input.Revision+1))
```

Wrong:

```go
title := pan115Adapter.GuessTitle(file.Name) // provider-specific recognition drifts
templates := currentLibraryTemplates()       // changes an in-flight task
```

Correct:

```go
snapshot := snapshotProfile(profile) // rules + recognition + naming at enqueue
verified := verifyCompletedPackage(snapshot, completeManifest)
provider.Execute(verified.Plan)       // provider only performs bounded mutation
```

Wrong:

```go
rules, _ := http.Get("https://raw.githubusercontent.com/.../TV.txt")
result := trustDirectTMDBHint(rule) // mutable runtime dependency and unverified identity
```

Correct:

```go
processor := mediarecognition.NewBuiltinWordProcessor(profilePackSnapshot, limits)
hint := processor.Apply(ctx, providerNeutralTitle)
match := tmdb.GetByID(ctx, hint.MediaType, hint.TMDBID) // verify before classify
```

Wrong:

```ts
const draft = structuredClone(apiProfile.rules) // Vue proxy may throw DataCloneError
```

Correct:

```ts
const draft = cloneRules(apiProfile.rules) // explicit field-level DTO copy
```
