import assert from 'node:assert/strict'
import type { DataSource, DataSourceType, MediaDetail, MediaItem } from '../src/services/datasource/types'
import { searchAcrossDataSources } from '../src/services/datasource/searchAggregation'

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

console.log(JSON.stringify({
  sourceFailureIsolated: true,
  sourceOrderPreserved: true,
  sourceScopedDeduplication: true,
  globalLimitApplied: true,
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
