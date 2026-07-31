import type { MediaItem, SubtitleSearchInput, SubtitleSearchResult } from '@/services/datasource/types'

export type SubtitleLanguage = 'zh-CN' | 'zh-TW' | 'en' | 'ja' | 'ko'
export type SubtitleKeywordMode = 'mediaTitle' | 'fileName' | 'custom'

export interface LocalSubtitleSearchInput extends Omit<SubtitleSearchInput, 'language'> {
  language: SubtitleLanguage
  localFilePath?: string
  remoteMediaUrl?: string
  remoteMediaHeaders?: Record<string, string>
  mediaFileName?: string
  originalTitle?: string
  seriesName?: string
  duration?: number
  keywordMode?: SubtitleKeywordMode
}

export interface LocalSubtitleDownloadResult {
  path: string
  title: string
  language: string
  format?: string
}

export interface SubtitleProvider {
  readonly id: string
  readonly name: string
  search: (input: LocalSubtitleSearchInput) => Promise<SubtitleSearchResult[]>
  download: (result: SubtitleSearchResult) => Promise<LocalSubtitleDownloadResult>
}

export interface SubtitleSearchMediaContext {
  itemId: string
  title: string
  localFilePath?: string
  remoteMediaUrl?: string
  remoteMediaHeaders?: Record<string, string>
  mediaFileName?: string
  mediaSourceId?: string
  originalTitle?: string
  seriesName?: string
  duration?: number
  keywordMode?: SubtitleKeywordMode
  year?: number
  mediaType?: MediaItem['type']
  seasonNumber?: number
  episodeNumber?: number
  imdbId?: string
  tmdbId?: number
}
