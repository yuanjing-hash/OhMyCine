import assert from 'node:assert/strict'
import type { MediaDetail } from '../src/services/datasource/types'
import { filterTombstonedMediaDetail } from '../src/services/mediaDelete'

const detail: MediaDetail = {
  id: 'root', sourceId: 'source', name: 'Root', type: 'series', path: 'root',
  children: [{ id: 'hidden-child', sourceId: 'source', name: 'Hidden', type: 'episode', path: 'hidden' }, { id: 'visible-child', sourceId: 'source', name: 'Visible', type: 'episode', path: 'visible' }],
  similarItems: [{ id: 'hidden-similar', sourceId: 'source', name: 'Hidden', type: 'movie', path: 'hidden' }],
  collections: [{ id: 'visible-collection', sourceId: 'source', name: 'Visible', type: 'folder', path: 'visible', children: [{ id: 'hidden-nested', sourceId: 'source', name: 'Hidden', type: 'file', path: 'hidden' }] }],
}
const filtered = filterTombstonedMediaDetail(detail, item => item.id.startsWith('hidden'))
assert.deepEqual(filtered.children?.map(item => item.id), ['visible-child'])
assert.deepEqual(filtered.similarItems, [])
assert.deepEqual(filtered.collections?.[0]?.children, [])
console.log('media delete nested tombstone verification passed')
