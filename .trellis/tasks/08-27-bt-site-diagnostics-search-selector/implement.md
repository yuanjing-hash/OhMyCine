# Implementation plan

## Phase 1: Regression fixtures and baselines

- [ ] Add the complete four-season 115 manifest/identity fixture and reproduce S02 flattening.
- [ ] Add ACG.RIP current RSS, EZTV string-size and LimeTorrents controlled redirect fixtures.
- [ ] Capture current `site_id` single-site behavior and 115 pacing contracts before refactoring.

## Phase 2: Multi-season transfer and recovery

- [ ] Extract one validated per-file episode resolver from identity snapshot, manifest and deterministic parser.
- [ ] Make transfer validation and local/cloud plan builders consume it; remove unconditional automatic `ScrapeSeason` overwrite for multi-season packages.
- [ ] Keep chosen season/episode through managed-item and catalog boundaries.
- [ ] Correlate reorganization items back to original manifest by stable provider identity and reuse the resolver.
- [ ] Verify the affected task previews 6/6/8/8 episodes across Seasons 01–04 without mutating files.

## Phase 3: 115 pacing and progress

- [ ] Split the pan115 limiter into per-operation lanes while preserving shared risk backoff/circuit.
- [ ] Precompute/persist the directory DAG and reuse listings/checkpoints across files and retries; healthy mkdir must not inherit the move/rename two-second delay.
- [ ] Add bounded, target-directory-grouped batch move/copy only if partial success is safely reconcilable from provider IDs and post-call listings.
- [ ] Add safe phase/backoff fields and replace the blanket Downloads/Organization “风控限速逐个准备目录” copy.
- [ ] Test per-operation pacing, real 405/429 backoff, resume, cancellation, no duplicate mkdir, batch partial success and idempotent retry.

## Phase 4: Public BT repairs

- [ ] Update ACG.RIP RSS path, EZTV decoder and LimeTorrents exact host-set redirects.
- [ ] Preserve safe diagnostics for AniDex/YTS/The Pirate Bay external failures.

## Phase 5: Search site selection

- [ ] Add the safe searchable-site options endpoint.
- [ ] Add bounded `site_ids` to JSON, SSE and TMDB multi-name search while retaining singular `site_id`.
- [ ] Bind site scope to session identity, retry and pagination.
- [ ] Build the accessible selection dialog with full/select-none controls, zero-selection guard and versioned browser-local last-selection persistence (first use selects all; later new sites require “select all” or explicit selection).
- [ ] Add backend permission/filter tests and frontend restore/locked-single-site tests.

## Phase 6: Rendered fetch capability

- [ ] Add provider-neutral rendered-fetch interfaces and profile opt-in.
- [ ] Implement bounded FlareSolverr client and health/config surface.
- [ ] Implement optional CloakBrowser companion discovery/health/lifecycle without redistributing its binary; add license/install UX.
- [ ] Route only exact controlled public-BT profile URLs through the configured provider.
- [ ] Test SSRF, redirects, response size, cookie isolation, timeout, process cleanup and redaction.
- [ ] Run live read-only searches for 1337x/EXT.to when a provider is available.

## Phase 7: Documentation and verification

- [ ] Update OpenAPI, architecture, roadmap and security docs.
- [ ] Run focused Go/WebUI tests, then `go test ./...`, typecheck, lint and build.
- [ ] Run `git diff --check` and inspect for secrets, arbitrary URL surfaces and stale copy.

## Phase 8: Convergent deletion preview

- [ ] Reproduce the 115 deletion-preview delay and distinguish per-item API pacing from SQLite/job-state blocking.
- [ ] Make `record_only` DB-only and fast; add assertions that it performs zero cloud/downloader calls.
- [ ] Treat provider task/root/item not-found as already deleted while keeping identity mismatch and live local workers fail-closed.
- [ ] Batch/group 115 source and managed-item reconciliation by parent/root where safe, and add bounded Server/client timeout plus actionable modal recovery.
- [ ] Test manually deleted provider task, missing root, partial/all missing files, stale provider state, truly live worker, timeout and scope isolation.
- [ ] Add the real 23/28-cancelled, 38-manifest-item/5-parent/no-managed-item shape; verify detached media-library items are never recycled by source cleanup.

## Phase 9: Server-only beta release

- [ ] Change Server Release to use/create a namespaced `server-vX.Y.Z` prerelease from the latest remote `develop`, without requiring or triggering a Player release.
- [ ] Preserve embedded WebUI, Windows/Linux archives, checksums, TMDB Secret validation, source guardrails and idempotent asset upload; update DEVELOPMENT/release documentation and workflow tests/guards.
- [ ] After final check and merge/push to `develop`, select the next Server version, dispatch the workflow from `develop`, monitor it to success and verify release assets/checksums. Do not run Player Release.

## Risky files / rollback points

- Transfer/reorganization: require preview fixtures before runtime repair because wrong precedence can create duplicate targets.
- pan115 client: pacing affects every 115 workflow; preserve a switch back to conservative defaults.
- Site handlers/services: invalid IDs must fail closed instead of widening search.
- Rendered fetch: public-BT-only first release and loopback companion are mandatory SSRF/cookie boundaries.

## Start gate

- [x] User confirmed the final plan and selection persistence: first use selects all, later searches restore the current browser's previous scope.
- [x] Planning artifacts and manifests are current.
- [ ] Only then run `task.py start` and dispatch implementation/check work.
