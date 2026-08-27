import type { DataSource, MediaDetail, MediaItem } from '../src/services/datasource/types'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { buildOfflineHierarchy, OFFLINE_SOURCE_ID, toOfflineMediaDetail } from '../src/services/datasource/offline'
import { planMediaDownload, summarizeDownloadPlan } from '../src/services/downloadPlanning'
import { deriveOfflineBadge } from '../src/stores/downloads'

function episode(id: string, season: number, number: number): MediaItem {
  return {
    id,
    sourceId: 'source-1',
    name: `Episode ${number}`,
    type: 'episode',
    path: `/private/${id}.mkv`,
    seasonNumber: season,
    episodeNumber: number,
  }
}
const first = episode('episode-1', 1, 1)
const second = episode('episode-2', 1, 2)
const third = episode('episode-3', 1, 3)
const season: MediaDetail = {
  id: 'season-1',
  sourceId: 'source-1',
  name: 'Season 1',
  type: 'season',
  path: '/private/season-1',
  children: [first, second, third],
}
const series: MediaDetail = {
  id: 'series-1',
  sourceId: 'source-1',
  name: 'Series',
  type: 'series',
  path: '/private/series',
  children: [season],
}
const firstDetail: MediaDetail = {
  ...first,
  mediaSources: [
    { id: 'episode-1-ui-current', providerMediaSourceId: 'episode-1-provider-current', name: 'Current 4K', size: 4_001 },
    { id: 'episode-1-ui-alternative', providerMediaSourceId: 'episode-1-provider-alternative', name: 'Alternative 1080p', size: 2_001 },
  ],
}
const secondDetail: MediaDetail = {
  ...second,
  mediaSources: [
    { id: 'episode-2-ui-current', providerMediaSourceId: 'episode-2-provider-current', name: 'Current 1080p', size: 2_002 },
    { id: 'episode-2-ui-alternative', providerMediaSourceId: 'episode-2-provider-alternative', name: 'Alternative 720p', size: 1_002 },
  ],
}
const details = new Map<string, MediaDetail | MediaItem>([
  [series.id, series],
  [season.id, season],
  [first.id, firstDetail],
  [second.id, secondDetail],
  [third.id, third],
])
const source = {
  getDetail: async (id: string) => details.get(id) as MediaDetail,
  list: async (id?: string) => (details.get(id ?? '')?.children ?? []),
} as DataSource

const plan = await planMediaDownload(source, {
  kind: 'media',
  sourceId: 'source-1',
  sourceType: 'emby',
  itemId: series.id,
  mediaType: 'series',
  display: { name: series.name },
})
assert.equal(plan.aggregate, true)
assert.deepEqual(plan.files.map(file => ({
  itemId: file.itemId,
  mediaSourceId: file.mediaSourceId,
  expectedBytes: file.expectedBytes,
})), [
  { itemId: 'episode-1', mediaSourceId: 'episode-1-provider-current', expectedBytes: 4_001 },
  { itemId: 'episode-2', mediaSourceId: 'episode-2-provider-current', expectedBytes: 2_002 },
  { itemId: 'episode-3', mediaSourceId: undefined, expectedBytes: undefined },
])
assert.equal(plan.files.some(file => file.mediaSourceId?.includes('alternative')), false, 'each episode must select only its current version')
assert.equal(new Set(plan.files.map(file => file.itemId)).size, 3, 'distinct episodes must not be deduplicated by version selection')
assert.equal(JSON.stringify(plan).includes('/private/'), false, 'download plan must not expose provider paths')
assert.deepEqual(summarizeDownloadPlan(plan), {
  fileCount: 3,
  knownBytes: 6_003,
  unknownSizeFiles: 1,
  usesExplicitSelection: true,
})

const movie: MediaDetail = {
  id: 'movie-1',
  sourceId: 'source-1',
  name: 'Movie',
  type: 'movie',
  path: '/private/movie.mkv',
  overview: 'A bounded offline synopsis.',
  genres: ['Drama'],
  cast: ['Actor One'],
  tmdbId: 42,
  mediaSources: [
    { id: 'movie-ui-current', providerMediaSourceId: 'movie-provider-current', name: 'Current 4K', size: 4_000 },
    { id: 'movie-ui-alternative', providerMediaSourceId: 'movie-provider-alternative', name: 'Alternative 1080p', size: 2_000 },
  ],
}
details.set(movie.id, movie)
const versions = await planMediaDownload(source, {
  kind: 'media',
  sourceId: 'source-1',
  sourceType: 'emby',
  itemId: movie.id,
  mediaType: 'movie',
  display: { name: movie.name },
})
assert.equal(versions.aggregate, false)
assert.deepEqual(versions.files.map(file => file.mediaSourceId), ['movie-provider-current'])
assert.deepEqual(versions.files.map(file => file.expectedBytes), [4_000])
assert.deepEqual(summarizeDownloadPlan(versions), {
  fileCount: 1,
  knownBytes: 4_000,
  unknownSizeFiles: 0,
  usesExplicitSelection: true,
})

