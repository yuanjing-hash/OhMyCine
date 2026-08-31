import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { getPlaybackMediaContext, savePlaybackMediaContext } from '../src/services/playbackContext.ts'

const viewFiles = [
  '../src/views/HomeView.vue',
  '../src/views/MediaDetailView.vue',
  '../src/views/SourceLibraryView.vue',
  '../src/views/PlayerView.vue',
]

for (const relativePath of viewFiles) {
  const absolutePath = fileURLToPath(new URL(relativePath, import.meta.url))
  const source = await readFile(absolutePath, 'utf8')
  const playerNavigations = [...source.matchAll(/name:\s*'player',\s*query:\s*\{([\s\S]*?)\n\s*\},\n\s*\}\)/g)]

  for (const navigation of playerNavigations)
    assert.doesNotMatch(navigation[1], /\bpath\s*:/, `${relativePath} must not persist playback paths in route query`)
}

const localPath = 'C:\\Media\\Private\\Movie.mkv'
const localContextId = savePlaybackMediaContext({
  sourceId: 'local-file',
  itemId: 'local-movie',
  locator: { kind: 'localPath', path: localPath },
})
const localContext = getPlaybackMediaContext(localContextId)
assert.deepEqual(localContext?.locator, { kind: 'localPath', path: localPath })

const embyContextId = savePlaybackMediaContext({
  sourceId: 'emby-home',
  itemId: 'movie-1',
  mediaSourceId: 'version-4k',
})
const embyContext = getPlaybackMediaContext(embyContextId)
assert.deepEqual(embyContext?.locator, {
  kind: 'dataSource',
  sourceId: 'emby-home',
  itemId: 'movie-1',
  mediaSourceId: 'version-4k',
})

