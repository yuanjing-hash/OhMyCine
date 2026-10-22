import assert from 'node:assert/strict'
import { ServerDataSource } from '../src/services/datasource/server.ts'
import { getServerRouteRecommendation } from '../src/services/serverDiscovery.ts'
import { parseServerSharePreview, previewServerShare, toggleServerShareEntry, validateServerShareSelection } from '../src/services/serverSharePreview.ts'

const token = `omc_player_${'a'.repeat(43)}`
const fixture = {
  token: 'preview-opaque', expires_at: new Date(Date.now() + 60_000).toISOString(), total_size: 110, file_count: 2,
  entries: [
    { token: 'directory', path: '/剧集', name: '剧集', is_dir: true, size: 0 },
    { token: 'video', path: '/剧集/第一集.mkv', name: '第一集.mkv', is_dir: false, size: 100 },
    { token: 'subtitle', path: '/剧集/第一集.ass', name: '第一集.ass', is_dir: false, size: 10 },
  ],
}
const parsed = parseServerSharePreview(fixture)
assert.deepEqual(toggleServerShareEntry(parsed, [], parsed.entries[0]), ['video', 'subtitle'])
assert.deepEqual(toggleServerShareEntry(parsed, ['video', 'subtitle'], parsed.entries[0]), [])
assert.deepEqual(validateServerShareSelection(parsed, ['subtitle']), { previewToken: 'preview-opaque', entryTokens: ['subtitle'] })
assert.equal(validateServerShareSelection(parsed, ['directory']), null)
assert.equal(validateServerShareSelection(parsed, ['not-in-preview']), null)
assert.equal(validateServerShareSelection(parsed, ['video', 'video']), null)
assert.equal(validateServerShareSelection(parsed, ['video'], Date.now() + 120_000), null)
const large = { ...parsed, entries: Array.from({ length: 501 }, (_, index) => ({ token: `file-${index}`, path: `/f-${index}.mkv`, name: `f-${index}.mkv`, isDirectory: false, size: 1 })), fileCount: 501, totalSize: 501 }
assert.equal(validateServerShareSelection(large, large.entries.map(entry => entry.token)), null)
assert.equal(validateServerShareSelection(large, large.entries.slice(0, 500).map(entry => entry.token))?.entryTokens.length, 500)
assert.throws(() => parseServerSharePreview({ ...fixture, file_count: 3 }), /分享目录无效/)
assert.throws(() => parseServerSharePreview({ ...fixture, entries: [fixture.entries[1], fixture.entries[1]] }), /分享目录无效/)

const calls: Array<{ path: string, body?: unknown }> = []
let delayPreview = false
let finishPreview!: (value: { status: number, body: unknown }) => void
const source = new ServerDataSource({
  readCredential: async () => ({ accessToken: token }),
  bridge: { async request(request) {
    calls.push({ path: request.path, body: request.body })
    if (request.path.endsWith('/share-preview')) {
      if (delayPreview)
        return new Promise(resolve => { finishPreview = resolve })
      return { status: 200, body: { code: 0, data: fixture } }
    }
    const data = request.path.endsWith('/routes/recommend')
      ? { source_kind: 'quark_share', recommended: { downloader_id: 'quark-receiver', media_library_id: 2 }, choices: [{ downloader_id: 'quark-receiver', downloader_name: '夸克转存', downloader_type: 'quark_share', media_library_id: 2, library_name: '夸克剧集', enabled: true }] }
      : {}
    return { status: 200, body: { code: 0, data } }
  } },
})
await source.init({ id: 'quark-via-server', name: 'Server', type: 'server', order: 0, enabled: true, url: 'http://127.0.0.1:3000', extra: { credentialRef: 'test-only' } })
const route = await getServerRouteRecommendation(source, 'result-opaque')
assert.equal(route.sourceKind, 'quark_share')
const preview = await previewServerShare(source, 'result-opaque', 'quark-receiver')
assert.deepEqual(preview.entries, parsed.entries)
await source.createDiscoveryDownload({ result_token: 'result-opaque', downloader_id: 'quark-receiver', media_library_id: 2, priority: 0, preview_token: preview.token, selected_entry_tokens: ['video'] })
assert.deepEqual(calls.find(item => item.path.endsWith('/downloads'))?.body, { result_token: 'result-opaque', downloader_id: 'quark-receiver', media_library_id: 2, priority: 0, preview_token: 'preview-opaque', selected_entry_tokens: ['video'] })
assert.equal(JSON.stringify(calls).includes('Cookie'), false)
delayPreview = true
const pending = previewServerShare(source, 'result-opaque', 'quark-receiver')
while (!finishPreview) await Promise.resolve()
source.destroy()
finishPreview({ status: 200, body: { code: 0, data: fixture } })
await assert.rejects(pending, /连接已变化/)
const before = calls.length
await assert.rejects(previewServerShare(source, 'result-opaque', 'quark-receiver'), /连接已变化/)
assert.equal(calls.length, before)
console.log('Server share preview: Quark routing, partial selection, expiration, secret boundary and retired source checks passed.')
