import type { DataSource, DataSourceConfig, HomeSection, MediaItem, MediaLibrary } from '@/services/datasource/types'
import type { PlaybackHistoryEntry } from '@/services/playbackHistory'
import type { RawFileSourceType } from '@/services/scraper/types'
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { getAppSetting, setAppSetting } from '@/services/appSettings'
import { removeCredential } from '@/services/datasource/credentialStore'
import { dataSourceManager } from '@/services/datasource/manager'
import { clearPlayerMediaCache, deleteMediaPlaybackPreferencesForSource } from '@/services/mediaPlaybackPreferences'
import { removeNavigationShortcutBinding } from '@/services/navigationShortcuts'
import { deletePlaybackHistoryForSource, listLocalContinueWatching, toContinueWatchingMediaItem } from '@/services/playbackHistory'
import { clearRawSourceScanCache } from '@/services/scraper/localScanCache'

const STORAGE_KEY = 'ohmycine-datasources'
const HOME_CACHE_TTL_MS = 5 * 60 * 1000
const SOURCE_ROOT_CACHE_TTL_MS = 5 * 60 * 1000

export interface SourceRootSnapshot {
  libraries: MediaLibrary[]
  homeSections: HomeSection[]
  updatedAt: number
}

export interface LoadHomeSectionsOptions {
  force?: boolean
  background?: boolean
}

