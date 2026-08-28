import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { buildDiscoveryMediaSearchPath, buildDiscoveryPath, buildRefreshPayload, coverageStatusLabel, discoveryCoveragePath, discoveryResourceRoute, discoveryWorkQuery, mediaIdentitySearchURL, normalizeMediaCoverage } from '@/discovery'

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

  it('builds poster, coverage and stable identity resource routes', () => {
    expect(buildDiscoveryMediaSearchPath(' 三体 ', 'tv', 900)).toBe('/api/v1/discovery/media-search?query=%E4%B8%89%E4%BD%93&media_type=tv&page=500')
    expect(discoveryCoveragePath('tv', 100)).toBe('/api/v1/discovery/media/tv/100/coverage')
    expect(mediaIdentitySearchURL('tv', 100, { season: 0, siteID: 2 }, true)).toBe('/api/v1/discovery/media/tv/100/torrent-search/stream?page=1&season=0&site_id=2')
    expect(mediaIdentitySearchURL('tv', 100, { siteIDs: [3, 1, 3] }, true)).toBe('/api/v1/discovery/media/tv/100/torrent-search/stream?page=1&site_ids=3&site_ids=1')
    expect(discoveryResourceRoute({ provider: 'tmdb', provider_id: '100', media_type: 'tv', title: '三体', tmdb_id: 100 })).toEqual({ path: '/discovery/explore', query: { title: '三体', media_type: 'tv', provider: 'tmdb', provider_id: '100', tmdb_id: '100', mode: 'resources', identity: 'tmdb' } })
    expect(coverageStatusLabel('future')).toBe('未播')
  })

  it('keeps poster search as default and renders coverage without duplicating detail views', () => {
    const explore = readFileSync(new URL('./views/ExploreView.vue', import.meta.url), 'utf8')
    const detail = readFileSync(new URL('./views/DiscoveryDetailView.vue', import.meta.url), 'utf8')
    const layout = readFileSync(new URL('./layouts/AppLayout.vue', import.meta.url), 'utf8')
    expect(explore).toContain("const mode = ref<'media' | 'resources'>")
    expect(explore).toContain('>搜索</button>')
    expect(explore).toContain('>直接搜索</button>')
    expect(explore).toContain("route.query.identity === 'tmdb'")
    expect(explore).toContain('mediaIdentitySearchURL')
    expect(explore).toContain('new AbortController()')
    expect(explore).toContain('{ signal: controller.signal }')
    expect(detail).toContain('媒体库覆盖率')
    expect(detail).not.toContain('Season 0')
    expect(detail).toContain('特别篇 · 不计普通缺集')
    expect(detail).toContain('discoveryResourceRoute')
    expect(layout).toContain('submitGlobalMediaSearch')
    expect(layout).toContain("path: '/discovery/explore', query: { query }")
    expect(layout).not.toContain('全局搜索服务尚未实现')
  })

  it('normalizes production-shaped nullable coverage collections at the API boundary', () => {
    const coverage = normalizeMediaCoverage({
      media_type: 'tv',
      tmdb_id: 100,
      title: '测试剧',
      status: 'missing',
      libraries: null,
      freshness: { checked_at: '2026-08-28T00:00:00Z', library_scan_state: 'complete', tmdb_state: 'complete' },
      tv: {
        counts: { total: 8, present: 0, missing: 8, future: 0, unknown: 0 },
        seasons: [
          {
            season_number: 1,
            name: '第 1 季',
            special: false,
            status: 'missing',
            counts: { total: 8, present: 0, missing: 8, future: 0, unknown: 0 },
            episodes: [{ episode_number: 1, name: null, air_date: null, status: null, library_ids: null }],
          },
          {
            season_number: 2,
            name: '第 2 季',
            special: false,
            status: 'unknown',
            counts: null,
            episodes: null,
          },
        ],
      },
    })

    expect(coverage.libraries).toEqual([])
    expect(coverage.tv?.seasons[0].episodes[0]).toEqual({ episode_number: 1, name: undefined, air_date: undefined, status: 'unknown', library_ids: [] })
    expect(coverage.tv?.seasons[1].episodes).toEqual([])
    expect(coverage.tv?.seasons[1].counts).toEqual({ total: 0, present: 0, missing: 0, future: 0, unknown: 0 })
  })

  it('normalizes nullable movie library references and rejects an invalid root payload', () => {
    const coverage = normalizeMediaCoverage({ media_type: 'movie', tmdb_id: 200, title: '测试电影', status: 'missing', libraries: null, freshness: null, movie: { present: false, library_ids: null } })
    expect(coverage.movie?.library_ids).toEqual([])
    expect(coverage.freshness).toEqual({ checked_at: '', library_scan_state: 'unscanned', tmdb_state: 'partial' })
    expect(() => normalizeMediaCoverage(null)).toThrow('媒体库覆盖率数据格式无效')
    expect(() => normalizeMediaCoverage({ media_type: 'tv', tmdb_id: 100, title: '损坏响应' })).toThrow('媒体库覆盖率数据格式无效')
  })
})
