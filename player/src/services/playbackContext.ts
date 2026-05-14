import type { AudioTrack, SubtitleTrack } from '@/services/datasource/types'

export interface PlaybackMediaContext {
  id: string
  sourceId: string
  itemId: string
  title?: string
  mediaSourceId?: string
  subtitles: SubtitleTrack[]
  audioTracks: AudioTrack[]
}

export interface PlaybackMediaContextInput {
  sourceId: string
  itemId: string
  title?: string
  mediaSourceId?: string
  subtitles?: readonly SubtitleTrack[]
  audioTracks?: readonly AudioTrack[]
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
  })
  trimOldContexts()
  return id
}

export function getPlaybackMediaContext(id: string): PlaybackMediaContext | null {
  return playbackContexts.get(id) ?? null
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
