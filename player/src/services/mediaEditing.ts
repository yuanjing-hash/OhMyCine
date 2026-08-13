import type { DataSourceConfig, EditableMediaMetadata, MediaDetail, SubtitleSearchResult } from '@/services/datasource/types'
import type { MediaItemActionTarget } from '@/services/mediaActions'
import type { RawFileSourceType, RawLocalScanCache, RawMediaCandidate, TmdbImageKind } from '@/services/scraper'
import type { SubtitleLanguage } from '@/services/subtitle'
import { open } from '@tauri-apps/plugin-dialog'
import { getMediaPlaybackPreference, saveMediaPlaybackPreference } from '@/services/mediaPlaybackPreferences'
import { applyRawManualArtworkOverride, applyRawManualMetadataOverride, loadRawSourceScanCache, saveRawSourceScanCache } from '@/services/scraper'
import { listRawScannedChildren } from '@/services/scraper/rawHomeMapping'
import { describeLocalSubtitleSearchProviders, downloadLocalSubtitle, importLocalSubtitle, searchLocalSubtitles } from '@/services/subtitle'

const RAW_TYPES = new Set<RawFileSourceType>(['alist', 'clouddrive2', 'webdav', 'local', '115', '123', 'quark'])

export interface RawEditableContext {
  readonly cache: RawLocalScanCache
  readonly candidate: RawMediaCandidate
}

export async function loadRawEditableContext(target: MediaItemActionTarget, config: DataSourceConfig | undefined): Promise<RawEditableContext | null> {
  if (!config || !RAW_TYPES.has(config.type as RawFileSourceType))
    return null
  const sourceType = config.type as RawFileSourceType
  const rootPath = typeof config.extra?.rootPath === 'string' ? config.extra.rootPath : '/'
  const cache = await loadRawSourceScanCache(config.id, sourceType, rootPath)
  if (!cache)
    return null
  const direct = cache.candidates.find(candidate => candidate.record.providerPath === target.itemId || candidate.record.id === target.itemId)
  if (direct)
    return { cache, candidate: direct }
  const playableId = firstPlayableRawChildId(cache, target.itemId)
  const candidate = playableId ? cache.candidates.find(entry => entry.record.providerPath === playableId) : undefined
  return candidate ? { cache, candidate } : null
}

export async function saveRawMetadata(context: RawEditableContext, metadata: EditableMediaMetadata): Promise<RawLocalScanCache> {
  const next = applyRawManualMetadataOverride(context.cache, {
    targetRecordId: context.candidate.record.id,
    title: metadata.name,
    originalTitle: metadata.originalTitle,
    overview: metadata.overview,
    releaseYear: metadata.year,
    rating: metadata.rating,
    genres: metadata.genres,
  })
  if (!await saveRawSourceScanCache(next))
    throw new Error('Player 本地元数据缓存写入失败。')
  return next
}

export async function saveRawArtwork(context: RawEditableContext, kind: Extract<TmdbImageKind, 'poster' | 'logo' | 'backdrop'>, imageUrl?: string): Promise<RawLocalScanCache> {
  const next = applyRawManualArtworkOverride(context.cache, {
    targetRecordId: context.candidate.record.id,
    kind,
    imageUrl: sanitizePublicImageUrl(imageUrl),
  })
  if (!await saveRawSourceScanCache(next))
    throw new Error('Player 本地图片覆盖写入失败。')
  return next
}

export async function describeMediaSubtitleProviders(): Promise<string> {
  return describeLocalSubtitleSearchProviders({})
}

export async function searchMediaSubtitles(detail: MediaDetail, language: SubtitleLanguage, keyword: string): Promise<SubtitleSearchResult[]> {
  return searchLocalSubtitles({
    itemId: detail.id,
    title: keyword.trim() || detail.seriesName || detail.name,
    language,
    keywordMode: 'custom',
    year: detail.year,
    mediaType: detail.type,
    seasonNumber: detail.seasonNumber,
    episodeNumber: detail.episodeNumber,
    imdbId: detail.imdbId,
    tmdbId: detail.tmdbId,
    originalTitle: detail.originalTitle,
    seriesName: detail.seriesName,
    duration: detail.duration,
  })
}