export const useDataSourceStore = defineStore('datasource', () => {
  const configs = ref<DataSourceConfig[]>([])
  const activeSourceId = ref<string | null>(null)
  const homeSections = ref<HomeSection[]>([])
  const homeLoadedAt = ref(0)
  const sourceRootSnapshots = ref<Record<string, SourceRootSnapshot>>({})
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
      const raw = getAppSetting(STORAGE_KEY)
      if (raw)
        configs.value = sanitizeConfigs(JSON.parse(raw) as unknown)
      void syncManager()
    }
    catch {
      configs.value = []
    }
  }

  async function saveConfigs() {
    await setAppSetting(STORAGE_KEY, JSON.stringify(configs.value.map(sanitizePersistedConfig)))
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
      await saveConfigs()
      await syncManager()
      invalidateSourceRootSnapshot(safeConfig.id)
      invalidateHomeCache()
    }
    catch (error) {
      configs.value = previousConfigs
      await saveConfigs().catch(() => undefined)
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
      await saveConfigs()
      await syncManager()
      invalidateSourceRootSnapshot(id)
      invalidateHomeCache()
    }
    catch (error) {
      configs.value = previousConfigs
      await saveConfigs().catch(() => undefined)
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
      await saveConfigs()
      await syncManager()
      invalidateSourceRootSnapshot(id)
      invalidateHomeCache()
    }
    catch (error) {
      configs.value = previousConfigs
      await saveConfigs().catch(() => undefined)
      throw error
    }
  }

  async function removeConfig(id: string) {
    const config = configs.value.find(c => c.id === id)
    const credentialRef = typeof config?.extra?.credentialRef === 'string' ? config.extra.credentialRef : null
    const previousConfigs = cloneConfigs(configs.value)
    configs.value = configs.value.filter(c => c.id !== id)
    configs.value.forEach((c, i) => c.order = i)
    try {
      await saveConfigs()
    }
    catch (error) {
      configs.value = previousConfigs
      throw error
    }
    dataSourceManager.removeSource(id)
    invalidateSourceRootSnapshot(id)
    invalidateHomeCache()
    homeLoadId++
    isLoading.value = false
    if (activeSourceId.value === id)
      activeSourceId.value = null
    homeSections.value = homeSections.value
      .filter(section => section.sourceId !== id)
      .map(section => ({
        ...section,
        items: section.items.filter(item => item.sourceId !== id),
      }))
      .filter(section => section.items.length > 0)

    const cleanupTasks: Promise<unknown>[] = [
      deletePlaybackHistoryForSource(id),
      deleteMediaPlaybackPreferencesForSource(id),
      removeNavigationShortcutBinding(`source:${id}`),
    ]
    if (config && isRawFileSourceType(config.type)) {
      cleanupTasks.push(clearRawSourceScanCache(
        id,
        config.type,
        config.type === 'local' ? '/' : readConfiguredRootPath(config),
      ))
    }
    if (credentialRef)
      cleanupTasks.push(removeCredential(credentialRef))

    await Promise.allSettled(cleanupTasks)
  }

  async function clearSourceCache(id: string) {
    await syncManager()
    dataSourceManager.clearSourceCache(id)
    invalidateSourceRootSnapshot(id)
    invalidateHomeCache()
    homeSections.value = homeSections.value.filter(section => section.sourceId !== id)
  }

  async function clearAllMediaCaches() {
    await syncManager().catch(() => undefined)
    dataSourceManager.clearAllSourceCaches()
    homeLoadId++
    homeSections.value = []
    homeLoadedAt.value = 0
    sourceRootSnapshots.value = {}
    return clearPlayerMediaCache()
  }

  async function reorderConfigs(ids: string[]) {
    const map = new Map(configs.value.map(c => [c.id, c]))
    configs.value = ids
      .map((id, order) => {
        const c = map.get(id)
        if (c)
          c.order = order
        return c
      })
      .filter((c): c is DataSourceConfig => c != null)
    await saveConfigs()
    invalidateHomeCache()
  }

  async function loadHomeSections(options: LoadHomeSectionsOptions = {}) {
    const hasCachedContent = homeLoadedAt.value > 0 && homeSections.value.length > 0
    const cacheIsFresh = hasCachedContent && Date.now() - homeLoadedAt.value < HOME_CACHE_TTL_MS
    if (!options.force && cacheIsFresh)
      return

    const loadId = ++homeLoadId
    const showLoading = !options.background && !hasCachedContent
    if (showLoading)
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
      homeLoadedAt.value = Date.now()
    }
    finally {
      if (loadId === homeLoadId)
        isLoading.value = false
    }
  }

  function getSourceRootSnapshot(id: string): SourceRootSnapshot | null {
    return sourceRootSnapshots.value[id] ?? null
  }

  function isSourceRootSnapshotFresh(id: string): boolean {
    const snapshot = getSourceRootSnapshot(id)
    return snapshot != null && Date.now() - snapshot.updatedAt < SOURCE_ROOT_CACHE_TTL_MS
  }

  function setSourceRootSnapshot(id: string, snapshot: Omit<SourceRootSnapshot, 'updatedAt'>) {
    sourceRootSnapshots.value = {
      ...sourceRootSnapshots.value,
      [id]: {
        libraries: snapshot.libraries,
        homeSections: snapshot.homeSections,
        updatedAt: Date.now(),
      },
    }
  }

  function invalidateSourceRootSnapshot(id: string) {
    if (!sourceRootSnapshots.value[id])
      return
    const next = { ...sourceRootSnapshots.value }
    delete next[id]
    sourceRootSnapshots.value = next
  }

  function invalidateHomeCache() {
    homeLoadedAt.value = 0
  }

  async function searchAllSources(keyword: string, limit = 60): Promise<MediaItem[]> {
    await syncManager()
    return dataSourceManager.searchAcrossSources(orderedConfigs.value, keyword, { limit })
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
        titleLogoUrl: firstNonEmpty(item.titleLogoUrl, detail.titleLogoUrl),
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
    homeLoadedAt,
    isLoading,
    lastError,
    loadConfigs,
    addConfig,
    replaceConfig,
    updateConfig,
    removeConfig,
    clearSourceCache,
    clearAllMediaCaches,
    reorderConfigs,
    loadHomeSections,
    getSourceRootSnapshot,
    isSourceRootSnapshotFresh,
    setSourceRootSnapshot,
    invalidateSourceRootSnapshot,
    invalidateHomeCache,
    searchAllSources,
    getSource,
    syncManager,
  }
})

const RAW_FILE_SOURCE_TYPES = new Set<RawFileSourceType>(['alist', 'clouddrive2', 'webdav', 'local', '115', '123', 'quark'])

function isRawFileSourceType(type: DataSourceConfig['type']): type is RawFileSourceType {
  return RAW_FILE_SOURCE_TYPES.has(type as RawFileSourceType)
}

function readConfiguredRootPath(config: DataSourceConfig): string {
  const rootPath = typeof config.extra?.rootPath === 'string' ? config.extra.rootPath.trim() : ''
  return rootPath || '/'
}

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
    titleLogoUrl: firstNonEmpty(providerItem.titleLogoUrl, localItem.titleLogoUrl),
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
