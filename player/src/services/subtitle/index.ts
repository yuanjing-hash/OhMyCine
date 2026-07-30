import type { LocalSubtitleDownloadResult, LocalSubtitleSearchInput, SubtitleProvider } from './types'
import type { SubtitleSearchResult } from '@/services/datasource/types'
import { HashSubtitleProvider } from './hashProviders'
import { OpenSubtitlesProvider } from './opensubtitles'
import { loadSubtitleSearchSettings } from './settings'

const providers: Record<'opensubtitles' | 'shooter' | 'xunlei', SubtitleProvider> = {
  opensubtitles: new OpenSubtitlesProvider(),
  shooter: new HashSubtitleProvider('shooter', '射手网'),
  xunlei: new HashSubtitleProvider('xunlei', '迅雷字幕'),
}

export async function searchLocalSubtitles(input: LocalSubtitleSearchInput): Promise<SubtitleSearchResult[]> {
  const settings = loadSubtitleSearchSettings()
  const enabledProviders = [
    settings.openSubtitlesEnabled ? providers.opensubtitles : null,
    settings.shooterEnabled ? providers.shooter : null,
    settings.xunleiEnabled ? providers.xunlei : null,
  ].filter((provider): provider is SubtitleProvider => provider != null)
  if (enabledProviders.length === 0)
    throw new Error('当前没有启用的 Player 本地字幕提供器，请先到“设置 → 播放与字幕”启用。')

  const settled = await Promise.allSettled(enabledProviders.map(provider => provider.search(input)))
  const results = settled.flatMap(result => result.status === 'fulfilled' ? result.value : [])
  if (results.length > 0)
    return results
  const failure = settled.find(result => result.status === 'rejected')
  if (failure?.status === 'rejected')
    throw failure.reason
  return []
}

export async function downloadLocalSubtitle(result: SubtitleSearchResult): Promise<LocalSubtitleDownloadResult> {
  const providerId = result.id.split(':', 1)[0]
  const provider = Object.values(providers).find(candidate => candidate.id === providerId)
  if (!provider)
    throw new Error('找不到该字幕结果对应的 Player 提供器。')
  return provider.download(result)
}

export * from './settings'
export * from './types'
