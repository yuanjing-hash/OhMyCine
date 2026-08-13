# Design: Media Classification Profiles

## Domain Boundary

```text
MediaClassificationProfile
  -> classifies already identified movie/tv metadata into logical library groups
  -> selected by future MediaLibrary
  -> never selects a download destination or writes files

Pipeline CategoryRule
  -> future download/import placement, naming and transfer strategy
  -> remains under categories.* and is not implemented here
```

The code/API/permission prefix is `media_classification_profiles`; the user-facing label is “规则管理”. This deliberate verbosity prevents accidental reuse of pipeline `categories.*`.

## Persistence and Seed

Migration v3 creates `media_classification_profiles`:

- `id INTEGER PRIMARY KEY AUTOINCREMENT`
- `code TEXT UNIQUE NULL` (`default-v1` for the built-in profile; custom rows use NULL)
- `name`, `name_normalized UNIQUE`
- `kind CHECK(system|custom)` and `protected`
- `schema_version INTEGER CHECK(schema_version=1)`
- `rules_json TEXT`
- `revision INTEGER NOT NULL DEFAULT 1`
- timestamps

The migration creates the table only. A deterministic seed function upserts the protected `default-v1` row by stable code and validates its payload. It may refresh the known built-in row only as part of an explicit application version contract; it never matches or overwrites rows by display name and never touches custom rows. Migrate remains idempotent and preserves v1/v2 databases.

## Version 1 Schema

Go value types mirror Player v1 semantics while using JSON snake_case consistently:

```text
RulesV1 { version: 1, groups: [RuleGroupV1] }
RuleGroupV1 { media_type: movie|tv, categories: [...], fallback_category_name }
CategoryRuleV1 { id, name, conditions }
ConditionsV1 {
  genre_ids { include, exclude }
  original_languages { include, exclude }
  production_countries? { include, exclude }
  origin_countries? { include, exclude }
  release_year: null | { from?, to? }
}
```

Decode with `json.Decoder.DisallowUnknownFields`, require exactly one movie and one tv group, normalize only presentation-safe whitespace/case, and reject invalid values rather than silently dropping them. Store the canonical re-encoded JSON so API round trips are stable.

Category IDs are opaque random IDs generated with `crypto/rand`, not names or timestamps. A copy regenerates every category ID to make the copy structurally independent while preserving order and conditions.

## Matcher Contract

```text
Classify(metadata, rules) -> { category_name, matched_rule_id?, matched_rule_name? }
```

- Find the group for metadata media type.
- Iterate categories in persisted order and return the first whose dimensions all match.
- Within a dimension, any excluded actual value rejects first; non-empty include requires at least one actual value; otherwise it is unconstrained.
- String comparison uses normalized uppercase codes. Year bounds are inclusive.
- If no category matches, return the group's non-empty fallback.

The matcher package has no GORM, HTTP, filesystem or Player dependency. A checked-in contract fixture records inputs and expected categories derived from the current Player v1 implementation.

## API Contract

```text
GET    /api/v1/media-classification-profiles
GET    /api/v1/media-classification-profiles/:id
POST   /api/v1/media-classification-profiles
POST   /api/v1/media-classification-profiles/:id/copy
PATCH  /api/v1/media-classification-profiles/:id
DELETE /api/v1/media-classification-profiles/:id
```

- List returns summaries plus compact group/category counts; detail returns full rules.
- Create accepts `name` and optional full `rules`; omitted rules means the empty v1 template.
- Copy accepts optional `name` and returns a full independent custom profile.
- Update accepts required `revision`, `name` and full `rules`, then performs `WHERE id=? AND revision=?` and increments revision atomically.
- Delete rejects protected/system profiles. A `ProfileReferenceChecker` service interface returns references; its no-library implementation reports the domain as unavailable/empty only within this release, and the next task replaces it with real MediaLibrary queries before references can exist.
- Stable errors cover invalid payload, not found, name conflict, protected profile, revision conflict and profile-in-use (reserved for the next task).

Handlers only bind/return JSON. Validation, default naming, deep-copy, policy, optimistic concurrency, uniqueness-race mapping and audit belong to the service.

## Authorization and Audit

- Permissions: `.read`, `.create`, `.update`, `.delete` under `media_classification_profiles`.
- administrator gains all through `system.admin`; operator is seeded all four; viewer receives none.
- Router middleware checks the action permission and `MediaClassificationProfileService` repeats it.
- Audits record action, target ID, outcome, kind/revision/category counts and request metadata, never `rules_json`.

## Web UI

- Add a non-planned `规则管理` item under the System navigation group at `/system/media-rules`.
- Route meta uses generated read permission. Buttons use their generated action permissions.
- Use a master/detail workspace: compact Profile list and a detail/editor panel. The editor owns movie/tv group tabs, category order controls, fallback, and three-state chips/selectors.
- Create/copy opens a named draft; editing never mutates list response objects. Failed save keeps the draft. A revision conflict asks the user to reload or preserve/copy their draft; it does not silently overwrite.
- The page uses the Server semantic light/dark tokens and conventional admin density. It does not reintroduce Player liquid glass styling.

## Compatibility and Rollback

- No existing API, permission code or table changes meaning. `categories.*` remains reserved for pipeline CategoryRule.
- The new migration is additive. Rolling back application code leaves an unused table/seed that older binaries ignore; no destructive down migration is required.
- Player remains independently useful and retains its local classification settings.
