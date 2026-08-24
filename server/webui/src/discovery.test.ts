import { describe, expect, it } from 'vitest'
import { buildDiscoveryPath, buildRefreshPayload, discoveryWorkQuery } from '@/discovery'

describe('discovery contracts', () => {
  it('builds bounded provider queries', () => {
    expect(buildDiscoveryPath('douban', 9)).toBe('/api/v1/discovery/recommendations?page=5&provider=douban')
  })

  it('keeps only stable work identity in navigation query', () => {
    expect(discoveryWorkQuery({ provider: 'tmdb', provider_id: '42', media_type: 'movie', title: '七武士', year: 1954, tmdb_id: 42, poster_url: 'https://image.example/secret' })).toEqual({ title: '七武士', media_type: 'movie', provider: 'tmdb', provider_id: '42', year: '1954', tmdb_id: '42' })
  })

  it('refreshes one section rather than the full page', () => {
    expect(buildRefreshPayload({ provider: 'tmdb', code: 'trending-movie', title: '热门', media_type: 'movie', page: 2, total_pages: 3, items: [], fetched_at: '', stale: false })).toEqual({ provider: 'tmdb', section: 'trending-movie', page: 2 })
  })
})
