import type { DataSource, DataSourceConfig, HomeSection, MediaDetail, MediaItem } from '@/services/datasource/types'
import type { MediaItemActionTarget } from '@/services/mediaActions'
import type { RawLocalScanCache } from '@/services/scraper/localScanCache'
import { invoke } from '@tauri-apps/api/core'
import { getAppSetting, setAppSetting } from '@/services/appSettings'
import { loadRawSourceScanCache, saveRawSourceScanCache } from '@/services/scraper/localScanCache'
import { getRawScannedMediaDetail, isRawScannedSyntheticId, listRawScannedChildren } from '@/services/scraper/rawHomeMapping'

const TOMBSTONE_KEY = 'ohmycine-media-tombstones-v1'

export interface MediaDeletePlan {
  readonly target: MediaItemActionTarget
  readonly sourceDeleteAvailable: boolean
  readonly sourceDeleteDisabledReason?: string
  readonly providerItemIds: readonly string[]
  readonly pathSummaries: readonly string[]
}

export interface MediaDeleteOutcome {
  readonly succeeded: readonly string[]
  readonly failed: readonly { itemId: string, message: string }[]
}

export async function resolveMediaDeletePlan(target: MediaItemActionTarget, config: DataSourceConfig | undefined): Promise<MediaDeletePlan> {
  const providerItemIds = await resolveProviderItemIds(target, config)
  const sourceType = target.sourceType
  const sourceDeleteAvailable = sourceType != null && ['local', 'emby', 'jellyfin', 'alist', 'webdav', '123', 'quark'].includes(sourceType)
  return {
    target,
    sourceDeleteAvailable,
    sourceDeleteDisabledReason: sourceDeleteAvailable ? undefined : '此来源尚未提供经过安全验证的原生删除接口，只能从 Player 媒体库移除。',
    providerItemIds,
    pathSummaries: providerItemIds.slice(0, 30).map(safePathSummary),
  }
}

export async function executeMediaDelete(plan: MediaDeletePlan, source: DataSource | null, config: DataSourceConfig | undefined, deleteSourceFiles: boolean): Promise<MediaDeleteOutcome> {
  if (!deleteSourceFiles) {
    await addMediaTombstones(plan.target.sourceId, [plan.target.itemId])
    return { succeeded: [plan.target.itemId], failed: [] }
  }
  if (!plan.sourceDeleteAvailable)
    throw new Error(plan.sourceDeleteDisabledReason)

  const succeeded: string[] = []
  const failed: Array<{ itemId: string, message: string }> = []
  const sourceType = plan.target.sourceType
  for (const itemId of plan.providerItemIds) {
    try {
      if (plan.target.sourceType === 'local') {
        await invoke('local_file_delete_owned', { sourceId: plan.target.sourceId, path: itemId })
      }
      else if (sourceType != null && ['alist', 'webdav', '123', 'quark'].includes(sourceType)) {
        await invoke('provider_source_file_delete', {
          request: {
            sourceId: plan.target.sourceId,
            sourceType,
            itemId,
          },
        })
      }
      else {
        if (!source?.deleteMedia)
          throw new Error('媒体服务未提供原生删除接口。')
        await source.deleteMedia(itemId)
      }
      succeeded.push(itemId)
    }
    catch (error) {
      failed.push({ itemId, message: error instanceof Error ? error.message : '删除失败' })
    }
  }
  if (succeeded.length)
    await removeSuccessfulRawRecords(plan, config, succeeded)
  if (!failed.length)
    await addMediaTombstones(plan.target.sourceId, [plan.target.itemId])
  return { succeeded, failed }
}

export function withMediaTombstoneFiltering(source: DataSource): DataSource {
  return new Proxy(source, {
    get(target, property, receiver) {
      const method = Reflect.get(target, property, receiver) as unknown
      if (property === 'list' || property === 'search' || property === 'getFeaturedItems' || property === 'getContinueWatching' || property === 'getRecentlyAdded') {
        if (typeof method !== 'function')
          return method
        return async (...args: unknown[]) => filterMediaItems(source.id, await Reflect.apply(method, target, args) as MediaItem[])
      }
      if (property === 'getHomeSections') {
        if (typeof method !== 'function')
          return method
        return async (...args: unknown[]) => filterHomeSections(source.id, await Reflect.apply(method, target, args) as HomeSection[])
      }
      if (property === 'getDetail') {
        return async (...args: unknown[]) => {
          const detail = await Reflect.apply(Reflect.get(target, property, receiver), target, args) as MediaDetail
          if (isMediaTombstoned(source.id, detail.id))
            throw new Error('此条目已从 Player 媒体库移除。')
          return filterTombstonedMediaDetail(detail, item => isMediaTombstoned(item.sourceId, item.id))
        }
      }
      return typeof method === 'function' ? method.bind(target) : method
    },
  })
}

export function isMediaTombstoned(sourceId: string, itemId: string): boolean {
  return loadTombstones().has(`${sourceId}\0${itemId}`)
}

async function addMediaTombstones(sourceId: string, itemIds: readonly string[]) {
  const tombstones = loadTombstones()
  for (const itemId of itemIds)
    tombstones.add(`${sourceId}\0${itemId}`)
  await setAppSetting(TOMBSTONE_KEY, JSON.stringify([...tombstones]))
}

