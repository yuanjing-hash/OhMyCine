import type { AudioTrack, MediaDetail, MediaItem, SubtitleTrack } from '@/services/datasource/types'

export interface PlaybackQueueItem {
  id: string
  sourceId: string
  libraryId?: string
  title: string
  name: string
  path: string
  type: MediaItem['type']
  posterUrl?: string
  backdropUrl?: string
  titleLogoUrl?: string
  overview?: string
  duration?: number
  seasonNumber?: number
  episodeNumber?: number
}

export interface PlaybackQueueState {
  items: PlaybackQueueItem[]
  currentIndex: number
}

export interface PlaybackMediaContext {
  id: string
  sourceId: string
  itemId: string
  title?: string
  mediaSourceId?: string
  subtitles: SubtitleTrack[]
  audioTracks: AudioTrack[]
  queue?: PlaybackQueueState
  detail?: MediaDetail
  relatedItems?: MediaItem[]
}

export interface PlaybackMediaContextInput {
  sourceId: string
  itemId: string
  title?: string
  mediaSourceId?: string
  subtitles?: readonly SubtitleTrack[]
  audioTracks?: readonly AudioTrack[]
  queue?: PlaybackQueueInput
  detail?: MediaDetail
  relatedItems?: readonly MediaItem[]
}

export interface PlaybackQueueInput {
  items: readonly PlaybackQueueItemInput[]
  currentIndex: number
}

export interface PlaybackQueueItemInput {
  id: string
  sourceId: string
  libraryId?: string
  title?: string
  name: string
  path: string
  type: MediaItem['type']
  posterUrl?: string
  backdropUrl?: string
  titleLogoUrl?: string
  overview?: string
  duration?: number
  seasonNumber?: number
  episodeNumber?: number
}

const MAX_CONTEXTS = 20
const playbackContexts = new Map<string, PlaybackMediaContext>()

export function savePlaybackMediaContext(input: PlaybackMediaContextInput): string {
  const id = createContextId()
  playbackContexts.set(id, {
    id,
    sourceId: input.sourceId,
    itemId: input.itemId,
    title: input.title,
    mediaSourceId: input.mediaSourceId,
    subtitles: (input.subtitles ?? []).map(track => ({ ...track })),
    audioTracks: (input.audioTracks ?? []).map(track => ({ ...track })),
    queue: normalizeQueue(input.queue),
    detail: input.detail ? cloneMediaDetail(input.detail) : undefined,
    relatedItems: input.relatedItems?.map(cloneMediaItem),
  })
  trimOldContexts()
  return id
}

function cloneMediaDetail(detail: MediaDetail): MediaDetail {
  return {
    ...cloneMediaItem(detail),
    genres: detail.genres ? [...detail.genres] : undefined,
    directors: detail.directors ? [...detail.directors] : undefined,
    cast: detail.cast ? [...detail.cast] : undefined,
    imdbId: detail.imdbId,
    tmdbId: detail.tmdbId,
    resolution: detail.resolution,
    codec: detail.codec,
    audioCodec: detail.audioCodec,
    subtitles: detail.subtitles?.map(track => ({ ...track })),
    audioTracks: detail.audioTracks?.map(track => ({ ...track })),
    mediaSources: detail.mediaSources?.map(source => ({ ...source })),
    stills: detail.stills ? [...detail.stills] : undefined,
    similarItems: detail.similarItems?.map(cloneMediaItem),
    collections: detail.collections?.map(cloneMediaItem),
  }
}

function cloneMediaItem(item: MediaItem): MediaItem {
  return {
    ...item,
    children: item.children?.map(cloneMediaItem),
  }
}

export function getPlaybackMediaContext(id: string): PlaybackMediaContext | null {
  return playbackContexts.get(id) ?? null
}

export function createPlaybackQueueItem(item: MediaItem): PlaybackQueueItem {
  return {
    id: item.id,
    sourceId: item.sourceId,
    libraryId: item.libraryId,
    title: item.name,
    name: item.name,
    path: item.path,
    type: item.type,
    posterUrl: item.posterUrl,
    backdropUrl: item.backdropUrl,
    titleLogoUrl: item.titleLogoUrl,
    overview: item.overview,
    duration: item.duration,
    seasonNumber: item.seasonNumber,
    episodeNumber: item.episodeNumber,
  }
}

export function createPlaybackQueue(items: readonly MediaItem[], currentItemId: string): PlaybackQueueInput | undefined {
  const queueItems = items
    .filter(isPlayableQueueItem)
    .map(createPlaybackQueueItem)
  const currentIndex = queueItems.findIndex(item => item.id === currentItemId)

  if (currentIndex < 0)
    return undefined

  return {
    items: queueItems,
    currentIndex,
  }
}

function normalizeQueue(queue: PlaybackQueueInput | undefined): PlaybackQueueState | undefined {
  if (!queue || queue.items.length === 0)
    return undefined

  const items = queue.items.map(item => ({
    id: item.id,
    sourceId: item.sourceId,
    libraryId: item.libraryId,
    title: item.title ?? item.name,
    name: item.name,
    path: item.path,
    type: item.type,
    posterUrl: item.posterUrl,
    backdropUrl: item.backdropUrl,
    titleLogoUrl: item.titleLogoUrl,
    overview: item.overview,
    duration: item.duration,
    seasonNumber: item.seasonNumber,
    episodeNumber: item.episodeNumber,
  }))
  const currentIndex = Math.min(Math.max(queue.currentIndex, 0), items.length - 1)

  return { items, currentIndex }
}

function isPlayableQueueItem(item: MediaItem): boolean {
  return item.type !== 'folder' && item.type !== 'series' && item.type !== 'season'
}

function trimOldContexts() {
  while (playbackContexts.size > MAX_CONTEXTS) {
    const oldest = playbackContexts.keys().next().value as string | undefined
    if (!oldest)
      return
    playbackContexts.delete(oldest)
  }
}

function createContextId(): string {
  if (globalThis.crypto?.randomUUID)
    return globalThis.crypto.randomUUID()

  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}
