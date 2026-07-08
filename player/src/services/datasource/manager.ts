import type { DataSource, DataSourceConfig, DataSourceType, HomeSection } from './types'
import { AlistDataSource } from './alist'
import { EmbyDataSource } from './emby'
import { toSafeErrorMessage } from './errors'
import { collectHomeSectionsFromSources } from './homeAggregation'
import { LocalFileDataSource } from './local'

export class DataSourceManager {
  private readonly sources = new Map<string, DataSource>()
  private readonly configSignatures = new Map<string, string>()

  async syncConfigs(configs: readonly DataSourceConfig[]): Promise<void> {
    const enabledConfigs = configs.filter(config => config.enabled !== false)
    const nextIds = new Set(enabledConfigs.map(config => config.id))
    const errors: string[] = []

    for (const [id, source] of this.sources) {
      if (!nextIds.has(id)) {
        source.destroy()
        this.sources.delete(id)
        this.configSignatures.delete(id)
      }
    }

    for (const config of enabledConfigs) {
      try {
        await this.addSource(config)
      }
      catch (error) {
        this.removeSource(config.id)
        errors.push(`${config.displayName ?? config.name}: ${toSafeErrorMessage(error)}`)
      }
    }

    if (errors.length > 0)
      throw new Error(errors.join('；'))
  }

  async addSource(config: DataSourceConfig): Promise<DataSource> {
    const signature = stableConfigSignature(config)
    const old = this.sources.get(config.id)
    if (old && this.configSignatures.get(config.id) === signature)
      return old

    if (old)
      old.destroy()

    const source = createDataSource(config.type)
    await source.init(config)
    this.sources.set(config.id, source)
    this.configSignatures.set(config.id, signature)
    return source
  }

  removeSource(id: string): void {
    const source = this.sources.get(id)
    source?.clearCache?.()
    source?.destroy()
    this.sources.delete(id)
    this.configSignatures.delete(id)
  }

  clearSourceCache(id: string): void {
    const source = this.sources.get(id)
    source?.clearCache?.()
  }

  getSource(id: string): DataSource | null {
    return this.sources.get(id) ?? null
  }

  getAllSources(): DataSource[] {
    return [...this.sources.values()]
  }

  getOrderedSources(configs: readonly DataSourceConfig[]): DataSource[] {
    return [...configs]
      .sort((a, b) => a.order - b.order)
      .map(config => this.sources.get(config.id))
      .filter((source): source is DataSource => source != null)
  }

  async getAggregatedHome(configs: readonly DataSourceConfig[]): Promise<HomeSection[]> {
    return collectHomeSectionsFromSources(this.getOrderedSources(configs))
  }

  exportAllConfigs(): DataSourceConfig[] {
    return this.getAllSources().map(source => source.exportConfig())
  }
}

export function createDataSource(type: DataSourceType): DataSource {
  switch (type) {
    case 'emby':
      return new EmbyDataSource()
    case 'alist':
      return new AlistDataSource()
    case 'local':
      return new LocalFileDataSource()
    default:
      throw new Error(`${type} data source is not implemented yet.`)
  }
}

function stableConfigSignature(config: DataSourceConfig): string {
  return JSON.stringify(sortConfigValue(config))
}

function sortConfigValue(value: unknown): unknown {
  if (Array.isArray(value))
    return value.map(sortConfigValue)

  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, nested]) => [key, sortConfigValue(nested)]),
    )
  }

  return value
}

export const dataSourceManager = new DataSourceManager()
