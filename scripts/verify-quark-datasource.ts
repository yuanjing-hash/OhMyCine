import type { QuarkBridge } from '../src/services/datasource/quark.ts'
import type { DataSourceConfig } from '../src/services/datasource/types.ts'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { QuarkDataSource, QUARK_PROVIDER_URL } from '../src/services/datasource/quark.ts'

const calls: Array<{ operation: string, request: unknown }> = []
const savedCookies: string[] = []
const bridge: QuarkBridge = {
  async list(request) {
    calls.push({ operation: 'list', request })
    return {
      entries: [
        { fid: 'folder-1', name: '电影', path: '/媒体/电影', isDir: true, modifiedMs: 1_700_000_000_000 },
        { fid: 'file-1', name: '超级少女.mkv', path: '/媒体/超级少女.mkv', isDir: false, size: 1024 },
        { fid: 'subtitle-1', name: '超级少女.zh-CN.srt', path: '/媒体/超级少女.zh-CN.srt', isDir: false, size: 32 },
      ],
      updatedCookie: '__uid=owner; __puus=rotated',
    }
  },
  async search(request) {
    calls.push({ operation: 'search', request })
    return {
      entries: [
        { fid: 'file-1', name: '超级少女.mkv', path: '/媒体/超级少女.mkv', isDir: false, size: 1024 },
        { fid: 'outside', name: '不应出现.mkv', path: '/其他/不应出现.mkv', isDir: false, size: 2048 },
      ],
    }
  },
  async getStream(request) {
    calls.push({ operation: 'stream', request })
    return {
      url: `https://download.example.test/${request.fid}`,
      headers: {
        Cookie: '__uid=owner; __puus=rotated',
        Referer: QUARK_PROVIDER_URL,
        'User-Agent': 'Quark',
      },
    }
  },
}

const config: DataSourceConfig = {
  id: 'quark-test',
  type: 'quark',
  name: '我的夸克',
  displayName: '我的夸克',
  order: 0,
  url: QUARK_PROVIDER_URL,
  enabled: true,
  extra: {
    credentialRef: 'datasource:quark-test:quark-credential',
    rootPath: '/媒体',
    cookie: 'must-not-survive',
  },
}

const source = new QuarkDataSource({
  bridge,
  readCredential: async () => ({ cookie: '__uid=owner; __puus=initial' }),
  saveCredential: async (_ref, credential) => { savedCookies.push(credential.cookie) },
})
await source.init(config)

const exported = source.exportConfig()
assert.equal(exported.url, QUARK_PROVIDER_URL)
assert.equal(exported.extra?.rootPath, '/媒体')
assert.equal('cookie' in (exported.extra ?? {}), false)
assert.doesNotMatch(JSON.stringify(exported), /__puus|owner/)

const listed = await source.list()
assert.deepEqual(listed.map(item => item.path), ['/媒体/电影', '/媒体/超级少女.mkv'])
assert.equal(savedCookies.at(-1), '__uid=owner; __puus=rotated')
assert.deepEqual(calls[0], {
  operation: 'list',
  request: { cookie: '__uid=owner; __puus=initial', path: '/媒体' },
})

const search = await source.search('超级少女')
assert.deepEqual(search.map(item => item.path), ['/媒体/超级少女.mkv'])

const detail = await source.getDetail('/媒体/超级少女.mkv')
assert.equal(detail.mediaSources?.[0]?.id, 'file-1')
assert.equal(detail.mediaSources?.[0]?.name, '夸克网盘原始直链')
assert.equal(detail.subtitles?.[0]?.title, '超级少女.zh-CN.srt')

const stream = await source.getStreamRequest({ itemId: '/媒体/超级少女.mkv' })
assert.equal(stream.url, 'https://download.example.test/file-1')
assert.equal(stream.headers?.Referer, QUARK_PROVIDER_URL)
assert.equal(calls.some(call => call.operation === 'stream'), true)

await assert.rejects(() => source.list('/其他'), /根目录/)

const implementation = await readFile(new URL('../src/services/datasource/quark.ts', import.meta.url), 'utf8')
assert.match(implementation, /readRawCredentialBackup/)
assert.match(implementation, /saveRawCredentialBackup\(credentialRef, previousCredential\)/)
assert.match(implementation, /removeCredential\(credentialRef\)/)
assert.match(implementation, /loadRawSourceScanCache\(this\.id, 'quark', this\.rootPath\)/)
assert.match(implementation, /quark_auth_start_qr/)
assert.match(implementation, /quark_auth_start_account/)
assert.match(implementation, /quark_auth_cancel/)

const nativeImplementation = await readFile(new URL('../src-tauri/src/commands/quark.rs', import.meta.url), 'utf8')
assert.match(nativeImplementation, /getTokenForQrcodeLogin/)
assert.match(nativeImplementation, /getServiceTicketByQrcodeToken/)
assert.match(nativeImplementation, /\("client_id", "532"\)/)
assert.match(nativeImplementation, /\("v", "1\.2"\)/)
assert.match(nativeImplementation, /\("request_id", request_id\.as_str\(\)\)/)
assert.match(nativeImplementation, /render_qr_data_url/)
assert.match(nativeImplementation, /cookies_for_url/)
assert.doesNotMatch(nativeImplementation, /api\.qrserver|quickchart|googleapis\.com\/chart/)

console.log('Quark DataSource regression checks passed.')
