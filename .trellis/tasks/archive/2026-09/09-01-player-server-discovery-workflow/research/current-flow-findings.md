# Current flow findings

- `GlobalSearchWorkspace.vue::runSearch` currently calls local `searchAllSources` and Server `searchServerDiscovery` together, so typing causes the Server/TMDB request the product no longer wants.
- The search UI contains the native `type=search` clear affordance, a custom clear button and a modal close X.
- `ServerDiscoveryDetailView.resolveSource` calls `source.test()` before operations. Its initial load then fetches detail, sites, all target options and coverage, making unrelated failures contaminate the whole page.
- `src-tauri/src/commands/server.rs::server_request_json` buffers the whole response and applies a fixed 20-second timeout. It cannot surface the existing Server SSE site events.
- Server already exposes authenticated player-scoped discovery routes; the new stream bridge must retain the `/api/v1/player/` allowlist and `omc_player_` token validation.
