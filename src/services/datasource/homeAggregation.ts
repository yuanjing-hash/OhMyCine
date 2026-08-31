import type { DataSource, HomeSection, MediaItem } from './types'
import { mergeMediaItemsByIdentity } from './identityMerge'

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
  const items = dedupeHomeItems(typedSections.flatMap(section => section.items))
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
  return itemTimestamp(right) - itemTimestamp(left)
    || (right.year ?? 0) - (left.year ?? 0)
    || right.name.localeCompare(left.name, 'zh-Hans-CN')
}

function itemTimestamp(item: MediaItem): number {
  const modifiedTime = item.modified ? Date.parse(item.modified) : Number.NaN
  if (Number.isFinite(modifiedTime))
    return modifiedTime
  return item.year ? Date.UTC(item.year, 0, 1) : 0
}
