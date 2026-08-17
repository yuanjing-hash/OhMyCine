import type { MediaItem } from '../src/services/datasource/types'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { episodeSearchTitle, searchSeriesEpisodes } from '../src/services/seriesEpisodeSearch'

const seasonOne = mediaItem('season-1', '第 1 季', 'season')
const seasonTwo = mediaItem('season-2', '第 2 季', 'season')
const entries = [
  { season: seasonOne, episode: { ...mediaItem('episode-1', '慢慢来', 'episode'), seasonNumber: 1, episodeNumber: 1 } },
  { season: seasonTwo, episode: { ...mediaItem('episode-2', 'A New Mission', 'episode'), seasonNumber: 2, episodeNumber: 3 } },
]

assert.deepEqual(searchSeriesEpisodes(entries, '  慢慢  ').map(entry => entry.episode.id), ['episode-1'])
assert.deepEqual(searchSeriesEpisodes(entries, 'new mission').map(entry => entry.episode.id), ['episode-2'])
assert.deepEqual(searchSeriesEpisodes(entries, 'NEW').map(entry => entry.episode.id), ['episode-2'])
assert.deepEqual(searchSeriesEpisodes(entries, ''), [])
assert.equal(episodeSearchTitle({ ...mediaItem('untitled', '  ', 'episode'), episodeNumber: 7 }), '第 7 集')

const detailView = await readFile(fileURLToPath(new URL('../src/views/MediaDetailView.vue', import.meta.url)), 'utf8')
assert.match(detailView, /const seasonEpisodeCache = new Map<string, MediaItem\[\]>\(\)/)
assert.match(detailView, /const generation = \+\+episodeSearchGeneration/)
assert.match(detailView, /if \(generation === episodeSearchGeneration\)/)
assert.match(detailView, /await selectSeason\(entry\.season\)/)
assert.match(detailView, /selectEpisodeIndex\(index\)/)
const locateFunctionStart = detailView.indexOf('async function locateEpisodeSearchResult')
const locateFunctionEnd = detailView.indexOf('\n}\n\nfunction episodeSearchSeasonLabel', locateFunctionStart)
assert.notEqual(locateFunctionStart, -1)
assert.notEqual(locateFunctionEnd, -1)
const locateFunction = detailView.slice(locateFunctionStart, locateFunctionEnd)
assert.doesNotMatch(locateFunction, /playItem\(/)
assert.match(detailView, /role="dialog"/)
assert.match(detailView, /aria-modal="true"/)
assert.match(detailView, /选择后只定位到该集，不会自动播放/)

console.log(JSON.stringify({
  crossSeasonTitleSearch: true,
  caseInsensitiveTrimmedMatching: true,
  unnamedEpisodeFallback: true,
  seasonCachePresent: true,
  staleRequestGuardPresent: true,
  selectionDoesNotAutoplay: true,
  accessibleDialogPresent: true,
}, null, 2))

function mediaItem(id: string, name: string, type: MediaItem['type']): MediaItem {
  return {
    id,
    sourceId: 'source-a',
    name,
    type,
    path: id,
  }
}
