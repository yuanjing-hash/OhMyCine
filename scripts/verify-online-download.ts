import type { OfflineItemSummary } from '../src/services/downloads'
import assert from 'node:assert/strict'
import { ServerDataSource } from '../src/services/datasource/server'
import { createOnlineVersionID, createOnlineWorkID, parseOnlineLibraryList } from '../src/services/datasource/serverOnline'
import { planMediaDownload } from '../src/services/downloadPlanning'
import { deriveOfflineBadge } from '../src/stores/downloads'

let allowed = true
let mismatched = false
const calls: Array<{ path: string, body?: unknown }> = []
const source = new ServerDataSource({
  readCredential: async () => ({ accessToken: `omc_player_${'b'.repeat(43)}` }),
  bridge: { async request(request) {
    calls.push({ path: request.path, body: request.body })
    let data: unknown = {}
    if (request.path === '/api/v1/player/bootstrap')
      data = { capabilities: ['online_offline_plan_v1'] }
    else if (request.path === '/api/v1/player/online-libraries')
      data = { list: [{ id: 'library', pluginId: 'org.ohmycine.fixture', connectionId: 'connection', name: 'Fixture', available: true, offlineDownloadSupported: allowed }] }
    else if (request.path.endsWith('/playback')) {
      const body = request.body as { segmentId: string, versionId: string, variantId?: string }
      data = {
        workId: 'work', segmentId: mismatched ? 'wrong-episode' : body.segmentId, versionId: body.versionId,
        variantId: body.variantId ?? 'hd', delivery: 'server-gateway',
        variants: [{ id: 'hd', label: 'HD', available: true }, { id: 'preview', label: 'Preview', available: false }],
        assets: [{ kind: 'hls', urlRef: '/api/v1/player/online-assets/fixture' }],
      }
    }
    else if (request.path.includes('/items/work')) {
      data = {
        id: 'work', title: 'Series', kind: 'series', identity: { scheme: 'fixture', value: 'work' },
        segments: [1, 2].map(index => ({ id: `segment-${index}`, title: `Episode ${index}`, index, seasonNumber: 1, episodeNumber: index,
          versions: [{ id: `version-${index}`, label: 'Primary', variants: [] }, { id: `alternative-${index}`, label: 'Alternative', variants: [] }] })),
      }
    }
    return { status: 200, body: { code: 0, message: 'success', data } }
  } },
})

await source.init({ id: 'source', type: 'server', name: 'Server', order: 0, url: 'https://server.example', extra: { credentialRef: 'credential', deviceId: 'device' } })
const workId = createOnlineWorkID('library', 'work')
const itemId = createOnlineVersionID('library', 'work', 'segment-2', 'version-2')
await source.listLibraries()
assert.equal(source.canDownloadItem(itemId), false, 'old Server capabilities fail closed')
await source.refreshCapabilities()
assert.equal(source.canDownloadItem(itemId), true)
const detail = await source.getDetail(itemId)
assert.equal(detail.id, itemId)
assert.equal(detail.type, 'episode')
assert.equal(detail.episodeNumber, 2)
assert.equal(detail.children, undefined, 'exact episode does not recurse into the complete work')
assert.deepEqual(detail.mediaSources?.map(item => item.id), ['version-2'])

const selected = await source.resolveDownloadSelection({ itemId, mediaSourceId: 'version-2' })
assert.deepEqual(selected, { itemId, mediaSourceId: 'version-2', variantId: 'hd', onlineIdentity: { libraryId: 'library', workId: 'work', segmentId: 'segment-2', versionId: 'version-2' } })
assert.equal(JSON.stringify(selected).includes('http'), false)
assert.equal(JSON.stringify(selected).includes('omc_player_'), false)
assert.deepEqual(calls.findLast(call => call.path.endsWith('/playback'))?.body, { segmentId: 'segment-2', versionId: 'version-2' })
await assert.rejects(source.resolveDownloadSelection({ itemId, mediaSourceId: 'version-2', variantId: 'preview' }), /没有完整播放或下载权益/)
await assert.rejects(source.resolveDownloadSelection({ itemId, mediaSourceId: 'alternative-2' }), /所选节目版本已不可用/)
const planned = await planMediaDownload(source, { kind: 'media', sourceId: 'source', sourceType: 'server', itemId: workId, mediaType: 'series', display: { name: 'Series' } })
assert.equal(planned.aggregate, true)
assert.deepEqual(planned.files.map(file => [file.onlineIdentity?.segmentId, file.mediaSourceId, file.variantId]), [['segment-1', 'version-1', 'hd'], ['segment-2', 'version-2', 'hd']], 'bulk downloads choose one version per episode')
assert.equal(planned.files.every(file => file.detailSnapshot.mediaType === 'episode'), true)
assert.equal(JSON.stringify(planned).includes('omc_player_'), false)
assert.equal(JSON.stringify(planned).includes('online-assets'), false)
mismatched = true
await assert.rejects(source.resolveDownloadSelection({ itemId }), /播放身份与所选节目或清晰度不一致/)
mismatched = false
allowed = false
await assert.rejects(source.resolveDownloadSelection({ itemId }), /不支持本地下载/)
assert.equal(source.canDownloadItem(itemId), false, 'fresh revocation disables cached capability')
assert.equal(parseOnlineLibraryList({ list: [{ id: 'old', pluginId: 'plugin', connectionId: 'connection', name: 'old' }] })[0]?.offlineDownloadSupported, false)
const movieWork = { sourceId: 'source', id: createOnlineWorkID('library|汉字', 'movie%work'), name: 'Movie', type: 'movie' as const }
const completedMovie: OfflineItemSummary = {
  id: 'completed', sourceId: 'source', itemId: createOnlineVersionID('library|汉字', 'movie%work', 'main', 'version'), mediaSourceId: 'version',
  displayName: 'Movie', mediaType: 'movie', videoBytes: 1024, completedAt: 1, attachmentState: 'complete',
}
assert.equal(deriveOfflineBadge(movieWork, [completedMovie])?.state, 'complete', 'movie work cards project the completed exact work identity')
for (const changed of [
  { sourceId: 'other-source' },
  { itemId: createOnlineVersionID('other-library', 'movie%work', 'main', 'version') },
  { itemId: createOnlineVersionID('library|汉字', 'movie%work-extra', 'main', 'version') },
  { mediaType: 'episode' },
  { mediaSourceId: 'different-version' },
])
  assert.equal(deriveOfflineBadge(movieWork, [{ ...completedMovie, ...changed }]), null, 'movie badges cannot cross source/library/work/type/version')
assert.equal(deriveOfflineBadge({ ...movieWork, id: createOnlineVersionID('library|汉字', 'movie%work', 'main', 'other-version') }, [completedMovie]), null, 'explicit version cards never use the work fallback')
assert.equal(deriveOfflineBadge({ ...movieWork, type: 'series' }, [{ ...completedMovie, mediaType: 'episode', seriesName: 'Movie' }]), null, 'online series cannot alias a matching title without exact children')
console.log('online local download capabilities, lazy entitlement, stable identities and episode planning verified')
