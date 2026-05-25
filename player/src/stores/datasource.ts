import type { DataSource, DataSourceConfig, HomeSection, MediaItem } from '@/services/datasource/types'
import type { PlaybackHistoryEntry } from '@/services/playbackHistory'
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { removeCredential } from '@/services/datasource/credentialStore'
import { dataSourceManager } from '@/services/datasource/manager'
import { listLocalContinueWatching, toContinueWatchingMediaItem } from '@/services/playbackHistory'

const STORAGE_KEY = 'ohmycine-datasources'

export const useDataSourceStore = defineStore('datasource', () => {
  const configs = ref<DataSourceConfig[]>([])
  const activeSourceId = ref<string | null>(null)
  const homeSections = ref<HomeSection[]>([])
  const isLoading = ref(false)
  const lastError = ref<string | null>(null)
  let homeLoadId = 0

  const orderedConfigs = computed(() =>
    [...configs.value].sort((a, b) => a.order - b.order),
  )

  const activeSource = computed(() =>
    configs.value.find(c => c.id === activeSourceId.value) ?? null,
  )

  function loadConfigs() {
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (raw)
        configs.value = sanitizeConfigs(JSON.parse(raw) as unknown)
      void syncManager()
    }
    catch {
      configs.value = []
    }
  }

  function saveConfigs() {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(configs.value.map(sanitizePersistedConfig)))
  }

  async function replaceConfig(config: DataSourceConfig) {
    const previousConfigs = cloneConfigs(configs.value)
    try {
      const safeConfig = sanitizePersistedConfig(config)
      const idx = configs.value.findIndex(c => c.id === safeConfig.id)
      if (idx >= 0) {
        const existing = configs.value[idx]
        configs.value[idx] = sanitizePersistedConfig({
          ...existing,
          ...safeConfig,
          order: safeConfig.order ?? existing.order,
        })
      }
      else {
        configs.value.push({ ...safeConfig, order: safeConfig.order ?? configs.value.length })
      }
      saveConfigs()
      await syncManager()
    }
    catch (error) {
      configs.value = previousConfigs
      throw error
    }
  }

  async function syncManager() {
    try {
      await dataSourceManager.syncConfigs(configs.value)
      lastError.value = null
    }
    catch (error) {
      lastError.value = error instanceof Error ? error.message : '数据源初始化失败'
    }
  }

  async function addConfig(config: Omit<DataSourceConfig, 'id' | 'order'> & Partial<Pick<DataSourceConfig, 'id' | 'order'>>) {
    const id = config.id ?? `${config.type}-${Date.now()}`
    const order = config.order ?? configs.value.length
    const previousConfigs = cloneConfigs(configs.value)
    try {
      configs.value.push(sanitizePersistedConfig({ ...config, id, order }))
      saveConfigs()
      await syncManager()
    }
    catch (error) {
      configs.value = previousConfigs
      throw error
    }
    return id
  }

  async function updateConfig(id: string, patch: Partial<DataSourceConfig>) {
    const idx = configs.value.findIndex(c => c.id === id)
    if (idx === -1)
      return
    const previousConfigs = cloneConfigs(configs.value)
    try {
      configs.value[idx] = sanitizePersistedConfig({ ...configs.value[idx], ...patch })
      saveConfigs()
      await syncManager()
    }
    catch (error) {
      configs.value = previousConfigs
      throw error
    }
  }

  async function removeConfig(id: string) {
    const config = configs.value.find(c => c.id === id)
    const credentialRef = typeof config?.extra?.credentialRef === 'string' ? config.extra.credentialRef : null
    if (credentialRef)
      await removeCredential(credentialRef)
    dataSourceManager.removeSource(id)
    configs.value = configs.value.filter(c => c.id !== id)
    configs.value.forEach((c, i) => c.order = i)
    saveConfigs()
  }

  async function clearSourceCache(id: string) {
    await syncManager()
    dataSourceManager.clearSourceCache(id)
    homeSections.value = homeSections.value.filter(section => section.sourceId !== id)
  }

  function reorderConfigs(ids: string[]) {
    const map = new Map(configs.value.map(c => [c.id, c]))
    configs.value = ids
      .map((id, order) => {
        const c = map.get(id)
        if (c)
          c.order = order
        return c
      })
      .filter((c): c is DataSourceConfig => c != null)
    saveConfigs()
  }

  async function loadHomeSections() {
    const loadId = ++homeLoadId
    isLoading.value = true
    try {
      await syncManager().catch(() => undefined)
      const [sections, localContinueEntries] = await Promise.all([
        loadAggregatedHomeSections(orderedConfigs.value),
        listLocalContinueWatchingSafely(20),
      ])
      const localContinueItems = await enrichLocalContinueWatchingItems(localContinueEntries.map(toContinueWatchingMediaItem))
      const continueSection = mergeContinueWatchingSections(sections, localContinueItems)
      const nonContinueSections = sections.filter(section => section.type !== 'continueWatching')
      const mergedSections = continueSection.items.length > 0
        ? [continueSection, ...nonContinueSections]
        : nonContinueSections

      if (loadId !== homeLoadId)
        return

      homeSections.value = mergedSections.length > 0
        ? mergedSections
        : [
            {
              id: 'hero',
              title: 'Featured',
              type: 'hero',
              items: generatePlaceholderHeroItems(),
            },
            continueSection,
          ]
    }
    finally {
      if (loadId === homeLoadId)
        isLoading.value = false
    }
  }

  async function enrichLocalContinueWatchingItems(items: readonly MediaItem[]): Promise<MediaItem[]> {
    return Promise.all(items.map(enrichLocalContinueWatchingItem))
  }

  async function enrichLocalContinueWatchingItem(item: MediaItem): Promise<MediaItem> {
    const needsEpisodeParent = item.type === 'episode' && !item.seriesName
    if (hasArtwork(item) && !needsEpisodeParent)
      return item

    const source = dataSourceManager.getSource(item.sourceId)
    if (!source)
      return item

    try {
      const detail = await source.getDetail(item.id)
      return {
        ...item,
        posterUrl: firstNonEmpty(item.posterUrl, detail.posterUrl),
        backdropUrl: firstNonEmpty(item.backdropUrl, detail.backdropUrl),
        duration: item.duration ?? detail.duration,
        libraryId: item.libraryId ?? detail.libraryId,
        seriesName: firstNonEmpty(item.seriesName, detail.seriesName),
      }
    }
    catch {
      return item
    }
  }

  function getSource(id: string): DataSource | null {
    return dataSourceManager.getSource(id)
  }

  return {
    configs,
    orderedConfigs,
    activeSourceId,
    activeSource,
    homeSections,
    isLoading,
    lastError,
    loadConfigs,
    addConfig,
    replaceConfig,
    updateConfig,
    removeConfig,
    clearSourceCache,
    reorderConfigs,
    loadHomeSections,
    getSource,
    syncManager,
  }
})

