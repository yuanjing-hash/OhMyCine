import type { RawMediaCandidate } from './types'
import { SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME } from './classificationRules'
import { cleanMediaTitle, normalizeTitleKey } from './parser'
import { stripFileExtension } from './pathUtils'

export const RAW_MOVIE_CATEGORY_NAME = '电影'
export const RAW_TV_CATEGORY_NAME = '剧集'
export const RAW_UNRESOLVED_CATEGORY_NAME = '未识别'

const GENERIC_LIBRARY_HINTS = new Set([
  'movie',
  'movies',
  'film',
  'films',
  'cinema',
  'video',
  'videos',
  'tv',
  'tv series',
  'series',
  'show',
  'shows',
  'episode',
  'episodes',
  'media',
  'library',
  'librarys',
  'libraries',
  '影视',
  '影视库',
  '片库',
  '媒体库',
  '视频',
  '影片',
])

export function deriveRawCandidateCategoryName(candidate: RawMediaCandidate): string {
  const categoryHint = normalizeReasonableCategoryHint(candidate)
  if (categoryHint)
    return categoryHint

  switch (candidate.kind) {
    case 'movie':
      return RAW_MOVIE_CATEGORY_NAME
    case 'episode':
    case 'tv':
      return RAW_TV_CATEGORY_NAME
    case 'unresolved':
      return RAW_UNRESOLVED_CATEGORY_NAME
    default:
      return SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME
  }
}

export function normalizeReasonableCategoryHint(candidate: RawMediaCandidate): string | undefined {
  const rawHint = candidate.categoryHint?.trim()
  if (!rawHint)
    return undefined

  const hint = cleanMediaTitle(rawHint)
  if (!hint)
    return undefined

  const normalizedHint = normalizeTitleKey(hint)
  if (GENERIC_LIBRARY_HINTS.has(normalizedHint))
    return undefined

  const titleKeys = [
    candidate.title,
    candidate.seriesTitle,
    candidate.normalizedTitle,
    stripFileExtension(candidate.record.fileName),
  ]
    .map(value => value ? normalizeTitleKey(value) : '')
    .filter(Boolean)

  if (titleKeys.includes(normalizedHint))
    return undefined

  return hint
}
