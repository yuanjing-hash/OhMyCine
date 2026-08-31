import assert from 'node:assert/strict'
import type { MediaDetail } from '../src/services/datasource/types'
import type { RawMediaCandidate, RawScrapedMediaItem } from '../src/services/scraper/types'
import { filterTombstonedMediaDetail, resolveRawMovieVariantPaths } from '../src/services/mediaDelete'

function movie(path: string, normalizedTitle: string, year: number, recordId = path): RawMediaCandidate {
  return {
    kind: 'movie', parseStatus: 'parsed', title: normalizedTitle, normalizedTitle, year,
    record: { id: recordId, sourceId: 'local-1', sourceType: 'local', rootPath: '/', providerPath: path, relativePath: path.slice(1), parentPath: '/', fileName: path.slice(1), extension: 'mkv' },
    confidence: 1, signals: [],
  }
}

const candidates = [movie('/Movie.1080p.mkv', 'movie', 2024, 'a'), movie('/Movie.2160p.mkv', 'movie', 2024, 'b'), movie('/Other.mkv', 'other', 2024, 'c')]
const scrapedItems = ['a', 'b'].map(recordId => ({ recordId, providerPath: candidates.find(item => item.record.id === recordId)!.record.providerPath, matchStatus: 'matched', searchTitles: ['Movie'], mediaType: 'movie', categoryName: 'Movies', metadata: { tmdbId: 42, mediaType: 'movie', title: 'Movie', genreIds: [], genres: [], originCountries: [], productionCountries: [], scrapedAt: '' } } satisfies RawScrapedMediaItem))
assert.deepEqual(resolveRawMovieVariantPaths({ candidates, scrapedItems }, '/Movie.1080p.mkv'), ['/Movie.1080p.mkv', '/Movie.2160p.mkv'])
assert.deepEqual(resolveRawMovieVariantPaths({ candidates, scrapedItems: [] }, '/Movie.1080p.mkv'), ['/Movie.1080p.mkv', '/Movie.2160p.mkv'])

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
console.log('media delete multi-version and nested tombstone verification passed')