function loadTombstones(): Set<string> {
  try {
    const value = JSON.parse(getAppSetting(TOMBSTONE_KEY) ?? '[]') as unknown
    return new Set(Array.isArray(value) ? value.filter(item => typeof item === 'string') : [])
  }
  catch { return new Set() }
}

function filterMediaItems(sourceId: string, items: MediaItem[]): MediaItem[] {
  return items.filter(item => !isMediaTombstoned(sourceId, item.id))
}

function filterHomeSections(sourceId: string, sections: HomeSection[]): HomeSection[] {
  return sections.map(section => ({ ...section, items: filterMediaItems(sourceId, section.items) })).filter(section => section.items.length)
}

async function resolveProviderItemIds(target: MediaItemActionTarget, config: DataSourceConfig | undefined): Promise<string[]> {
  if (!config || !isRawType(config.type))
    return [target.itemId]
  const rootPath = typeof config.extra?.rootPath === 'string' ? config.extra.rootPath : '/'
  const cache = await loadRawSourceScanCache(config.id, config.type, rootPath)
  if (!cache)
    return [target.itemId]
  if (!isRawScannedSyntheticId(target.itemId)) {
    const movieVariants = resolveRawMovieVariantPaths(cache, target.itemId)
    return movieVariants.length ? movieVariants : [target.itemId]
  }
  const paths = new Set<string>()
  const visit = (id: string) => {
    for (const item of listRawScannedChildren(cache, id) ?? []) {
      if (isRawScannedSyntheticId(item.id))
        visit(item.id)
      else if (item.path)
        paths.add(item.path)
    }
  }
  visit(target.itemId)
  return [...paths]
}

export function resolveRawMovieVariantPaths(cache: Pick<RawLocalScanCache, 'candidates' | 'scrapedItems'>, targetItemId: string): string[] {
  const target = cache.candidates.find(candidate => candidate.record.providerPath === targetItemId)
  if (!target || target.kind !== 'movie')
    return []
  const scrapedByRecordId = new Map((cache.scrapedItems ?? []).map(item => [item.recordId, item]))
  const targetKey = rawMovieWorkKey(target, scrapedByRecordId.get(target.record.id))
  return cache.candidates
    .filter(candidate => candidate.kind === 'movie' && rawMovieWorkKey(candidate, scrapedByRecordId.get(candidate.record.id)) === targetKey)
    .map(candidate => candidate.record.providerPath)
}

function rawMovieWorkKey(candidate: RawLocalScanCache['candidates'][number], scraped: NonNullable<RawLocalScanCache['scrapedItems']>[number] | undefined): string {
  const metadata = scraped?.metadata ?? candidate.scrapeMetadata
  return metadata?.mediaType === 'movie'
    ? `tmdb:movie:${metadata.tmdbId}`
    : `${candidate.normalizedTitle || candidate.record.providerPath}:${candidate.year ?? ''}`
}

export function filterTombstonedMediaDetail(detail: MediaDetail, isHidden: (item: MediaItem) => boolean): MediaDetail {
  const filterNested = (items: MediaItem[] | undefined): MediaItem[] | undefined => items
    ?.filter(item => !isHidden(item))
    .map(item => ({ ...item, children: filterNested(item.children) }))
  return {
    ...detail,
    children: filterNested(detail.children),
    similarItems: filterNested(detail.similarItems),
    collections: filterNested(detail.collections),
  }
}

async function removeSuccessfulRawRecords(plan: MediaDeletePlan, config: DataSourceConfig | undefined, succeeded: readonly string[]) {
  if (!config || !isRawType(config.type))
    return
  const rootPath = typeof config.extra?.rootPath === 'string' ? config.extra.rootPath : '/'
  const cache = await loadRawSourceScanCache(config.id, config.type, rootPath)
  if (!cache)
    return
  const deleted = new Set(succeeded)
  const records = cache.records.filter(record => !deleted.has(record.providerPath))
  const recordIds = new Set(records.map(record => record.id))
  const nextCache = {
    ...cache,
    records,
    candidates: cache.candidates.filter(candidate => recordIds.has(candidate.record.id)),
    scrapedItems: cache.scrapedItems?.filter(item => recordIds.has(item.recordId)),
    fileCount: Math.max(0, cache.fileCount - (cache.records.length - records.length)),
  }
  await saveRawSourceScanCache(nextCache)
  if (!getRawScannedMediaDetail(nextCache, plan.target.itemId))
    await addMediaTombstones(plan.target.sourceId, [plan.target.itemId])
}

function safePathSummary(value: string): string {
  const normalized = value.replace('\\', '/')
  const segments = normalized.split('/').filter(Boolean)
  return segments.slice(-3).join('/') || '媒体文件'
}

function isRawType(type: DataSourceConfig['type']): type is 'alist' | 'clouddrive2' | 'webdav' | 'local' | '115' | '123' | 'quark' {
  return ['alist', 'clouddrive2', 'webdav', 'local', '115', '123', 'quark'].includes(type)
}
