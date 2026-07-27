import assert from 'node:assert/strict'
import { CloudDrive2DataSource, readCloudDrive2RootPath } from '../src/services/datasource/clouddrive2.ts'
import type { DataSourceConfig } from '../src/services/datasource/types.ts'

const credentialRef = 'datasource:cd2-main:clouddrive2-credential'
const calls: Array<{ operation: string, baseUrl: string, apiToken: string, path: string, keyword?: string }> = []
const apiToken = 'app-scoped-api-token'

const source = new CloudDrive2DataSource({
  readCredential: async ref => ref === credentialRef ? { apiToken } : null,
  bridge: {
    list: async (request) => {
      calls.push({ operation: 'list', ...request })
      if (request.path === '/媒体库') {
        return [
          { name: '电影', path: '/媒体库/电影', isDir: true, modifiedMs: Date.parse('2026-07-27T00:00:00Z') },
          { name: '阿凡达.mp4', path: '/媒体库/阿凡达.mp4', isDir: false, size: 2048 },
          { name: 'README.txt', path: '/媒体库/README.txt', isDir: false, size: 12 },
        ]
      }
      if (request.path === '/媒体库/电影') {
        return [{ name: '流浪地球.mkv', path: '/媒体库/电影/流浪地球.mkv', isDir: false, size: 4096 }]
      }
      throw new Error(`unexpected list path ${request.path}`)
    },
    search: async (request) => {
      calls.push({ operation: 'search', ...request })
      return [
        { name: '流浪地球.mkv', path: '/媒体库/电影/流浪地球.mkv', isDir: false, size: 4096 },
        { name: 'escape.mkv', path: '/其他/escape.mkv', isDir: false, size: 1 },
      ]
    },
    getStream: async (request) => {
      calls.push({ operation: 'stream', ...request })
      return {
        url: 'https://cdn.example.test/video.mkv?signature=temporary',
        headers: {
          Referer: 'https://provider.example.test/',
          'User-Agent': 'CloudDrive2',
        },
      }
    },
  },
})

const config: DataSourceConfig = {
  id: 'cd2-main',
  type: 'clouddrive2',
  name: 'CloudDrive2',
  displayName: '家庭 CloudDrive2',
  order: 3,
  url: 'http://127.0.0.1:19798',
  enabled: true,
  extra: {
    credentialRef,
    credentialVersion: 2,
    rootPath: '/媒体库',
    apiToken: 'must-not-persist',
  },
}

assert.equal(readCloudDrive2RootPath(config), '/媒体库')
await source.init(config)
assert.equal(await source.test(), true)

const libraries = await source.listLibraries()
assert.equal(libraries[0]?.name, '媒体库')

const rootItems = await source.list()
assert.deepEqual(rootItems.map(item => item.id).sort(), ['/媒体库/电影', '/媒体库/阿凡达.mp4'].sort())
assert.equal(rootItems.some(item => item.id.includes('README')), false)

const searchResults = await source.search('流浪')
assert.deepEqual(searchResults.map(item => item.id), ['/媒体库/电影/流浪地球.mkv'])

const detail = await source.getDetail('/媒体库/电影/流浪地球.mkv')
assert.equal(detail.mediaSources?.[0]?.container, 'mkv')
assert.equal(detail.mediaSources?.[0]?.name, 'CloudDrive2 原生直链')

const streamRequest = await source.getStreamRequest?.('/媒体库/电影/流浪地球.mkv')
assert.equal(streamRequest?.url, 'https://cdn.example.test/video.mkv?signature=temporary')
assert.deepEqual(streamRequest?.headers, {
  Referer: 'https://provider.example.test/',
  'User-Agent': 'CloudDrive2',
})

assert.ok(calls.length >= 4)
assert.ok(calls.every(call => call.baseUrl === 'http://127.0.0.1:19798'))
assert.ok(calls.every(call => call.apiToken === apiToken))
assert.equal(calls.find(call => call.operation === 'search')?.keyword, '流浪')

await assert.rejects(() => source.list('../escape'), /relative|路径/)
await assert.rejects(() => source.getStreamURL('/其他/阿凡达.mp4'), /根目录/)
await assert.rejects(() => source.getStreamURL('/媒体库/README.txt'), /不是支持的视频格式/)

const exported = source.exportConfig()
assert.equal(JSON.stringify(exported).includes('must-not-persist'), false)
assert.equal(JSON.stringify(exported).includes(apiToken), false)
assert.equal(exported.extra?.credentialRef, credentialRef)

console.log(JSON.stringify({
  library: libraries[0]?.name,
  rootItemIds: rootItems.map(item => item.id),
  searchIds: searchResults.map(item => item.id),
  streamHeaderNames: Object.keys(streamRequest?.headers ?? {}),
  operations: calls.map(call => ({ operation: call.operation, path: call.path })),
}, null, 2))
