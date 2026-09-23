import assert from 'node:assert/strict'
import type { DataSource, DataSourceType, MediaDetail, MediaItem } from '../src/services/datasource/types'
import { normalizeWorkLevelSearchResults, searchAcrossDataSources } from '../src/services/datasource/searchAggregation'

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
const sourceD = fakeSource('source-d', async () => [
  { ...shared, id: 'episode-1', sourceId: '', name: '慢慢来', type: 'episode' },
  { ...shared, id: 'season-1', sourceId: '', name: '第 1 季', type: 'season' },
  { ...shared, id: 'series', sourceId: '', name: '莉可丽丝', type: 'series' },
  { ...shared, id: 'movie', sourceId: '', name: '莉可丽丝 剧场版', type: 'movie' },
  { ...shared, id: 'file', sourceId: '', name: '未识别视频.mkv', type: 'file' },
  { ...shared, id: 'folder', sourceId: '', name: '未识别目录', type: 'folder' },
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

const workLevelResults = await searchAcrossDataSources([sourceD], '莉可丽丝', { limitPerSource: 2, limit: 10 })
assert.deepEqual(workLevelResults.map(item => `${item.type}:${item.id}`), [
  'series:series',
  'movie:movie',
])

assert.deepEqual(
  normalizeWorkLevelSearchResults(await sourceD.search('未识别')).map(item => item.type),
  ['series', 'movie', 'file', 'folder'],
)

console.log('aggregate search behavior verification passed')

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
