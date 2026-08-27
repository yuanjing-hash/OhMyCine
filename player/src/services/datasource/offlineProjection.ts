import type { HomeSection, MediaItem } from './types'
import { OFFLINE_SOURCE_ID } from './offline'

export function isOfflineProjectionItem(item: Pick<MediaItem, 'sourceId' | 'originType'>): boolean {
  return item.sourceId === OFFLINE_SOURCE_ID || item.originType === 'offline'
}

export function stripOfflineProjectionSections(sections: readonly HomeSection[]): HomeSection[] {
  return sections.flatMap((section) => {
    if (section.sourceId === OFFLINE_SOURCE_ID)
      return []
    const items = section.items.filter(item => !isOfflineProjectionItem(item))
    if (items.length === 0)
      return []
    return [{ ...section, items }]
  })
}
