import assert from 'node:assert/strict'
import type { MediaItem } from '../src/services/datasource/types'
import type { ScannedDisplayItem } from '../src/services/sourceLibraryScannedMedia'
import type { RawMediaCandidate } from '../src/services/scraper/types'
import { createScannedCategory } from '../src/services/sourceLibraryScannedMedia'

function scannedMovie(id: string, name: string, posterUrl?: string): ScannedDisplayItem {
  const path = `/${name}.mkv`
  const candidate: RawMediaCandidate = {
    kind: 'movie',
    parseStatus: 'parsed',
    title: name,
    normalizedTitle: name.toLocaleLowerCase(),
    confidence: 1,
    signals: [],
    record: {
      id,
      sourceId: 'local-artwork',
      sourceType: 'local',
      rootPath: '/',
      providerPath: path,
      relativePath: path.slice(1),
      parentPath: '/',
      fileName: path.slice(1),
      extension: 'mkv',
    },
  }
  const item: MediaItem = {
    id,
    sourceId: 'local-artwork',
    name,
    type: 'movie',
    path,
    posterUrl,
  }
  return { item, candidate, categoryName: '电影', domain: 'movie' }
}

function category(entries: ScannedDisplayItem[]) {
  return createScannedCategory({ sourceId: 'local-artwork', rootPath: '/', name: '电影', entries })
}

const first = category([
  scannedMovie('one', 'A', 'https://image.example/a.jpg'),
  scannedMovie('two', 'B', 'https://image.example/b.jpg'),
  scannedMovie('three', 'C', 'https://image.example/b.jpg'),
  scannedMovie('four', 'D', 'https://image.example/d.jpg'),
  scannedMovie('five', 'E', 'https://image.example/e.jpg'),
])
assert.deepEqual(first.library.artworkCandidates, [
  'https://image.example/a.jpg',
  'https://image.example/b.jpg',
  'https://image.example/d.jpg',
  'https://image.example/e.jpg',
])
assert.equal(first.library.artworkSource, 'generated')
assert.match(first.library.artworkRevision ?? '', /^local-[0-9a-f]{16}$/)

const same = category([...first.entries].reverse())
assert.equal(same.library.artworkRevision, first.library.artworkRevision)

const changed = category([
  scannedMovie('one', 'A', 'https://image.example/a-v2.jpg'),
  ...first.entries.slice(1),
])
assert.notEqual(changed.library.artworkRevision, first.library.artworkRevision)

const single = category([scannedMovie('one', 'A', 'https://image.example/a.jpg')])
assert.equal(single.library.artworkSource, 'provider')
assert.deepEqual(single.library.artworkCandidates, ['https://image.example/a.jpg'])

const empty = category([scannedMovie('one', 'A')])
assert.equal(empty.library.artworkSource, 'fallback')
assert.deepEqual(empty.library.artworkCandidates, [])

console.log(JSON.stringify({
  playerOwnedArtwork: true,
  distinctCandidates: true,
  deterministicRevision: true,
  contentChangeInvalidatesRevision: true,
  providerAndFallbackStates: true,
}, null, 2))
