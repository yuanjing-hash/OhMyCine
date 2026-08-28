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
  category?: string
}

export function mediaCatalogEndpoint(libraryID: number | null, filters: MediaCatalogQuery): string {
  const params = new URLSearchParams({
    page: String(Math.max(1, Math.trunc(filters.page))),
    page_size: String(filters.pageSize),
  })
  const query = filters.query.trim()
  if (query) params.set('query', query)
  if (filters.mediaType) params.set('media_type', filters.mediaType)
  if (filters.matchStatus) params.set('match_status', filters.matchStatus)
  if (filters.category?.trim()) params.set('category', filters.category.trim())
  const base = libraryID == null ? '/api/v1/media-libraries/catalog' : `/api/v1/media-libraries/${libraryID}/catalog`
  return `${base}?${params.toString()}`
}

export function mediaCatalogDetailEndpoint(libraryID: number, workID: string): string {
  return `/api/v1/media-libraries/${libraryID}/catalog/${encodeURIComponent(workID)}`
}

export function mediaCatalogActionEndpoint(libraryID: number, workID: string, action: 'tmdb-candidates' | 'rescrape' | 'override' | 'deletion-preview' | 'deletion-confirm'): string {
  return `${mediaCatalogDetailEndpoint(libraryID, workID)}/${action}`
}

export function mediaCatalogOverrideEndpoint(libraryID: number, workID: string): string { return mediaCatalogActionEndpoint(libraryID, workID, 'override') }

export function mediaCatalogOpenTargets(item: import('@/types/api').MediaCatalogItem, selectedLibrary: number | null): import('@/types/api').MediaCatalogLibraryWork[] {
  const works = Array.isArray(item.library_works) ? item.library_works.filter(work => Number.isFinite(work.library_id) && work.library_id > 0 && !!work.work_id) : []
  if (selectedLibrary == null) return works
  return works.filter(work => work.library_id === selectedLibrary)
}

export function normalizeMediaCatalogDetail(value: unknown): import('@/types/api').MediaCatalogDetail {
  const source = value && typeof value === 'object' ? value as Record<string, unknown> : {}
  const work = source.work && typeof source.work === 'object' ? source.work as Record<string, unknown> : {}
  const item = {
    ...work,
    id: typeof work.id === 'string' ? work.id : '', title: typeof work.title === 'string' ? work.title : '未命名作品',
    kind: work.kind === 'series' ? 'series' as const : 'movie' as const,
    file_count: finiteNumber(work.file_count), season_count: finiteNumber(work.season_count), episode_count: finiteNumber(work.episode_count),
    size: finiteNumber(work.size), modified_at: typeof work.modified_at === 'string' ? work.modified_at : '', category_name: typeof work.category_name === 'string' ? work.category_name : '',
    match_status: typeof work.match_status === 'string' ? work.match_status : 'unrecognized', manual_override: work.manual_override === true,
    library_works: arrayOf(work.library_works).filter(isRecord).map(entry => ({ library_id: finiteNumber(entry.library_id), library_name: text(entry.library_name), work_id: text(entry.work_id), file_count: finiteNumber(entry.file_count) })),
  } as import('@/types/api').MediaCatalogItem
  const episodes = (input: unknown): import('@/types/api').MediaCatalogEpisode[] => arrayOf(input).filter(isRecord).map(entry => ({ id: finiteNumber(entry.id), title: text(entry.title) || '未命名文件', season: nullableNumber(entry.season), episode: nullableNumber(entry.episode), relative_path: text(entry.relative_path), size: finiteNumber(entry.size), modified_at: text(entry.modified_at) }))
  return { work: item, seasons: arrayOf(source.seasons).filter(isRecord).map(season => ({ number: finiteNumber(season.number), episodes: episodes(season.episodes) })), files: episodes(source.files), reorganizable_transfers: arrayOf(source.reorganizable_transfers).filter(isRecord).map(item => ({ transfer_task_id: text(item.transfer_task_id), download_task_id: text(item.download_task_id), identity_revision: finiteNumber(item.identity_revision), file_count: finiteNumber(item.file_count) })) }
}

function arrayOf(value: unknown): unknown[] { return Array.isArray(value) ? value : [] }
function isRecord(value: unknown): value is Record<string, unknown> { return !!value && typeof value === 'object' && !Array.isArray(value) }
function finiteNumber(value: unknown): number { return typeof value === 'number' && Number.isFinite(value) ? value : 0 }
function nullableNumber(value: unknown): number | null { return value == null ? null : finiteNumber(value) }
function text(value: unknown): string { return typeof value === 'string' ? value : '' }

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
