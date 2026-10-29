import type { ServerDataSource } from '@/services/datasource/server'
import type { ServerResourceItem } from '@/services/serverDiscovery'
import type { ShareContentAnalysis } from '@/services/serverShareContent'
import { ServerRequestError } from '@/services/datasource/server'
import { parseShareContentAnalysis } from '@/services/serverShareContent'

export interface ServerResourceRecognition {
  status: 'matched' | 'unrecognized'
  title: string
  mediaType?: 'movie' | 'tv'
  tmdbId?: number
  year?: number
  episodes?: { season?: number, minimum?: number, maximum?: number }
  shareAnalysis?: ShareContentAnalysis
}

export function isServerCloudResource(item: ServerResourceItem): boolean {
  if (item.sourceKind === 'bt' || item.sourceKind === 'pt' || item.payloadKind === 'torrent' || item.payloadKind === 'magnet')
    return false
  return item.sourceKind === '115_share' || item.sourceKind === 'quark_share' || !!item.cloudProvider
    || (!!item.resultType && item.resultType !== 'magnet' && item.resultType !== 'ed2k')
}

export async function recognizeServerResource(source: ServerDataSource, token: string, titleOnly = false): Promise<ServerResourceRecognition> {
  try {
    return parseServerResourceRecognition(await source.recognizeDiscoveryResource(token, titleOnly))
  }
  catch (reason) {
    if (reason instanceof ServerRequestError && (reason.status === 405 || (reason.status === 404 && !reason.code)))
      throw new Error('当前 Server 不支持资源文件分析，请升级 Server 后重新检测。')
    throw reason
  }
}

export function parseServerResourceRecognition(value: unknown): ServerResourceRecognition {
  if (!value || typeof value !== 'object')
    throw new Error('Server 返回的资源识别信息无效。')
  const data = value as Record<string, unknown>
  if ((data.status !== 'matched' && data.status !== 'unrecognized') || typeof data.title !== 'string' || data.title.length > 512)
    throw new Error('Server 返回的资源识别信息无效。')
  const rawEpisodes = data.episodes && typeof data.episodes === 'object' ? data.episodes as Record<string, unknown> : {}
  const integer = (value: unknown, minimum: number, maximum: number): number | undefined => typeof value === 'number' && Number.isSafeInteger(value) && value >= minimum && value <= maximum ? value : undefined
  const minimum = integer(rawEpisodes.episode_min, 1, 100000)
  const maximum = integer(rawEpisodes.episode_max, minimum ?? 1, 100000)
  const shareAnalysis = parseShareContentAnalysis(data.share_analysis)
  if (data.share_analysis !== undefined && !shareAnalysis)
    throw new Error('Server 返回的分享文件分析无效或已过期，请重新检测。')
  return {
    status: data.status,
    title: data.title,
    mediaType: data.media_type === 'movie' || data.media_type === 'tv' ? data.media_type : undefined,
    tmdbId: integer(data.tmdb_id, 1, Number.MAX_SAFE_INTEGER),
    year: integer(data.year, 1888, 2200),
    episodes: { season: integer(rawEpisodes.season, 0, 200), minimum, maximum },
    shareAnalysis,
  }
}

export function serverTitleEpisodeLabel(result: ServerResourceRecognition): string {
  if (result.mediaType !== 'tv')
    return ''
  const facts = result.episodes
  return [facts?.season !== undefined ? `第 ${facts.season} 季` : '', facts?.minimum ? `第 ${facts.minimum}${facts.maximum && facts.maximum !== facts.minimum ? `–${facts.maximum}` : ''} 集` : ''].filter(Boolean).join(' · ')
}
