# Evidence

## Runtime season bug

- DownloadTask: `764db4df-6565-4f55-af9f-4f190db66d5b`
- TransferTask: `5dd1cac0-aef0-4027-9877-5efeb677ef5a`
- Download identity: TMDB 75129, task-level season 2.
- Identity snapshot: 28 structured file facts with correct S01–S04 distribution (6/6/8/8).
- Persisted transfer plan: all 28 paths under Season 02.
- Root cause: `transferEpisodeFactsForManifest` resolves each file, then unconditionally replaces every season with `download.ScrapeSeason`.
- Recovery fact: source manifest, identity episode paths and stable provider item IDs remain available for safe reorganization correlation.

## MoviePilot / 115 risk-control comparison

Reference checkout: `server/.runtime/research/MoviePilot-Plugins`, commit `798cf264829b2058abe865ca29ff92178f691970`.

- `plugins.v2/p115disk/p115_api.py:54-68` creates separate limiters for get-item, path lookup, list, move, copy, rename and delete.
- Move/copy/rename/delete/list are typically one call per two seconds per operation; path lookup is a separate one-per-second lane.
- `plugins.v2/p115disk/p115_api.py:84-105,292-317,344-362` shows path/ID cache lookup and folder creation without a second shared global mutation limiter; created directory IDs are immediately cached.
- `plugins.v2/p115strmhelper/helper/transfer/handler.py:706-829` groups file IDs by target directory and uses batch delete/move/copy, then updates per-item cache/task state. It is useful precedent for batching, but OhMyCine still needs stronger partial-success reconciliation before enabling the same optimization.
- `p115strmhelper/core/p115.py` has endpoint-specific cooldown modes and alternates cookie/app endpoints for life-event reads; its life monitor tracks repeated iOS 405 and temporarily switches to web API.

OhMyCine `server/pkg/cloud/pan115/client.go:185-218,361-544` currently uses one `mutationRate` at one request per two seconds for mkdir/upload/move/copy/rename/recycle/purge. The transfer worker also performs repeated list/stat/move/rename calls. Different operations therefore block each other and the current `server/webui/src/views/OrganizationView.vue` “按风控限速逐个准备目录” description is misleading. Healthy mkdir should remain bounded and idempotent, but it should not pay the move/rename/delete delay unless 115 actually returns a risk signal.

## MoviePilot browser emulation

Reference checkout: `server/.runtime/research/MoviePilot`, commit `988b4255100364fe59be7df19365dddd33cec42e`.

- `app/adapters/network/browser.py` defaults to CloakBrowser and optionally calls FlareSolverr.
- FlareSolverr source-fetch mode can return the solution response directly.
- Browser-action mode can obtain FlareSolverr cookies/UA, then open a CloakBrowser context.
- Page/context and temporary FlareSolverr sessions are explicitly closed.
- `docker/browser.sh` resolves a dedicated persistent browser cache and installs the CloakBrowser kernel when missing.

CloakBrowser checkout: commit `8fb3350dd18db19445081da3e94a0904ea8c840f`.

- Python wrapper depends on Playwright and downloads its own patched Chromium.
- Supported wrapper targets include Linux x64/arm64, macOS x64/arm64 and Windows x64.
- Wrapper source is MIT; compiled binary has a separate restrictive license, cannot be redistributed/bundled without a separate agreement, and latest builds may require a license/subscription.
- Dependency listing with end-user download from official channels is expressly allowed.
