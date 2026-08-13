import assert from 'node:assert/strict'
import type { DataSource, MediaDetail, MediaItem } from '../src/services/datasource/types'
import { planMediaDownload } from '../src/services/downloadPlanning'

const episode = (id: string, season: number, number: number): MediaItem => ({
  id,
  sourceId: 'source-1',
  name: `Episode ${number}`,
  type: 'episode',
  path: `/private/${id}.mkv`,
  seasonNumber: season,
  episodeNumber: number,
})
const first = episode('episode-1', 1, 1)
const second = episode('episode-2', 1, 2)
const season: MediaDetail = {
  id: 'season-1', sourceId: 'source-1', name: 'Season 1', type: 'season', path: '/private/season-1', children: [first, second],
}
const series: MediaDetail = {
  id: 'series-1', sourceId: 'source-1', name: 'Series', type: 'series', path: '/private/series', children: [season],
}
const firstDetail = { ...first, mediaSources: [{ id: 'episode-1-4k', name: '4K' }, { id: 'episode-1-1080p', name: '1080p' }] }
const details = new Map<string, MediaDetail | MediaItem>([[series.id, series], [season.id, season], [first.id, firstDetail], [second.id, second]])
const source = {
  getDetail: async (id: string) => details.get(id) as MediaDetail,
  list: async (id?: string) => (details.get(id ?? '')?.children ?? []),
} as DataSource

const plan = await planMediaDownload(source, {
  kind: 'media', sourceId: 'source-1', sourceType: 'emby', itemId: series.id, mediaType: 'series', display: { name: series.name },
})
assert.equal(plan.aggregate, true)
assert.deepEqual(plan.files.map(file => file.itemId), ['episode-1', 'episode-1', 'episode-2'])
assert.deepEqual(plan.files.map(file => file.mediaSourceId), ['episode-1-4k', 'episode-1-1080p', undefined])
assert.equal(JSON.stringify(plan).includes('/private/'), false, 'download plan must not expose provider paths')

const movie: MediaDetail = {
  id: 'movie-1', sourceId: 'source-1', name: 'Movie', type: 'movie', path: '/private/movie.mkv', mediaSources: [
    { id: '4k', name: '4K', size: 4_000 },
    { id: '1080p', name: '1080p', size: 2_000 },
  ],
}
details.set(movie.id, movie)
const versions = await planMediaDownload(source, {
  kind: 'media', sourceId: 'source-1', sourceType: 'emby', itemId: movie.id, mediaType: 'movie', display: { name: movie.name },
})
assert.equal(versions.aggregate, true)
assert.deepEqual(versions.files.map(file => file.mediaSourceId), ['4k', '1080p'])
assert.deepEqual(versions.files.map(file => file.expectedBytes), [4_000, 2_000])

console.log('download planning verification passed')
