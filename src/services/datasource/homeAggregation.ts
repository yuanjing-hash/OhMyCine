import type { DataSource, HomeSection, MediaItem } from './types'
import type { PlaybackHistoryEntry } from '@/services/playbackHistory'
import { isCompletedPosition, playbackSeriesId, toContinueWatchingMediaItem } from '@/services/playbackHistory'
import { mergeMediaItemsByIdentity } from './identityMerge'
import { parseServerWorkItemIdentity } from './serverIdentity'
import { parseOnlineItemID } from './serverOnline'

export async function collectHomeSectionsFromSources(sources: readonly DataSource[]): Promise<HomeSection[]> {
  const settled = await Promise.allSettled(
    sources.map(source => collectHomeSectionsFromSource(source)),
  )

  return mergeAggregatedHomeSections(settled.flatMap(result => result.status === 'fulfilled' ? result.value : []))
}

async function collectHomeSectionsFromSource(source: DataSource): Promise<HomeSection[]> {
  if (!source.getHomeSections)
    return []

  try {
    const sections = await source.getHomeSections()
    return sections.filter(hasVisibleHomeItems)
  }
  catch {
    return []
  }
}

function hasVisibleHomeItems(section: HomeSection): boolean {
  return section.items.length > 0 || Boolean(section.providerIdentity && section.errorCode)
}

function mergeAggregatedHomeSections(sections: readonly HomeSection[]): HomeSection[] {
  const contributionSections = sections.filter(section => Boolean(section.providerIdentity))
  const ordinarySections = sections.filter(section => !section.providerIdentity)
  const heroSection = mergeSectionsOfType(ordinarySections, 'hero', {
    id: 'hero-aggregated',
    title: '精选',
    limit: 16,
    sortItems: compareHeroItems,
  })
  const recentlyAddedSection = mergeSectionsOfType(ordinarySections, 'recentlyAdded', {
    id: 'recently-added-aggregated',
    title: '最新影片',
    limit: 24,
    sortItems: compareRecentlyAddedItems,
  })
  const continueSections = ordinarySections.filter(section => section.type === 'continueWatching')
  const passthroughSections = ordinarySections.filter(section =>
    section.type !== 'hero'
    && section.type !== 'recentlyAdded'
    && section.type !== 'continueWatching',
  )

  return [
    heroSection,
    ...contributionSections,
    ...continueSections,
    recentlyAddedSection,
    ...passthroughSections,
  ].filter((section): section is HomeSection => section != null && (section.items.length > 0 || Boolean(section.providerIdentity && section.errorCode)))
}

function mergeSectionsOfType(
  sections: readonly HomeSection[],
  type: HomeSection['type'],
  options: {
    readonly id: string
    readonly title: string
    readonly limit: number
    readonly sortItems: (left: MediaItem, right: MediaItem) => number
  },
): HomeSection | null {
  const typedSections = sections.filter(section => section.type === type)
  const sourceItems = typedSections.flatMap(section => section.items)
  // Merge duplicate catalog routes only after the newest admission is first.
  // Otherwise an older Server library can hide the same work's new admission.
  if (type === 'recentlyAdded')
    sourceItems.sort(options.sortItems)
  const items = dedupeHomeItems(sourceItems)
    .sort(options.sortItems)
    .slice(0, options.limit)
  if (items.length === 0)
    return null

  return {
    id: options.id,
    sourceId: singleSourceId(items),
    title: options.title,
    type,
    items,
  }
}

function dedupeHomeItems(items: readonly MediaItem[]): MediaItem[] {
  return mergeMediaItemsByIdentity(items)
}

function singleSourceId(items: readonly MediaItem[]): string | undefined {
  const sourceIds = [...new Set(items.map(item => item.sourceId).filter(Boolean))]
  return sourceIds.length === 1 ? sourceIds[0] : undefined
}

function compareHeroItems(left: MediaItem, right: MediaItem): number {
  return heroScore(right) - heroScore(left)
    || compareRecentlyAddedItems(left, right)
}

function heroScore(item: MediaItem): number {
  return (item.backdropUrl ? 4 : 0)
    + (item.titleLogoUrl ? 3 : 0)
    + (item.overview ? 2 : 0)
    + (item.posterUrl ? 1 : 0)
    + (item.rating ?? 0) / 10
}

function compareRecentlyAddedItems(left: MediaItem, right: MediaItem): number {
  const leftTime = itemTimestamp(left)
  const rightTime = itemTimestamp(right)
  if (leftTime !== rightTime)
    return rightTime > leftTime ? 1 : -1
  // Stable sort retains each provider's delivered tie/order. In particular,
  // Server already orders equal admissions by library and work identity.
  return left.sourceId.localeCompare(right.sourceId)
}

function itemTimestamp(item: MediaItem): number {
  const addedTime = item.addedAt ? Date.parse(item.addedAt) : Number.NaN
  return Number.isFinite(addedTime) ? addedTime : Number.NEGATIVE_INFINITY
}

