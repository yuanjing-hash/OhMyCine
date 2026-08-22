import type { DownloadPlan, FeedSection, MediaWork, NavigationItem, PlaybackPlan } from './media'

export const PLUGIN_OPERATION_CODES = {
  'site.navigation': 1,
  'site.feed': 2,
  'site.search': 3,
  'site.detail': 4,
  'media.playback': 5,
  'media.download_plan': 6,
  'site.history': 7,
  'playback.progress_sync': 8,
} as const

/**
 * Runtime v1 exports use a copied JSON ABI:
 * - omc_alloc(size: i32): i32
 * - omc_invoke(operationCode: i32, requestPointer: i32, requestLength: i32): i64
 *
 * The invoke result packs response pointer in the high 32 bits and response
 * length in the low 32 bits. Plugins may not retain host request buffers.
 */
export interface WasmPluginExportsV1 {
  readonly memory: WebAssembly.Memory
  omc_api_version(): number
  omc_alloc(size: number): number
  omc_invoke(operationCode: number, requestPointer: number, requestLength: number): bigint
  omc_start?(): void
}

export type PluginLifecycleState
  = 'discovered'
    | 'validating'
    | 'installed'
    | 'disabled'
    | 'starting'
    | 'enabled'
    | 'unhealthy'
    | 'upgrading'
    | 'rollback-pending'
    | 'failed'

export type PluginErrorCode
  = 'invalid-request'
    | 'permission-denied'
    | 'not-authenticated'
    | 'not-found'
    | 'rate-limited'
    | 'upstream-unavailable'
    | 'response-too-large'
    | 'timeout'
    | 'invalid-response'
    | 'internal'

export interface PluginRequestMap {
  'site.navigation': { connectionId: string }
  'site.feed': { connectionId: string, routeKey: string, cursor?: string, refreshSession?: string }
  'site.search': { connectionId: string, query: string, cursor?: string }
  'site.detail': { connectionId: string, itemId: string }
  'media.playback': { connectionId: string, itemId: string, segmentId: string, versionId: string, variantId?: string }
  'media.download_plan': { connectionId: string, itemId: string, segmentId: string, versionId: string, variantId: string }
  'site.history': { connectionId: string, cursor?: string, pageSize?: number }
  'playback.progress_sync': PlaybackProgressSyncRequest
}

export interface PluginResponseMap {
  'site.navigation': readonly NavigationItem[]
  'site.feed': readonly FeedSection[]
  'site.search': readonly FeedSection[]
  'site.detail': MediaWork
  'media.playback': PlaybackPlan
  'media.download_plan': DownloadPlan
  'site.history': PluginHistoryPage
  'playback.progress_sync': PlaybackProgressSyncResponse
}

export type PlaybackProgressEvent = 'started' | 'progress' | 'paused' | 'resumed' | 'stopped' | 'completed'

export interface PlaybackProgressSyncRequest {
  connectionId: string
  itemId: string
  segmentId: string
  versionId: string
  event: PlaybackProgressEvent
  positionSeconds: number
  durationSeconds?: number
  idempotencyKey: string
  occurredAt?: string
}

export interface PlaybackProgressSyncResponse {
  accepted: boolean
  remote: boolean
  duplicate?: boolean
  retryAfterSeconds?: number
}

export interface PluginHistoryItem {
  work: MediaWork
  segmentId?: string
  versionId?: string
  positionSeconds?: number
  durationSeconds?: number
  updatedAt?: string
}

export interface PluginHistoryPage {
  list: readonly PluginHistoryItem[]
  cursor?: string
  hasMore: boolean
}

export interface HostHttpRequest {
  method: 'GET' | 'POST'
  url: string
  headers?: Readonly<Record<string, string>>
  credentialRef?: string
  bodyRef?: string
  timeoutMs?: number
}

export interface HostHttpResponse {
  status: number
  headers: Readonly<Record<string, string>>
  bodyRef: string
}

export interface HostApi {
  http(request: HostHttpRequest): Promise<HostHttpResponse>
  log(level: 'debug' | 'info' | 'warn' | 'error', operation: string, fields?: Readonly<Record<string, string | number | boolean>>): void
  storageGet(key: string): Promise<string | null>
  storageSet(key: string, value: string): Promise<void>
  now(): Promise<string>
  registerAsset(input: HostAssetRegistration): Promise<{ ref: string, expiresAt: string }>
}

export const HOST_OPERATION_CODES = {
  http: 1,
  storageGet: 2,
  storageSet: 3,
  log: 4,
  now: 5,
  eventPoll: 6,
  assetRegister: 7,
} as const

export interface HostAssetRegistration {
  url: string
  headers?: Readonly<Record<string, string>>
  ttlSeconds?: number
}

export interface OhMyCinePlugin {
  invoke<K extends keyof PluginRequestMap>(operation: K, request: PluginRequestMap[K], host: HostApi): Promise<PluginResponseMap[K]>
}
