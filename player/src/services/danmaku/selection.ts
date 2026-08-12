import type { DanmakuMatch, DanmakuSearchResponse } from './types'

/**
 * Selects a structured-search result only when the provider returned one
 * unambiguous exact-title group and that group contains one filtered episode.
 */
export function selectExactStructuredDanmakuMatch(
  response: DanmakuSearchResponse,
  requestedSeriesTitle: string,
): DanmakuMatch | null {
  const requestedTitle = normalizeDanmakuSeriesTitle(requestedSeriesTitle)
  if (!requestedTitle)
    return null

  const exactGroups = response.animes.filter(
    anime => normalizeDanmakuSeriesTitle(anime.animeTitle) === requestedTitle,
  )
  if (exactGroups.length !== 1 || exactGroups[0].episodes.length !== 1)
    return null

  const anime = exactGroups[0]
  const episode = anime.episodes[0]
  return {
    episodeId: episode.episodeId,
    animeId: anime.animeId,
    animeTitle: anime.animeTitle,
    episodeTitle: episode.episodeTitle,
    shift: 0,
  }
}

export function normalizeDanmakuSeriesTitle(value: string): string {
  return value.normalize('NFKC').trim().toLowerCase().replace(/[\s·・･•‧∙]+/g, '')
}
