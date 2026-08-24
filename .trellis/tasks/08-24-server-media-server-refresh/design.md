# Technical Design

## 1. Ownership

This child owns Server-side change persistence/readiness, media-server adapters, refresh targets/jobs, management APIs and Web UI. The parent design defines the shared DTO and convergence contract. Player delivery is owned by the sibling child.

## 2. Schema and migration

Use the next additive SQLite migration to add:

- a per-library content revision projection;
- durable `MediaLibraryChange` rows with ready/pending state and bounded safe kinds;
- refresh target and refresh run tables with foreign keys and unique `(library_id, connection_id, upstream_library_id)` identity;
- indexes for ready change sequence, target desired revision, Job lookup and retention cleanup.

Migration tests cover fresh, prior-version upgrade, repeat application, foreign keys and default values. Existing libraries start at revision zero and do not trigger a synthetic external refresh until a real change or explicit manual refresh.

## 3. Media change service

Create one service boundary responsible for:

- comparing whether a transaction changed user-visible catalog/metadata/delivery state;
- incrementing the library revision once per committed logical reconciliation;
- writing a pending or ready change in the owning transaction;
- marking a generation ready after artifact completion/cleanup prerequisites;
- publishing an in-memory wake only after commit;
- serving bounded filtered changes and current revisions to the Player child;
- pruning expired rows without deleting catalog or artifact state.

Integrations must call this service from scan reconciliation, manual recognition override/clear, artifact terminal success and relevant removal cleanup. Provider life events and download/transfer workers continue to wake the existing authoritative reconciliation rather than emitting changes directly.

## 4. Media-server package

Add a small provider-neutral contract in `pkg/mediaserver`. Extend the Emby client and add a Jellyfin adapter with:

- strict endpoint/API-key validation;
- fixed-endpoint bounded HTTP client, no redirects, context deadlines and response caps;
- `Probe`, `ListLibraries`, and `RefreshLibrary`;
- stable library ID/name/content-type mapping;
- provider-specific authentication/header differences kept inside adapters;
- stable error categories for unavailable, unauthorized, rate-limited, invalid response and missing library.

Do not reuse the client-facing Emby gateway credential flow for refresh requests. The encrypted administration API key is decrypted only at the explicit service call boundary.

## 5. Refresh target service and worker

- CRUD/test/list operations load Connection + OhMyCine library, enforce provider/capability/permission constraints and use revision CAS for updates.
- Target creation first lists/verifies the selected upstream library; only stable ID and bounded safe label persist.
- A ready media change atomically advances every enabled target’s desired revision and enqueues/coalesces `media_server_refresh` by target resource key.
- Queue payload contains `refresh_target_id` only. Worker reloads the target/Connection/latest desired revision, calls the adapter and atomically records success.
- If Job generation advanced during the call, worker reconciles again before terminal success so a concurrent change is not swallowed.
- Retry classification follows existing queue policy. Authentication/configuration errors remain failed until corrected/manual retry; transient network/rate-limit errors use bounded backoff.

## 6. APIs and Web UI

Add resource-oriented management endpoints for target list/create/update/delete/test and action endpoints for manual refresh/retry. Normal responses use the standard envelope and `no-store` where they expose operational state.

Extend `PlayersView.vue` and its typed service rather than Storage UI. Reuse global Toast, revision conflict handling, abort stale loads, permission-generated constants and truthful partial/unknown states.

## 7. Security and observability

- All APIs repeat service-layer authorization.
- Secret input is strict, encrypted and write-only.
- External errors map to client-safe codes; logs include connection/target/library numeric identities and duration only.
- Audit target mutations and manual refresh requests without upstream ID/path secrets beyond the safe target identity.
- Job DTO/checkpoint never contains credentials, endpoints, upstream response, absolute paths or signed URLs.

## 8. Testing strategy

- Adapter fake servers cover path prefixes, headers, timeouts, redirects, response bounds, library enumeration and refresh calls for both providers.
- Service tests cover readiness barriers, no-op scans, manual metadata changes, coalescing, concurrent desired revision advance, restart recovery and failure isolation.
- Router/UI tests cover permission matrices, strict JSON, CAS, redaction, target lifecycle and truthful states.
- Parent integration owns the real end-to-end Player observation.