/** Reconcile exact episode state, then select the newest work before resume filtering. */
export function mergeContinueWatchingSections(
  sections: readonly HomeSection[],
  localItems: readonly MediaItem[],
  tombstones: readonly PlaybackHistoryEntry[],
  limit = 20,
): HomeSection {
  const merged = new Map<string, { item: MediaItem, deleted: boolean }>()
  const providerItems = sections.filter(section => section.type === 'continueWatching').flatMap(section => section.items)
  for (const item of providerItems) {
    const key = continueWatchingKey(item)
    const previous = merged.get(key)
    if (!previous || comparePlaybackItems(item, previous.item) < 0)
      merged.set(key, { item, deleted: false })
  }
  for (const item of localItems) {
    const key = continueWatchingKey(item)
    const previous = merged.get(key)
    merged.set(key, { item: previous ? mergeContinueWatchingItem(item, previous.item) : item, deleted: false })
  }
  for (const entry of tombstones) {
    const item = toContinueWatchingMediaItem(entry)
    const aliases = new Set([entry.mediaIdentity, entry.itemId].filter(Boolean))
    for (const [key, candidate] of merged) {
      if (candidate.item.sourceId === entry.sourceId
        && (aliases.has(candidate.item.id) || aliases.has(candidate.item.historyIdentity))
        && playbackTimestamp(candidate.item) <= entry.updatedAt) {
        merged.delete(key)
      }
    }
    const key = continueWatchingKey(item)
    const newer = merged.get(key)
    if (!newer || playbackTimestamp(newer.item) <= entry.updatedAt)
      merged.set(key, { item, deleted: true })
  }

  const seenWorks = new Set<string>()
  const items: MediaItem[] = []
  for (const candidate of [...merged.values()].sort((left, right) => comparePlaybackItems(left.item, right.item))) {
    const item = candidate.item
    const seriesId = playbackSeriesId(item)
    const physical = seriesId ? parseServerWorkItemIdentity(seriesId) : null
    const online = seriesId ? parseOnlineItemID(seriesId) : null
    const libraryId = physical?.libraryId ?? (online?.kind === 'work' ? online.libraryId : item.libraryId) ?? ''
    const workKey = JSON.stringify([item.sourceId, libraryId, seriesId ? 'series' : 'item', seriesId ?? item.historyIdentity ?? item.id])
    if (seenWorks.has(workKey))
      continue
    seenWorks.add(workKey)
    if (candidate.deleted || item.played === true || usableResumePosition(item) === undefined)
      continue
    items.push({ ...item, seriesId })
    if (items.length >= limit)
      break
  }
  return { id: 'continue-watching', title: '继续观看', type: 'continueWatching', items }
}

function mergeContinueWatchingItem(localItem: MediaItem, providerItem: MediaItem): MediaItem {
  const preferLocal = comparePlaybackItems(localItem, providerItem) <= 0
  const current = preferLocal ? localItem : providerItem
  return {
    ...localItem,
    ...providerItem,
    id: current.id,
    historyIdentity: current.historyIdentity,
    exactIdentity: current.exactIdentity,
    playbackTargets: current.playbackTargets,
    libraryId: providerItem.libraryId ?? localItem.libraryId,
    posterUrl: providerItem.posterUrl || localItem.posterUrl,
    backdropUrl: providerItem.backdropUrl || localItem.backdropUrl,
    titleLogoUrl: providerItem.titleLogoUrl || localItem.titleLogoUrl,
    path: providerItem.path || localItem.path,
    seriesId: playbackSeriesId(providerItem) ?? playbackSeriesId(localItem),
    seriesName: providerItem.seriesName || localItem.seriesName,
    duration: current.duration ?? providerItem.duration ?? localItem.duration,
    resumePosition: current.resumePosition,
    progress: current.progress,
    progressSource: current.progressSource,
    played: current.played,
    lastPlayedAt: current.lastPlayedAt,
  }
}

function playbackTimestamp(item: MediaItem): number {
  const value = Date.parse(item.lastPlayedAt ?? '')
  return Number.isFinite(value) ? value : Number.NEGATIVE_INFINITY
}

function comparePlaybackItems(left: MediaItem, right: MediaItem): number {
  const leftTime = playbackTimestamp(left)
  const rightTime = playbackTimestamp(right)
  if (leftTime !== rightTime)
    return leftTime > rightTime ? -1 : 1
  return Number(right.played === true) - Number(left.played === true)
    || continueWatchingKey(left).localeCompare(continueWatchingKey(right))
}

function continueWatchingKey(item: MediaItem): string {
  return JSON.stringify([item.sourceId, item.historyIdentity ?? item.id])
}

function usableResumePosition(item: MediaItem): number | undefined {
  const position = item.resumePosition
  if (typeof position !== 'number' || !Number.isFinite(position) || position < 30)
    return undefined
  return item.played === false || !isCompletedPosition(position, item.duration) ? position : undefined
}
