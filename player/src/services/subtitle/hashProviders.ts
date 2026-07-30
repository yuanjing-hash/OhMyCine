import type { LocalSubtitleDownloadResult, LocalSubtitleSearchInput, SubtitleProvider } from './types'
import type { SubtitleSearchResult } from '@/services/datasource/types'
import { invoke } from '@tauri-apps/api/core'

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
    if (!input.localFilePath)
      return []

    return invoke<SubtitleSearchResult[]>('subtitle_search_hash_provider', {
      request: {
        provider: this.id,
        filePath: input.localFilePath,
        language: input.language,
      },
    })
  }

  async download(result: SubtitleSearchResult): Promise<LocalSubtitleDownloadResult> {
    if (!result.downloadRef)
      throw new Error(`该${this.name}结果已经过期，请重新搜索。`)

    const downloaded = await invoke<DownloadedSubtitle>('subtitle_download_hash_provider', {
      request: {
        provider: this.id,
        downloadRef: result.downloadRef,
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
