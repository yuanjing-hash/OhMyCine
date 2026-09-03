import assert from 'node:assert/strict'
import fs from 'node:fs'
import { getAppSetting, setAppSetting } from '../src/services/appSettings.ts'
import { readServerCredential, removeCredential } from '../src/services/datasource/credentialStore.ts'
import { configureOhMyCineServerOrigins, embyInstanceFingerprint, extractTrustedOhMyCineArtifactIdentity, namesByPersonType } from '../src/services/datasource/emby.ts'
import { forgetPlaybackTargetsForSource, mergeMediaItemsByIdentity, prunePlaybackTargets, rememberPlaybackTargetsForItems } from '../src/services/datasource/identityMerge.ts'
import { describeMediaSource } from '../src/services/datasource/mediaSourceDisplay.ts'
import { loginServerAndCreateConfig, logoutServerBestEffort, mapServerHistoryItem, ServerDataSource } from '../src/services/datasource/server.ts'
import { createPlaybackQueueItem } from '../src/services/playbackContext.ts'
import { playbackProgressIdentityForMediaItem } from '../src/services/playbackHistory.ts'
import { getServerAcquisitions, searchServerResources } from '../src/services/serverDiscovery.ts'

const token = `omc_player_${'a'.repeat(43)}`
const calls: Array<{ path: string, accessToken?: string, method?: string, body?: unknown }> = []
const bridge = {
  async request(request: { path: string, accessToken?: string, method?: string, body?: unknown }) {
    calls.push({ path: request.path, accessToken: request.accessToken, method: request.method, body: request.body })
    const page = Number(new URL(`http://player.test${request.path}`).searchParams.get('page') ?? '1')
    const data = request.path === '/api/v1/player/discovery/acquisitions?page=2&page_size=12'
      ? { list: [{ id: 'acquisition-1', title: '七武士', media_type: 'movie', tmdb_id: 346, stage: 'download', status: 'running', progress: 62.5, bytes_completed: 625, bytes_total: 1000, download_speed: 100, eta_seconds: 4, processed_files: 1, total_files: 2, revision: 3, updated_at: '2026-09-02T00:00:00Z' }], total: 13, page: 2, page_size: 12 }
      : request.path.startsWith('/api/v1/player/discovery/torrent-search?')
        ? { groups: [{ site_id: 7, site_name: 'Nyaa', site_type: 'bt', status: 'success', page: 2, has_next: true, skipped: 4, items: [{ token: 'opaque-result-token', title: 'Seven Samurai' }] }] }
        : request.path === '/api/v1/player/bootstrap'
      ? { capabilities: ['media_catalog', 'direct_stream'] }
      : request.path === '/api/v1/player/media-libraries'
        ? { list: [
            { id: 9, name: '115 电影', storage_type: 'pan115', entry_count: 102, work_count: 101, artwork_url: '/api/v1/assets/library-covers/library-cloud.png', artwork_revision: 'fixed-cloud-v1', artwork_source: 'fallback' },
            { id: 10, name: '不安全封面', storage_type: 'local', entry_count: 0, artwork_url: 'https://attacker.example/cover.png' },
          ], total: 2 }
        : request.path === '/api/v1/player/media-libraries/9/categories'
          ? { list: [{ id: 'category-movie', name: '外语电影', media_type: 'movie', item_count: 101, artwork_url: '/api/v1/assets/generated-library-covers/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa?exp=1787565600&sig=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', artwork_revision: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', artwork_source: 'generated' }], total: 1 }
        : request.path.startsWith(`/api/v1/player/media-libraries/9/catalog/${noPlayableItem().id}`)
          ? {
              item: noPlayableItem(),
              versions: [{ id: 88, title: '不可播放版本', size: 512, modified_at: '2026-08-22T00:00:00Z', playable: false, exact_identity: '' }],
            }
          : request.path.startsWith(`/api/v1/player/media-libraries/9/catalog/${seriesItem().id}`)
            ? {
                item: seriesItem(),
                versions: [
                  { id: 201, title: '改稻为桑', season: 1, episode: 1, overview: '第一集简介', still_path: '/episode-1.jpg', runtime_minutes: 47, rating: 8.2, size: 2048, modified_at: '2026-08-22T00:00:00Z', playable: true, stream_path: '/api/v1/player/media-entries/201/stream', delivery_kind: 'server_stream', exact_identity: 'server:entry:201' },
                  { id: 202, title: '示例剧 - S01E02', season: 1, episode: 2, size: 2048, modified_at: '2026-08-22T00:00:00Z', playable: true, stream_path: '/api/v1/player/media-entries/202/stream', delivery_kind: 'server_stream', exact_identity: 'server:entry:202' },
                  { id: 246, title: '落幕', season: 1, episode: 46, overview: '第四十六集简介', still_path: '/episode-46.jpg', runtime_minutes: 49, size: 2048, modified_at: '2026-08-22T00:00:00Z', playable: true, stream_path: '/api/v1/player/media-entries/246/stream', delivery_kind: 'server_stream', exact_identity: 'server:entry:246' },
                ],
              }
            : request.path.startsWith(`/api/v1/player/media-libraries/9/catalog/${legacyArtworkItem().id}`)
              ? {
                  item: legacyArtworkItem(),
                  versions: [{ id: 301, title: '旧 Server 电影', size: 1024, modified_at: '2026-08-22T00:00:00Z', playable: true, stream_path: '/api/v1/player/media-entries/301/stream', exact_identity: 'server:entry:301' }],
                }
          : request.path.startsWith('/api/v1/player/media-libraries/9/catalog/')
          ? {
              item: mediaItem(),
              versions: [
                { id: 76, title: '不可播放旧版本', size: 512, modified_at: '2026-08-21T00:00:00Z', playable: false, exact_identity: '' },
                { id: 77, title: '七武士 (1954)', size: 1024, modified_at: '2026-08-22T00:00:00Z', playable: true, stream_path: '/api/v1/player/media-entries/77/stream', delivery_kind: 'server_redirect', exact_identity: 'ohmycine:artifact:artifact_test' },
              ],
            }
          : request.path.startsWith('/api/v1/player/media-libraries/9/catalog')
            ? { list: page === 1 ? Array.from({ length: 100 }, (_, index) => mediaItem(index)) : [mediaItem(100)], total: 101, page, page_size: 100 }
            : { list: [], total: 0, page: 1, page_size: 50 }
    return { status: 200, body: { code: 0, message: 'success', data } }
  },
}

const source = new ServerDataSource({
  bridge,
  readCredential: async () => ({ accessToken: token }),
})
await source.init({
  id: 'server-home', type: 'server', name: '家庭 Server', order: 0,
  url: 'http://127.0.0.1:3000', enabled: true,
  extra: { credentialRef: 'datasource:server-home:server-credential', deviceId: 'device-test' },
})
assert.equal(await source.test(), true)
assert.deepEqual(source.capabilityCodes, ['direct_stream', 'media_catalog'])
assert.equal(source.hasCapability('media_catalog'), true)
assert.equal(source.hasCapability('discovery_search'), false)
const acquisitionPage = await getServerAcquisitions(source, 2, 12)
assert.deepEqual(acquisitionPage, {
  list: [{ id: 'acquisition-1', title: '七武士', mediaType: 'movie', tmdbId: 346, stage: 'download', status: 'running', progress: 62.5, bytesCompleted: 625, bytesTotal: 1000, downloadSpeed: 100, etaSeconds: 4, processedFiles: 1, totalFiles: 2, revision: 3, updatedAt: '2026-09-02T00:00:00Z', downloadTaskId: undefined, followSubscriptionId: undefined, targetLibraryId: undefined, transferTaskId: undefined, lastErrorCode: undefined }],
  total: 13,
  page: 2,
  pageSize: 12,
})
const resourceGroups = await searchServerResources(source, { mediaType: 'movie', title: 'Seven Samurai', direct: true, siteIds: [7], page: 2 })
assert.equal(resourceGroups[0]?.page, 2)
assert.equal(resourceGroups[0]?.hasNext, true)
assert.equal(resourceGroups[0]?.skipped, 4)
await source.createDiscoveryDownload({ result_token: 'opaque-result-token', downloader_id: 'downloader-1', media_library_id: 9, profile_id: 2, priority: 0, expected_tmdb_id: 346, expected_media_type: 'movie' })
assert.deepEqual(calls.find(call => call.path === '/api/v1/player/discovery/downloads')?.body, {
  result_token: 'opaque-result-token',
  downloader_id: 'downloader-1',
  media_library_id: 9,
  profile_id: 2,
  priority: 0,
  expected_tmdb_id: 346,
  expected_media_type: 'movie',
})
const libraries = await source.listLibraries()
assert.deepEqual(libraries.map(item => [item.id, item.sourceId, item.name]), [
  ['9', 'server-home', '115 电影'],
  ['10', 'server-home', '不安全封面'],
])
assert.equal(libraries[0].backdropUrl, 'http://127.0.0.1:3000/api/v1/assets/library-covers/library-cloud.png')
assert.equal(libraries[0].artworkRevision, 'fixed-cloud-v1')
assert.equal(libraries[0].artworkSource, 'fallback')
assert.equal(libraries[0].artworkCandidates, undefined)
assert.equal(libraries[0].itemCount, 101)
assert.equal(libraries[1].backdropUrl, undefined)
const partialLibrarySource = new ServerDataSource({
  bridge: {
    async request(request) {
      if (request.path === '/api/v1/player/media-libraries')
        return { status: 503, body: { code: 'UPSTREAM_UNAVAILABLE', message: 'physical catalog unavailable', data: {} } }
      return { status: 200, body: { code: 0, message: 'success', data: { list: [], total: 0 } } }
    },
  },
  readCredential: async () => ({ accessToken: token }),
})
await partialLibrarySource.init({ id: 'server-partial-library', type: 'server', name: '部分可用', order: 0, url: 'http://127.0.0.1:3000', enabled: true, extra: { credentialRef: 'server-partial-library-credential', deviceId: 'device-partial-library' } })
assert.deepEqual(await partialLibrarySource.listLibraries(), [])
await assert.rejects(partialLibrarySource.listLibrariesForMediaChangeRefresh(), /physical catalog unavailable/)
partialLibrarySource.destroy()
const categories = await source.list('9')
assert.deepEqual(categories.map(item => [item.type, item.name]), [['folder', '外语电影']])
assert.equal(categories[0].posterUrl, 'http://127.0.0.1:3000/api/v1/assets/generated-library-covers/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa?exp=1787565600&sig=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb')
assert.equal(categories[0].artworkRevision, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')
assert.equal(categories[0].artworkSource, 'generated')

const acquisitionCatalogSource = new ServerDataSource({
  bridge: {
    async request(request) {
      const acquisition = { id: 'active-acquisition', title: 'Seven Samurai release', media_type: 'movie', tmdb_id: 346, stage: 'download', status: 'running', target_library_id: 9, progress: 25, processed_files: 0, total_files: 0, revision: 1, updated_at: '2026-09-02T00:00:00Z' }
      if (request.path === '/api/v1/player/media-libraries/9/categories')
        return { status: 200, body: { code: 0, message: 'success', data: { list: [], total: 0 } } }
      if (request.path.startsWith('/api/v1/player/discovery/acquisitions?'))
        return { status: 200, body: { code: 0, message: 'success', data: { list: [acquisition], total: 1, page: 1, page_size: 100 } } }
      if (request.path === '/api/v1/player/discovery/details/tmdb/movie/346')
        return { status: 200, body: { code: 0, message: 'success', data: { work: { provider: 'tmdb', provider_id: '346', media_type: 'movie', title: '七武士', tmdb_id: 346 }, genres: [], directors: [], cast: [] } } }
      if (request.path === '/api/v1/player/discovery/media/movie/346/acquisition')
        return { status: 200, body: { code: 0, message: 'success', data: acquisition } }
      return { status: 404, body: { code: 'NOT_FOUND', message: 'not found', data: {} } }
    },
  },
  readCredential: async () => ({ accessToken: token }),
})
await acquisitionCatalogSource.init({ id: 'server-acquisition-catalog', type: 'server', name: '入库占位测试', order: 0, url: 'http://127.0.0.1:3000', enabled: true, extra: { credentialRef: 'acquisition-catalog-credential', deviceId: 'acquisition-catalog-device' } })
const acquisitionCategories = await acquisitionCatalogSource.list('9')
assert.deepEqual(acquisitionCategories.map(item => [item.type, item.name]), [['folder', '正在入库']])
const acquisitionItems = await acquisitionCatalogSource.list(acquisitionCategories[0].id)
assert.deepEqual(acquisitionItems.map(item => [item.name, item.type, item.acquisition?.progress]), [['Seven Samurai release', 'movie', 25]])
const acquisitionDetail = await acquisitionCatalogSource.getDetail(acquisitionItems[0].id)
assert.equal(acquisitionDetail.name, '七武士')
assert.equal(acquisitionDetail.acquisition?.stage, 'download')
assert.deepEqual(acquisitionDetail.mediaSources, [])
acquisitionCatalogSource.destroy()

const items = await source.list(categories[0].id)
assert.equal(items.length, 101)
assert.equal(calls.filter(call => call.path.startsWith('/api/v1/player/media-libraries/9/catalog?')).length, 2)
assert.equal(calls.some(call => call.path.includes('category=%E5%A4%96%E8%AF%AD%E7%94%B5%E5%BD%B1') && call.path.includes('media_type=movie')), true)
assert.equal(items[0].originType, 'server')
assert.deepEqual(items[0].workIdentity, { scheme: 'tmdb', mediaType: 'movie', value: '346' })
const detail = await source.getDetail(items[0].id)
assert.equal(detail.mediaSources?.[0]?.id, '77')
assert.deepEqual(detail.mediaSources?.map(item => item.id), ['77'])
assert.equal(detail.mediaSources?.[0]?.isStrm, undefined)
assert.equal(detail.mediaSources?.[0]?.sourceLabel, '家庭 Server')
assert.equal(detail.mediaSources?.[0]?.deliveryKind, 'server_redirect')
assert.equal(describeMediaSource(detail.mediaSources![0]!), '1.0 KB · 来自 家庭 Server · 302 直链')
assert.deepEqual(detail.children?.map(item => item.id), [`entry|9|${mediaItem().id}|77`])
assert.equal(detail.originalTitle, '七人の侍')
assert.equal(detail.rating, 8.5)
assert.equal(detail.duration, 207 * 60)
assert.equal(detail.tagline, '他们站了起来。')
assert.deepEqual(detail.genres, ['剧情', '动作'])
assert.deepEqual(detail.directors, ['黑泽明'])
assert.deepEqual(detail.writers, ['桥本忍'])
assert.deepEqual(detail.cast, ['三船敏郎', '志村乔'])
assert.deepEqual(detail.people?.map(person => [person.name, person.role, person.character]), [
  ['黑泽明', 'Director', undefined],
  ['三船敏郎', 'Actor', '菊千代'],
])
assert.match(detail.people?.[1]?.imageUrl ?? '', /image\.tmdb\.org\/t\/p\/w500\/mifune\.jpg$/)
assert.equal(detail.tmdbId, 346)
assert.equal(detail.imdbId, 'tt0047478')
assert.equal(detail.stills?.length, 2)
const seriesSeasons = await source.list(`work|9|${seriesItem().id}`)
assert.deepEqual(seriesSeasons.map(item => [item.type, item.seasonNumber]), [['season', 1]])
const seriesDetail = await source.getDetail(`work|9|${seriesItem().id}`)
assert.deepEqual(seriesDetail.children?.map(item => [item.type, item.seasonNumber, item.episodeNumber]), [['episode', 1, 1], ['episode', 1, 2], ['episode', 1, 46]])
assert.equal(seriesDetail.children?.[0]?.name, '改稻为桑')
assert.equal(seriesDetail.children?.[0]?.overview, '第一集简介')
assert.match(seriesDetail.children?.[0]?.posterUrl ?? '', /image\.tmdb\.org\/t\/p\/w500/)
assert.match(seriesDetail.children?.[0]?.backdropUrl ?? '', /backdrop\.jpg$/)
assert.match(seriesDetail.children?.[0]?.episodeStillUrl ?? '', /episode-1\.jpg$/)
assert.equal(seriesDetail.children?.[0]?.historyIdentity, `server:v1:episode:9:${seriesItem().id}:1:1`)
assert.equal(seriesDetail.children?.[0]?.duration, 47 * 60)
assert.equal(seriesDetail.children?.[1]?.overview, undefined)
assert.match(seriesDetail.children?.[1]?.backdropUrl ?? '', /backdrop\.jpg$/)
assert.equal(seriesDetail.children?.[1]?.episodeStillUrl, undefined)
assert.equal(seriesDetail.children?.[2]?.name, '落幕')
assert.match(seriesDetail.children?.[2]?.backdropUrl ?? '', /backdrop\.jpg$/)
assert.match(seriesDetail.children?.[2]?.episodeStillUrl ?? '', /episode-46\.jpg$/)
assert.equal(seriesDetail.mediaSources?.[0]?.isStrm, undefined)
assert.equal(seriesDetail.mediaSources?.[0]?.deliveryKind, 'server_stream')
assert.equal(describeMediaSource(seriesDetail.mediaSources![0]!), '2.0 KB · 来自 家庭 Server · 文件流')
const legacyArtworkDetail = await source.getDetail(`work|9|${legacyArtworkItem().id}`)
assert.equal(legacyArtworkDetail.stills?.length, 1)
assert.match(legacyArtworkDetail.stills?.[0] ?? '', /\/backdrop\.jpg$/)
assert.equal(legacyArtworkDetail.mediaSources?.[0]?.sourceLabel, '家庭 Server')
assert.equal(legacyArtworkDetail.mediaSources?.[0]?.deliveryKind, undefined)
assert.equal(legacyArtworkDetail.mediaSources?.[0]?.isStrm, undefined)
assert.equal(describeMediaSource(legacyArtworkDetail.mediaSources![0]!), '1.0 KB · 来自 家庭 Server')
assert.equal(describeMediaSource({ id: 'emby-strm', name: 'Emby STRM', isStrm: true, isRemote: true }), 'STRM · 远程')
const stream = await source.getStreamRequest({ itemId: items[0].id })
assert.equal(stream.url, 'http://127.0.0.1:3000/api/v1/player/media-entries/77/stream')
assert.equal(stream.headers?.Authorization, `Bearer ${token}`)
await assert.rejects(source.getStreamRequest({ itemId: items[0].id, mediaSourceId: '76' }), /可播放的 Server 媒体版本/)
const noPlayableDetail = await source.getDetail(`work|9|${noPlayableItem().id}`)
assert.deepEqual(noPlayableDetail.mediaSources, [])
assert.deepEqual(noPlayableDetail.children, [])
assert.ok(calls.filter(call => call.path !== '/api/v1/player/auth/login').every(call => call.accessToken === token))

const mappedEpisodeHistory = mapServerHistoryItem('server-home', {
  history_identity: `server:v1:episode:9:${seriesItem().id}:1:2`,
  item_token: `entry|9|${seriesItem().id}|202`,
  display_title: '示例剧',
  display_subtitle: 'S01E02 · 改稻为桑',
  series_title: '示例剧',
  episode_title: '改稻为桑',
  season_number: 1,
  episode_number: 2,
  media_type: 'episode',
  poster_url: 'https://image.example.test/series-poster.jpg',
  backdrop_url: 'https://image.example.test/series-backdrop.jpg',
  episode_still_url: 'https://image.example.test/episode-still.jpg',
  position: 600,
  duration: 2400,
  completed: false,
  updated_at: 1_788_220_800_000,
})[0]!
assert.equal(mappedEpisodeHistory.name, '示例剧')
assert.equal(mappedEpisodeHistory.displaySubtitle, 'S01E02 · 改稻为桑')
assert.equal(mappedEpisodeHistory.posterUrl, 'https://image.example.test/series-poster.jpg')
assert.equal(mappedEpisodeHistory.episodeStillUrl, 'https://image.example.test/episode-still.jpg')
assert.equal(mappedEpisodeHistory.cardLayout, 'poster')
assert.deepEqual(playbackProgressIdentityForMediaItem(mappedEpisodeHistory), { sourceId: 'server-home', mediaIdentity: mappedEpisodeHistory.historyIdentity })
assert.equal(createPlaybackQueueItem(mappedEpisodeHistory).historyIdentity, mappedEpisodeHistory.historyIdentity)

const overviewCollectionIds = {
  automatic: '11111111-1111-4111-8111-111111111111',
  manual: '22222222-2222-4222-8222-222222222222',
}
const overviewSource = new ServerDataSource({
  bridge: {
    async request(request) {
      const path = request.path
      const data = path === '/api/v1/player/media-libraries'
        ? { list: [{ id: 9, name: '家庭影片', storage_type: 'local', entry_count: 1, work_count: 1, artwork_url: '/api/v1/assets/library-covers/library-local.png', artwork_revision: 'library-v1', artwork_source: 'provider' }], total: 1 }
        : path === '/api/v1/player/online-libraries'
          ? { list: [], total: 0 }
          : path === '/api/v1/player/home-contributions'
            ? { contributions: [] }
            : path.startsWith('/api/v1/player/media-libraries/9/catalog')
              ? { list: [mediaItem()], total: 1, page: 1, page_size: 100 }
              : path === '/api/v1/player/history?page=1&page_size=24&source_kind=server' || path === '/api/v1/player/history?page=1&page_size=100&source_kind=server'
                ? { list: [{ history_identity: 'server:v1:movie:9:bW92aWU6dG1kYjozNDY', item_token: 'work|9|bW92aWU6dG1kYjozNDY', display_title: '七武士', media_type: 'movie', position: 300, duration: 1200, completed: false, updated_at: 1_788_220_800_000 }], total: 1, page: 1, page_size: 24, has_more: false }
                : path === '/api/v1/player/favorites'
                  ? { list: [mediaItem()], total: 1 }
                  : path === '/api/v1/player/collections?kind=collection'
                    ? { list: [
                        { id: overviewCollectionIds.automatic, name: '系统合集', kind: 'collection', source: 'tmdb', item_count: 2, poster_path: '/automatic.jpg' },
                        { id: overviewCollectionIds.manual, name: '我的合集', kind: 'collection', source: 'manual', item_count: 1, poster_path: '/manual.jpg' },
                      ], total: 2 }
                    : path === `/api/v1/player/collections/${overviewCollectionIds.automatic}/items`
                      ? { list: [mediaItem()], total: 1 }
                      : { list: [], total: 0 }
      return { status: 200, body: { code: 0, message: 'success', data } }
    },
  },
  readCredential: async () => ({ accessToken: token }),
})
await overviewSource.init({ id: 'server-overview', type: 'server', name: '总览测试', order: 0, url: 'http://127.0.0.1:3000', enabled: true, extra: { credentialRef: 'overview-credential', deviceId: 'overview-device' } })
const overviewSections = await overviewSource.getHomeSections()
assert.deepEqual(overviewSections.filter(section => !section.providerIdentity).map(section => section.purpose ?? section.type), [
  'hero', 'continueWatching', 'recentlyAdded', 'favorites', 'automaticCollections', 'manualCollections', 'libraries',
])
const automaticSection = overviewSections.find(section => section.purpose === 'automaticCollections')!
assert.equal(automaticSection.collectionSource, 'automatic')
assert.equal(automaticSection.items[0]?.displaySubtitle, '2 部影片')
const automaticCards = await overviewSource.list(automaticSection.viewAllRoute!.path)
assert.equal(automaticCards[0]?.name, '系统合集')
assert.equal((await overviewSource.list(automaticCards[0]!.id))[0]?.name, '七武士')
overviewSource.destroy()

const fastOverviewCalls: string[] = []
const fastOverviewSource = new ServerDataSource({
  bridge: {
    async request(request) {
      fastOverviewCalls.push(request.path)
      const history = { history_identity: 'server:v1:movie:9:bW92aWU6dG1kYjozNDY', item_token: 'work|9|bW92aWU6dG1kYjozNDY', display_title: '七武士', media_type: 'movie', position: 300, duration: 1200, completed: false, updated_at: 1_788_220_800_000 }
      const section = (list: unknown[], status: 'ok' | 'unavailable' = 'ok') => ({ status, list, has_more: false, ...(status === 'unavailable' ? { error_code: 'INTERNAL_ERROR' } : {}) })
      const data = request.path === '/api/v1/player/overview'
        ? {
            version: 'v1',
            sections: {
              featured: section([mediaItem()]),
              continue_watching: section([history, { ...history, item_token: 'entry|9|bW92aWU6dG1kYjozNDY|77' }]),
              recently_added: section([mediaItem()]),
              favorites: section([mediaItem()]),
              automatic_collections: section([{ id: overviewCollectionIds.automatic, name: '系统合集', kind: 'collection', source: 'tmdb', item_count: 2, poster_path: '/automatic.jpg' }]),
              manual_collections: section([], 'unavailable'),
              recent_history: section([history, { ...history, item_token: 'entry|9|bW92aWU6dG1kYjozNDY|77' }]),
              media_libraries: section([{ id: 9, name: '家庭影片', storage_type: 'local', entry_count: 1, work_count: 1, artwork_url: '/api/v1/assets/library-covers/library-local.png', artwork_revision: 'library-v1', artwork_source: 'provider' }]),
            },
          }
        : request.path === '/api/v1/player/online-libraries'
          ? { list: [], total: 0 }
          : request.path === '/api/v1/player/home-contributions'
            ? { contributions: [] }
            : { list: [], total: 0 }
      return { status: 200, body: { code: 0, message: 'success', data } }
    },
  },
  readCredential: async () => ({ accessToken: token }),
})
await fastOverviewSource.init({ id: 'server-fast-overview', type: 'server', name: '聚合总览测试', order: 0, url: 'http://127.0.0.1:3000', enabled: true, extra: { credentialRef: 'fast-overview-credential', deviceId: 'fast-overview-device', capabilities: ['media_overview_v1'] } })
const fastOverviewSections = await fastOverviewSource.getHomeSections()
assert.deepEqual(fastOverviewSections.filter(section => !section.providerIdentity).map(section => section.purpose ?? section.type), [
  'hero', 'continueWatching', 'recentlyAdded', 'favorites', 'automaticCollections', 'manualCollections', 'libraries',
])
assert.equal(fastOverviewSections.find(section => section.purpose === 'manualCollections')?.errorCode, 'INTERNAL_ERROR')
assert.equal(fastOverviewSections.find(section => section.purpose === 'favorites')?.items[0]?.favorite, true)
assert.equal(fastOverviewSections.find(section => section.type === 'continueWatching')?.items.length, 1)
assert.equal(fastOverviewSections.some(section => section.purpose === 'history'), false)
assert.deepEqual(fastOverviewCalls.filter(path => ['/api/v1/player/overview', '/api/v1/player/media-libraries', '/api/v1/player/history?page=1&page_size=24&source_kind=server', '/api/v1/player/favorites', '/api/v1/player/collections?kind=collection'].includes(path)), ['/api/v1/player/overview'])
fastOverviewSource.destroy()

const changeCalls: string[] = []
const changeSource = new ServerDataSource({
  bridge: {
    async request(request) {
      changeCalls.push(request.path)
      return { status: 200, body: { code: 0, message: 'success', data: { cursor: '8', resync_required: false, changes: [{ library_id: 9, content_revision: 3, kind: 'catalog', changed_at: '2026-08-26T00:00:00Z' }] } } }
    },
  },
  readCredential: async () => ({ accessToken: token }),
})
await changeSource.init({ id: 'server-change', type: 'server', name: '变更测试', order: 0, url: 'http://127.0.0.1:3000', enabled: true, extra: { credentialRef: 'server-change-credential', deviceId: 'device-change' } })
let stopChangeWatch = () => {}
const observedChange = await new Promise<{ sourceId: string, libraryIds: string[], resyncRequired: boolean }>((resolve) => {
  stopChangeWatch = changeSource.watchMediaChanges((change) => {
    stopChangeWatch()
    resolve(change)
  })
})
assert.deepEqual(observedChange, { sourceId: 'server-change', libraryIds: ['9'], libraryRevisions: { 9: 3 }, resyncRequired: false })
assert.match(changeCalls[0] ?? '', /\/api\/v1\/player\/media-changes\?cursor=0&wait_seconds=12/)
changeSource.destroy()

let unsupportedCalls = 0
const unsupportedCursorKey = `ohmycine:server-media-change-cursor:server-unsupported:${encodeURIComponent('http://127.0.0.1:3000')}`
await setAppSetting(unsupportedCursorKey, '18446744073709551616')
const unsupportedSource = new ServerDataSource({
  bridge: {
    async request(request) {
      unsupportedCalls += 1
      assert.match(request.path, /cursor=0&wait_seconds=12/)
      return { status: 404, body: { code: 'NOT_FOUND', message: 'endpoint unavailable', data: {} } }
    },
  },
  readCredential: async () => ({ accessToken: token }),
})
await unsupportedSource.init({ id: 'server-unsupported', type: 'server', name: '旧版 Server', order: 0, url: 'http://127.0.0.1:3000', enabled: true, extra: { credentialRef: 'server-unsupported-credential', deviceId: 'device-unsupported' } })
const stopUnsupportedWatch = unsupportedSource.watchMediaChanges(() => assert.fail('unsupported Server emitted a media change'))
await delay(40)
assert.equal(unsupportedCalls, 1)
await delay(300)
assert.equal(unsupportedCalls, 1)
stopUnsupportedWatch()
unsupportedSource.destroy()

let invalidPageCalls = 0
const invalidPageSource = new ServerDataSource({
  bridge: {
    async request() {
      invalidPageCalls += 1
      return { status: 200, body: { code: 0, message: 'success', data: { cursor: '1', resync_required: false, changes: [{ library_id: 9, content_revision: 4, kind: 'metadata', changed_at: 'not-a-time' }] } } }
    },
  },
  readCredential: async () => ({ accessToken: token }),
})
await invalidPageSource.init({ id: 'server-invalid-page', type: 'server', name: '无效响应', order: 0, url: 'http://127.0.0.1:3000', enabled: true, extra: { credentialRef: 'server-invalid-page-credential', deviceId: 'device-invalid-page' } })
const stopInvalidPageWatch = invalidPageSource.watchMediaChanges(() => assert.fail('invalid response emitted a media change'))
await delay(350)
assert.equal(invalidPageCalls, 1)
stopInvalidPageWatch()
invalidPageSource.destroy()

const resyncSourceId = 'server-resync'
const resyncCursorKey = `ohmycine:server-media-change-cursor:${resyncSourceId}:${encodeURIComponent('http://127.0.0.1:3000')}`
await setAppSetting(resyncCursorKey, '8')
let resyncCalls = 0
const resyncSource = new ServerDataSource({
  bridge: {
    async request(request) {
      resyncCalls += 1
      if (resyncCalls === 1) {
        assert.match(request.path, /cursor=8&wait_seconds=12/)
        return { status: 200, body: { code: 0, message: 'success', data: { cursor: '2', resync_required: true, changes: [] } } }
      }
      return { status: 404, body: { code: 'NOT_FOUND', message: 'endpoint unavailable', data: {} } }
    },
  },
  readCredential: async () => ({ accessToken: token }),
})
await resyncSource.init({ id: resyncSourceId, type: 'server', name: '重同步', order: 0, url: 'http://127.0.0.1:3000', enabled: true, extra: { credentialRef: 'server-resync-credential', deviceId: 'device-resync' } })
const observedResync = await new Promise<{ resyncRequired: boolean }>((resolve) => {
  resyncSource.watchMediaChanges(resolve)
})
assert.equal(observedResync.resyncRequired, true)
await delay(40)
assert.equal(getAppSetting(resyncCursorKey), '2')
assert.equal(resyncCalls, 2)
resyncSource.destroy()

const staleCursorSourceId = 'server-stale-cursor'
const staleCursorKey = `ohmycine:server-media-change-cursor:${staleCursorSourceId}:${encodeURIComponent('http://127.0.0.1:3000')}`
await setAppSetting(staleCursorKey, '8')
let staleCursorCalls = 0
const staleCursorSource = new ServerDataSource({
  bridge: {
    async request() {
      staleCursorCalls += 1
      return { status: 200, body: { code: 0, message: 'success', data: { cursor: '7', resync_required: false, changes: [] } } }
    },
  },
  readCredential: async () => ({ accessToken: token }),
})
await staleCursorSource.init({ id: staleCursorSourceId, type: 'server', name: '游标回退', order: 0, url: 'http://127.0.0.1:3000', enabled: true, extra: { credentialRef: 'server-stale-cursor-credential', deviceId: 'device-stale-cursor' } })
const stopStaleCursorWatch = staleCursorSource.watchMediaChanges(() => assert.fail('stale cursor emitted a media change'))
await delay(350)
assert.equal(staleCursorCalls, 1)
assert.equal(getAppSetting(staleCursorKey), '8')
stopStaleCursorWatch()
staleCursorSource.destroy()

let revokedCalls = 0
const revokedSource = new ServerDataSource({
  bridge: {
    async request() {
      revokedCalls += 1
      return { status: 401, body: { code: 'NOT_AUTHENTICATED', message: 'device revoked', data: {} } }
    },
  },
  readCredential: async () => ({ accessToken: token }),
})
await revokedSource.init({ id: 'server-revoked', type: 'server', name: '凭据撤销', order: 0, url: 'http://127.0.0.1:3000', enabled: true, extra: { credentialRef: 'server-revoked-credential', deviceId: 'device-revoked' } })
const stopRevokedWatch = revokedSource.watchMediaChanges(() => assert.fail('revoked source emitted a media change'))
await delay(40)
assert.equal(revokedCalls, 1)
assert.equal(revokedSource.isConnected, false)
stopRevokedWatch()
revokedSource.destroy()

const logoutCalls: Array<{ path: string, accessToken?: string }> = []
await logoutServerBestEffort(source.exportConfig(), {
  async request(request) {
    logoutCalls.push({ path: request.path, accessToken: request.accessToken })
    throw new Error(`must stay hidden: ${request.accessToken}`)
  },
}, async () => ({ accessToken: token }))
assert.deepEqual(logoutCalls, [{ path: '/api/v1/player/auth/logout', accessToken: token }])

const merged = mergeMediaItemsByIdentity([
  {
    id: 'emby-1', sourceId: 'emby-home', originType: 'emby', name: 'Seven Samurai', type: 'movie', path: '',
    posterUrl: 'https://image.example.test/poster.jpg', workIdentity: { scheme: 'tmdb', mediaType: 'movie', value: '346' },
    playbackTargets: [{ sourceId: 'emby-home', itemId: 'emby-1', label: 'Emby' }],
  },
  items[0],
])
assert.equal(merged.length, 1)
assert.equal(merged[0].sourceId, 'server-home')
assert.equal(merged[0].posterUrl, 'https://image.example.test/poster.jpg')
assert.equal(merged[0].playbackTargets?.length, 2)
const mergedDetail = await source.getDetail(items[0].id)
assert.deepEqual(mergedDetail.mediaSources?.map(route => route.sourceId), ['server-home', 'emby-home'])

prunePlaybackTargets(new Set(['server-home']))
assert.deepEqual((await source.getDetail(items[0].id)).mediaSources?.map(route => route.sourceId), ['server-home'])
rememberPlaybackTargetsForItems([merged[0]])
assert.deepEqual((await source.getDetail(items[0].id)).mediaSources?.map(route => route.sourceId), ['server-home', 'emby-home'])
forgetPlaybackTargetsForSource('emby-home')
assert.deepEqual((await source.getDetail(items[0].id)).mediaSources?.map(route => route.sourceId), ['server-home'])
rememberPlaybackTargetsForItems([merged[0]])
assert.equal(mergeMediaItemsByIdentity([
  { id: 'one', sourceId: 'one', name: '同名', type: 'movie', path: '' },
  { id: 'two', sourceId: 'two', name: '同名', type: 'movie', path: '' },
]).length, 2)

assert.equal(await embyInstanceFingerprint('  SYSTEM-ID  '), await embyInstanceFingerprint('system-id'))
assert.notEqual(await embyInstanceFingerprint('system-id'), await embyInstanceFingerprint('other-system-id'))
assert.deepEqual(namesByPersonType([
  { Name: ' 黑泽明 ', Type: 'director' },
  { Name: '黑泽明', Type: 'DIRECTOR' },
  { Name: '三船敏郎', Type: 'Actor' },
], 'Director'), ['黑泽明'])

configureOhMyCineServerOrigins(['http://127.0.0.1:3000'])
assert.equal(
  extractTrustedOhMyCineArtifactIdentity('http://127.0.0.1:3000/proxy/strm/artifact_test_1234?exp=123&sig=secret'),
  'ohmycine:artifact:artifact_test_1234',
)
assert.equal(extractTrustedOhMyCineArtifactIdentity('https://attacker.example/proxy/strm/artifact_test_1234?sig=secret'), undefined)
assert.equal(extractTrustedOhMyCineArtifactIdentity('/proxy/strm/artifact_test_1234?sig=secret'), undefined)

let successfulCredential = ''
const successfulLogin = await loginServerAndCreateConfig({
  id: 'server-capability-login',
  url: 'http://127.0.0.1:3000',
  username: 'owner',
  password: 'temporary-password',
  deviceId: 'stable-device-id',
}, {
  async request(request) {
    if (request.path === '/api/v1/player/auth/login')
      return { status: 200, body: { code: 0, message: 'success', data: { access_token: token } } }
    if (request.path === '/api/v1/player/bootstrap')
      return { status: 200, body: { code: 0, message: 'success', data: { capabilities: ['media_catalog', 'discovery_search', 'acquisition_create'] } } }
    if (request.path === '/api/v1/player/media-libraries')
      return { status: 200, body: { code: 0, message: 'success', data: { list: [], total: 0 } } }
    return { status: 404, body: { code: 'NOT_FOUND', message: 'not found', data: {} } }
  },
}, async (_ref, value) => {
  successfulCredential = value.accessToken
})
assert.equal(successfulCredential, token)
assert.deepEqual(successfulLogin.config.extra?.capabilities, ['acquisition_create', 'discovery_search', 'media_catalog'])

const failedLoginCalls: Array<{ path: string, accessToken?: string, body?: unknown }> = []
await assert.rejects(loginServerAndCreateConfig({
  id: 'server-reconnect',
  url: 'http://127.0.0.1:3000',
  displayName: '家庭 Server',
  username: 'owner',
  password: 'temporary-password',
  deviceId: 'stable-device-id',
  deviceName: 'Test Player',
}, {
  async request(request) {
    failedLoginCalls.push({ path: request.path, accessToken: request.accessToken, body: request.body })
    if (request.path === '/api/v1/player/auth/login')
      return { status: 200, body: { code: 0, message: 'success', data: { access_token: token } } }
    if (request.path === '/api/v1/player/auth/logout')
      return { status: 200, body: { code: 0, message: 'success', data: {} } }
    return { status: 503, body: { code: 1, message: 'bootstrap failed', data: {} } }
  },
}), /bootstrap failed/)
assert.deepEqual(failedLoginCalls.find(call => call.path === '/api/v1/player/auth/login')?.body, {
  username: 'owner',
  password: 'temporary-password',
  device_id: 'stable-device-id',
  device_name: 'Test Player',
})
assert.equal(failedLoginCalls.find(call => call.path === '/api/v1/player/auth/logout')?.accessToken, token)
assert.equal(failedLoginCalls.filter(call => call.path === '/api/v1/player/auth/logout').length, 1)

const retainedCredentialRef = 'datasource:server-reconnect-retained:server-credential'
const retainedFailureCalls: Array<{ path: string, accessToken?: string }> = []
await assert.rejects(loginServerAndCreateConfig({
  id: 'server-reconnect-retained',
  url: 'http://127.0.0.1:3000',
  username: 'owner',
  password: 'temporary-password',
  deviceId: 'stable-device-id',
  retainTokenOnValidationFailure: true,
}, {
  async request(request) {
    retainedFailureCalls.push({ path: request.path, accessToken: request.accessToken })
    if (request.path === '/api/v1/player/auth/login')
      return { status: 200, body: { code: 0, message: 'success', data: { access_token: token } } }
    return { status: 503, body: { code: 1, message: 'bootstrap failed', data: {} } }
  },
}), /bootstrap failed/)
assert.equal(retainedFailureCalls.some(call => call.path === '/api/v1/player/auth/logout'), false)
assert.equal((await readServerCredential(retainedCredentialRef))?.accessToken, token)
await removeCredential(retainedCredentialRef).catch(() => undefined)

const failedCredentialWriteCalls: Array<{ path: string, accessToken?: string }> = []
await assert.rejects(loginServerAndCreateConfig({
  id: 'server-credential-write-failure',
  url: 'http://127.0.0.1:3000',
  username: 'owner',
  password: 'temporary-password',
  deviceId: 'stable-device-id',
  retainTokenOnValidationFailure: true,
}, {
  async request(request) {
    failedCredentialWriteCalls.push({ path: request.path, accessToken: request.accessToken })
    if (request.path === '/api/v1/player/auth/login')
      return { status: 200, body: { code: 0, message: 'success', data: { access_token: token } } }
    if (request.path === '/api/v1/player/auth/logout')
      return { status: 200, body: { code: 0, message: 'success', data: {} } }
    if (request.path === '/api/v1/player/bootstrap')
      return { status: 200, body: { code: 0, message: 'success', data: { capabilities: ['media_catalog'] } } }
    return { status: 200, body: { code: 0, message: 'success', data: { list: [], total: 0 } } }
  },
}, async () => {
  throw new Error(`credential accessToken=${token} write failed`)
}), (error: unknown) => {
  assert.ok(error instanceof Error)
  assert.match(error.message, /无法安全保存新的 Server 登录凭据，已撤销本次登录/)
  assert.match(error.message, /accessToken=\[redacted\]/)
  assert.doesNotMatch(error.message, new RegExp(token))
  return true
})
assert.equal(failedCredentialWriteCalls.filter(call => call.path === '/api/v1/player/auth/logout').length, 1)
assert.equal(failedCredentialWriteCalls.find(call => call.path === '/api/v1/player/auth/logout')?.accessToken, token)

const settingsSource = fs.readFileSync(new URL('../src/views/SettingsView.vue', import.meta.url), 'utf8')
assert.match(settingsSource, /deviceId:\s*existing\.type === 'server'.*existing\.extra\?\.deviceId/)
assert.match(settingsSource, /retainTokenOnValidationFailure:\s*sameServerOrigin/)
assert.match(settingsSource, /changedServerOrigin/)
assert.match(settingsSource, /result\.config\.type === 'server' && !changedServerOrigin[\s\S]*store\.reloadSource\(id\)/)
assert.match(settingsSource, /if \(result\.config\.type === 'server'\)\s*await logoutServerBestEffort\(result\.config\)\s*await restoreCredentialForConfig/)
const serverSource = fs.readFileSync(new URL('../src/services/datasource/server.ts', import.meta.url), 'utf8')
assert.match(serverSource, /credentialVersion:\s*Date\.now\(\)/)
assert.match(serverSource, /libraries = await source\.listLibraries\(\)[\s\S]*await persistServerCredentialOrRevoke/)
assert.match(serverSource, /mapServerHistoryItem[\s\S]*cardLayout:\s*type === 'episode' \? 'poster'/)
assert.match(serverSource, /purpose:\s*'favorites'[\s\S]*purpose:\s*'automaticCollections'[\s\S]*purpose:\s*'manualCollections'[\s\S]*purpose:\s*'libraries'/)
assert.doesNotMatch(serverSource, /title:\s*'最近历史'/)
const historySyncSource = fs.readFileSync(new URL('../src/services/playbackHistorySync.ts', import.meta.url), 'utf8')
assert.match(historySyncSource, /configsById\.get\(entry\.sourceId\)[\s\S]*sourceConfig\.type === 'server' && sourceConfig\.id !== target\.config\.id/)
assert.match(serverSource, /history\?page=\$\{page\}&page_size=\$\{pageSize\}&source_kind=server/)
assert.match(historySyncSource, /change\.source_kind === 'server'[\s\S]*\? currentServer/)
assert.match(historySyncSource, /mediaIdentity:\s*change\.deleted === true \? change\.media_identity : presentation\?\.historyIdentity/)
const sourceLibraryView = fs.readFileSync(new URL('../src/views/SourceLibraryView.vue', import.meta.url), 'utf8')
assert.match(sourceLibraryView, /supplementalHomeSections/)
assert.match(sourceLibraryView, /section\.viewAllRoute/)
const embySource = fs.readFileSync(new URL('../src/services/datasource/emby.ts', import.meta.url), 'utf8')
assert.match(embySource, /DETAIL_IMAGE_QUERY[\s\S]*ImageTypeLimit: '8'/)
assert.match(embySource, /fetchDetailPayload[\s\S]*getItem\(id, true\)/)
assert.match(embySource, /PrimaryImageTag/)
assert.match(embySource, /people:[\s\S]*this\.imageUrl\(person\.Id, 'Primary'/)
const detailViewSource = fs.readFileSync(new URL('../src/views/MediaDetailView.vue', import.meta.url), 'utf8')
assert.match(detailViewSource, /detail\.value\?\.originType !== 'server' \|\| selectedMediaSource\.value != null/)
assert.match(detailViewSource, /Server 媒体库中暂时没有可播放的分集/)
assert.match(detailViewSource, /visiblePeople/)

console.log(JSON.stringify({
  serverDataSource: true,
  bearerOnlyAtServerBoundary: true,
  serverPreferredIdentityMerge: true,
  uncertainTitlesNotMerged: true,
  embySystemIdFingerprint: true,
  workIdResolvesPlayableEntry: true,
  unplayableVersionsExcluded: true,
  catalogPagination: true,
  durableServerMediaChangePolling: true,
  stableServerDeviceId: true,
  deviceTokenRevocation: true,
  ownerScopedAcquisitionContract: true,
  expectedIdentitySubmission: true,
  perSiteResultPagination: true,
  acquisitionPlaceholderCategory: true,
  canonicalHistoryIdentity: true,
  seriesPosterHistoryCard: true,
  providerNeutralServerOverview: true,
  collectionMemberNavigation: true,
}, null, 2))

function mediaItem(index = 0) {
  return {
    id: index === 0 ? 'bW92aWU6dG1kYjozNDY' : `movie-${index}`, library_id: 9, title: index === 0 ? '七武士' : `电影 ${index}`, kind: 'movie', release_year: 1954,
    original_title: '七人の侍', overview: '日本电影', tagline: '他们站了起来。', rating: 8.5, runtime_minutes: 207,
    genres: ['剧情', '动作'], directors: ['黑泽明'], writers: ['桥本忍'], cast: ['三船敏郎', '志村乔'],
    people: [{ tmdb_id: 1, name: '黑泽明', role: 'Director' }, { tmdb_id: 2, name: '三船敏郎', role: 'Actor', character: '菊千代', profile_path: '/mifune.jpg' }], tmdb_id: 346, imdb_id: 'tt0047478',
    poster_path: '', backdrop_path: '/backdrop.jpg', still_paths: ['/backdrop.jpg', '/still-2.jpg'], work_identity: { scheme: 'tmdb', media_type: 'movie', value: '346' },
    file_count: 1, season_count: 0, episode_count: 0, modified_at: '2026-08-22T00:00:00Z', match_status: 'matched',
  }
}

function seriesItem() {
  return {
    ...mediaItem(),
    id: 'c2VyaWVzOnRtZGI6MTAw',
    title: '示例剧',
    original_title: 'Example Series',
    poster_path: '/series-poster.jpg',
    kind: 'series',
    work_identity: { scheme: 'tmdb', media_type: 'series', value: '100' },
    tmdb_id: 100,
    imdb_id: 'tt0000100',
    file_count: 3,
    season_count: 1,
    episode_count: 3,
  }
}

function legacyArtworkItem() {
  return {
    ...mediaItem(),
    id: 'bW92aWU6dG1kYjozNDYtbGVnYWN5',
    still_paths: undefined,
  }
}

function noPlayableItem() {
  return {
    ...mediaItem(),
    id: 'movie-no-playable',
    title: '无可播放版本',
  }
}

function delay(milliseconds: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, milliseconds))
}
