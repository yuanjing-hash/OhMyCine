# Danmaku Contract

## 1. Scope

Player danmaku is independent from Server and works in the shared Vue layer on desktop and Android. The first supported remote contract is DanDanPlay API v2 plus compatible custom API roots.

## 2. Data contract

Normalize remote comments to `{ id, time, mode, color, text }`. Supported modes are scrolling (`1`), bottom fixed (`4`), and top fixed (`5`). Reject invalid times, unsupported modes, empty or oversized text, and excessive comment counts.

DanDanPlay `/api/v2/match` receives a basename stem without folders or the video extension. In `fileNameOnly` mode, do not fabricate empty hash or zero-size identity fields. Preserve the response `isMatched` signal: auto-load only the single exact association, while ranked fuzzy results remain explicit user choices.

For episode playback, identity uses structured metadata before display labels: `seriesName + seasonNumber + episodeNumber` is the preferred match name and `seriesName` is the manual-search keyword. A file basename is the fallback when structured series metadata is unavailable. Never use an episode's individual display title as the series search keyword merely because it is the Player chrome title.

Manual recovery uses `GET /api/v2/search/episodes` with `v2=true`, a title of at least two characters, and an optional positive episode filter (`1`, `C1`, `S1`, or `O1`). Parse its `animes[] -> episodes[]` shape at the service boundary, group results by work, and load comments only after the user selects an episode. This flow remains read-only and must not call any comment submission API.

When structured series metadata triggers the episode search fallback, automatic selection is deterministic at the service/domain boundary. Normalize the requested title and returned anime-group titles conservatively with Unicode NFKC, trimming, case folding, and removal of whitespace/common Chinese or English middle-dot separators. Auto-load only when exactly one anime group has that exact normalized title and that group contains exactly one episode returned for the requested episode filter. Global flattened result count, substring matches, fuzzy ranking, and first-result order are never selection signals; any duplicate exact group or multi-episode exact group remains a manual-search decision.

## 3. Media privacy

Matching may send only a logical media title or basename and rounded duration. Never send a local absolute path, `content://` URI, stream URL, redirect URL, playback headers, cookies, signed query parameters, or DataSource credentials. Danmaku identity and provider state never belong in the Player route query.

## 4. Native HTTP boundary

Remote danmaku calls go through Tauri/Rust so desktop and Android share timeout, response-size, redirect, and safe-error behavior. Validate custom roots as HTTP(S) URLs without userinfo, query, or fragment. Official requests use build-injected credentials and a timestamped signature. Follow at most three comment acceleration redirects, reject HTTPS downgrade, and do not forward official authentication headers to redirect targets.

## 5. Playback lifecycle

Match after the media title and duration are stable. Cancel stale async results with a generation token, cache successful results in process memory, and reload on media/provider changes. Failure to load danmaku must never block playback, seeking, Emby progress reporting, or queue switching.

## 6. Rendering and controls

Use a pointer-transparent Canvas overlay above the native video/touch surface and below Player chrome. Derive positions from current mpv time so pause and seek remain synchronized. Desktop and Android controls both expose adjacent on/off and settings controls; `D` toggles display and `Shift+D` opens settings.

The danmaku render layer is media content, not Player chrome. While media exists, `PlayerView` teleports it to a fixed full-window layer under `body`, outside the PlayerView subtree and every desktop/mobile chrome transition or visibility wrapper. The Player root, `html`, and `body` must never receive a chrome-hidden class: auto-hide state may control only explicitly identified chrome nodes, while the cursor and layout-level `WindowChrome` consume their own narrow state. Its canvas keeps a dedicated compositor boundary (`contain: strict`, `isolation: isolate`, and a promoted transform), but alpha backplanes are not a substitute for lifecycle separation. `shouldShowChrome` and chrome transition classes must never control the overlay's mount, display, size, opacity, RAF lifecycle, or settings.

The on/off control must differ by icon shape, not color alone. Manual search opens as a desktop modal and a full-screen mobile surface; selecting a result enables danmaku display and loads that episode immediately. Candidate browsing and correction belong only to this dedicated search surface; the compact settings content must not duplicate them in a native match selector. The composable may retain the selected match internally for loaded comments and process-memory cache identity.

Danmaku loading is a temporary Player-chrome activity state. Starting automatic matching, comment loading, reload, or manual-result loading must reveal the controls, clear any pending auto-hide timer, and prevent auto-hide for the full duration of `danmakuLoading`. When loading settles, successful or failed, restart the normal inactivity timer instead of leaving chrome permanently visible. This affects only chrome timing and must not couple the independently mounted danmaku render layer back to chrome visibility.

Remote responses may contain up to 50,000 comments. Rendering must use the sorted comment timeline to locate the current lifetime window with sub-linear lookup; never scan the entire response on every animation frame. Bound both inspected candidates and drawn comments per frame so same-timestamp floods, hidden modes, or keyword filters cannot monopolize the WebView main thread. When playback is paused, redraw only after time, settings, comments, or canvas size changes instead of continuously consuming frames.

Mpv time events are synchronization anchors, not animation frames. While playing, interpolate media time from the latest mpv time anchor using `requestAnimationFrame`, monotonic `performance.now()`, and the current playback speed. Re-anchor on time events, seek, pause/resume, and speed changes so scrolling motion follows the display refresh cadence without drifting from mpv.

## 7. Verification

Run frontend typecheck, lint, build, `verify:danmaku`, secure playback routing, Android playback, Emby HTTP/progress checks, Cargo tests, and strict Clippy. Danmaku work must not modify `mpv/mobile_proxy.rs` or the existing 302 playback chain.
