# Plugin Repository Discovery

> Executable contracts for administrator-managed GitHub plugin repositories, verified packages, and the WASM installation lifecycle.

## Scenario: Pinned GitHub Registry discovery

### 1. Scope / Trigger

- Trigger: adding or changing plugin repository persistence, GitHub Registry refresh, repository ordering, marketplace merging, or the Server plugin administration page.
- This scenario covers discovery only. Package download, Manifest permission review, installation, WASM runtime generations, upgrade, rollback, and uninstall require their own verified lifecycle before the UI may expose those actions.

### 2. Signatures

Database migration:

```text
plugin_repositories
  id, name, github_url, github_owner, github_repo
  enabled, priority, revision
  last_commit_sha, last_refreshed_at, last_error_code
  cached_registry_json, created_at, updated_at
```

Management API:

```text
GET    /api/v1/plugin-repositories
POST   /api/v1/plugin-repositories
PATCH  /api/v1/plugin-repositories/:id
DELETE /api/v1/plugin-repositories/:id
PUT    /api/v1/plugin-repositories/order
POST   /api/v1/plugin-repositories/:id/refresh
GET    /api/v1/plugins/marketplace
GET    /api/v1/plugins/installed
```

Repository input and concurrency fields:

```json
{"github_url":"https://github.com/owner/repo","name":"optional","enabled":true}
{"revision":3}
{"order":[{"id":1,"revision":3},{"id":2,"revision":7}]}
```

### 3. Contracts

- Accept only a GitHub repository homepage with canonical identity `https://github.com/{owner}/{repo}`. Reject credentials, ports, query strings, fragments, raw URLs, tree/blob paths, non-HTTPS schemes, and non-GitHub hosts.
- The fetcher owns the GitHub endpoint. It uses GET requests to the fixed `api.github.com` host with a bounded timeout, no redirects, and bounded response bodies; callers never provide an API or raw-content host.
- Resolve the default branch, resolve it to a canonical 40-character lowercase commit SHA, then read `ohmycine-plugin-registry.v1.json` at that SHA. Never cache a branch-relative Registry as trusted state.
- Parse Registry JSON with unknown-field rejection and the shared Registry v1 validator. Manifest/package/icon references must be GitHub Release assets from the configured repository. Version values use the shared strict SemVer subset and range validator.
- Write `last_commit_sha`, `last_refreshed_at`, and `cached_registry_json` atomically only after the entire Registry validates. A failed refresh records a stable error code and preserves the last successful cache, successful timestamp, and commit SHA.
- Repository changes use revision compare-and-swap. Reordering must include every current repository exactly once and update all priorities in one transaction.
- Marketplace merging uses enabled repositories in administrator order. The first repository wins an ID conflict, but every source and the conflict flag remain visible. Stable releases are preferred within one repository before comparing versions.
- Discovery metadata is not an installed plugin. Installation actions are enabled only when the verified package and WASM lifecycle in the next scenario are available; otherwise `/plugins/installed` returns a truthful `runtime_status=unavailable` and the Web UI disables installation.
- Routes require authenticated plugin permissions, install `Cache-Control: no-store`, use the standard response envelope, bound request bodies, and audit configuration mutations. Runtime logs contain repository IDs, safe counts, durations, and stable error codes only; never Registry bodies, package URLs, credentials, or upstream response bodies.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| URL is raw GitHub content, a tree path, credentialed, queried, fragmented, redirected, or non-GitHub | Reject with a stable invalid-repository error before any network request |
| Default branch cannot resolve to a 40-character lowercase SHA | Reject the refresh as an invalid Registry commit |
| GitHub responds with a redirect | Do not follow it; preserve the last good cache |
| Metadata or Registry response exceeds its limit | Stop reading, return a stable too-large error, and preserve the last good cache |
| Registry contains unknown fields, invalid SemVer/range, duplicate entries, or a cross-repository Release URL | Reject the whole new snapshot; do not partially merge it |
| Refresh fails after an earlier success | Keep the successful cache/SHA/timestamp and expose the latest stable error code |
| Revision or repository set changed concurrently | Return conflict and require the client to reload; do not partially reorder |
| Two enabled repositories publish the same plugin ID | Select by repository priority and expose all sources plus `source_conflict=true` |
| Runtime/installer is unavailable | Return truthful unavailable state and render installation as disabled |
| Caller lacks read/manage permission | Return 403 with no-store and no cached Registry payload |

