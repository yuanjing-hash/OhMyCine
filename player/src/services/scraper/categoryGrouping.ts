import type { RawCategoryAssignment, RawMediaCandidate } from './types'
import { SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME } from './classificationRules'
import { cleanMediaTitle, extractMediaSearchTitles, normalizeTitleKey } from './parser'
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
  'download',
  'downloads',
  'complete',
  'completed',
  'incoming',
  'temp',
  'tmp',
  '影视',
  '影视库',
  '片库',
  '媒体库',
  '视频',
  '影片',
  '下载',
  '下载完成',
  '未整理',
  '待整理',
  '临时',
])

const EXPLICIT_CATEGORY_HINTS = new Set([
  '电影',
  '剧集',
  '电视剧',
  '华语电影',
  '外语电影',
  '动画电影',
  '纪录片',
  '纪录',
  '综艺',
  '儿童',
  '动漫',
  '国漫',
  '日番',
  '国产动漫',
  '国产剧',
  '欧美剧',
  '美剧',
  '日韩剧',
  '日剧',
  '韩剧',
  '短剧',
].map(value => normalizeTitleKey(value)))

export function deriveRawCandidateCategoryName(candidate: RawMediaCandidate): string {
  return deriveRawCandidateCategoryAssignment(candidate).categoryName
}

export function deriveRawCandidateCategoryAssignment(candidate: RawMediaCandidate): RawCategoryAssignment {
  const categoryHint = normalizeExplicitCategoryHint(candidate)
  if (categoryHint) {
    return {
      categoryName: categoryHint,
      source: 'pathHint',
    }
  }

  switch (candidate.kind) {
    case 'movie':
      return { categoryName: RAW_MOVIE_CATEGORY_NAME, source: 'kindFallback' }
    case 'episode':
    case 'tv':
      return { categoryName: RAW_TV_CATEGORY_NAME, source: 'kindFallback' }
    case 'unresolved':
      return { categoryName: RAW_UNRESOLVED_CATEGORY_NAME, source: 'kindFallback' }
    default:
      return { categoryName: SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME, source: 'kindFallback' }
  }
}

export function normalizeExplicitCategoryHint(candidate: RawMediaCandidate): string | undefined {
  const hint = normalizeReasonableCategoryHint(candidate)
  if (!hint)
    return undefined

  const normalizedHint = normalizeTitleKey(hint)
  const hasStructuralCategorySignal = candidate.signals.includes('category-title-season-hierarchy')
  if (!hasStructuralCategorySignal && !EXPLICIT_CATEGORY_HINTS.has(normalizedHint))
    return undefined

  return hint
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

  if (isLikelyWorkTitleHint(normalizedHint, candidate))
    return undefined

  return hint
}

function isLikelyWorkTitleHint(normalizedHint: string, candidate: RawMediaCandidate): boolean {
  const titleKeys = [
    candidate.title,
    candidate.seriesTitle,
    candidate.normalizedTitle,
    stripFileExtension(candidate.record.fileName),
    ...extractMediaSearchTitles(candidate.title),
    ...extractMediaSearchTitles(candidate.seriesTitle ?? ''),
    ...extractMediaSearchTitles(stripFileExtension(candidate.record.fileName)),
  ]
    .map(value => value ? normalizeTitleKey(value) : '')
    .filter(Boolean)

  return titleKeys.some((titleKey) => {
    if (titleKey === normalizedHint)
      return true

    const compactHint = compactTitleKey(normalizedHint)
    const compactTitle = compactTitleKey(titleKey)
    if (!compactHint || !compactTitle)
      return false
    if (compactHint === compactTitle)
      return true
    if (compactHint.length >= 4 && compactTitle.length >= 4) {
      const shorter = Math.min(compactHint.length, compactTitle.length)
      const longer = Math.max(compactHint.length, compactTitle.length)
      if ((compactTitle.includes(compactHint) || compactHint.includes(compactTitle)) && shorter / longer >= 0.55)
        return true
    }

    const hintTokens = tokenizeTitleKey(normalizedHint)
    const titleTokens = tokenizeTitleKey(titleKey)
    if (hintTokens.length === 0 || titleTokens.length === 0)
      return false

    const overlap = hintTokens.filter(token => titleTokens.includes(token)).length
    return overlap / Math.max(hintTokens.length, titleTokens.length) >= 0.6
  })
}

function compactTitleKey(value: string): string {
  return value.replace(/[^a-z0-9\u4E00-\u9FFF]+/g, '')
}

function tokenizeTitleKey(value: string): string[] {
  return value
    .split(/[^a-z0-9\u4E00-\u9FFF]+/g)
    .map(token => token.trim())
    .filter(token => token.length >= 2)
}