function mergeContinueWatchingSections(sections: readonly HomeSection[], localItems: readonly MediaItem[]): HomeSection {
  const providerItems = sections.filter(section => section.type === 'continueWatching').flatMap(section => section.items)
  const merged = new Map<string, MediaItem>()

  for (const item of providerItems)
    merged.set(continueWatchingKey(item), item)

  for (const item of localItems) {
    const key = continueWatchingKey(item)
    const providerItem = merged.get(key)
    merged.set(key, providerItem ? mergeContinueWatchingItem(item, providerItem) : item)
  }

  return {
    id: 'continue-watching',
    title: '继续观看',
    type: 'continueWatching',
    items: [...localItems.map(continueWatchingKey), ...providerItems.map(continueWatchingKey)]
      .filter((key, index, keys) => keys.indexOf(key) === index)
      .map(key => merged.get(key))
      .filter((item): item is MediaItem => item != null),
  }
}

function mergeContinueWatchingItem(localItem: MediaItem, providerItem: MediaItem): MediaItem {
  return {
    ...localItem,
    ...providerItem,
    libraryId: providerItem.libraryId ?? localItem.libraryId,
    posterUrl: firstNonEmpty(providerItem.posterUrl, localItem.posterUrl),
    backdropUrl: firstNonEmpty(providerItem.backdropUrl, localItem.backdropUrl),
    duration: providerItem.duration ?? localItem.duration,
    path: providerItem.path || localItem.path,
    resumePosition: localItem.resumePosition ?? providerItem.resumePosition,
    progress: localItem.progress ?? providerItem.progress,
    progressSource: providerItem.progressSource,
    seriesName: firstNonEmpty(providerItem.seriesName, localItem.seriesName),
  }
}

function firstNonEmpty(...values: Array<string | undefined>): string | undefined {
  return values.find(value => typeof value === 'string' && value.trim().length > 0)
}

function hasArtwork(item: MediaItem): boolean {
  return firstNonEmpty(item.backdropUrl, item.posterUrl) != null
}

async function loadAggregatedHomeSections(configs: readonly DataSourceConfig[]): Promise<HomeSection[]> {
  try {
    return await dataSourceManager.getAggregatedHome(configs)
  }
  catch {
    return []
  }
}

async function listLocalContinueWatchingSafely(limit: number): Promise<PlaybackHistoryEntry[]> {
  try {
    return await listLocalContinueWatching(limit)
  }
  catch {
    return []
  }
}

function continueWatchingKey(item: MediaItem): string {
  return `${item.sourceId}:${item.id}`
}

