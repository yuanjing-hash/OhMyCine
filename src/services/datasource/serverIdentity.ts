export interface ServerWorkItemIdentity {
  libraryId: string
  workId: string
  kind: 'work' | 'season' | 'entry'
  seasonNumber?: number
  entryId?: string
}

export interface ServerHistoryIdentity {
  libraryId: string
  workId: string
  kind: 'movie' | 'episode' | 'episode-entry'
  seasonNumber?: number
  episodeNumber?: number
}

function isLibraryId(value: string | undefined): value is string {
  return Boolean(value && /^[1-9]\d*$/.test(value) && Number(value) <= 0xFFFFFFFF)
}

function isWorkToken(value: string | undefined): value is string {
  if (!value || value.length > 256 || !/^[\w-]+$/.test(value) || value.length % 4 === 1)
    return false
  try {
    const decoded = atob(value.replace(/-/g, '+').replace(/_/g, '/'))
    return decoded.length <= 80 && /^(?:series|movie|file):/.test(decoded)
  }
  catch {
    return false
  }
}

function episodeNumber(value: string | undefined, allowZero: boolean, maximum: number): number | undefined {
  if (!value || !/^\d+$/.test(value))
    return undefined
  const parsed = Number(value)
  return Number.isSafeInteger(parsed) && parsed >= (allowZero ? 0 : 1) && parsed <= maximum ? parsed : undefined
}

/** Parse only current catalog tokens; a title or path never establishes a parent. */
export function parseServerWorkItemIdentity(value: string): ServerWorkItemIdentity | null {
  const [kind, libraryId, workId, suffix, ...rest] = value.split('|')
  if (!isLibraryId(libraryId) || !isWorkToken(workId) || rest.length > 0)
    return null
  if (kind === 'work' && suffix === undefined)
    return { kind, libraryId, workId }
  if (kind === 'season') {
    const seasonNumber = episodeNumber(suffix, true, 10_000)
    return seasonNumber === undefined ? null : { kind, libraryId, workId, seasonNumber }
  }
  if (kind === 'entry' && isLibraryId(suffix))
    return { kind, libraryId, workId, entryId: suffix }
  return null
}

export function parseServerHistoryIdentity(value: string): ServerHistoryIdentity | null {
  const [prefix, version, kind, libraryId, workId, ...suffix] = value.split(':')
  if (prefix !== 'server' || version !== 'v1' || !isLibraryId(libraryId) || !isWorkToken(workId))
    return null
  if (kind === 'movie' && suffix.length === 0)
    return { kind, libraryId, workId }
  if (kind === 'episode' && suffix.length === 2) {
    const seasonNumber = episodeNumber(suffix[0], true, 10_000)
    const number = episodeNumber(suffix[1], false, 100_000)
    return seasonNumber === undefined || number === undefined ? null : { kind, libraryId, workId, seasonNumber, episodeNumber: number }
  }
  if (kind === 'episode-entry' && suffix.length === 1 && isLibraryId(suffix[0]))
    return { kind, libraryId, workId }
  return null
}

export function createServerWorkItemId(identity: { libraryId: string, workId: string }): string {
  return `work|${identity.libraryId}|${identity.workId}`
}
