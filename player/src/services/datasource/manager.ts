import type { DataSource, DataSourceConfig, DataSourceType, HomeSection } from './types'
import { EmbyDataSource } from './emby'
import { toSafeErrorMessage } from './errors'

export class DataSourceManager {
  private readonly sources = new Map<string, DataSource>()

  async syncConfigs(configs: readonly DataSourceConfig[]): Promise<void> {
    const enabledConfigs = configs.filter(config => config.enabled !== false)
    const nextIds = new Set(enabledConfigs.map(config => config.id))
    const errors: string[] = []

    for (const [id, source] of this.sources) {
      if (!nextIds.has(id)) {
        source.destroy()
        this.sources.delete(id)
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
    const old = this.sources.get(config.id)
    if (old)
      old.destroy()

    const source = createDataSource(config.type)
    await source.init(config)
    this.sources.set(config.id, source)
    return source
  }

  removeSource(id: string): void {
    const source = this.sources.get(id)
    source?.clearCache?.()
    source?.destroy()
    this.sources.delete(id)
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
    const sections: HomeSection[] = []

    for (const source of this.getOrderedSources(configs)) {
      if (!source.getHomeSections)
        continue

      try {
        const sourceSections = await source.getHomeSections()
        sections.push(...sourceSections.filter(section => section.items.length > 0))
      }
      catch (error) {
        sections.push({
          id: `error-${source.id}`,
          sourceId: source.id,
          title: `${source.name} 暂不可用：${toSafeErrorMessage(error)}`,
          type: 'recentlyAdded',
          items: [],
        })
      }
    }

    return sections
  }

  exportAllConfigs(): DataSourceConfig[] {
    return this.getAllSources().map(source => source.exportConfig())
  }
}

export function createDataSource(type: DataSourceType): DataSource {
  switch (type) {
    case 'emby':
      return new EmbyDataSource()
    default:
      throw new Error(`${type} data source is not implemented yet.`)
  }
}

export const dataSourceManager = new DataSourceManager()
