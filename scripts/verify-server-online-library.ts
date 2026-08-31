import assert from 'node:assert/strict'
import { ServerDataSource } from '../src/services/datasource/server.ts'

const token = `omc_player_${'b'.repeat(43)}`
const calls: Array<{ path: string, method?: string, body?: unknown }> = []
const source = new ServerDataSource({
  readCredential: async () => ({ accessToken: token }),
  bridge: {
    async request(request) {
      calls.push({ path: request.path, method: request.method, body: request.body })
      let data: unknown = {}
      if (request.path === '/api/v1/player/media-libraries')
        data = { list: [] }
      else if (request.path === '/api/v1/player/home-contributions')
        data = { list: [{
          id: 'library-1:recommended', libraryId: 'library-1', pluginId: 'org.ohmycine.fixture', providerLabel: 'Fixture', routeKey: 'recommended', title: '在线视频推荐', layout: 'hero', refreshable: true,
          sections: [{ id: 'hero', title: '在线视频推荐', layout: 'hero', homeEligible: true, refreshable: true, refreshSession: 'session-1', items: [{ work: onlineWork(), actions: [{ id: 'favorite.add', label: '收藏', state: false }, { id: 'watch-later.remove', label: '移出稍后再看', state: true }] }] }],
        }] }
      else if (request.path === '/api/v1/player/online-libraries')
        data = { list: [
          { id: 'library-1', pluginId: 'org.ohmycine.fixture', connectionId: 'connection-1', name: '在线视频', providerLabel: 'Fixture', capabilities: ['site.feed', 'site.search', 'site.detail', 'media.playback'], available: true, homeContributions: ['recommended'], artworkUrl: '/api/v1/assets/plugin-covers/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', artworkRevision: 'fixed-plugin-v1', artworkSource: 'custom' },
          { id: 'library-unsafe', pluginId: 'org.ohmycine.fixture', connectionId: 'connection-unsafe', name: '不安全在线封面', providerLabel: 'Fixture', capabilities: ['site.feed'], available: true, homeContributions: [], artworkUrl: 'https://attacker.example/plugin-cover.png' },
        ] }
      else if (request.path.endsWith('/navigation'))
        data = { version: 2, mode: 'hierarchical', nodes: [{ id: 'recommended', title: '推荐', kind: 'feed', routeKey: 'recommended', refreshable: true, artworkUrl: '/api/v1/assets/generated-library-covers/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa?exp=1787565600&sig=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', artworkRevision: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', artworkSource: 'generated' }, { id: 'anime', title: '番剧', kind: 'branch', nodeToken: 'signed-anime-node', hasChildren: true, artworkUrl: '/api/v1/assets/generated-library-covers/cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc?exp=1787565600&sig=ddddddddddddddddddddddddddddddddddddddddddd', artworkRevision: 'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc', artworkSource: 'generated' }] }
      else if (request.path.endsWith('/navigation/signed-anime-node/children'))
        data = { version: 2, mode: 'hierarchical', nodes: [{ id: 'anime-jp', title: '日本番剧', kind: 'feed', routeKey: 'recommended', refreshable: true }] }
      else if (request.path.includes('/feeds/recommended'))
        data = { sections: [{ id: 'hero', title: '在线视频推荐', layout: 'hero', homeEligible: true, refreshable: true, refreshSession: 'session-1', items: [{ work: onlineWork(), actions: [{ id: 'favorite.add', label: '收藏', state: false }, { id: 'watch-later.remove', label: '移出稍后再看', state: true }, { id: 'provider-private-action', label: '越权动作' }] }] }] }
      else if (request.path.includes('/search?'))
        data = { sections: [{ id: 'search', title: '搜索结果', layout: 'video-list', items: [{ work: onlineWork() }] }] }
      else if (request.path.startsWith('/api/v1/player/online-history?'))
        data = { list: [{ libraryId: 'library-1', work: onlineWork(), segmentId: 'part-1', versionId: 'source-1', positionSeconds: 120, durationSeconds: 600, updatedAt: '2026-08-23T00:00:00Z' }], cursor: 'next-page', hasMore: true }
      else if (request.path.includes('/progress'))
        data = { accepted: true, remote: true }
      else if (request.path.includes('/items/video-1/playback'))
        data = {
          workId: 'video-1', segmentId: 'part-1', versionId: 'source-1', variantId: '1080p', delivery: 'server-gateway',
          variants: [{ id: '720p', label: '720P', available: true, width: 1280, height: 720 }, { id: '1080p', label: '1080P', available: true, width: 1920, height: 1080 }],
          assets: [
            { kind: 'dash-video', urlRef: '/api/v1/player/online-assets/asset-1' },
            { kind: 'dash-audio', urlRef: '/api/v1/player/online-assets/audio-1' },
          ],
          subtitles: [{ id: 'zh', label: '中文', language: 'zh-CN', format: 'vtt', urlRef: '/api/v1/player/online-assets/subtitle-1' }],
          danmaku: [{ id: 'native', label: '来源弹幕', format: 'ohmycine-danmaku-v1+json', urlRef: '/api/v1/player/online-assets/danmaku-1' }],
        }
      else if (request.path.includes('/items/video-1'))
        data = onlineWork()
      if (request.path === '/api/v1/player/online-assets/danmaku-1')
        return { status: 200, body: { comments: [{ id: 'comment-1', time: 12.5, mode: 'scroll', color: '#ffffff', text: '来源弹幕' }] } }
      return { status: 200, body: { code: 0, message: 'success', data } }
    },
  },
})

await source.init({
  id: 'server-online', type: 'server', name: 'Server', order: 0, url: 'http://127.0.0.1:3000', enabled: true,
  extra: { credentialRef: 'datasource:server-online:server-credential', deviceId: 'device-1' },
})

const libraries = await source.listLibraries()
assert.deepEqual(libraries.map(item => [item.id, item.name, item.providerIdentity]), [
  ['online-library|library-1', '在线视频', 'plugin:org.ohmycine.fixture:library-1'],
  ['online-library|library-unsafe', '不安全在线封面', 'plugin:org.ohmycine.fixture:library-unsafe'],
])
assert.equal(libraries[0].backdropUrl, 'http://127.0.0.1:3000/api/v1/assets/plugin-covers/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')
assert.equal(libraries[0].artworkRevision, 'fixed-plugin-v1')
assert.equal(libraries[0].artworkSource, 'custom')
assert.equal(libraries[0].artworkCandidates, undefined)
assert.equal(libraries[1].backdropUrl, undefined)
const navigation = await source.list(libraries[0].id)
assert.deepEqual(navigation.map(item => [item.type, item.name]), [['folder', '推荐'], ['folder', '番剧']])
assert.match(navigation[0].backdropUrl ?? '', /generated-library-covers/)
assert.equal(navigation[0].artworkSource, 'generated')
const nested = await source.list(navigation[1].id)
assert.deepEqual(nested.map(item => [item.type, item.name]), [['folder', '日本番剧']])
assert.equal(calls.some(call => call.path.endsWith('/navigation/signed-anime-node/children')), true)
const feed = await source.list(nested[0].id)
assert.deepEqual(feed.map(item => [item.name, item.workIdentity?.scheme]), [['演示视频', 'plugin']])
assert.deepEqual(feed[0].siteActions, [
  { id: 'favorite.add', label: '收藏', state: false, requiresConfirmation: false, destructive: false },
  { id: 'watch-later.remove', label: '移出稍后再看', state: true, requiresConfirmation: false, destructive: false },
])
const detail = await source.getDetail(feed[0].id)
assert.equal(detail.children?.[0]?.name, '第一部分')
assert.deepEqual(detail.mediaSources?.map(item => item.name), ['默认线路'])
const stream = await source.getStreamRequest({ itemId: detail.children![0].id, variantId: '1080p' })
assert.equal(stream.url, 'http://127.0.0.1:3000/api/v1/player/online-assets/asset-1')
assert.equal(stream.audioUrl, 'http://127.0.0.1:3000/api/v1/player/online-assets/audio-1')
assert.equal(stream.headers?.Authorization, `Bearer ${token}`)
assert.equal(stream.audioHeaders?.Authorization, `Bearer ${token}`)
assert.equal(stream.variantId, '1080p')
assert.deepEqual(stream.variants?.map(item => item.id), ['720p', '1080p'])
assert.equal(stream.subtitles?.[0]?.url, 'http://127.0.0.1:3000/api/v1/player/online-assets/subtitle-1')
assert.equal(stream.danmaku?.[0]?.url, 'http://127.0.0.1:3000/api/v1/player/online-assets/danmaku-1')
assert.deepEqual(await source.getDanmakuComments(stream.danmaku![0]!), [{ id: 'comment-1', time: 12.5, mode: 'scroll', color: '#ffffff', text: '来源弹幕' }])
assert.deepEqual(calls.find(call => call.path.includes('/playback'))?.body, { segmentId: 'part-1', versionId: 'source-1', variantId: '1080p' })
const home = await source.getHomeSections()
assert.equal(home.find(section => section.title === '在线视频推荐')?.items[0]?.name, '演示视频')
const onlineHome = home.find(section => section.title === '在线视频推荐')!
assert.equal(onlineHome.sourceLabel, 'Fixture')
assert.equal(onlineHome.refreshable, true)
assert.deepEqual(onlineHome.items[0]?.siteActions, [
  { id: 'favorite.add', label: '收藏', state: false, requiresConfirmation: false, destructive: false },
  { id: 'watch-later.remove', label: '移出稍后再看', state: true, requiresConfirmation: false, destructive: false },
])
await source.refreshHomeSection!(onlineHome.refreshKey!)
assert.equal(calls.some(call => call.path.endsWith('/feeds/recommended/refresh') && call.method === 'POST'), true)
await source.performSiteAction!(feed[0].id, 'favorite.add', true, true)
assert.equal(calls.some(call => call.path.endsWith('/actions/favorite.add') && call.method === 'POST' && (call.body as { confirmed?: boolean }).confirmed === true), true)
await source.enqueueOnlineDownload!({ itemId: detail.children![0].id })
assert.equal(calls.some(call => call.path.endsWith('/download') && call.method === 'POST'), true)
assert.equal((await source.search('演示')).some(item => item.name === '演示视频'), true)
const history = await source.listPlaybackHistory({ limit: 24, libraryId: libraries[0].id })
assert.equal(history.items[0]?.resumePosition, 120)
assert.equal(history.cursor, 'next-page')
assert.equal(history.hasMore, true)
assert.equal(calls.some(call => call.path.includes('library_id=library-1')), true)
await source.syncPlaybackProgress({ itemId: detail.children![0].id, event: 'progress', position: 180, duration: 600, isPaused: false, completed: false })
assert.equal(calls.some(call => call.path.includes('/progress') && (call.body as { positionSeconds?: number }).positionSeconds === 180), true)
const historyItem = history.items[0]!
const historyStream = await source.getStreamRequest({ itemId: historyItem.id })
await source.syncPlaybackProgress({ itemId: historyItem.id, mediaSourceId: historyStream.mediaSourceId, event: 'paused', position: 240, duration: 600, isPaused: true, completed: false })
assert.equal(calls.some(call => call.path.includes('/progress') && (call.body as { event?: string }).event === 'paused' && (call.body as { segmentId?: string }).segmentId === 'part-1'), true)

await assert.rejects(async () => {
  const unsafe = new ServerDataSource({
    readCredential: async () => ({ accessToken: token }),
    bridge: {
      async request(request) {
        if (request.path.includes('/playback')) {
          return { status: 200, body: { code: 0, message: 'success', data: {
            workId: 'video-1', segmentId: 'part-1', versionId: 'source-1', variantId: '1080p', delivery: 'direct', variants: [],
            assets: [{ kind: 'progressive', urlRef: 'https://attacker.example/video.mp4' }],
          } } }
        }
        return { status: 200, body: { code: 0, message: 'success', data: onlineWork() } }
      },
    },
  })
  await unsafe.init({ id: 'unsafe', type: 'server', name: 'Unsafe', order: 0, url: 'http://127.0.0.1:3000', extra: { credentialRef: 'credential' } })
  await unsafe.getStreamRequest({ itemId: 'online-version|library-1|video-1|part-1|source-1' })
}, /安全网关/)

console.log(JSON.stringify({ onlineLibrary: true, nestedPluginNavigation: true, genericPluginDTO: true, qualityVariants: true, dashAudio: true, sameOriginGateway: true, homeRefresh: true, siteActions: true, serverDownload: true, providerHistory: true, providerProgressSync: true, providerDanmaku: true }, null, 2))

function onlineWork() {
  return {
    id: 'video-1', title: '演示视频', kind: 'video', identity: { scheme: 'fixture', value: 'video-1' },
    overview: '通用在线媒体 DTO', posterUrl: 'https://images.example.test/poster.jpg', backdropUrl: 'https://images.example.test/backdrop.jpg',
    segments: [{ id: 'part-1', title: '第一部分', index: 1, versions: [{
      id: 'source-1', label: '默认线路', sourceLabel: 'Fixture', variants: [
        { id: '720p', label: '720P', available: true, width: 1280, height: 720 },
        { id: '1080p', label: '1080P', available: true, width: 1920, height: 1080 },
      ],
    }] }],
  }
}
