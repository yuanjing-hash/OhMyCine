export interface PlaybackRouteIdentity {
  sourceId?: string
  itemId?: string
  mediaSourceId?: string
  contextId?: string
}

export type PlaybackRouteQuery = Partial<Record<keyof PlaybackRouteIdentity, string>>

const PLAYBACK_ROUTE_QUERY_KEYS = ['sourceId', 'itemId', 'mediaSourceId', 'contextId'] as const

export function createPlaybackRouteQuery(identity: PlaybackRouteIdentity): PlaybackRouteQuery {
  return sanitizePlaybackRouteQuery(identity)
}

export function sanitizePlaybackRouteQuery(query: object): PlaybackRouteQuery {
  const sanitized: PlaybackRouteQuery = {}
  const values = query as Readonly<Record<string, unknown>>

  for (const key of PLAYBACK_ROUTE_QUERY_KEYS) {
    const value = routeStringValue(values[key])
    if (value)
      sanitized[key] = value
  }

  return sanitized
}

export function playbackRouteQueryNeedsSanitization(query: object): boolean {
  const values = query as Readonly<Record<string, unknown>>
  const keys = Object.keys(query)
  if (keys.some(key => !PLAYBACK_ROUTE_QUERY_KEYS.includes(key as typeof PLAYBACK_ROUTE_QUERY_KEYS[number])))
    return true

  if (keys.some((key) => {
    const value = values[key]
    return typeof value !== 'string' || value !== value.trim() || value.length === 0
  })) {
    return true
  }

  const sanitized = sanitizePlaybackRouteQuery(query)
  return PLAYBACK_ROUTE_QUERY_KEYS.some((key) => {
    const current = routeStringValue(values[key])
    return current !== sanitized[key]
  })
}

function routeStringValue(value: unknown): string | undefined {
  if (typeof value !== 'string')
    return undefined

  const normalized = value.trim()
  return normalized || undefined
}
