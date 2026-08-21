import { describe, expect, it } from 'vitest'
import { mediaCatalogDetailEndpoint, mediaCatalogEndpoint, mediaCatalogPageCount, mediaCatalogVisibleRange } from './media-catalog'

describe('media catalog paging', () => {
  it('builds a trimmed filtered catalog request', () => {
    expect(mediaCatalogEndpoint(12, { page: 2, pageSize: 50, query: '  莉可丽丝  ', mediaType: 'series' }))
      .toBe('/api/v1/media-libraries/12/catalog?page=2&page_size=50&query=%E8%8E%89%E5%8F%AF%E4%B8%BD%E4%B8%9D&media_type=series')
  })

  it('keeps opaque work ids inside one encoded path segment', () => {
    expect(mediaCatalogDetailEndpoint(3, 'series:key/part')).toBe('/api/v1/media-libraries/3/catalog/series%3Akey%2Fpart')
  })

  it('calculates page counts and visible ranges for large libraries', () => {
    expect(mediaCatalogPageCount(12099, 50)).toBe(242)
    expect(mediaCatalogVisibleRange(2, 50, 12099)).toEqual({ start: 51, end: 100 })
    expect(mediaCatalogVisibleRange(1, 50, 0)).toEqual({ start: 0, end: 0 })
  })
})
