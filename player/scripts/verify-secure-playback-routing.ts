import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import {
  createPlaybackRouteQuery,
  playbackRouteQueryNeedsSanitization,
  sanitizePlaybackRouteQuery,
} from '../src/services/playbackRoute.ts'

const root = new URL('../', import.meta.url)

async function source(path: string): Promise<string> {
  return readFile(fileURLToPath(new URL(path, root)), 'utf8')
}

assert.deepEqual(createPlaybackRouteQuery({
  sourceId: 'emby-home',
  itemId: 'item-1',
  mediaSourceId: 'version-2',
  contextId: 'context-3',
}), {
  sourceId: 'emby-home',
  itemId: 'item-1',
  mediaSourceId: 'version-2',
  contextId: 'context-3',
})

const leakedQuery = {
  sourceId: 'emby-home',
  itemId: 'item-1',
  posterUrl: 'https://emby.example.test/Items/item-1/Images/Primary?api_key=secret',
  backdropUrl: 'https://emby.example.test/Items/item-1/Images/Backdrop?api_key=secret',
  titleLogoUrl: 'https://emby.example.test/Items/item-1/Images/Logo?api_key=secret',
  path: '/Users/example/private/movie.mkv',
  title: 'Movie',
}
assert.equal(playbackRouteQueryNeedsSanitization(leakedQuery), true)
assert.deepEqual(sanitizePlaybackRouteQuery(leakedQuery), {
  sourceId: 'emby-home',
  itemId: 'item-1',
})
assert.equal(playbackRouteQueryNeedsSanitization({ sourceId: ['emby-home'], itemId: 'item-1' }), true)

const router = await source('src/router/index.ts')
const playerView = await source('src/views/PlayerView.vue')
const mobileProxy = await source('src-tauri/src/mpv/mobile_proxy.rs')
const desktopPlayer = await source('src-tauri/src/commands/player.rs')
const navigationFiles = await Promise.all([
  source('src/views/HomeView.vue'),
  source('src/views/MediaDetailView.vue'),
  source('src/views/SourceLibraryView.vue'),
  source('src/components/media/GlobalSearchWorkspace.vue'),
  source('src/components/layout/FloatingControls.vue'),
  source('src/components/layout/MobileNavigation.vue'),
])

assert.match(router, /playbackRouteQueryNeedsSanitization\(to\.query\)/)
assert.match(router, /sanitizePlaybackRouteQuery\(to\.query\)/)
for (const navigationFile of navigationFiles)
  assert.match(navigationFile, /createPlaybackRouteQuery\(/)

assert.doesNotMatch(playerView, /route\.query\.(?:path|title|libraryId|mediaType|posterUrl|backdropUrl|titleLogoUrl|resumePosition|audioIndex|subtitleIndex)/)
assert.doesNotMatch(playerView, /legacyPath/)
assert.match(playerView, /source\.getStreamRequest\s*\?\s*source\.getStreamRequest\(request\)/)
assert.match(playerView, /locator\?\.kind === 'localPath'/)

assert.match(mobileProxy, /redirect\(reqwest::redirect::Policy::none\(\)\)/)
assert.match(mobileProxy, /for redirect_count in 0\.\.=MAX_UPSTREAM_REDIRECTS/)
assert.match(mobileProxy, /same_origin\(&current_url, &next_url\)/)
assert.match(mobileProxy, /loopback_bridge_follows_http_redirect_and_preserves_range_without_private_headers/)
assert.match(desktopPlayer, /stream_proxy\.prepare\(path, headers\)\.await/)
assert.match(desktopPlayer, /stream_proxy\.clear\(\)\.await/)

console.log(JSON.stringify({
  routeIdentityOnly: true,
  legacyPathRejected: true,
  artworkQueryRejected: true,
  streamResolvedAtPlaybackBoundary: true,
  mobile302BridgePreserved: true,
  desktop302BridgePreserved: true,
}, null, 2))
