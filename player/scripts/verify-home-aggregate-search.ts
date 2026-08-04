import assert from 'node:assert/strict'
import type { DataSource, DataSourceType, MediaDetail, MediaItem } from '../src/services/datasource/types'
import { searchAcrossDataSources } from '../src/services/datasource/searchAggregation'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const shared: MediaItem = {
  id: 'shared',
  sourceId: 'source-a',
  name: '超级少女',
  type: 'movie',
  path: '/shared',
}

const sourceA = fakeSource('source-a', async keyword => keyword === '超级少女'
  ? [shared, { ...shared, id: 'a-only', name: '超级少女 2026' }]
  : [])
const sourceB = fakeSource('source-b', async () => {
  throw new Error('temporary provider failure')
})
const sourceC = fakeSource('source-c', async () => [
  { ...shared, sourceId: 'source-c' },
  { ...shared, id: 'c-only', sourceId: '', name: 'Supergirl' },
])

assert.deepEqual(await searchAcrossDataSources([sourceA], '  '), [])

const results = await searchAcrossDataSources(
  [sourceA, sourceB, sourceC],
  '超级少女',
  { limitPerSource: 3, limit: 10 },
)
assert.deepEqual(results.map(item => `${item.sourceId}:${item.id}`), [
  'source-a:shared',
  'source-a:a-only',
  'source-c:shared',
  'source-c:c-only',
])
assert.equal(results.some(item => item.sourceId === 'source-b'), false)

const limited = await searchAcrossDataSources([sourceA, sourceC], '超级少女', { limit: 2 })
assert.equal(limited.length, 2)

const workspace = await readFile(fileURLToPath(new URL('../src/components/media/GlobalSearchWorkspace.vue', import.meta.url)), 'utf8')
assert.match(workspace, /全部来源/)
assert.match(workspace, /全部类型/)
assert.match(workspace, /sourceLibraries/)
assert.match(workspace, /suggestedKeywords/)
assert.match(workspace, /searchAllSources\(keyword, 100, sourceIds\)/)
assert.match(workspace, /@media \(max-width: 767px\)[\s\S]*?\.search-workspace \{ inset: 0;/)

const windowChrome = await readFile(fileURLToPath(new URL('../src/components/layout/WindowChrome.vue', import.meta.url)), 'utf8')
assert.match(windowChrome, /searchWorkspace\.toggle/)
assert.match(windowChrome, />\s*搜索\s*</)

const homeView = await readFile(fileURLToPath(new URL('../src/views/HomeView.vue', import.meta.url)), 'utf8')
assert.doesNotMatch(homeView, /HomeAggregateSearch/)

console.log(JSON.stringify({
  sourceFailureIsolated: true,
  sourceOrderPreserved: true,
  sourceScopedDeduplication: true,
  globalLimitApplied: true,
  desktopSearchWorkspace: true,
  mobileFullscreenSearch: true,
  sourceLibraryTypeFilters: true,
}, null, 2))

function fakeSource(id: string, search: (keyword: string) => Promise<MediaItem[]>): DataSource {
  return {
    id,
    name: id,
    type: 'emby' as DataSourceType,
    isConnected: true,
    init: async () => {},
    test: async () => true,
    destroy: () => {},
    list: async () => [],
    search,
    getDetail: async (itemId): Promise<MediaDetail> => ({
      id: itemId,
      sourceId: id,
      name: itemId,
      type: 'movie',
      path: itemId,
    }),
    getStreamURL: async itemId => itemId,
    exportConfig: () => ({ id, name: id, type: 'emby', order: 0, url: '' }),
  }
}