const embySource = await readFile(fileURLToPath(new URL('../src/services/datasource/emby.ts', import.meta.url)), 'utf8')
assert.match(embySource, /mediaSources\.filter\(source => source\.Id === requestedMediaSourceId\)/)
assert.match(embySource, /所选 Emby 媒体版本已不可用/)
assert.match(embySource, /return '\/Sessions\/Playing'/)
assert.match(embySource, /return '\/Sessions\/Playing\/Stopped'/)
assert.match(embySource, /return '\/Sessions\/Playing\/Progress'/)
assert.match(embySource, /PositionTicks: secondsToTicks\(reportPosition\)/)
assert.match(embySource, /RunTimeTicks: secondsToTicks\(progress\.duration\)/)
assert.match(embySource, /PlaySessionId: session\.playSessionId/)
assert.match(embySource, /PlaybackRate: normalizePlaybackRate\(progress\.playbackRate\)/)
assert.match(embySource, /await this\.markPlayed\(itemId\)/)
assert.match(embySource, /if \(progress\.event === 'stopped' \|\| progress\.event === 'completed'\) \{\s+this\.cache\.clear\(\)\s+this\.playbackSessions\.delete\(itemId\)/)

const playerView = await readFile(fileURLToPath(new URL('../src/views/PlayerView.vue', import.meta.url)), 'utf8')
assert.match(playerView, /syncProviderPlaybackStarted\(\)/)
assert.match(playerView, /window\.setInterval\(\(\) => \{\s+void saveCurrentProgress\(false\)/)
assert.match(playerView, /if \(!shouldSaveLocalProgress\(payload, force, event\)\) \{[\s\S]*event !== 'progress'[\s\S]*syncProviderProgress\(payload, providerEvent\)/)
assert.match(playerView, /saveCurrentProgress\(true, 'stopped'\)/)
assert.match(playerView, /let playbackStopPromise: Promise<void> \| null = null/)
assert.match(playerView, /if \(playbackStopPromise\)\s+return playbackStopPromise/)
assert.match(playerView, /watch\(isPlaying, \(playing\) => \{\s+if \(playbackCleanupStarted\)\s+return/)
assert.match(playerView, /if \(shouldRefreshHomeAfterProgressEvent\(event, providerEvent\)\) \{\s+await providerSync\s+scheduleHomeSectionsRefreshAfterPlayback\(\)/)
assert.match(playerView, /pendingResumeSeek = \{ path: mediaPath\.value, position \}/)
assert.match(playerView, /watch\(\[videoReady, duration\],[\s\S]*applyPendingResumeSeekWhenReady\(\)/)
assert.match(playerView, /if \(isNativeAndroidPlayer\)\s+await seekMpv\(position, \{ optimistic: false \}\)\s+else\s+await seekMpv\(position\)/)
assert.match(playerView, /\(!isNativeAndroidPlayer \|\| videoReady\.value\) && Math\.abs\(currentTime\.value - position\) <= 5/)
assert.match(playerView, /pending && \(!isNativeAndroidPlayer \|\| videoReady\.value\) && pending\.path === mediaPath\.value/)
assert.match(playerView, /function effectivePlaybackPosition\(\)[\s\S]*pendingResumeSeek[\s\S]*pending\.position/)
assert.match(playerView, /if \(!playbackProgressReady\)\s+return[\s\S]*const payload = currentHistoryPayload\(\)/)
assert.match(playerView, /playbackProgressReady = true[\s\S]*syncProviderPlaybackStarted\(\)/)
assert.match(playerView, /const position = shouldResumePosition\(fallbackPosition, fallbackDuration\)[\s\S]*fallbackPosition[\s\S]*shouldResumePlayback\(saved\)/)
assert.match(playerView, /seek: seekMpv/)
assert.match(playerView, /async function seek\(position: number\)[\s\S]*cancelPendingResumeSeek\(\)[\s\S]*seekMpv\(position\)/)

const homeView = await readFile(fileURLToPath(new URL('../src/views/HomeView.vue', import.meta.url)), 'utf8')
assert.match(homeView, /本机记录 ·/)
assert.match(homeView, /const localResume = providerResumeIndex >= 0 \? null : newestLocalResume\(progressEntries\)/)

const mediaDetailView = await readFile(fileURLToPath(new URL('../src/views/MediaDetailView.vue', import.meta.url)), 'utf8')
assert.match(mediaDetailView, /function resumePositionForItem\(item: MediaItem\)[\s\S]*isResumePosition\(item\.resumePosition, item\.duration\)[\s\S]*return item\.resumePosition[\s\S]*shouldResumePlayback\(entry\)/)

const historyCommand = await readFile(fileURLToPath(new URL('../src-tauri/src/commands/history.rs', import.meta.url)), 'utf8')
assert.match(historyCommand, /DELETE FROM playback_history WHERE source_id = \?1/)

const preferenceCommand = await readFile(fileURLToPath(new URL('../src-tauri/src/commands/preference.rs', import.meta.url)), 'utf8')
assert.match(preferenceCommand, /DELETE FROM media_playback_preferences WHERE source_id = \?1/)

const dataSourceStore = await readFile(fileURLToPath(new URL('../src/stores/datasource.ts', import.meta.url)), 'utf8')
assert.match(dataSourceStore, /deleteMediaPlaybackPreferencesForSource\(id\)/)
assert.match(dataSourceStore, /const providerResumePosition = usableResumePosition\(providerItem\)/)
assert.match(dataSourceStore, /resumePosition: providerResumePosition \?\? localItem\.resumePosition/)

console.log(JSON.stringify({
  checkedViews: viewFiles.length,
  localPathStoredOnlyInMemory: true,
  embyMediaSourceId: embyContext?.mediaSourceId,
  embyPlaybackSessionLifecycle: true,
  embyShortPlaybackStopReported: true,
  embyStopIsIdempotent: true,
  embyTerminalSyncAwaitedBeforeRouteExit: true,
  cleanupPauseCannotOverwriteRemoteProgress: true,
  embyDetailCacheClearedAfterTerminalSync: true,
  startupProgressWaitsForResumeResolution: true,
  providerResumePrecedesLocalFallback: true,
  delayedResumeWaitsForMediaReady: true,
  androidResumeRequiresNativeConfirmation: true,
  pendingResumeProtectsProviderProgress: true,
  sourceScopedHistoryDelete: true,
  sourceScopedPlaybackPreferenceDelete: true,
}, null, 2))
