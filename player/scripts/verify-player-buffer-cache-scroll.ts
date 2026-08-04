import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)

async function source(path: string): Promise<string> {
  return readFile(fileURLToPath(new URL(path, root)), 'utf8')
}

const useMpv = await source('src/composables/useMpv.ts')
assert.match(useMpv, /readMpvProperty\('paused-for-cache'\)/)
assert.match(useMpv, /readMpvProperty\('cache-speed'\)/)
assert.match(useMpv, /BUFFER_DISPLAY_DELAY_MS = 500/)
assert.match(useMpv, /isBuffering/)
assert.match(useMpv, /bufferSpeedBytesPerSecond/)
assert.match(useMpv, /stopBufferingPolling\(\)/)

const bufferingIndicator = await source('src/components/player/BufferingIndicator.vue')
assert.match(bufferingIndicator, /正在缓冲/)
assert.match(bufferingIndicator, /MB\/s/)
assert.match(bufferingIndicator, /KB\/s/)
assert.match(bufferingIndicator, /buffering-spinner/)

const playerView = await source('src/views/PlayerView.vue')
assert.match(playerView, /<BufferingIndicator/)
assert.match(playerView, /v-if="hasMedia && isBuffering"/)
assert.match(playerView, /:is-buffering="isBuffering"/)
assert.match(playerView, /loadHomeSections\(\{ force: true, background: true \}\)/)

const datasourceStore = await source('src/stores/datasource.ts')
assert.match(datasourceStore, /HOME_CACHE_TTL_MS = 5 \* 60 \* 1000/)
assert.match(datasourceStore, /SOURCE_ROOT_CACHE_TTL_MS = 5 \* 60 \* 1000/)
assert.match(datasourceStore, /DISPLAY_CACHE_KEY = 'ohmycine-media-display-cache-v1'/)
assert.match(datasourceStore, /hydrateDisplayCache\(\)/)
assert.match(datasourceStore, /persistDisplayCache\(\)/)
assert.match(datasourceStore, /path: ''/)
assert.match(datasourceStore, /isSensitiveUrlKey/)
assert.match(datasourceStore, /await removeAppSetting\(DISPLAY_CACHE_KEY\)/)
assert.match(datasourceStore, /sourceRootSnapshots/)
assert.match(datasourceStore, /isSourceRootSnapshotFresh/)
assert.match(datasourceStore, /setSourceRootSnapshot/)
assert.match(datasourceStore, /invalidateSourceRootSnapshot/)
assert.match(datasourceStore, /const showLoading = !options\.background && !hasCachedContent/)

const homeView = await source('src/views/HomeView.vue')
assert.doesNotMatch(homeView, /scheduleSettledContinueWatchingRefresh/)
assert.equal((homeView.match(/store\.loadHomeSections\(\)/g) ?? []).length, 1)

const appLayout = await source('src/components/layout/AppLayout.vue')
assert.match(appLayout, /ref="mainScrollRef"/)
assert.match(appLayout, /watch\(\(\) => route\.fullPath, scrollContentToTop/)
assert.match(appLayout, /APP_SCROLL_TO_TOP_EVENT/)
assert.match(appLayout, /mainScrollRef\.value\?\.scrollTo\(\{ top: 0, left: 0, behavior: 'auto' \}\)/)

const sourceLibrary = await source('src/views/SourceLibraryView.vue')
assert.match(sourceLibrary, /getSourceRootSnapshot\(loadingSourceId\)/)
assert.match(sourceLibrary, /isSourceRootSnapshotFresh\(loadingSourceId\)/)
assert.match(sourceLibrary, /setSourceRootSnapshot\(loadingSourceId/)
assert.match(sourceLibrary, /requestAppScrollTop\(\)/)
assert.match(sourceLibrary, /loadGeneration !== sourceRootLoadGeneration/)

const surfaceHost = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/MpvSurfaceHost.kt')
assert.match(surfaceHost, /"pause", "paused-for-cache"/)
assert.match(surfaceHost, /"brightness", "sub-delay", "cache-speed"/)

console.log(JSON.stringify({
  delayedBufferIndicator: true,
  bufferTransferSpeed: true,
  androidBufferProperties: true,
  homeSessionCache: true,
  sourceRootSessionCache: true,
  androidPersistentDisplayCache: true,
  sensitiveDisplayUrlsExcluded: true,
  staleWhileRevalidate: true,
  customScrollContainerReset: true,
}, null, 2))
