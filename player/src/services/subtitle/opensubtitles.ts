import type { LocalSubtitleDownloadResult, LocalSubtitleSearchInput, SubtitleProvider } from './types'
import type { SubtitleSearchResult } from '@/services/datasource/types'
import { invoke } from '@tauri-apps/api/core'
import { readOpenSubtitlesApiKey } from './settings'

interface OpenSubtitlesSearchResponse {
  data?: unknown[]
}

interface OpenSubtitlesRecord {
  id?: string
  attributes?: {
    language?: string
    download_count?: number
    hearing_impaired?: boolean
    machine_translated?: boolean
    ai_translated?: boolean
    foreign_parts_only?: boolean
    ratings?: number
    release?: string
    comments?: string
    uploader?: { name?: string }
    files?: Array<{ file_id?: number, file_name?: string }>
  }
}

interface DownloadedSubtitle {
  path: string
}

export class OpenSubtitlesProvider implements SubtitleProvider {
  readonly id = 'opensubtitles'
  readonly name = 'OpenSubtitles'

  async search(input: LocalSubtitleSearchInput): Promise<SubtitleSearchResult[]> {
    const apiKey = await requiredApiKey()
    const payload = await invoke<OpenSubtitlesSearchResponse>('subtitle_search_opensubtitles', {
      request: {
        apiKey,
        language: toOpenSubtitlesLanguage(input.language),
        query: input.title,
        imdbId: input.imdbId,
        tmdbId: input.tmdbId,
        year: input.year,
        seasonNumber: input.seasonNumber,
        episodeNumber: input.episodeNumber,
        mediaType: input.mediaType,
      },
    })

    return (payload.data ?? [])
      .map(parseRecord)
      .filter((result): result is SubtitleSearchResult => result != null)
  }

  async download(result: SubtitleSearchResult): Promise<LocalSubtitleDownloadResult> {
    const apiKey = await requiredApiKey()
    const fileId = Number.parseInt(result.downloadRef ?? '', 10)
    if (!Number.isSafeInteger(fileId) || fileId <= 0)
      throw new Error('该 OpenSubtitles 结果缺少可下载文件。')

    const downloaded = await invoke<DownloadedSubtitle>('subtitle_download_opensubtitles', {
      request: { apiKey, fileId },
    })
    return {
      path: downloaded.path,
      title: result.title,
      language: result.language,
      format: result.format,
    }
  }
}

async function requiredApiKey(): Promise<string> {
  const apiKey = await readOpenSubtitlesApiKey()
  if (!apiKey)
    throw new Error('尚未配置 OpenSubtitles API Key，请先到“设置 → 播放与字幕”完成配置。')
  return apiKey
}

function parseRecord(value: unknown): SubtitleSearchResult | null {
  if (!isObject(value))
    return null
  const record = value as OpenSubtitlesRecord
  const attributes = record.attributes
  const file = attributes?.files?.find(candidate => typeof candidate.file_id === 'number')
  if (!attributes || !file?.file_id)
    return null
  const title = attributes.release?.trim() || file.file_name?.trim() || `OpenSubtitles #${record.id ?? file.file_id}`
  return {
    id: `opensubtitles:${record.id ?? file.file_id}:${file.file_id}`,
    origin: 'local',
    providerName: 'OpenSubtitles',
    language: attributes.language?.trim() || 'unknown',
    title,
    format: extensionOf(file.file_name),
    author: attributes.uploader?.name?.trim() || undefined,
    comments: attributes.comments?.trim() || undefined,
    rating: finiteNumber(attributes.ratings),
    downloadCount: finiteInteger(attributes.download_count),
    aiTranslated: attributes.ai_translated,
    machineTranslated: attributes.machine_translated,
    forced: attributes.foreign_parts_only,
    hearingImpaired: attributes.hearing_impaired,
    downloadRef: String(file.file_id),
  }
}

function toOpenSubtitlesLanguage(language: string): string {
  return language === 'zh-CN' ? 'zh-cn' : language === 'zh-TW' ? 'zh-tw' : language.toLowerCase()
}

function extensionOf(fileName: string | undefined): string | undefined {
  const match = fileName?.match(/\.([a-z0-9]{2,5})$/i)
  return match?.[1]?.toLowerCase()
}

function finiteNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function finiteInteger(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isInteger(value) ? value : undefined
}

function isObject(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === 'object' && !Array.isArray(value)
}
