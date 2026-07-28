import assert from 'node:assert/strict'
import { WebDavDataSource, readWebDavRootPath } from '../src/services/datasource/webdav.ts'
import type { DataSourceConfig } from '../src/services/datasource/types.ts'

const credentialRef = 'datasource:webdav-main:webdav-credential'
const fetchCalls: Array<{ url: string, method?: string, depth?: string, authorization?: string }> = []

const source = new WebDavDataSource({
  readCredential: async ref => ref === credentialRef
    ? { username: 'webdav-user', password: 'webdav-pass' }
    : null,
  fetcher: async (request, options) => {
    const url = String(request)
    const headers = options?.headers as Record<string, string> | undefined
    fetchCalls.push({
      url,
      method: typeof options?.method === 'string' ? options.method : undefined,
      depth: headers?.Depth,
      authorization: headers?.Authorization,
    })

    assert.equal(options?.method, 'PROPFIND')
    assert.equal(headers?.Authorization, `Basic ${Buffer.from('webdav-user:webdav-pass', 'utf8').toString('base64')}`)
    assert.equal(headers?.Authorization.includes('webdav-pass'), false)

    if (url === 'https://cloud.example.test/dav/%E5%AA%92%E4%BD%93%E5%BA%93/')
      return multistatus([
        collection('/dav/%E5%AA%92%E4%BD%93%E5%BA%93/', '媒体库'),
        collection('/dav/%E5%AA%92%E4%BD%93%E5%BA%93/%E7%94%B5%E5%BD%B1/', '电影'),
        file('/dav/%E5%AA%92%E4%BD%93%E5%BA%93/%E9%98%BF%E5%87%A1%E8%BE%BE.mp4', '阿凡达.mp4', 2048),
        file('/dav/%E5%AA%92%E4%BD%93%E5%BA%93/README.txt', 'README.txt', 12),
      ])

    if (url === 'https://cloud.example.test/dav/%E5%AA%92%E4%BD%93%E5%BA%93/%E7%94%B5%E5%BD%B1/')
      return multistatus([
        collection('/dav/%E5%AA%92%E4%BD%93%E5%BA%93/%E7%94%B5%E5%BD%B1/', '电影'),
        file('/dav/%E5%AA%92%E4%BD%93%E5%BA%93/%E7%94%B5%E5%BD%B1/%E6%B5%81%E6%B5%AA%E5%9C%B0%E7%90%83.mkv', '流浪地球.mkv', 4096),
      ])

    if (url === 'https://cloud.example.test/dav/%E5%AA%92%E4%BD%93%E5%BA%93/%E9%98%BF%E5%87%A1%E8%BE%BE.mp4')
      return multistatus([
        file('/dav/%E5%AA%92%E4%BD%93%E5%BA%93/%E9%98%BF%E5%87%A1%E8%BE%BE.mp4', '阿凡达.mp4', 2048),
      ])

    if (url === 'https://cloud.example.test/dav/%E5%AA%92%E4%BD%93%E5%BA%93/README.txt')
      return multistatus([
        file('/dav/%E5%AA%92%E4%BD%93%E5%BA%93/README.txt', 'README.txt', 12),
      ])

    throw new Error(`unexpected PROPFIND ${url}`)
  },
})

const config: DataSourceConfig = {
  id: 'webdav-main',
  type: 'webdav',
  name: 'WebDAV',
  displayName: '家庭 WebDAV',
  order: 3,
  url: 'https://cloud.example.test/dav',
  enabled: true,
  extra: {
    credentialRef,
    credentialVersion: 1,
    rootPath: '/媒体库',
    username: 'must-not-persist',
    password: 'must-not-persist',
  },
}

assert.equal(readWebDavRootPath(config), '/媒体库')

await source.init(config)
assert.equal(await source.test(), true)

const libraries = await source.listLibraries()
assert.deepEqual(libraries.map(library => ({
  id: library.id,
  sourceId: library.sourceId,
  name: library.name,
  type: library.type,
})), [{
  id: '/媒体库',
  sourceId: 'webdav-main',
  name: '媒体库',
  type: 'folders',
}])

const rootItems = await source.list()
assert.deepEqual(rootItems.map(item => item.id).sort(), ['/媒体库/电影', '/媒体库/阿凡达.mp4'].sort())
assert.equal(rootItems.some(item => item.id.includes('README')), false)

const searchResults = await source.search('流浪')
assert.deepEqual(searchResults.map(item => item.id), ['/媒体库/电影/流浪地球.mkv'])

const detail = await source.getDetail('/媒体库/阿凡达.mp4')
assert.equal(detail.mediaSources?.[0]?.container, 'mp4')
assert.equal(detail.mediaSources?.[0]?.isRemote, true)

const streamUrl = await source.getStreamURL('/媒体库/阿凡达.mp4')
assert.equal(streamUrl, 'https://cloud.example.test/dav/%E5%AA%92%E4%BD%93%E5%BA%93/%E9%98%BF%E5%87%A1%E8%BE%BE.mp4')
assert.equal(streamUrl.includes('webdav-user'), false)
assert.equal(streamUrl.includes('webdav-pass'), false)

const streamRequest = await source.getStreamRequest?.({ itemId: '/媒体库/阿凡达.mp4' })
assert.equal(streamRequest?.url, streamUrl)
assert.equal(streamRequest?.headers?.Authorization, `Basic ${Buffer.from('webdav-user:webdav-pass', 'utf8').toString('base64')}`)

await assert.rejects(() => source.getStreamURL('/媒体库/README.txt'), /不是支持的视频格式/)
await assert.rejects(() => source.list('../escape'), /relative|路径/)
await assert.rejects(() => source.getStreamURL('/其他/阿凡达.mp4'), /根目录/)

const exported = source.exportConfig()
assert.equal(JSON.stringify(exported).includes('must-not-persist'), false)
assert.equal(JSON.stringify(exported).includes('webdav-pass'), false)
assert.equal(exported.extra?.credentialRef, credentialRef)

const homeSections = await source.getHomeSections()
assert.deepEqual(homeSections, [])

console.log(JSON.stringify({
  libraries: libraries.map(library => library.name),
  rootItemIds: rootItems.map(item => item.id),
  searchIds: searchResults.map(item => item.id),
  streamUrl,
  streamHeaders: Object.keys(streamRequest?.headers ?? {}),
  fetchCalls,
}, null, 2))

function multistatus(items: string[]): string {
  return `<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:">${items.join('')}</d:multistatus>`
}

function collection(href: string, name: string): string {
  return response(href, name, '<d:resourcetype><d:collection /></d:resourcetype>')
}

function file(href: string, name: string, size: number): string {
  return response(href, name, `<d:resourcetype /><d:getcontentlength>${size}</d:getcontentlength>`)
}

function response(href: string, name: string, props: string): string {
  return [
    '<d:response>',
    `<d:href>${href}</d:href>`,
    '<d:propstat>',
    '<d:prop>',
    `<d:displayname>${name}</d:displayname>`,
    props,
    '<d:getlastmodified>Wed, 08 Jul 2026 00:00:00 GMT</d:getlastmodified>',
    '</d:prop>',
    '<d:status>HTTP/1.1 200 OK</d:status>',
    '</d:propstat>',
    '</d:response>',
  ].join('')
}
