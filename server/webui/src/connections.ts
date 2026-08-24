export interface EmbyConnectionDraft {
  provider: 'emby' | 'jellyfin'
  name: string
  endpoint: string
  apiKey: string
  enabled: boolean
}

export function connectionListPath(provider: 'pan115' | 'emby' | 'jellyfin') {
  return `/api/v1/connections?provider=${provider}`
}

export function buildEmbyCreatePayload(draft: EmbyConnectionDraft) {
  return {
    name: draft.name.trim(),
    provider: draft.provider,
    endpoint: draft.endpoint.trim(),
    api_key: draft.apiKey.trim(),
    enabled: draft.enabled,
  }
}

export function buildEmbyUpdatePayload(draft: EmbyConnectionDraft, revision: number) {
  const payload: {
    name: string
    endpoint: string
    enabled: boolean
    revision: number
    api_key?: string
  } = {
    name: draft.name.trim(),
    endpoint: draft.endpoint.trim(),
    enabled: draft.enabled,
    revision,
  }
  const apiKey = draft.apiKey.trim()
  if (apiKey) payload.api_key = apiKey
  return payload
}

export function consumeEmbyCreatePayload(draft: EmbyConnectionDraft) {
  const payload = buildEmbyCreatePayload(draft)
  draft.apiKey = ''
  return payload
}

export function consumeEmbyUpdatePayload(draft: EmbyConnectionDraft, revision: number) {
  const payload = buildEmbyUpdatePayload(draft, revision)
  draft.apiKey = ''
  return payload
}

export function buildEmbyGatewayUpdatePayload(enabled: boolean, alias: string, externalPlayerEnabled: boolean, fanartEnabled: boolean, revision: number) {
  return { enabled, alias: alias.trim(), external_player_enabled: externalPlayerEnabled, fanart_enabled: fanartEnabled, revision }
}

export function canEnableEmbyGateway(connectionEnabled: boolean, healthStatus: string) {
  return connectionEnabled && healthStatus === 'online'
}

export function canBrowseProviderDirectory(canReadConnection: boolean, canBrowseStorage: boolean, canWriteStorage: boolean) {
  return canReadConnection && canBrowseStorage && canWriteStorage
}

export function isLoopbackURL(value: string) {
  try {
    const hostname = new URL(value).hostname.toLowerCase()
    return hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '[::1]' || hostname === '::1'
  } catch {
    return false
  }
}
