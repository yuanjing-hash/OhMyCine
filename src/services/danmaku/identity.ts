import { isVideoFileName } from '@/services/scraper/pathUtils'
import { inferDanmakuEpisode, safeMediaName } from './client'

export interface DanmakuMediaIdentityInput {
  mediaTitle: string
  fileName: string
  seriesName?: string
  seasonNumber?: number
  episodeNumber?: number
}

export interface DanmakuMediaIdentity {
  matchName: string
  searchTitle: string
  episode: string
}

export function resolveDanmakuMediaIdentity(input: DanmakuMediaIdentityInput): DanmakuMediaIdentity {
  const seriesName = safeLogicalTitle(input.seriesName)
  const fileStem = seriesName || !isSafeVideoFileHint(input.fileName) ? '' : safeMediaName(input.fileName)
  const mediaTitle = safeLogicalTitle(input.mediaTitle) || '未命名影片'
  const episode = positiveIntegerText(input.episodeNumber) || inferDanmakuEpisode(fileStem || mediaTitle)
  const matchName = buildStructuredEpisodeName(seriesName, input.seasonNumber, episode) || seriesName || fileStem || mediaTitle
  return {
    matchName,
    searchTitle: seriesName || inferSeriesTitle(fileStem) || mediaTitle,
    episode,
  }
}

function isSafeVideoFileHint(value: string): boolean {
  const trimmed = value.trim()
  return Boolean(trimmed)
    && !/^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed)
    && !trimmed.includes('?')
    && !trimmed.includes('#')
    && isVideoFileName(trimmed)
}

function buildStructuredEpisodeName(seriesName: string, seasonNumber: number | undefined, episode: string): string {
  if (!seriesName || !episode)
    return ''
  const season = Number.isSafeInteger(seasonNumber) && Number(seasonNumber) > 0
    ? `S${String(seasonNumber).padStart(2, '0')}`
    : ''
  return `${seriesName}.${season}E${episode.padStart(2, '0')}`
}

function inferSeriesTitle(fileStem: string): string {
  if (!fileStem)
    return ''
  const markers = [
    /[ ._-]+S\d{1,3}[ ._-]*EP?[ ._-]*\d{1,4}\b/i,
    /[ ._-]+EP?[ ._-]*\d{1,4}\b/i,
    /第\s*\d{1,4}\s*[集话]/,
  ]
  const markerIndex = markers.reduce((earliest, pattern) => {
    const index = fileStem.search(pattern)
    return index >= 0 && (earliest < 0 || index < earliest) ? index : earliest
  }, -1)
  return (markerIndex >= 0 ? fileStem.slice(0, markerIndex) : fileStem).trim()
}

function safeLogicalTitle(value: string | undefined): string {
  const title = value?.replace(/\s+/g, ' ').trim() ?? ''
  if (!title || title.includes('://') || title.includes('\\') || title.startsWith('/') || /^[A-Z]:/i.test(title))
    return ''
  return title.slice(0, 160)
}

function positiveIntegerText(value: number | undefined): string {
  return Number.isSafeInteger(value) && Number(value) > 0 ? String(value) : ''
}