function sanitizeConfigs(value: unknown): DataSourceConfig[] {
  if (!Array.isArray(value))
    return []

  return value
    .filter((config): config is DataSourceConfig => {
      if (typeof config !== 'object' || config == null)
        return false
      const record = config as Record<string, unknown>
      return typeof record.id === 'string'
        && typeof record.type === 'string'
        && typeof record.name === 'string'
        && typeof record.order === 'number'
        && typeof record.url === 'string'
    })
    .map(sanitizePersistedConfig)
}

function sanitizePersistedConfig(config: DataSourceConfig): DataSourceConfig {
  const safeExtra = Object.fromEntries(
    Object.entries(config.extra ?? {}).filter(([key]) => !isSensitiveConfigKey(key)),
  )

  return {
    id: config.id,
    type: config.type,
    name: config.name,
    displayName: config.displayName,
    iconUrl: config.iconUrl,
    order: config.order,
    url: config.url,
    enabled: config.enabled,
    extra: safeExtra,
  }
}

function isSensitiveConfigKey(key: string): boolean {
  const normalized = key.toLowerCase()
  return ['apikey', 'api_key', 'access_token', 'passwd', 'pwd'].includes(normalized)
    || normalized.includes('token')
    || normalized.includes('password')
    || normalized.includes('username')
    || normalized.includes('authorization')
    || normalized.includes('cookie')
    || normalized.includes('passkey')
}

function cloneConfigs(configs: readonly DataSourceConfig[]): DataSourceConfig[] {
  return configs.map(config => ({
    ...config,
    extra: config.extra ? { ...config.extra } : undefined,
  }))
}

function generatePlaceholderHeroItems(): MediaItem[] {
  const items: MediaItem[] = [
    {
      id: 'placeholder-1',
      sourceId: 'placeholder',
      name: 'Dune: Part Two',
      type: 'movie',
      tagline: 'Long live the fighters.',
      overview: 'Paul Atreides unites with the Fremen while on a warpath of revenge against the conspirators who destroyed his family.',
      year: 2024,
      rating: 8.5,
      duration: 166,
      path: '/placeholder/dune-part-two',
      posterUrl: '',
      backdropUrl: '',
    },
    {
      id: 'placeholder-2',
      sourceId: 'placeholder',
      name: 'Blade Runner 2049',
      type: 'movie',
      tagline: 'The key to the future is finally unearthed.',
      overview: 'A young blade runner discovers a long-buried secret that leads him to track down former blade runner Rick Deckard.',
      year: 2017,
      rating: 8.0,
      duration: 164,
      path: '/placeholder/blade-runner-2049',
    },
    {
      id: 'placeholder-3',
      sourceId: 'placeholder',
      name: 'Interstellar',
      type: 'movie',
      tagline: 'Mankind was born on Earth. It was never meant to die here.',
      overview: 'A team of explorers travel through a wormhole in space in an attempt to ensure humanity\'s survival.',
      year: 2014,
      rating: 8.7,
      duration: 169,
      path: '/placeholder/interstellar',
    },
    {
      id: 'placeholder-4',
      sourceId: 'placeholder',
      name: 'The Batman',
      type: 'movie',
      tagline: 'Unmask the truth.',
      overview: 'Batman ventures into Gotham City\'s underworld when a sadistic killer leaves behind cryptic clues.',
      year: 2022,
      rating: 7.8,
      duration: 176,
      path: '/placeholder/the-batman',
    },
    {
      id: 'placeholder-5',
      sourceId: 'placeholder',
      name: 'Arrival',
      type: 'movie',
      tagline: 'Why are they here?',
      overview: 'A linguist works with the military to communicate with alien lifeforms after twelve mysterious spacecraft land.',
      year: 2016,
      rating: 7.9,
      duration: 116,
      path: '/placeholder/arrival',
    },
  ]

  const backdrops = [
    'linear-gradient(135deg, #0c1a2e 0%, #1a3a5c 40%, #2d1b4e 70%, #0a0a1a 100%)',
    'linear-gradient(135deg, #1a0a2e 0%, #2d1b6e 40%, #0a2a3a 70%, #0a0a1a 100%)',
    'linear-gradient(135deg, #0a1a0a 0%, #1a3a1a 40%, #2d4e1b 70%, #0a0a1a 100%)',
    'linear-gradient(135deg, #2e0a0a 0%, #4e1b1b 40%, #1a0a2e 70%, #0a0a1a 100%)',
    'linear-gradient(135deg, #0a2e2e 0%, #1b4e4e 40%, #2e0a2e 70%, #0a0a1a 100%)',
  ]

  return items.map((item, i) => ({
    ...item,
    backdropUrl: backdrops[i],
  }))
}
