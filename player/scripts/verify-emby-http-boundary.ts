import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)
const embySource = await readFile(fileURLToPath(new URL('src/services/datasource/emby.ts', root)), 'utf8')
const embyCommand = await readFile(fileURLToPath(new URL('src-tauri/src/commands/emby.rs', root)), 'utf8')
const lib = await readFile(fileURLToPath(new URL('src-tauri/src/lib.rs', root)), 'utf8')
const playerView = await readFile(fileURLToPath(new URL('src/views/PlayerView.vue', root)), 'utf8')
const playerShared = await readFile(fileURLToPath(new URL('src-tauri/src/commands/player_shared.rs', root)), 'utf8')
const playerMobile = await readFile(fileURLToPath(new URL('src-tauri/src/commands/player_mobile.rs', root)), 'utf8')

assert.doesNotMatch(embySource, /from ['"]ofetch['"]/)
assert.match(embySource, /invoke<EmbyNativePlaybackJsonResponse>\('emby_request_json'/)
assert.match(embySource, /redirect: 'error'/)
assert.match(embySource, /EMBY_HTTP_TIMEOUT_MS = 15_000/)
assert.match(embySource, /EMBY_MAX_JSON_RESPONSE_BYTES = 4 \* 1024 \* 1024/)
assert.match(embySource, /if \(!response\.body\)\s+return ''/)
assert.match(embyCommand, /timeout\(Duration::from_secs\(HTTP_TIMEOUT_SECONDS\)\)/)
assert.match(embyCommand, /redirect\(reqwest::redirect::Policy::none\(\)\)/)
assert.match(embyCommand, /read_limited_response_body/)
assert.match(embyCommand, /MAX_RESPONSE_BODY_BYTES: usize = 4 \* 1024 \* 1024/)
assert.match(embyCommand, /generic_get_uses_bounded_native_client_and_auth_headers/)
assert.match(embyCommand, /redirects_are_rejected_instead_of_followed/)
assert.match(embyCommand, /oversized_json_responses_are_rejected/)
assert.match(embySource, /EMBY_CLIENT_VERSION = playerPackage\.version/)
assert.match(embyCommand, /EMBY_CLIENT_VERSION: &str = env!\("CARGO_PKG_VERSION"\)/)
assert.doesNotMatch(embySource, /Version=\\?"0\.1\.0/)
assert.doesNotMatch(embyCommand, /Version=\\?"0\.1\.0/)
assert.match(lib, /emby_request_json/)
assert.match(embySource, /emby_post_playback_json/)
assert.match(embySource, /readonly MediaStreams\?: EmbyMediaStreamRecord\[\]/)
assert.match(embySource, /buildPlaybackStreamRequest/)
assert.match(embySource, /mediaSourceId: source\.Id/)
assert.match(embySource, /source\.MediaStreams\?\.length \? source\.MediaStreams : fallbackStreams/)
assert.match(embySource, /mapSubtitleTrack\(item\.Id, stream, mediaSourceId, undefined, false\)/)
assert.match(embySource, /safeRequiredHttpHeaders/)
assert.match(playerView, /syncKnownSubtitleTracks\(request\.subtitles \?\? \[\]\)/)
assert.match(playerView, /activeResolvedMediaSourceId\.value = request\.mediaSourceId \?\? ''/)
assert.match(playerShared, /prepare_external_subtitle/)
assert.match(playerShared, /headers\.clear\(\)/)
assert.match(playerShared, /strips_sensitive_headers_on_cross_origin_subtitle_redirects/)
assert.match(playerMobile, /prepare_external_subtitle\(&app, &url/)

console.log(JSON.stringify({
  ordinaryRequestsUseNativeBoundary: true,
  browserFallbackIsBounded: true,
  timeoutEnabled: true,
  redirectsDisabled: true,
  responseSizeBounded: true,
  clientVersionTracksRelease: true,
  playbackPostBoundaryPreserved: true,
  selectedMediaSourceSubtitlesPreserved: true,
  transientSubtitleHeadersUseNativeCache: true,
}, null, 2))
