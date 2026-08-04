import { invoke } from '@tauri-apps/api/core'

const cachedReads = new Map<string, Promise<string | null>>()
const cacheWrites = new Map<string, Promise<string>>()

export type ArtworkKind = 'poster' | 'backdrop' | 'logo' | 'thumbnail'

export function artworkCacheKey(sourceId: string, itemId: string, kind: ArtworkKind): string {
  return `${sourceId}:${itemId}:${kind}`
}

export function isTauriImageCacheAvailable(): boolean {
  const root = globalThis as {
    readonly __TAURI_INTERNALS__?: unknown
    readonly window?: { readonly __TAURI_INTERNALS__?: unknown }
  }
  return root.__TAURI_INTERNALS__ != null || root.window?.__TAURI_INTERNALS__ != null
}

export async function getCachedImage(cacheKey: string): Promise<string | null> {
  if (!isTauriImageCacheAvailable())
    return null
  const existing = cachedReads.get(cacheKey)
  if (existing)
    return existing

  const request = invoke<string | null>('player_get_cached_image', { cacheKey })
    .catch(() => null)
    .finally(() => cachedReads.delete(cacheKey))
  cachedReads.set(cacheKey, request)
  return request
}

export async function cacheImage(cacheKey: string, url: string): Promise<string> {
  if (!isTauriImageCacheAvailable() || !/^https?:\/\//i.test(url))
    return url
  const requestKey = `${cacheKey}\n${url}`
  const existing = cacheWrites.get(requestKey)
  if (existing)
    return existing

  const request = invoke<string>('player_cache_image', {
    request: { cacheKey, url },
  })
    .catch(() => url)
    .finally(() => cacheWrites.delete(requestKey))
  cacheWrites.set(requestKey, request)
  return request
}
