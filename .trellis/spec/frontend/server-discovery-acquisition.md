# Server Discovery and Acquisition

## Scope

Apply this contract when changing Player global search, `ServerDataSource`, Server discovery detail, native Server SSE commands, or Server-backed acquisition/subscription actions.

## Contracts

- Ordinary global search queries only enabled Player data sources. Server TMDB discovery starts only after the user clicks the explicit “从 Server 搜索更多并入库” action.
- The action is disabled when no Server is connected or the refreshed bootstrap capability set lacks `discovery_search`. Download/import and subscription additionally require `acquisition_create` and `subscription_create`.
- Capability values are runtime-validated, cached only as non-secret config hints, and refreshed from bootstrap before a privileged workflow. Server 403 remains authoritative.
- Local and Server discovery details share the same media hero/detail component. Local actions remain play/history/download; Server actions are search, direct search, acquisition, and subscription.
- Server workflow is progressive: method, sites, resources, target/options, confirmation. Site selection supports explicit all/none and failed-site retry.
- Coverage and acquisition status load independently from the detail shell; either may fail without blanking the page.

## Native SSE boundary

- Tauri owns the streaming HTTP request so the device Bearer never enters browser fetch state. Only Player API paths for discovery streams are allowed.
- Reject redirects, invalid token/path/method/body, non-SSE content, malformed JSON, oversized events/streams, and idle streams.
- Forward only `media`, `progress`, `site`, `done`, and `error` through a typed channel.
- Starting a new search, cancelling, leaving the page, or destroying the source terminates the prior request. Events carry a generation/request identity so late data cannot alter current UI.
- Site events update results incrementally; final display uses Server order, not arrival order. Partial failure preserves successful results.

## Security and persistence

- Device tokens remain in the secure credential boundary. Ordinary configuration may persist only Server origin, credential reference, stable device ID, safe library summaries, and capability strings.
- Player never stores site credentials, claim internals, torrent URLs, provider identities, or Server management sessions.
- Player remains fully usable for local playback and local search without Server.

## Required checks

- Typecheck, lint, production build, `verify:server-datasource`, Rust tests, formatting, and Clippy with warnings denied.
- Verify explicit-only discovery, capability denial, shared detail component, incremental progress, cancellation, retry, stable order, Bearer/path allowlist, and response size limits.
