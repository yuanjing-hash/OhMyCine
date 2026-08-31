import type { MediaItem, MediaPlaybackTarget } from './types'

const playbackTargetsByWork = new Map<string, MediaPlaybackTarget[]>()

export function rememberPlaybackTargetsForItems(items: readonly MediaItem[]): void {
  for (const item of items)
    rememberPlaybackTargets(item)
}

export function prunePlaybackTargets(activeSourceIds: ReadonlySet<string>): void {
  for (const [key, targets] of playbackTargetsByWork) {
    const activeTargets = targets.filter(target => activeSourceIds.has(target.sourceId))
    if (activeTargets.length > 0)
      playbackTargetsByWork.set(key, activeTargets)
    else
      playbackTargetsByWork.delete(key)
  }
}

export function forgetPlaybackTargetsForSource(sourceId: string): void {
  if (!sourceId)
    return
  for (const [key, targets] of playbackTargetsByWork) {
    const retained = targets.filter(target => target.sourceId !== sourceId)
    if (retained.length > 0)
      playbackTargetsByWork.set(key, retained)
    else
      playbackTargetsByWork.delete(key)
  }
}

export function mergeMediaItemsByIdentity(items: readonly MediaItem[]): MediaItem[] {
  const result: MediaItem[] = []
  const indexes = new Map<string, number>()
  for (const item of items) {
    const keys = identityKeys(item)
    if (keys.length === 0) {
      result.push(item)
      continue
    }
    const index = keys.map(key => indexes.get(key)).find((value): value is number => value != null)
    if (index == null) {
      const nextIndex = result.length
      for (const key of keys)
        indexes.set(key, nextIndex)
      const initial = withPlaybackTarget(item)
      result.push(initial)
      rememberPlaybackTargets(initial)
      continue
    }
    result[index] = mergePair(result[index], item)
    for (const key of [...identityKeys(result[index]), ...keys])
      indexes.set(key, index)
    rememberPlaybackTargets(result[index])
  }
  return result
}

export function playbackTargetsForItem(item: MediaItem): MediaPlaybackTarget[] {
  const key = workIdentityKey(item)
  return key ? [...(playbackTargetsByWork.get(key) ?? [])] : []
}

function identityKeys(item: MediaItem): string[] {
  const keys: string[] = []
  if (item.exactIdentity?.trim())
    keys.push(`exact:${item.exactIdentity.trim()}`)
  const workKey = workIdentityKey(item)
  if (workKey)
    keys.push(workKey)
  return keys
}

function workIdentityKey(item: MediaItem): string | null {
  const identity = item.workIdentity
  if (!identity?.value.trim())
    return null
  return `work:${identity.scheme}:${identity.mediaType}:${identity.value.trim()}`
}

function rememberPlaybackTargets(item: MediaItem): void {
  const key = workIdentityKey(item)
  if (!key || !item.playbackTargets?.length)
    return
  playbackTargetsByWork.set(key, mergeTargetLists(playbackTargetsByWork.get(key) ?? [], item.playbackTargets))
}

function mergePair(left: MediaItem, right: MediaItem): MediaItem {
  const preferred = prefersRight(left, right) ? right : left
  const alternate = preferred === left ? right : left
  return {
    ...alternate,
    ...preferred,
    originalTitle: preferred.originalTitle ?? alternate.originalTitle,
    titleLogoUrl: preferred.titleLogoUrl ?? alternate.titleLogoUrl,
    posterUrl: preferred.posterUrl ?? alternate.posterUrl,
    backdropUrl: preferred.backdropUrl ?? alternate.backdropUrl,
    year: preferred.year ?? alternate.year,
    rating: preferred.rating ?? alternate.rating,
    overview: preferred.overview ?? alternate.overview,
    tagline: preferred.tagline ?? alternate.tagline,
    duration: preferred.duration ?? alternate.duration,
    playbackTargets: mergeTargets(left, right),
  }
}

function prefersRight(left: MediaItem, right: MediaItem): boolean {
  if (left.originType === 'server')
    return false
  return right.originType === 'server'
}

function withPlaybackTarget(item: MediaItem): MediaItem {
  if (item.playbackTargets?.length)
    return item
  return { ...item, playbackTargets: [fallbackTarget(item)] }
}

function mergeTargets(left: MediaItem, right: MediaItem): MediaPlaybackTarget[] {
  return mergeTargetLists(withPlaybackTarget(left).playbackTargets ?? [], withPlaybackTarget(right).playbackTargets ?? [])
}

function mergeTargetLists(left: readonly MediaPlaybackTarget[], right: readonly MediaPlaybackTarget[]): MediaPlaybackTarget[] {
  const targets = [...left, ...right]
  const seen = new Set<string>()
  return targets.filter((target) => {
    // Exact identity proves media equivalence, not route equivalence. The same
    // artifact must retain both the Server-direct and authenticated Emby route.
    const key = `${target.sourceId}:${target.itemId}:${target.mediaSourceId ?? ''}`
    if (seen.has(key))
      return false
    seen.add(key)
    return true
  })
}

function fallbackTarget(item: MediaItem): MediaPlaybackTarget {
  return { sourceId: item.sourceId, itemId: item.id, label: item.originType === 'server' ? 'OhMyCine Server' : item.sourceId, exactIdentity: item.exactIdentity }
}
