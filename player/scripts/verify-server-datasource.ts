import assert from 'node:assert/strict'
import fs from 'node:fs'
import { readServerCredential, removeCredential } from '../src/services/datasource/credentialStore.ts'
import { configureOhMyCineServerOrigins, embyInstanceFingerprint, extractTrustedOhMyCineArtifactIdentity, namesByPersonType } from '../src/services/datasource/emby.ts'
import { forgetPlaybackTargetsForSource, mergeMediaItemsByIdentity, prunePlaybackTargets, rememberPlaybackTargetsForItems } from '../src/services/datasource/identityMerge.ts'
import { describeMediaSource } from '../src/services/datasource/mediaSourceDisplay.ts'
import { loginServerAndCreateConfig, logoutServerBestEffort, ServerDataSource } from '../src/services/datasource/server.ts'

const token = `omc_player_${'a'.repeat(43)}`
const calls: Array<{ path: string, accessToken?: string, method?: string, body?: unknown }> = []
const bridge = {
  async request(request: { path: string, accessToken?: string, method?: string, body?: unknown }) {
    calls.push({ path: request.path, accessToken: request.accessToken, method: request.method, body: request.body })
    const page = Number(new URL(`http://player.test${request.path}`).searchParams.get('page') ?? '1')
    const data = request.path === '/api/v1/player/bootstrap'
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
                  { id: 201, title: '第一集', season: 1, episode: 1, size: 2048, modified_at: '2026-08-22T00:00:00Z', playable: true, stream_path: '/api/v1/player/media-entries/201/stream', delivery_kind: 'server_stream', exact_identity: 'server:entry:201' },
                  { id: 202, title: '第二集', season: 1, episode: 2, size: 2048, modified_at: '2026-08-22T00:00:00Z', playable: true, stream_path: '/api/v1/player/media-entries/202/stream', delivery_kind: 'server_stream', exact_identity: 'server:entry:202' },
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
const categories = await source.list('9')
assert.deepEqual(categories.map(item => [item.type, item.name]), [['folder', '外语电影']])
assert.equal(categories[0].posterUrl, 'http://127.0.0.1:3000/api/v1/assets/generated-library-covers/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa?exp=1787565600&sig=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb')
assert.equal(categories[0].artworkRevision, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')
assert.equal(categories[0].artworkSource, 'generated')
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
assert.deepEqual(seriesDetail.children?.map(item => [item.type, item.seasonNumber, item.episodeNumber]), [['episode', 1, 1], ['episode', 1, 2]])
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

const changeCalls: string[] = []
const changeSource = new ServerDataSource({
  bridge: {
    async request(request) {
      changeCalls.push(request.path)
      return { status: 200, body: { code: 0, message: 'success', data: { cursor: '8', resync_required: false, changes: [{ library_id: 9, revision: 3, kind: 'catalog' }] } } }
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
assert.deepEqual(observedChange, { sourceId: 'server-change', libraryIds: ['9'], resyncRequired: false })
assert.match(changeCalls[0] ?? '', /\/api\/v1\/player\/media-changes\?cursor=0&wait_seconds=12/)
changeSource.destroy()

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
    kind: 'series',
    work_identity: { scheme: 'tmdb', media_type: 'series', value: '100' },
    tmdb_id: 100,
    imdb_id: 'tt0000100',
    file_count: 2,
    season_count: 1,
    episode_count: 2,
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