const selectedVersion = await planMediaDownload(source, {
  kind: 'media',
  sourceId: 'source-1',
  sourceType: 'emby',
  itemId: movie.id,
  mediaSourceId: 'movie-provider-alternative',
  variantId: '1080p',
  mediaType: 'movie',
  display: { name: movie.name },
})
assert.deepEqual(selectedVersion.files.map(file => ({
  mediaSourceId: file.mediaSourceId,
  variantId: file.variantId,
  expectedBytes: file.expectedBytes,
})), [{ mediaSourceId: 'movie-provider-alternative', variantId: '1080p', expectedBytes: 2_000 }])
await assert.rejects(planMediaDownload(source, {
  kind: 'media',
  sourceId: 'source-1',
  sourceType: 'emby',
  itemId: movie.id,
  mediaSourceId: 'removed-version',
  mediaType: 'movie',
  display: { name: movie.name },
}), /所选媒体版本已经不可用/)
assert.deepEqual(versions.files[0].detailSnapshot, {
  name: 'Movie',
  originalTitle: undefined,
  mediaType: 'movie',
  year: undefined,
  rating: undefined,
  overview: 'A bounded offline synopsis.',
  tagline: undefined,
  duration: undefined,
  genres: ['Drama'],
  directors: [],
  writers: [],
  cast: ['Actor One'],
  imdbId: undefined,
  tmdbId: 42,
  seriesName: undefined,
  seasonNumber: undefined,
  episodeNumber: undefined,
})
assert.equal(JSON.stringify(versions.files[0].detailSnapshot).includes('/private/'), false)

const offlineDetail = await toOfflineMediaDetail({
  id: 'offline-row-1',
  sourceId: 'remote-source',
  itemId: movie.id,
  mediaSourceId: 'movie-provider-alternative',
  variantId: '1080p',
  displayName: movie.name,
  mediaType: 'movie',
  videoBytes: 2_000,
  completedAt: 1,
  attachmentState: 'complete',
  assets: [],
  snapshot: versions.files[0].detailSnapshot,
})
assert.equal(offlineDetail.id, 'offline-row-1')
assert.equal(offlineDetail.sourceId, OFFLINE_SOURCE_ID)
assert.equal(offlineDetail.mediaSources?.[0]?.sourceId, OFFLINE_SOURCE_ID)
assert.equal(offlineDetail.mediaSources?.[0]?.itemId, 'offline-row-1')
assert.equal(offlineDetail.exactIdentity, 'remote-source:movie-1:movie-provider-alternative:1080p')

const offlineEpisodes = [1, 3].map(episodeNumber => ({
  id: `offline-episode-${episodeNumber}`,
  sourceId: 'remote-source',
  itemId: `episode-${episodeNumber}`,
  displayName: `Episode ${episodeNumber}`,
  mediaType: 'episode',
  videoBytes: 1_000,
  completedAt: episodeNumber,
  attachmentState: 'complete' as const,
  seriesName: 'Series',
  seasonNumber: 1,
  episodeNumber,
}))
const hierarchy = buildOfflineHierarchy(offlineEpisodes)
assert.equal(hierarchy.length, 1)
assert.equal(hierarchy[0].type, 'series')
assert.equal(hierarchy[0].children?.[0]?.type, 'season')
assert.deepEqual(hierarchy[0].children?.[0]?.children?.map(item => item.episodeNumber), [1, 3])
assert.deepEqual(deriveOfflineBadge({
  id: 'series-remote',
  sourceId: 'remote-source',
  name: 'Series',
  type: 'series',
}, offlineEpisodes), { state: 'partial', downloaded: 2, label: '2 集已下载' })
assert.deepEqual(deriveOfflineBadge({
  id: 'episode-1',
  sourceId: 'remote-source',
  name: 'Episode 1',
  type: 'episode',
}, offlineEpisodes), { state: 'complete', downloaded: 1, total: 1, label: '已下载' })
assert.equal(deriveOfflineBadge({
  id: 'episode-1',
  sourceId: 'remote-source',
  name: 'Episode 1',
  type: 'episode',
}, []), null)

const detailViewSource = readFileSync(new URL('../src/views/MediaDetailView.vue', import.meta.url), 'utf8')
assert.doesNotMatch(detailViewSource, />离线下载</)
assert.doesNotMatch(detailViewSource, /openOfflineDownload/)
const downloadViewSource = readFileSync(new URL('../src/views/DownloadsView.vue', import.meta.url), 'utf8')
assert.match(downloadViewSource, /打开离线详情/)
assert.match(downloadViewSource, /打开文件位置/)
assert.match(downloadViewSource, /重试附件/)
assert.match(downloadViewSource, /attachmentState !== 'complete'/)
const downloadAdapterSource = readFileSync(new URL('../src/services/mediaActions/downloadAdapter.ts', import.meta.url), 'utf8')
assert.match(downloadAdapterSource, /文件：\$\{summary\.fileCount\} 个/)
assert.match(downloadAdapterSource, /预计大小：\$\{size\}/)
assert.doesNotMatch(downloadAdapterSource, /@tauri-apps\/plugin-dialog'\s*.*\bask\b/)
assert.match(downloadAdapterSource, /message: downloadConfirmation\(plan\)/)
assert.match(downloadAdapterSource, /if \(!confirmation\.confirmed\)/)
const playerViewSource = readFileSync(new URL('../src/views/PlayerView.vue', import.meta.url), 'utf8')
assert.match(playerViewSource, /function currentOriginItemId\(\): string/)
assert.match(playerViewSource, /const itemId = currentOriginItemId\(\) \|\| undefined/)

console.log('download planning verification passed')
