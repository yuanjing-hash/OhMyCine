import { invoke } from '@tauri-apps/api/core'
import { getAppSetting, setAppSetting } from '@/services/appSettings'
import { isProtectedServerArtworkURL } from '@/services/serverArtwork'

const cachedReads = new Map<string, Promise<string | null>>()
const cacheWrites = new Map<string, Promise<string>>()
interface ServerArtworkCredential { baseUrl: string, accessToken: string, generation: number }
const serverArtworkSources = new Map<string, ServerArtworkCredential>()
let serverArtworkGeneration = 0

export function registerServerArtworkSource(sourceId: string, baseUrl: string, accessToken: string): void {
  if (sourceId && baseUrl && accessToken)
    serverArtworkSources.set(sourceId, { baseUrl, accessToken, generation: ++serverArtworkGeneration })
}

export function unregisterServerArtworkSource(sourceId: string, accessToken?: string): void {
  const registered = serverArtworkSources.get(sourceId)
  if (registered && (!accessToken || registered.accessToken === accessToken))
    serverArtworkSources.delete(sourceId)
}

function serverArtworkCredential(cacheKey: string, url: string): ServerArtworkCredential | undefined {
  const origin = new URL(url).origin
  return [...serverArtworkSources.entries()]
    .filter(([id, source]) => source.baseUrl === origin && cacheKey.startsWith(`${id}:`))
    .sort(([left], [right]) => right.length - left.length)[0]?.[1]
}
const IMAGE_CACHE_SETTINGS_KEY = 'ohmycine-image-cache-settings-v1'
const DEFAULT_IMAGE_CACHE_LIMIT_MB = 500
const MIN_IMAGE_CACHE_LIMIT_MB = 100
const MAX_IMAGE_CACHE_LIMIT_MB = 4096

export interface ImageCacheSettings {
  maxSizeMb: number
}

export interface ImageCacheStats {
  totalBytes: number
  fileCount: number
}

export type ArtworkKind = 'poster' | 'backdrop' | 'logo' | 'thumbnail'

export function artworkCacheKey(sourceId: string, itemId: string, kind: ArtworkKind): string {
  return `${sourceId}:${itemId}:${kind}`
}

export function artworkURLCacheKey(sourceId: string, url: string, kind: ArtworkKind): string {
  let hash = 2166136261
  for (let index = 0; index < url.length; index++) {
    hash ^= url.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }
  return `${sourceId}:${kind}:${(hash >>> 0).toString(16)}`
}

export function loadImageCacheSettings(): ImageCacheSettings {
  const raw = getAppSetting(IMAGE_CACHE_SETTINGS_KEY)
  if (!raw)
    return { maxSizeMb: DEFAULT_IMAGE_CACHE_LIMIT_MB }
  try {
    const parsed = JSON.parse(raw) as Partial<ImageCacheSettings>
    return { maxSizeMb: normalizeImageCacheLimitMb(parsed.maxSizeMb) }
  }
  catch {
    return { maxSizeMb: DEFAULT_IMAGE_CACHE_LIMIT_MB }
  }
}

export async function saveImageCacheSettings(settings: ImageCacheSettings): Promise<ImageCacheSettings> {
  const normalized = { maxSizeMb: normalizeImageCacheLimitMb(settings.maxSizeMb) }
  await setAppSetting(IMAGE_CACHE_SETTINGS_KEY, JSON.stringify(normalized))
  if (isTauriImageCacheAvailable())
    await invoke<ImageCacheStats>('player_trim_image_cache', { maxBytes: normalized.maxSizeMb * 1024 * 1024 })
  return normalized
}

export async function getImageCacheStats(): Promise<ImageCacheStats | null> {
  if (!isTauriImageCacheAvailable())
    return null
  try {
    return await invoke<ImageCacheStats>('player_image_cache_stats')
  }
  catch {
    return null
  }
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
  const protectedArtwork = isProtectedServerArtworkURL(url)
  if (!isTauriImageCacheAvailable() || !/^https?:\/\//i.test(url))
    return protectedArtwork ? '' : url
  const credential = protectedArtwork ? serverArtworkCredential(cacheKey, url) : undefined
  if (protectedArtwork && !credential)
    return ''
  const requestKey = `${cacheKey}\n${url}\n${credential?.generation ?? ''}`
  const existing = cacheWrites.get(requestKey)
  if (existing)
    return existing

  const request = invoke<string>('player_cache_image', {
    request: {
      cacheKey,
      url,
      maxBytes: loadImageCacheSettings().maxSizeMb * 1024 * 1024,
      ...(credential ? { serverBaseUrl: credential.baseUrl, serverAccessToken: credential.accessToken } : {}),
    },
  })
    .catch(() => protectedArtwork ? '' : url)
    .finally(() => cacheWrites.delete(requestKey))
  cacheWrites.set(requestKey, request)
  return request
}

function normalizeImageCacheLimitMb(value: unknown): number {
  const numeric = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(numeric))
    return DEFAULT_IMAGE_CACHE_LIMIT_MB
  return Math.round(Math.max(MIN_IMAGE_CACHE_LIMIT_MB, Math.min(MAX_IMAGE_CACHE_LIMIT_MB, numeric)))
}
