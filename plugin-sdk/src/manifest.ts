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
  'media.metadata',
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
  navigationMode?: 'flat' | 'hierarchical'
  permissions: readonly PluginPermission[]
  configSchema: Readonly<Record<string, unknown>>
  settingsPage?: PluginSettingsPage
  author: string
  license: string
  homepage?: string
  source: string
  packageSha256: string
  signature?: PluginSignature
  update?: PluginUpdateInfo
  changelog?: string
}

export interface PluginSettingsPage {
  version: 1
  tabs: readonly PluginSettingsTab[]
}

export interface PluginSettingsTab {
  id: string
  title: string
  sections: readonly PluginSettingsSection[]
}

export interface PluginSettingsSection {
  id: string
  title: string
  description?: string
  fields: readonly PluginSettingsField[]
}

export interface PluginSettingsField {
  type: 'switch' | 'text' | 'number' | 'select' | 'notice' | 'credential-status'
  key?: string
  label: string
  description?: string
  placeholder?: string
  options?: readonly { label: string, value: string }[]
  minimum?: number
  maximum?: number
}
