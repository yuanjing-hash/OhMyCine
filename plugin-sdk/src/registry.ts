export interface PluginRepositoryV1 {
  schemaVersion: 1
  repository: {
    id: string
    name: string
    homepage: string
    updatedAt: string
  }
  plugins: readonly PluginRepositoryEntryV1[]
}

export interface PluginRepositoryEntryV1 {
  id: string
  name: string
  description: string
  version: string
  channel: 'stable' | 'beta'
  categories: readonly string[]
  iconUrl?: string
  manifestUrl: string
  packageUrl: string
  packageSha256: string
  minServerVersion: string
  maxServerVersion?: string
  releaseNotes?: string
}