export async function downloadAndSelectLocalSubtitle(target: MediaItemActionTarget, result: SubtitleSearchResult): Promise<void> {
  const owner = { sourceId: target.sourceId, mediaIdentity: target.itemId }
  const downloaded = await downloadLocalSubtitle(result, owner)
  await selectCachedSubtitle(target, downloaded.path, downloaded.title, downloaded.language, downloaded.format)
}

export async function importAndSelectLocalSubtitle(target: MediaItemActionTarget): Promise<boolean> {
  const selected = await open({
    multiple: false,
    directory: false,
    title: '导入字幕到 Player 缓存',
    filters: [{ name: '字幕文件', extensions: ['srt', 'ass', 'ssa', 'vtt', 'sub'] }],
  })
  if (typeof selected !== 'string' || !selected.trim())
    return false
  const imported = await importLocalSubtitle(selected, { sourceId: target.sourceId, mediaIdentity: target.itemId })
  await selectCachedSubtitle(target, imported.path, imported.title, imported.language, imported.format)
  return true
}

export async function clearSelectedLocalSubtitle(target: MediaItemActionTarget): Promise<void> {
  const current = await getMediaPlaybackPreference({ sourceId: target.sourceId, mediaIdentity: target.itemId })
  await saveMediaPlaybackPreference({
    sourceId: target.sourceId,
    mediaIdentity: target.itemId,
    subtitle: { kind: 'off' },
    audio: current?.audio,
    subtitleDelay: current?.subtitleDelay ?? 0,
    playbackSpeed: current?.playbackSpeed ?? 1,
    videoBrightness: current?.videoBrightness ?? 0,
    aspectMode: current?.aspectMode ?? 'default',
    fitMode: current?.fitMode ?? 'fit',
  })
}

async function selectCachedSubtitle(target: MediaItemActionTarget, path: string, title: string, language?: string, codec?: string): Promise<void> {
  const current = await getMediaPlaybackPreference({ sourceId: target.sourceId, mediaIdentity: target.itemId })
  const saved = await saveMediaPlaybackPreference({
    sourceId: target.sourceId,
    mediaIdentity: target.itemId,
    subtitle: { kind: 'cachedExternal', cachedPath: path, track: { title, language, codec } },
    audio: current?.audio,
    subtitleDelay: current?.subtitleDelay ?? 0,
    playbackSpeed: current?.playbackSpeed ?? 1,
    videoBrightness: current?.videoBrightness ?? 0,
    aspectMode: current?.aspectMode ?? 'default',
    fitMode: current?.fitMode ?? 'fit',
  })
  if (!saved)
    throw new Error('字幕已下载，但无法保存为该媒体的默认字幕。')
}

function firstPlayableRawChildId(cache: RawLocalScanCache, id: string): string | null {
  const children = listRawScannedChildren(cache, id) ?? []
  for (const child of children) {
    if (cache.candidates.some(candidate => candidate.record.providerPath === child.id))
      return child.id
    const nested = firstPlayableRawChildId(cache, child.id)
    if (nested)
      return nested
  }
  return null
}

function sanitizePublicImageUrl(value: string | undefined): string | undefined {
  const text = value?.trim()
  if (!text)
    return undefined
  let url: URL
  try {
    url = new URL(text)
  }
  catch {
    throw new Error('请输入有效的 HTTP(S) 图片地址。')
  }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password)
    throw new Error('图片地址只支持不含账号信息的 HTTP(S) URL。')
  for (const key of url.searchParams.keys()) {
    if (/token|key|auth|cookie|password|signature|credential|^sig$|^exp$/i.test(key))
      throw new Error('图片地址包含敏感签名参数，不能写入本地元数据缓存。')
  }
  return url.toString()
}
