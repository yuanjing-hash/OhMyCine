import assert from 'node:assert/strict'
import { createLocalFileDataSourceConfig, LocalFileDataSource, validateLocalFileDataSourceConfig } from '../src/services/datasource/local.ts'
import type { LocalFileEntry } from '../src/services/datasource/local.ts'

const rootPath = '/mnt/ohmycine-media'
const listCalls: Array<{ rootPath: string, path?: string }> = []

const entriesByPath = new Map<string, LocalFileEntry[]>([
  ['/', [
    entry('电影', '/电影', true),
    entry('动漫', '/动漫', true),
    entry('阿凡达.mp4', '/阿凡达.mp4', false, 2_048),
    entry('README.txt', '/README.txt', false, 12),
  ]],
  ['/电影', [
    entry('流浪地球.mkv', '/电影/流浪地球.mkv', false, 4_096),
  ]],
  ['/动漫', [
    entry('灵笼', '/动漫/灵笼', true),
  ]],
  ['/动漫/灵笼', [
    entry('S01E01.mp4', '/动漫/灵笼/S01E01.mp4', false, 1_024),
  ]],
])

const allEntries = [...entriesByPath.values()].flat()
const source = new LocalFileDataSource({
  listEntries: async (inputRootPath, path = '/') => {
    listCalls.push({ rootPath: inputRootPath, path })
    assert.equal(inputRootPath, rootPath)
    return entriesByPath.get(path) ?? []
  },
  getMetadata: async (inputRootPath, path) => {
    assert.equal(inputRootPath, rootPath)
    const found = allEntries.find(item => item.path === path)
    if (!found)
      throw new Error(`missing metadata for ${path}`)
    return found
  },
  getStreamPath: async (inputRootPath, path) => {
    assert.equal(inputRootPath, rootPath)
    return `${inputRootPath}${path}`
  },
})

const config = createLocalFileDataSourceConfig({
  id: 'local-main',
  displayName: '',
  rootPath,
  order: 2,
})

assert.equal(config.type, 'local')
assert.equal(config.url, 'local://filesystem')
assert.equal(config.name, 'ohmycine-media')
assert.equal(config.extra?.rootPath, rootPath)
assert.equal(JSON.stringify(config).includes('username'), false)

const libraries = await validateLocalFileDataSourceConfig(config, {
  listEntries: async (inputRootPath, path = '/') => {
    assert.equal(inputRootPath, rootPath)
    assert.equal(path, '/')
    return entriesByPath.get(path) ?? []
  },
  getMetadata: async () => entry('root', '/', true),
  getStreamPath: async () => rootPath,
})

assert.deepEqual(libraries.map(library => ({
  id: library.id,
  sourceId: library.sourceId,
  name: library.name,
  type: library.type,
})), [{
  id: '/',
  sourceId: 'local-main',
  name: 'ohmycine-media',
  type: 'folders',
}])

await source.init(config)
assert.equal(await source.test(), true)
assert.equal(listCalls.at(-1)?.path, '/')

const rootItems = await source.list()
assert.deepEqual([...rootItems.map(item => item.id)].sort(), ['/动漫', '/电影', '/阿凡达.mp4'].sort())
assert.equal(rootItems.some(item => item.path.startsWith(rootPath)), false)
assert.equal(rootItems.find(item => item.id === '/阿凡达.mp4')?.type, 'file')

const movieDetail = await source.getDetail('/阿凡达.mp4')
assert.equal(movieDetail.mediaSources?.[0]?.isRemote, false)
assert.equal(movieDetail.mediaSources?.[0]?.container, 'mp4')

assert.equal(await source.getStreamURL('/阿凡达.mp4'), '/mnt/ohmycine-media/阿凡达.mp4')
await assert.rejects(() => source.getStreamURL('/动漫'), /不能直接播放/)
await assert.rejects(() => source.getStreamURL('/README.txt'), /不是支持的视频格式/)
await assert.rejects(() => source.list('../escape'), /relative|路径/)

const searchResults = await source.search('S01E01')
assert.deepEqual(searchResults.map(item => item.id), ['/动漫/灵笼/S01E01.mp4'])

const homeSections = await source.getHomeSections()
assert.deepEqual(homeSections, [])

console.log(JSON.stringify({
  libraries: libraries.map(library => library.name),
  rootItemIds: rootItems.map(item => item.id),
  streamUrl: await source.getStreamURL('/阿凡达.mp4'),
  searchIds: searchResults.map(item => item.id),
  listCalls,
}, null, 2))

function entry(name: string, path: string, isDir: boolean, size?: number): LocalFileEntry {
  return {
    name,
    path,
    isDir,
    size,
    modifiedMs: Date.parse('2026-07-08T00:00:00.000Z'),
  }
}
