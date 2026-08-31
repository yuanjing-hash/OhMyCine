import assert from 'node:assert/strict'
import fs from 'node:fs'
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
  scannedMovie('six', 'F', 'https://image.example/f.jpg'),
  scannedMovie('seven', 'G', 'https://image.example/g.jpg'),
  scannedMovie('eight', 'H', 'https://image.example/h.jpg'),
  scannedMovie('nine', 'I', 'https://image.example/i.jpg'),
  scannedMovie('ten', 'J', 'https://image.example/j.jpg'),
])
assert.deepEqual(first.library.artworkCandidates, [
  'https://image.example/a.jpg',
  'https://image.example/b.jpg',
  'https://image.example/d.jpg',
  'https://image.example/e.jpg',
  'https://image.example/f.jpg',
  'https://image.example/g.jpg',
  'https://image.example/h.jpg',
  'https://image.example/i.jpg',
  'https://image.example/j.jpg',
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
assert.equal(single.library.artworkSource, 'generated')
assert.deepEqual(single.library.artworkCandidates, ['https://image.example/a.jpg'])

const empty = category([scannedMovie('one', 'A')])
assert.equal(empty.library.artworkSource, 'fallback')
assert.deepEqual(empty.library.artworkCandidates, [])

const mediaCard = fs.readFileSync(new URL('../src/components/media/MediaCard.vue', import.meta.url), 'utf8')
assert.match(mediaCard, /\[2, 0, 4, 3, 1, 5, 8, 7, 6\]/)
assert.match(mediaCard, /rotate\(-15\.8deg\)/)
assert.match(mediaCard, /aspect-ratio: 410 \/ 610/)
assert.match(mediaCard, /left: 32\.81%/)
assert.match(mediaCard, /left: 56\.77%/)
assert.match(mediaCard, /left: 82\.81%/)
assert.match(mediaCard, /top: -47\.87%/)
assert.match(mediaCard, /height: 173\.52%/)
assert.match(mediaCard, /top: 33\.72%/)
assert.match(mediaCard, /top: 67\.45%/)
assert.match(mediaCard, /top: 39\.57%/)
assert.match(mediaCard, /padding-left: 3\.82%/)
assert.match(mediaCard, /v-if="!usesStyle3Artwork"/)
assert.match(mediaCard, /libraryArtworkCandidates\.value\.length > 0/)
assert.match(mediaCard, /Array\.from\(\{ length: 9 \}, \(_, index\) => candidates\[index % candidates\.length\]\)/)
assert.doesNotMatch(mediaCard, /library-artwork-collage/)

console.log(JSON.stringify({
  playerOwnedArtwork: true,
  distinctCandidates: true,
  deterministicRevision: true,
  contentChangeInvalidatesRevision: true,
  sparseAndFallbackStates: true,
  styleStatic3Layout: true,
}, null, 2))