### 5. Good / Base / Bad Cases

- Good: an administrator adds `https://github.com/example/plugins`, refresh resolves commit `0123...abcd`, validates Registry v1, and the market card identifies that repository and pinned version.
- Good: a later GitHub outage records `plugin_repository_unavailable` while the previous market entries remain available from the last successful pinned cache.
- Base: no repository or no successful refresh produces an empty market and an actionable repository-settings state.
- Bad: fetching a user-supplied raw URL, following a redirect, replacing a valid cache with malformed JSON, silently choosing a conflicting source, or rendering an install button that cannot complete a verified installation.

### 6. Tests Required

- URL parser tests cover canonical `.git` normalization and rejection of raw, tree/blob, credentialed, queried, fragmented, ported, non-HTTPS, and non-GitHub values.
- GitHub client tests assert fixed host/path, GET-only behavior, default-branch-to-SHA pinning, invalid SHA rejection, redirect refusal, rate-limit mapping, and response-size bounds.
- Registry tests assert unknown-field rejection, cross-repository Release rejection, strict SemVer ordering including numeric prerelease identifiers, invalid ranges, duplicate entries, and maximum payload/entry bounds.
- Service tests assert CRUD/revision conflicts, full-set atomic reorder, last-good-cache preservation, pinned-commit requirements, repository priority, stable-release preference, compatibility ranges, and source-conflict disclosure.
- Migration tests assert v33 additive/idempotent creation, indexes, defaults, and preservation of repository rows across repeated migration runs.
- HTTP tests assert authentication, plugin RBAC, CSRF on mutations, no-store, request-size limits, safe error envelopes, and absence of Registry bodies or internal causes.
- Web tests assert repository add/toggle/reorder/refresh/delete payloads, unavailable installed state, source-conflict rendering, both themes, and disabled installation when the runtime is unavailable.

### 7. Wrong vs Correct

#### Wrong

```go
// User input controls the download host and a failed refresh destroys the cache.
body, _ := http.Get(input.RegistryURL)
db.Model(&repo).Updates(map[string]any{"cached_registry_json": string(body), "last_commit_sha": ""})
```

#### Correct

```go
source, err := contract.ParseGitHubRepositoryURL(input.GitHubURL)
if err != nil { return err }
snapshot, err := githubFetcher.Fetch(ctx, source) // fixed API host, redirect refusal, bounded reads
if err != nil { return recordStableFailureWithoutReplacingCache(repo.ID, err) }
if err := snapshot.Registry.Validate(source); err != nil { return recordStableFailureWithoutReplacingCache(repo.ID, err) }
return replacePinnedCacheWithRevisionCAS(repo.ID, repo.Revision, snapshot)
```

## Scenario: Verified package installation and WASM lifecycle

### 1. Scope / Trigger

- Trigger: package preview or confirmation, enable/disable, update, rollback, uninstall, Server startup restoration, shutdown, or cleanup of expired previews and orphaned packages.
- The Server is the only plugin runtime. Player receives normalized plugin media APIs and never installs or executes a plugin package.

### 2. Signatures

```text
POST   /api/v1/plugins/:plugin_id/installation-preview
POST   /api/v1/plugins/:plugin_id/install
POST   /api/v1/plugins/:plugin_id/update
POST   /api/v1/plugins/:plugin_id/enable
POST   /api/v1/plugins/:plugin_id/disable
POST   /api/v1/plugins/:plugin_id/rollback
DELETE /api/v1/plugins/:plugin_id
```

```text
plugin_packages
  plugin_id, version, repository identity/id, registry_commit
  canonical registry_entry_json, manifest_url, package_url
  package_sha256, extracted_tree_sha256
  canonical manifest_json, managed package_path, verified_at

plugin_installations
  plugin_id, active_package_id, previous_package_id, status
  revision, runtime_generation, last_runtime_error_code

plugin_install_previews
  id, plugin_id, plugin_package_id, operation
  permission_fingerprint, installation_revision, created_by
  expires_at, consumed_at
```

### 3. Contracts

