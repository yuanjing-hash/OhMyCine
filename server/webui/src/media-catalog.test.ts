import { describe, expect, it } from 'vitest'
import { mediaCatalogDetailEndpoint, mediaCatalogEndpoint, mediaCatalogOpenTargets, mediaCatalogPageCount, mediaCatalogVisibleRange, normalizeMediaCatalogDetail } from './media-catalog'
import type { MediaCatalogItem } from './types/api'

describe('media catalog paging', () => {
  it('builds a trimmed filtered catalog request', () => {
    expect(mediaCatalogEndpoint(12, { page: 2, pageSize: 50, query: '  莉可丽丝  ', mediaType: 'series' }))
      .toBe('/api/v1/media-libraries/12/catalog?page=2&page_size=50&query=%E8%8E%89%E5%8F%AF%E4%B8%BD%E4%B8%9D&media_type=series')
  })

  it('keeps opaque work ids inside one encoded path segment', () => {
    expect(mediaCatalogDetailEndpoint(3, 'series:key/part')).toBe('/api/v1/media-libraries/3/catalog/series%3Akey%2Fpart')
  })

  it('uses the aggregate endpoint for all libraries and keeps category scope', () => {
    expect(mediaCatalogEndpoint(null, { page: 1, pageSize: 20, query: '', mediaType: '', category: '动画' }))
      .toBe('/api/v1/media-libraries/catalog?page=1&page_size=20&category=%E5%8A%A8%E7%94%BB')
  })

  it('normalizes nullable season collections at the API boundary', () => {
    const detail = normalizeMediaCatalogDetail({ work: { id: 'x', title: '剧', kind: 'series', library_works: null }, seasons: [{ number: 2, episodes: null }], files: null, reorganizable_transfers: null })
    expect(detail.work.library_works).toEqual([])
    expect(detail.seasons).toEqual([{ number: 2, episodes: [] }])
    expect(detail.files).toEqual([])
    expect(detail.reorganizable_transfers).toEqual([])
  })

  it('requires an explicit library choice for aggregate works in multiple libraries', () => {
    const item: MediaCatalogItem = { id: 'aggregate', title: '剧', kind: 'series', file_count: 2, season_count: 1, episode_count: 2, size: 20, modified_at: '', category_name: '动画', match_status: 'matched', manual_override: false, library_works: [
      { library_id: 1, library_name: '一号库', work_id: 'one', file_count: 1 },
      { library_id: 2, library_name: '二号库', work_id: 'two', file_count: 1 },
    ] }
    expect(mediaCatalogOpenTargets(item, null)).toHaveLength(2)
    expect(mediaCatalogOpenTargets(item, 2)).toEqual([item.library_works[1]])
  })

  it('calculates page counts and visible ranges for large libraries', () => {
    expect(mediaCatalogPageCount(12099, 50)).toBe(242)
    expect(mediaCatalogVisibleRange(2, 50, 12099)).toEqual({ start: 51, end: 100 })
    expect(mediaCatalogVisibleRange(1, 50, 0)).toEqual({ start: 0, end: 0 })
  })
})
