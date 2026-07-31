import type { LocalSubtitleDownloadResult, LocalSubtitleSearchInput, SubtitleCacheOwner, SubtitleProvider } from './types'
import type { SubtitleSearchResult } from '@/services/datasource/types'
import { HashSubtitleProvider } from './hashProviders'
import { OpenSubtitlesProvider } from './opensubtitles'
import { loadSubtitleSearchSettings, readOpenSubtitlesCredentials } from './settings'

const providers: Record<'opensubtitles' | 'shooter' | 'xunlei', SubtitleProvider> = {
  opensubtitles: new OpenSubtitlesProvider(),
  shooter: new HashSubtitleProvider('shooter', '射手网'),
  xunlei: new HashSubtitleProvider('xunlei', '迅雷字幕'),
}

export async function searchLocalSubtitles(input: LocalSubtitleSearchInput): Promise<SubtitleSearchResult[]> {
  const settings = loadSubtitleSearchSettings()
  const openSubtitlesCredential = await readOpenSubtitlesCredentials()
  const hasOpenSubtitlesCredential = Boolean(openSubtitlesCredential)
  const openSubtitlesActive = settings.openSubtitlesEnabled && hasOpenSubtitlesCredential
  const canUseHashProviders = Boolean(input.localFilePath || input.remoteMediaUrl)
  const hasKeywordProvider = openSubtitlesActive || settings.xunleiEnabled
  const enabledProviders = [
    openSubtitlesActive ? providers.opensubtitles : null,
    settings.shooterEnabled && canUseHashProviders ? providers.shooter : null,
    settings.xunleiEnabled ? providers.xunlei : null,
  ].filter((provider): provider is SubtitleProvider => provider != null)

  if (enabledProviders.length === 0 && settings.openSubtitlesEnabled && !hasOpenSubtitlesCredential) {
    if (!canUseHashProviders && (settings.shooterEnabled || settings.xunleiEnabled))
      throw new Error('当前媒体没有可供 Player 读取哈希的播放地址；请配置 OpenSubtitles 后重试。')
    throw new Error('尚未配置可用于当前媒体的字幕提供器。可配置 OpenSubtitles，或为本地视频启用射手网、迅雷字幕。')
  }
  if (enabledProviders.length === 0) {
    throw new Error(canUseHashProviders
      ? '当前没有启用的 Player 本地字幕提供器，请先到“设置 → 播放与字幕”启用。'
      : '当前媒体没有可供 Player 读取哈希的播放地址。')
  }

  const settled = await Promise.allSettled(enabledProviders.map(provider => provider.search(input)))
  const results = settled.flatMap(result => result.status === 'fulfilled' ? result.value : [])
  if (results.length > 0)
    return results
  const failure = settled.find(result => result.status === 'rejected')
  if (failure?.status === 'rejected')
    throw failure.reason
  if (!hasKeywordProvider) {
    const hashSearchSummary = canUseHashProviders && settings.shooterEnabled
      ? '射手网已按当前视频文件哈希查询，但没有命中字幕。'
      : '当前媒体无法进行射手网文件哈希查询。'
    if (hasOpenSubtitlesCredential) {
      throw new Error(`${hashSearchSummary} OpenSubtitles 已配置但当前处于关闭状态，输入的关键词没有被查询；请到“设置 → 播放与字幕”启用 OpenSubtitles。`)
    }
    throw new Error(`${hashSearchSummary} 当前未配置 OpenSubtitles，因此输入的关键词没有被查询。`)
  }
  return []
}

export async function downloadLocalSubtitle(result: SubtitleSearchResult, cacheOwner?: SubtitleCacheOwner): Promise<LocalSubtitleDownloadResult> {
  const providerId = result.id.split(':', 1)[0]
  const provider = Object.values(providers).find(candidate => candidate.id === providerId)
  if (!provider)
    throw new Error('找不到该字幕结果对应的 Player 提供器。')
  return provider.download(result, cacheOwner)
}

export * from './settings'
export * from './types'
