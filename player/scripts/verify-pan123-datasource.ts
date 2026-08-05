import type { Pan123Bridge } from '../src/services/datasource/pan123.ts'
import type { DataSourceConfig } from '../src/services/datasource/types.ts'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { PAN123_PROVIDER_URL, Pan123DataSource } from '../src/services/datasource/pan123.ts'

const calls: Array<{ operation: string, request: unknown }> = []
const savedTokens: string[] = []
const bridge: Pan123Bridge = {
  async login(request) {
    calls.push({ operation: 'login', request })
    return { accessToken: 'fresh-token' }
  },
  async list(request) {
    calls.push({ operation: 'list', request })
    return {
      entries: [
        { fileId: '10', name: '电影', path: '/媒体/电影', isDir: true, etag: '', s3KeyFlag: '' },
        { fileId: '11', name: '超级少女.mkv', path: '/媒体/超级少女.mkv', isDir: false, size: 1024, etag: 'video-etag', s3KeyFlag: 'video-key' },
        { fileId: '12', name: '超级少女.zh-CN.srt', path: '/媒体/超级少女.zh-CN.srt', isDir: false, size: 32, etag: 'sub-etag', s3KeyFlag: 'sub-key' },
      ],
      updatedAccessToken: 'rotated-token',
    }
  },
  async search(request) {
    calls.push({ operation: 'search', request })
    return {
      entries: [
        { fileId: '11', name: '超级少女.mkv', path: '/媒体/超级少女.mkv', isDir: false, size: 1024, etag: 'video-etag', s3KeyFlag: 'video-key' },
        { fileId: '99', name: '不应出现.mkv', path: '/其他/不应出现.mkv', isDir: false, size: 2048, etag: 'outside', s3KeyFlag: 'outside' },
      ],
    }
  },
  async getStream(request) {
    calls.push({ operation: 'stream', request })
    return {
      url: `https://download.example.test/${request.fileId}`,
      headers: { Referer: 'https://download.example.test/' },
    }
  },
}

const config: DataSourceConfig = {
  id: 'pan123-test',
  type: '123',
  name: '我的 123 云盘',
  displayName: '我的 123 云盘',
  order: 0,
  url: PAN123_PROVIDER_URL,
  enabled: true,
  extra: {
    credentialRef: 'datasource:pan123-test:123-credential',
    rootPath: '/媒体',
    accessToken: 'must-not-survive',
    password: 'must-not-survive',
  },
}

const source = new Pan123DataSource({
  bridge,
  readCredential: async () => ({ accessToken: 'initial-token', username: 'owner@example.test', password: 'secret' }),
  saveCredential: async (_ref, credential) => { savedTokens.push(credential.accessToken) },
})
await source.init(config)

const exported = source.exportConfig()
assert.equal(exported.url, PAN123_PROVIDER_URL)
assert.equal(exported.extra?.rootPath, '/媒体')
assert.equal('accessToken' in (exported.extra ?? {}), false)
assert.equal('password' in (exported.extra ?? {}), false)
assert.doesNotMatch(JSON.stringify(exported), /initial-token|secret/)

const listed = await source.list()
assert.deepEqual(listed.map(item => item.path), ['/媒体/电影', '/媒体/超级少女.mkv'])
assert.equal(savedTokens.at(-1), 'rotated-token')
assert.deepEqual(calls[0], {
  operation: 'list',
  request: {
    accessToken: 'initial-token',
    username: 'owner@example.test',
    password: 'secret',
    path: '/媒体',
    rootPath: '/媒体',
  },
})

const search = await source.search('超级少女')
assert.deepEqual(search.map(item => item.path), ['/媒体/超级少女.mkv'])

const detail = await source.getDetail('/媒体/超级少女.mkv')
assert.equal(detail.mediaSources?.[0]?.id, '11')
assert.equal(detail.mediaSources?.[0]?.name, '123 云盘原始直链')
assert.equal(detail.subtitles?.[0]?.title, '超级少女.zh-CN.srt')

const stream = await source.getStreamRequest({ itemId: '/媒体/超级少女.mkv' })
assert.equal(stream.url, 'https://download.example.test/11')
assert.equal(stream.headers?.Referer, 'https://download.example.test/')
assert.equal(calls.some(call => call.operation === 'stream'), true)

await assert.rejects(() => source.list('/其他'), /根目录/)

const implementation = await readFile(new URL('../src/services/datasource/pan123.ts', import.meta.url), 'utf8')
assert.match(implementation, /readRawCredentialBackup/)
assert.match(implementation, /saveRawCredentialBackup\(credentialRef, previousCredential\)/)
assert.match(implementation, /removeCredential\(credentialRef\)/)
assert.match(implementation, /loadRawSourceScanCache\(this\.id, '123', this\.rootPath\)/)
assert.match(implementation, /pan123_login/)
assert.doesNotMatch(implementation, /localStorage|sessionStorage/)

const nativeImplementation = await readFile(new URL('../src-tauri/src/commands/pan123.rs', import.meta.url), 'utf8')
assert.match(nativeImplementation, /login\.123pan\.com\/api\/user\/sign_in/)
assert.match(nativeImplementation, /yun\.123pan\.com\/b\/api/)
assert.match(nativeImplementation, /sign_path_at/)
assert.match(nativeImplementation, /crc32fast::hash/)
assert.match(nativeImplementation, /Authorization|AUTHORIZATION/)
assert.match(nativeImplementation, /decode_download_url/)
assert.match(nativeImplementation, /Policy::none\(\)/)
assert.doesNotMatch(nativeImplementation, /log::|println!|dbg!/)

console.log('123 Pan DataSource regression checks passed.')
