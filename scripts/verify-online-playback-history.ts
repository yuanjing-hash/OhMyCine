import type { DataSourceConfig, MediaItem } from '../src/services/datasource/types.ts'
import type { PlaybackHistoryEntry, PlaybackProgressUpsert } from '../src/services/playbackHistory.ts'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { setImmediate } from 'node:timers/promises'
import { fileURLToPath } from 'node:url'
import { createContext, runInContext } from 'node:vm'
import ts from 'typescript'
import { ServerDataSource } from '../src/services/datasource/server.ts'
import { onlineWorkToDetail, parseOnlineWork } from '../src/services/datasource/serverOnline.ts'
import { createPlaybackQueue } from '../src/services/playbackContext.ts'
import { shouldResumePlayback, toContinueWatchingMediaItem } from '../src/services/playbackHistory.ts'
import { createServerHistoryUploadChanges, mapServerHistoryChangeToLocalEntry } from '../src/services/playbackHistorySync.ts'

// Execute the actual view functions so a failed native open cannot pass a test
// that merely repeats the intended history policy in a fixture implementation.
async function viewFunctions(relative: string, names: string[]) {
  const vue = await readFile(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')
  const script = /<script setup lang="ts">([\s\S]*?)<\/script>/.exec(vue)?.[1]
  assert.ok(script)
  const ast = ts.createSourceFile(relative, script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const functions = ast.statements.filter(statement => ts.isFunctionDeclaration(statement) && names.includes(statement.name?.text ?? ''))
  assert.equal(functions.length, names.length)
  return ts.transpileModule(functions.map(statement => statement.getText(ast)).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
}

const work = parseOnlineWork({
  id: 'work/综艺',
  title: '官方综艺',
  kind: 'series',
  identity: { scheme: 'fixture', value: 'work/综艺' },
  defaultSegmentId: 'plus/33',
  segments: [
    { id: 'main/31', title: '2026-10-10 正片：秋日旅行', index: 31, versions: [{ id: 'version/正片', label: '来源', edition: '正片', variants: [] }] },
    { id: 'extra/32', title: '2026-10-10 彩蛋：幕后故事', index: 32, versions: [{ id: 'version/彩蛋', label: '来源', edition: '彩蛋', variants: [] }] },
    { id: 'plus/33', title: '2026-10-10 Plus：加长版', index: 33, versions: [{ id: 'version/Plus', label: '来源', edition: 'Plus', variants: [] }] },
  ],
}, 'https://server.example.test')!
const detail = onlineWorkToDetail('server-home', 'library/one', work)
const items = detail.children!
assert.equal(items.every(item => item.episodeNumber === undefined), true)
assert.equal(detail.defaultPlaybackItemId, items[2].id)
assert.equal(onlineWorkToDetail('server-home', 'library/one', { ...work, defaultSegmentId: undefined }).defaultPlaybackItemId, undefined)
assert.equal(onlineWorkToDetail('server-home', 'library/one', { ...work, defaultSegmentId: 'missing' }).defaultPlaybackItemId, undefined)

// A complete detail has a separate bound from small feed pages. Long official
// episode lists must retain the tail and its provider-selected exact identity.
for (const count of [201, 1000]) {
  const segments = Array.from({ length: count }, (_, index) => ({
    id: `segment/${index}`,
    title: `官方节目 ${index}`,
    index: index + 1,
    versions: [{ id: `version/${index}`, label: '来源', edition: '正片', variants: [] }],
  }))
  const longWork = parseOnlineWork({ ...work, defaultSegmentId: segments.at(-1)!.id, segments }, 'https://server.example.test')
  assert.ok(longWork)
  assert.equal(longWork.segments.length, count)
  const longDetail = onlineWorkToDetail('server-home', 'library/one', longWork)
  assert.equal(longDetail.children?.length, count)
  assert.equal(longDetail.defaultPlaybackItemId, longDetail.children?.at(-1)?.id, 'Tail default must not fall back to the first clip')
  assert.equal(longDetail.children?.at(-1)?.name, segments.at(-1)?.title)
}
assert.equal(parseOnlineWork({ ...work, segments: Array.from({ length: 1001 }, () => work.segments[0]) }, 'https://server.example.test'), null, 'Oversized complete details fail instead of silently discarding episodes')

const resumes = new Set<string>()
let explicitID: string | undefined
let selectedIndex = -1
const detailContext = createContext({
  editionEpisodes: { value: [] as MediaItem[] },
  selectedEpisodeEdition: { value: null as string | null },
  episodes: { value: [] as MediaItem[] },
  detail: { value: detail },
  sourceId: { value: 'server-home' },
  route: { query: { contextId: 'context' } },
  queryStringValue: (value: unknown) => value,
  getPlaybackMediaContext: () => ({ sourceId: 'server-home', currentItem: explicitID ? { id: explicitID } : undefined }),
  hasResumeProgress: (item: MediaItem) => resumes.has(item.id),
  selectEpisodeIndex: (index: number) => { selectedIndex = index },
  resetEpisodeWindow: () => {},
  refreshPlaybackProgress: async () => {},
})
runInContext(await viewFunctions('../src/views/MediaDetailView.vue', ['setEpisodeItems', 'selectEpisodeEdition', 'selectInitialEpisodeForSeason', 'episodeSearchNumberLabel']), detailContext)
detailContext.setEpisodeItems(items)
assert.equal(detailContext.episodes.value.length, 1)
detailContext.selectInitialEpisodeForSeason()
assert.equal(detailContext.episodes.value[selectedIndex].id, items[2].id, 'Provider current segment wins over the first intro')
resumes.add(items[1].id)
detailContext.selectInitialEpisodeForSeason()
assert.equal(detailContext.episodes.value[selectedIndex].id, items[1].id, 'History resume takes precedence over provider default')
explicitID = items[0].id
detailContext.selectInitialEpisodeForSeason()
assert.equal(detailContext.episodes.value[selectedIndex].id, items[0].id, 'Explicit selection takes precedence over history and provider default')
await detailContext.selectEpisodeEdition('彩蛋')
assert.equal(detailContext.episodes.value[0].id, items[1].id)
await detailContext.selectEpisodeEdition(null)
assert.equal(detailContext.episodes.value.length, 3, 'Explicit all-edition filter includes every official clip')
await detailContext.selectEpisodeEdition('彩蛋')
assert.equal(detailContext.episodes.value.length, 1, 'Scoped queue cannot auto-append main/Plus editions')
const scopedQueue = createPlaybackQueue(detailContext.episodes.value, items[1].id)!
assert.equal(scopedQueue.items.length, 1)
assert.equal(scopedQueue.items[0].edition, '彩蛋')
assert.equal(scopedQueue.items[0].displaySubtitle, items[1].displaySubtitle)
assert.equal(detailContext.episodeSearchNumberLabel(items[1]), '彩蛋')
assert.equal(detailContext.episodeSearchNumberLabel({ ...items[1], edition: undefined }), '节目')
assert.equal(detailContext.episodeSearchNumberLabel({ ...items[1], edition: undefined, episodeNumber: 7 }), '第 7 集')

const saved: PlaybackProgressUpsert[] = []
const events: string[] = []
let payload: PlaybackProgressUpsert = {
  sourceId: 'server-home',
  libraryId: items[1].libraryId,
  itemId: items[1].id,
  mediaIdentity: items[1].historyIdentity!,
  title: work.title,
  mediaType: 'episode',
  displaySubtitle: items[1].displaySubtitle,
  position: 0,
  duration: 600,
  completed: true,
}
const player = createContext({
  playbackProgressReady: false,
  fileLoaded: { value: false },
  playbackHistoryStarted: false,
  playbackEndReason: { value: null },
  playbackDiagnostics: { value: null },
  isPlaying: { value: true },
  lastSavedPosition: 0,
  HISTORY_MIN_SAVE_POSITION: 5,
  HISTORY_MIN_RESUME_POSITION: 30,
  currentHistoryPayload: () => ({ ...payload }),
  savePlaybackProgress: async (value: PlaybackProgressUpsert) => {
    saved.push({ ...value })
    events.push('local')
    return value
  },
  syncProviderProgress: async (_value: PlaybackProgressUpsert, event: string) => { events.push(`system:${event}`) },
  scheduleHomeSectionsRefreshAfterPlayback: () => {},
})
runInContext(await viewFunctions('../src/views/PlayerView.vue', ['syncProviderPlaybackStarted', 'saveCurrentProgress', 'shouldSaveLocalProgress', 'providerEventForPayload', 'isLowPositionTerminalEvent', 'shouldRefreshHomeAfterProgressEvent', 'playbackEndedNaturally', 'playbackHasFailed']), player)
async function flush() {
  await setImmediate()
  await setImmediate()
}

// Merely accepted load (or a failed gateway plan) has neither local nor remote history.
player.playbackProgressReady = true
player.syncProviderPlaybackStarted()
await player.saveCurrentProgress(true, 'paused')
assert.equal(saved.length, 0)
assert.equal(events.length, 0)
// Actual native FILE_LOADED creates one visible start record even before five seconds.
player.fileLoaded.value = true
player.syncProviderPlaybackStarted()
player.syncProviderPlaybackStarted()
await flush()
assert.equal(saved.length, 1)
assert.equal(saved[0].position, 0)
assert.equal(saved[0].completed, false, 'A start cannot inherit completion from a resume target')
assert.deepEqual(events, ['local', 'system:started'])
payload = { ...payload, position: 180, completed: false }
await player.saveCurrentProgress(false, 'progress')
assert.equal(saved.at(-1)?.position, 180)
assert.equal(saved.at(-1)?.mediaIdentity, items[1].id)
assert.equal(saved.at(-1)?.displaySubtitle, '彩蛋 · 2026-10-10 彩蛋：幕后故事')
// A subsequent failed source load cannot write a pending resume as watched.
player.fileLoaded.value = false
player.playbackHistoryStarted = false
payload = { ...payload, position: 550, completed: true }
await player.saveCurrentProgress(true, 'stopped')
player.syncProviderPlaybackStarted()
assert.equal(saved.length, 2)

const config: DataSourceConfig = { id: 'server-home', type: 'server', name: 'Server', order: 0, url: 'https://server.example.test', enabled: true }
const scope = 'a'.repeat(64)
const entry: PlaybackHistoryEntry = { ...saved[1], ownerScope: scope, completed: false, deleted: false, updatedAt: 1_791_600_010_000, progressSource: 'local' }
const [change] = await createServerHistoryUploadChanges([entry], [config], config.id, undefined, scope)
assert.equal(change.history_identity, items[1].id)
assert.equal(change.item_token, items[1].id)
assert.equal(change.sync_key, createHash('sha256').update(items[1].id).digest('hex'))
assert.equal(change.library_id, 'online-library|library%2Fone')
const otherDevice = { ...config, id: 'same-server-other-device' }
const [incoming] = mapServerHistoryChangeToLocalEntry(change, [otherDevice], otherDevice, 'b'.repeat(64))
assert.equal(incoming.sourceId, otherDevice.id)
assert.equal(incoming.mediaIdentity, items[1].id)
assert.equal(incoming.itemId, items[1].id)
assert.equal(incoming.displaySubtitle, items[1].displaySubtitle)
const resumed = toContinueWatchingMediaItem({ ...entry, ...incoming })
assert.equal(resumed.id, items[1].id)
assert.equal(resumed.historyIdentity, items[1].id)
assert.equal(resumed.resumePosition, 180)
assert.equal(shouldResumePlayback({ ...entry, ...incoming }), true)
assert.equal(resumed.displaySubtitle, items[1].displaySubtitle)

const workCard = createContext({
  mediaPath: { value: 'https://server.example.test/api/v1/player/online-assets/fixture' },
  activeSourceId: { value: 'server-home' },
  activeItemId: { value: 'online-work|library%2Fone|work%2F%E7%BB%BC%E8%89%BA' },
  activeStreamRequest: { itemId: items[1].id, historyIdentity: items[1].id },
  activeHistoryOwnerScope: scope,
  currentQueueItem: { value: undefined },
  LOCAL_FILE_SOURCE_ID: 'local-file',
  currentPlaybackContext: () => ({ sourceId: 'server-home', itemId: 'online-work|library%2Fone|work%2F%E7%BB%BC%E8%89%BA' }),
  currentPlaybackItem: () => ({ historyIdentity: 'online-work|library%2Fone|work%2F%E7%BB%BC%E8%89%BA' }),
  createSafeStreamIdentity: () => { throw new Error('Work playback must have exact identity') },
})
runInContext(await viewFunctions('../src/views/PlayerView.vue', ['currentHistoryIdentity', 'currentOriginItemId']), workCard)
assert.equal(workCard.currentOriginItemId(), items[1].id)
assert.equal(workCard.currentHistoryIdentity().mediaIdentity, items[1].id, 'Real Player history follows the exact fresh work-card selection')

// Equal titles and ordering never merge distinct versions/libraries or servers.
const isolatedEntries: PlaybackHistoryEntry[] = [entry, { ...entry, mediaIdentity: items[0].id, itemId: items[0].id }, { ...entry, mediaIdentity: items[1].id.replace('library%2Fone', 'library%2Ftwo'), itemId: items[1].id.replace('library%2Fone', 'library%2Ftwo'), libraryId: 'online-library|library%2Ftwo' }]
const isolated = await createServerHistoryUploadChanges(isolatedEntries, [config], config.id, undefined, scope)
assert.equal(new Set(isolated.map(item => item.sync_key)).size, 3)
assert.deepEqual(await createServerHistoryUploadChanges([entry], [config], 'another-server', undefined, scope), [])

const currentServer = new ServerDataSource({
  readCredential: async () => ({ accessToken: `omc_player_${'a'.repeat(43)}` }),
  bridge: { async request() { return { status: 200, body: { code: 0, data: { user: { id: 1 }, capabilities: ['online_playback_history_v1'] } } } } },
})
await currentServer.init({ ...config, extra: { credentialRef: 'fixture' } })
await currentServer.refreshCapabilities()
const physical = { ...entry, mediaIdentity: 'server:v1:movie:9:work-one', itemId: 'work|9|work-one', libraryId: '9', mediaType: 'movie' as const }
const tombstone = { ...entry, deleted: true }
const batch = [physical, entry, tombstone]
const upload = await createServerHistoryUploadChanges(batch, [config], config.id, identity => currentServer.canSyncOnlineHistory(identity), scope)
assert.equal(upload.length, 3, 'The current Server contract accepts bound physical and canonical online history')
assert.equal(upload[1].history_identity, entry.mediaIdentity)
assert.equal(upload[2].deleted, true)

console.log(JSON.stringify({ realViewHistoryPolicy: true, failedOpenNotWatched: true, actualNativeOpenStartsOnce: true, localBeforeSystem: true, exactOnlineProgress: true, editionGroups: true, officialTitles: true, canonicalSystemHistory: true, otherDeviceResume: true, sourceLibrarySegmentVersionIsolation: true }, null, 2))