- Preview resolves one enabled repository at its cached 40-character commit, downloads only the exact same-repository GitHub Release Manifest and `.omcp`, bounds both responses, rejects redirects to untrusted origins, and validates Registry/Manifest identity, compatibility, SHA-256, archive paths, and WASM ABI before persisting an immutable package record.
- ZIP extraction is confined to a Server-owned absolute plugin root. It rejects traversal, duplicate/case-colliding paths, links, Windows reparse points, reserved Windows names, special files, oversized entries, excessive aggregate size, and unsafe entry files.
- `package_sha256` authenticates the release archive. `extracted_tree_sha256` independently authenticates a deterministic, path-framed digest of every extracted directory and file byte. Confirm, enable, rollback, and restart must revalidate the full tree against the persisted digest before execution; a content-addressed directory name alone is insufficient.
- The preview is one-time, expires after 15 minutes, and binds actor, plugin ID, package, exact operation (`install` or `update`), permission fingerprint, and installation revision. The `/install` route accepts only an install preview and `/update` only an update preview.
- Confirmation revalidates that the repository still exists and is enabled, owner/repository identity is unchanged, the same pinned commit is current, and the cached Registry still contains the exact version, Manifest URL, package URL, and package SHA. Refresh, disable, delete, source change, or package-tree change invalidates the preview.
- Ordinary update accepts only a strictly higher SemVer from the already installed source. Same-version requests are idempotently rejected and downgrades use explicit rollback; repository precedence must never silently cross-grade an installed plugin.
- Permissions are canonical, duplicate-free, bounded to 64 entries, package-specific, and explicitly confirmed. Historical `granted_by` references are nullable with `ON DELETE SET NULL`, so user deletion does not destroy audit history or become blocked by an old grant.
- New installs start disabled. Every lifecycle mutation uses installation revision compare-and-swap and a serialized lifecycle lock. Runtime generations are append-only history and only terminal database states may claim what the in-memory host actually did.
- Update starts and validates the replacement before superseding the old module. Any runtime or persistence failure restores the old package and generation when possible; compensation persistence failure or failed runtime restoration stops the plugin fail-closed and returns a stable runtime-state error.
- Disable, rollback, uninstall, startup restore, and shutdown have equivalent compensation. A stop failure cannot leave a truthful `enabled` state. Startup closes stale `starting/running` generations before appending a fresh generation; successful host shutdown marks remaining active generations stopped.
- Expired/consumed previews and packages not referenced by installations, live previews, or generation history are pruned. Package deletion uses a Server-owned quarantine so database failure can restore files; interrupted staging is reconciled on startup.
- Plugin operations require plugin RBAC, CSRF, no-store, bounded bodies, safe error envelopes, and audit records. Logs may contain plugin ID, repository ID, counts, durations, generations, and stable error codes but no credentials, Registry bodies, package URLs, manifests, or internal paths.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Preview is expired, consumed, belongs to another actor, or reaches the wrong confirmation route | Reject and require a fresh preview |
| Repository was refreshed, disabled, deleted, or its exact Registry entry changed | Reject confirmation; do not consume trust from stale discovery state |
| Package directory, entry bytes, tree shape, link/reparse state, or persisted digest changed | Reject before executing any WASM |
| Requested update is equal/lower SemVer or from another repository | Reject; use rollback or an explicit future source-change flow |
| Replacement starts but generation persistence fails | Restore the previous verified module and database selection, or stop fail-closed |
| Disable/rollback/uninstall database compensation fails | Restore the prior verified runtime only when state can be made truthful; otherwise stop and mark failed |
| Server restarts with stale active generations | Mark them stopped, then append a new generation for each enabled verified plugin |
| Runtime is unavailable | Disable install/update/enable controls and return a stable unavailable error |

### 5. Tests Required

- Package tests cover traversal, case collisions, symlink/reparse/special files, Windows-reserved paths, size limits, entry validation, quarantine recovery, and byte-level WASM/tree tampering after extraction.
- Contract and SDK tests cover strict SemVer, maximum and unique permissions, unknown fields, exact Registry/Manifest identity, and cross-repository Release rejection.
- Service tests cover route-operation binding, preview TTL/fingerprint/revision/actor binding, repository refresh/disable/delete invalidation, source mismatch, equal/lower update rejection, package revalidation, lifecycle CAS, and permission-grant replacement.
- Failure-injection tests cover start/stop failures, update compensation persistence failure, rollback persistence plus restart failure, uninstall quarantine restoration, stale generation convergence, graceful shutdown convergence, and fail-closed status.
- HTTP/Web tests cover RBAC, CSRF, no-store, body bounds, stable status mapping, backend `install_status` decisions, runtime-unavailable controls, exact confirmation paths, permission-diff confirmation, Escape/focus-trap/focus restoration, and both themes.
