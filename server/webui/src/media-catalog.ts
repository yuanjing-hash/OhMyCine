export const mediaCatalogPageSizes = [20, 50, 100] as const

export type MediaCatalogPageSize = typeof mediaCatalogPageSizes[number]
export type MediaCatalogTypeFilter = '' | 'movie' | 'series'
export type MediaCatalogMatchFilter = '' | 'matched' | 'unrecognized'

export interface MediaCatalogQuery {
  page: number
  pageSize: MediaCatalogPageSize
  query: string
  mediaType: MediaCatalogTypeFilter
  matchStatus?: MediaCatalogMatchFilter
}

export function mediaCatalogEndpoint(libraryID: number, filters: MediaCatalogQuery): string {
  const params = new URLSearchParams({
    page: String(Math.max(1, Math.trunc(filters.page))),
    page_size: String(filters.pageSize),
  })
  const query = filters.query.trim()
  if (query) params.set('query', query)
  if (filters.mediaType) params.set('media_type', filters.mediaType)
  if (filters.matchStatus) params.set('match_status', filters.matchStatus)
  return `/api/v1/media-libraries/${libraryID}/catalog?${params.toString()}`
}

export function mediaCatalogDetailEndpoint(libraryID: number, workID: string): string {
  return `/api/v1/media-libraries/${libraryID}/catalog/${encodeURIComponent(workID)}`
}

export function mediaCatalogPageCount(total: number, pageSize: number): number {
  if (!Number.isFinite(total) || total <= 0 || !Number.isFinite(pageSize) || pageSize <= 0) return 1
  return Math.max(1, Math.ceil(total / pageSize))
}

export function mediaCatalogVisibleRange(page: number, pageSize: number, total: number): { start: number; end: number } {
  if (total <= 0) return { start: 0, end: 0 }
  const safePage = Math.max(1, Math.trunc(page))
  const start = (safePage - 1) * pageSize + 1
  return { start: Math.min(start, total), end: Math.min(start + pageSize - 1, total) }
}
