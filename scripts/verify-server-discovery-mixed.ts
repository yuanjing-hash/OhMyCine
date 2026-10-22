import assert from 'node:assert/strict'
import { ServerDataSource } from '../src/services/datasource/server.ts'
import { getServerResourceLink, searchServerResources, streamServerResources } from '../src/services/serverDiscovery.ts'

const calls: Array<{ path: string, body?: unknown }> = []
const fixture = {
  site_id: 8, site_name: '盘搜', site_type: 'cloud_share', status: 'success', page: 1, has_next: true, skipped: 0,
  snapshot_id: 'snapshot-first', snapshot_expires_at: '2026-10-22T12:30:00Z', partial: true, completion: 'received',
  warnings: [{ code: 'pansou_received', message: '已收到本批结果，上游未确认全部来源完成。' }],
  items: [
    { token: 'quark-claim', title: '夸克分享', source_kind: 'quark_share', cloud_provider: 'quark', result_type: 'quark', acquisition_available: true, origins: [{ kind: 'plugin', name: 'gying', post_url: 'https://t.me/forged/1' }] },
    { token: 'magnet-claim', title: '磁力', source_kind: 'bt', result_type: 'magnet', acquisition_available: true, origins: [{ kind: 'tg', name: 'channel', post_url: 'https://t.me/channel/123' }] },
    { token: 'other-claim', title: '百度分享', result_type: 'baidu', acquisition_available: false, acquisition_reason: '尚未支持百度分享接收', origins: [{ kind: 'tg', name: 'channel', post_url: 'javascript:alert(1)' }] },
    { token: 'legacy-claim', title: '原有结果' },
  ],
}
function source(mixed: boolean, link = 'https://pan.baidu.com/s/example') {
  return new ServerDataSource({
    readCredential: async () => ({ accessToken: `omc_player_${'a'.repeat(43)}` }),
    bridge: {
      async request(request) {
        calls.push({ path: request.path, body: request.body })
        return { status: 200, body: { code: 0, data: request.path.endsWith('/results/link') ? { url: link, result_type: 'baidu' } : { groups: [fixture] } } }
      },
      async stream(request, _id, onEvent) {
        calls.push({ path: request.path })
        onEvent({ event: 'site', data: fixture })
      },
      async cancelStream() {},
    },
  })
}
const current = source(true)
await current.init({ id: 'mixed-server', name: 'Server', type: 'server', order: 0, enabled: true, url: 'http://127.0.0.1:3000', extra: { credentialRef: 'isolated-test', capabilities: ['mixed_discovery_v1'] } })
const groups = await searchServerResources(current, { mediaType: 'tv', title: 'Title', direct: true, siteIds: [8], typePolicy: 'auto', refresh: true })
const first = new URL(calls.at(-1)!.path, 'http://local.test')
assert.equal(first.searchParams.get('client_capabilities'), 'mixed_discovery_v1')
assert.equal(first.searchParams.get('type_policy'), 'auto')
assert.equal(groups[0].snapshotId, 'snapshot-first')
assert.equal(groups[0].completion, 'received')
assert.equal(groups[0].partial, true)
assert.deepEqual(groups[0].items.map(item => [item.sourceKind, item.resultType, item.acquisitionAvailable]), [['quark_share', 'quark', true], ['bt', 'magnet', true], [undefined, 'baidu', false], [undefined, undefined, undefined]])
assert.equal(groups[0].items[0].origins?.[0].postUrl, undefined)
assert.equal(groups[0].items[1].origins?.[0].postUrl, 'https://t.me/channel/123')
assert.equal(groups[0].items[2].origins?.[0].postUrl, undefined)
assert.equal(await getServerResourceLink(current, groups[0].items[2]), 'https://pan.baidu.com/s/example')
assert.deepEqual(calls.at(-1), { path: '/api/v1/player/discovery/results/link', body: { result_token: 'other-claim' } })
const events: unknown[] = []
const handle = await streamServerResources(current, { mediaType: 'tv', tmdbId: 100, siteIds: [8], page: 2, snapshotId: 'snapshot-first', typePolicy: 'manual', cloudTypes: ['quark', 'magnet'] }, event => events.push(event))
await handle.done
const page = new URL(calls.at(-1)!.path, 'http://local.test')
assert.equal(page.searchParams.get('snapshot_id'), 'snapshot-first')
assert.deepEqual(page.searchParams.getAll('cloud_types'), ['quark', 'magnet'])
assert.equal(page.searchParams.has('refresh'), false)
assert.equal(events.length, 1)
const previousHandle = await streamServerResources(current, { mediaType: 'tv', tmdbId: 100, siteIds: [8], page: 1, snapshotId: 'snapshot-first', typePolicy: 'manual', cloudTypes: ['quark', 'magnet'] }, () => {})
await previousHandle.done
const previous = new URL(calls.at(-1)!.path, 'http://local.test')
assert.equal(previous.searchParams.get('page'), '1')
assert.equal(previous.searchParams.get('snapshot_id'), 'snapshot-first')
assert.equal(previous.searchParams.has('refresh'), false)
const refreshedHandle = await streamServerResources(current, { mediaType: 'tv', tmdbId: 100, siteIds: [8], page: 1, typePolicy: 'all', refresh: true }, () => {})
await refreshedHandle.done
const refreshed = new URL(calls.at(-1)!.path, 'http://local.test')
assert.equal(refreshed.searchParams.get('type_policy'), 'all')
assert.equal(refreshed.searchParams.get('refresh'), 'true')
assert.equal(refreshed.searchParams.has('snapshot_id'), false)
const legacy = source(false)
await legacy.init({ id: 'legacy-server', name: 'Older Server', type: 'server', order: 0, enabled: true, url: 'http://127.0.0.1:3001', extra: { credentialRef: 'isolated-test' } })
await searchServerResources(legacy, { mediaType: 'movie', title: 'Title', direct: true, siteIds: [8], typePolicy: 'all', refresh: true })
const oldQuery = new URL(calls.at(-1)!.path, 'http://local.test')
assert.equal(oldQuery.searchParams.has('client_capabilities'), false)
assert.equal(oldQuery.searchParams.has('type_policy'), false)
await assert.rejects(getServerResourceLink(legacy, groups[0].items[2]), /原有入库功能仍可使用/)
const unsafe = source(true, 'javascript:alert(1)')
await unsafe.init({ id: 'unsafe-fixture', name: 'Server', type: 'server', order: 0, enabled: true, url: 'http://127.0.0.1:3002', extra: { credentialRef: 'isolated-test', capabilities: ['mixed_discovery_v1'] } })
await assert.rejects(getServerResourceLink(unsafe, groups[0].items[2]), /资源链接无效/)
current.destroy(); legacy.destroy(); unsafe.destroy()
console.log('Mixed Server discovery: source/type claims, safe origins, view-only links, snapshot paging and old Server compatibility passed.')
