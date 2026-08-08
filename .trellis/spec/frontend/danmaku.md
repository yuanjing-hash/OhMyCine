# Danmaku Contract

## 1. Scope

Player danmaku is independent from Server and works in the shared Vue layer on desktop and Android. The first supported remote contract is DanDanPlay API v2 plus compatible custom API roots.

## 2. Data contract

Normalize remote comments to `{ id, time, mode, color, text }`. Supported modes are scrolling (`1`), bottom fixed (`4`), and top fixed (`5`). Reject invalid times, unsupported modes, empty or oversized text, and excessive comment counts.

## 3. Media privacy

Matching may send only a logical media title or basename and rounded duration. Never send a local absolute path, `content://` URI, stream URL, redirect URL, playback headers, cookies, signed query parameters, or DataSource credentials. Danmaku identity and provider state never belong in the Player route query.

## 4. Native HTTP boundary

Remote danmaku calls go through Tauri/Rust so desktop and Android share timeout, response-size, redirect, and safe-error behavior. Validate custom roots as HTTP(S) URLs without userinfo, query, or fragment. Official requests use build-injected credentials and a timestamped signature. Follow at most three comment acceleration redirects, reject HTTPS downgrade, and do not forward official authentication headers to redirect targets.

## 5. Playback lifecycle

Match after the media title and duration are stable. Cancel stale async results with a generation token, cache successful results in process memory, and reload on media/provider changes. Failure to load danmaku must never block playback, seeking, Emby progress reporting, or queue switching.

## 6. Rendering and controls

Use a pointer-transparent Canvas overlay above the native video/touch surface and below Player chrome. Derive positions from current mpv time so pause and seek remain synchronized. Desktop and Android controls both expose adjacent on/off and settings controls; `D` toggles display and `Shift+D` opens settings.

## 7. Verification

Run frontend typecheck, lint, build, `verify:danmaku`, secure playback routing, Android playback, Emby HTTP/progress checks, Cargo tests, and strict Clippy. Danmaku work must not modify `mpv/mobile_proxy.rs` or the existing 302 playback chain.
