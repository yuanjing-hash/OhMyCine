import type { LocalSubtitleDownloadResult, LocalSubtitleSearchInput, SubtitleCacheOwner, SubtitleProvider } from './types'
import type { SubtitleSearchResult } from '@/services/datasource/types'
import { invoke } from '@tauri-apps/api/core'
import { subtitleKeywordUsesMediaIdentity } from './mediaContext'

type HashSubtitleProviderId = 'shooter' | 'xunlei'

interface DownloadedSubtitle {
  path: string
}

export class HashSubtitleProvider implements SubtitleProvider {
  readonly id: HashSubtitleProviderId
  readonly name: string

  constructor(id: HashSubtitleProviderId, name: string) {
    this.id = id
    this.name = name
  }

  async search(input: LocalSubtitleSearchInput): Promise<SubtitleSearchResult[]> {
    const query = input.title?.trim() || input.mediaFileName?.trim()
    if (this.id === 'shooter' && !input.localFilePath && !input.remoteMediaUrl)
      return []
    if (this.id === 'xunlei' && !query)
      return []

    const useMediaIdentity = subtitleKeywordUsesMediaIdentity(input.keywordMode)
    return invoke<SubtitleSearchResult[]>('subtitle_search_hash_provider', {
      request: {
        provider: this.id,
        query: query ?? null,
        originalTitle: useMediaIdentity ? input.originalTitle ?? null : null,
        seriesName: input.seriesName ?? null,
        durationSeconds: useMediaIdentity ? input.duration ?? null : null,
        keywordMode: input.keywordMode ?? null,
        year: useMediaIdentity ? input.year ?? null : null,
        mediaType: input.mediaType ?? null,
        seasonNumber: useMediaIdentity ? input.seasonNumber ?? null : null,
        episodeNumber: useMediaIdentity ? input.episodeNumber ?? null : null,
        filePath: input.localFilePath ?? null,
        remoteUrl: input.remoteMediaUrl ?? null,
        headers: toHeaderPayload(input.remoteMediaHeaders),
        fileName: input.mediaFileName ?? null,
        language: input.language,
      },
    })
  }

  async download(result: SubtitleSearchResult, cacheOwner?: SubtitleCacheOwner): Promise<LocalSubtitleDownloadResult> {
    if (!result.downloadRef)
      throw new Error(`该${this.name}结果已经过期，请重新搜索。`)

    const downloaded = await invoke<DownloadedSubtitle>('subtitle_download_hash_provider', {
      request: {
        provider: this.id,
        downloadRef: result.downloadRef,
        cacheOwner: cacheOwner ?? null,
      },
    })
    return {
      path: downloaded.path,
      title: result.title,
      language: result.language,
      format: result.format,
    }
  }
}

function toHeaderPayload(headers: Record<string, string> | undefined) {
  if (!headers)
    return []
  return Object.entries(headers)
    .filter(([name, value]) => name.trim() && value.trim())
    .map(([name, value]) => ({ name, value }))
}
