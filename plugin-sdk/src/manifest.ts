export const PLUGIN_API_VERSION = '1' as const

export const PLUGIN_CAPABILITIES = [
  'site.navigation',
  'site.feed',
  'site.search',
  'site.detail',
  'site.user_library',
  'site.interaction',
  'media.playback',
  'media.quality_switch',
  'media.subtitle',
  'media.danmaku',
  'media.download_plan',
  'home.contribution',
  'feed.refresh',
  'site.history',
  'playback.progress_sync',
] as const

export type PluginCapability = typeof PLUGIN_CAPABILITIES[number]

export type PluginPermission
  = | { kind: 'network.http', domains: readonly string[] }
    | { kind: 'credential.use', scopes: readonly string[] }
    | { kind: 'storage.private', maxBytes: number }
    | { kind: 'event.subscribe', topics: readonly string[] }
    | { kind: 'download.plan' }

export interface PluginSignature {
  algorithm: 'ed25519'
  keyId: string
  value: string
}

export interface PluginUpdateInfo {
  registryUrl: string
  channel?: 'stable' | 'beta'
}

export interface PluginManifestV1 {
  schemaVersion: 1
  id: string
  name: string
  description: string
  version: string
  apiVersion: typeof PLUGIN_API_VERSION
  minServerVersion: string
  maxServerVersion?: string
  runtime: 'wasm'
  entry: string
  capabilities: readonly PluginCapability[]
  permissions: readonly PluginPermission[]
  configSchema: Readonly<Record<string, unknown>>
  author: string
  license: string
  homepage?: string
  source: string
  packageSha256: string
  signature?: PluginSignature
  update?: PluginUpdateInfo
  changelog?: string
}
