# Media Classification Profile Contract

> Executable Server contract for reusable movie/TV logical-classification rules. These profiles classify indexed metadata only; they are not download/import `CategoryRule` records.

## 1. Scope / Trigger

Apply this contract when changing `MediaClassificationProfile`, the versioned classification schema or matcher, Profile REST APIs, MediaLibrary Profile references, or the Server rule-management UI.

Do not apply it to pipeline `categories.*`, Storage Destinations, naming templates, transfer modes, or file placement.

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

## 3. Contracts

- Version 1 contains exactly one `movie` group and one `tv` group. Each group has ordered categories and a non-empty fallback.
- Conditions support allowlisted genre IDs, original languages, movie production countries, TV origin countries, and inclusive release-year bounds.
- A category match uses ordered first-match behavior: excludes win; includes are OR within one dimension; dimensions are AND; strings compare case-insensitively; missing actual metadata cannot satisfy a non-empty include.
- The protected built-in row has stable code `default-v1`, display name `默认分类规则`, revision 1, and rules exactly equivalent to Player v1 defaults. Server owns and seeds it independently; Server never reads or executes Player settings.
- API request envelopes and nested rule JSON reject unknown fields and trailing JSON values. Invalid values are rejected, not silently sanitized.
- Updates require the current revision and execute a database compare-and-swap. A successful update increments revision exactly once; stale or overflowing revisions fail safely.
- Copy is a deep copy: preserve group/category order, conditions and fallbacks while generating new Profile and category IDs. Automatic copy names retry unique suffixes under concurrent conflicts and remain within the name limit.
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
| Year outside 1888–2200 or `from > to` | Reject the write |
| Duplicate normalized Profile name, including a race | Stable name-conflict response, never raw SQLite/500 |
| Stale revision | Stable revision-conflict response; preserve UI draft |
| Revision at maximum value | Reject before increment |
| Update/delete built-in Profile | Protected-profile error |
| Delete referenced custom Profile | Profile-in-use error |
| Copy without a name under concurrent creation | Retry the next bounded suffix |

## 5. Good / Base / Bad Cases

- Good: MediaLibrary stores a Profile ID, obtains classification through the pure matcher, and marks itself for later reclassification after Profile revision changes without touching files.
- Base: an API client lists or copies the protected default Profile using dedicated Profile permissions.
- Bad: reusing `categories.*`, importing Player TypeScript, silently deleting unknown rule values, updating without revision CAS, retaining copied category IDs, or writing complete rules to audit metadata.

## 6. Tests Required

- Exact default movie/TV category order and condition values against the recorded Player v1 contract.
- Matcher tests for order, include OR, dimension AND, exclude precedence, case normalization, missing metadata, year boundaries and fallback.
- Strict outer/nested JSON, allowlist, duplicate and contradictory-condition validation.
- Fresh migration, v2→v3 upgrade, repeated migration, one exact built-in seed, and preservation of Storage/custom Profile data.
- Service tests for every policy action, protected operations, reference checker, deep-copy ID regeneration, CAS/revision overflow, normalized uniqueness race and copy-name concurrency.
- Router tests for administrator/operator/viewer matrices and safe status/error envelopes.
- Audit assertions that neither `rules_json` nor recognizable full-rule values appear.
- Web UI tests for DTO cloning from Vue reactive proxies, navigation permissions, draft preservation and accessible editor relationships.
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

```ts
const draft = structuredClone(apiProfile.rules) // Vue proxy may throw DataCloneError
```

Correct:

```ts
const draft = cloneRules(apiProfile.rules) // explicit field-level DTO copy
```
