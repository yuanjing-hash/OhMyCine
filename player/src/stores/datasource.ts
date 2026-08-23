import type { DataSource, DataSourceConfig, HomeSection, MediaItem, MediaLibrary } from '@/services/datasource/types'
import type { PlaybackHistoryEntry } from '@/services/playbackHistory'
import type { RawFileSourceType } from '@/services/scraper/types'
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { getAppSetting, removeAppSetting, setAppSetting } from '@/services/appSettings'
import { removeCredential } from '@/services/datasource/credentialStore'
import { rememberPlaybackTargetsForItems } from '@/services/datasource/identityMerge'
import { dataSourceManager } from '@/services/datasource/manager'
import { logoutServerBestEffort } from '@/services/datasource/server'
import { clearPlayerMediaCache, deleteMediaPlaybackPreferencesForSource } from '@/services/mediaPlaybackPreferences'
import { removeNavigationShortcutBinding } from '@/services/navigationShortcuts'
import { deletePlaybackHistoryForSource, isCompletedPosition, listLocalContinueWatching, toContinueWatchingMediaItem } from '@/services/playbackHistory'
import { clearRawSourceScanCache } from '@/services/scraper/localScanCache'

const STORAGE_KEY = 'ohmycine-datasources'
const DISPLAY_CACHE_KEY = 'ohmycine-media-display-cache-v1'
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

interface PersistedDisplayCache {
  homeSections: HomeSection[]
  homeLoadedAt: number
  sourceRootSnapshots: Record<string, SourceRootSnapshot>
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
  let displayCacheHydrated = false

  const orderedConfigs = computed(() =>
    [...configs.value].sort((a, b) => a.order - b.order),
  )

  const activeSource = computed(() =>
    configs.value.find(c => c.id === activeSourceId.value) ?? null,
  )

  function loadConfigs() {
    hydrateDisplayCache()
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
    if (config?.type === 'server')
      await logoutServerBestEffort(config)
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
    void persistDisplayCache()

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

  async function reloadSource(id: string) {
    dataSourceManager.removeSource(id)
    await syncManager()
  }

  async function clearAllMediaCaches() {
    await syncManager().catch(() => undefined)
    dataSourceManager.clearAllSourceCaches()
    homeLoadId++
    homeSections.value = []
    homeLoadedAt.value = 0
    sourceRootSnapshots.value = {}
    await removeAppSetting(DISPLAY_CACHE_KEY)
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
    const hasCachedContent = homeSections.value.length > 0
    const cacheIsFresh = homeLoadedAt.value > 0 && hasCachedContent && Date.now() - homeLoadedAt.value < HOME_CACHE_TTL_MS
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
      void persistDisplayCache()
    }
    finally {
      if (loadId === homeLoadId)
        isLoading.value = false
    }
  }

  async function refreshHomeSection(section: HomeSection): Promise<void> {
    if (!section.sourceId || !section.refreshKey)
      throw new Error('该栏目不支持单独刷新。')
    await syncManager()
    const source = dataSourceManager.getSource(section.sourceId)
    if (!source?.refreshHomeSection)
      throw new Error('当前媒体来源不支持单独刷新栏目。')
    const replacement = await source.refreshHomeSection(section.refreshKey)
    const index = homeSections.value.findIndex(item => item.id === section.id)
    if (index < 0)
      return
    const next = [...homeSections.value]
    next.splice(index, 1, ...replacement)
    homeSections.value = next
    homeLoadedAt.value = Date.now()
    void persistDisplayCache()
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
    void persistDisplayCache()
  }

  function invalidateSourceRootSnapshot(id: string) {
    if (!sourceRootSnapshots.value[id])
      return
    const next = { ...sourceRootSnapshots.value }
    delete next[id]
    sourceRootSnapshots.value = next
    void persistDisplayCache()
  }

  function invalidateHomeCache() {
    homeLoadedAt.value = 0
    void persistDisplayCache()
  }

  async function searchAllSources(keyword: string, limit = 60, sourceIds?: readonly string[]): Promise<MediaItem[]> {
    await syncManager()
    return dataSourceManager.searchAcrossSources(orderedConfigs.value, keyword, { limit, sourceIds })
  }

  function hydrateDisplayCache() {
    if (displayCacheHydrated)
      return
    displayCacheHydrated = true
    const raw = getAppSetting(DISPLAY_CACHE_KEY)
    if (!raw)
      return
    try {
      const cache = sanitizePersistedDisplayCache(JSON.parse(raw) as unknown)
      homeSections.value = cache.homeSections
      homeLoadedAt.value = cache.homeLoadedAt
      sourceRootSnapshots.value = cache.sourceRootSnapshots
      rememberPlaybackTargetsForItems([
        ...cache.homeSections.flatMap(section => section.items),
        ...Object.values(cache.sourceRootSnapshots).flatMap(snapshot => snapshot.homeSections.flatMap(section => section.items)),
      ])
    }
    catch {
      homeSections.value = []
      homeLoadedAt.value = 0
      sourceRootSnapshots.value = {}
    }
  }

  async function persistDisplayCache() {
    const cache: PersistedDisplayCache = {
      homeSections: homeSections.value.map(sanitizeDisplayHomeSection),
      homeLoadedAt: homeLoadedAt.value,
      sourceRootSnapshots: Object.fromEntries(
        Object.entries(sourceRootSnapshots.value).map(([sourceId, snapshot]) => [sourceId, {
          libraries: snapshot.libraries.map(sanitizeDisplayLibrary),
          homeSections: snapshot.homeSections.map(sanitizeDisplayHomeSection),
          updatedAt: snapshot.updatedAt,
        }]),
      ),
    }
    await setAppSetting(DISPLAY_CACHE_KEY, JSON.stringify(cache))
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
    reloadSource,
    clearAllMediaCaches,
    reorderConfigs,
    loadHomeSections,
    refreshHomeSection,
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

function sanitizePersistedDisplayCache(value: unknown): PersistedDisplayCache {
  if (!isRecord(value))
    return { homeSections: [], homeLoadedAt: 0, sourceRootSnapshots: {} }

  const homeSections = Array.isArray(value.homeSections)
    ? value.homeSections.map(sanitizeDisplayHomeSection).filter(section => section.items.length > 0)
    : []
  const sourceRootSnapshots = isRecord(value.sourceRootSnapshots)
    ? Object.fromEntries(
        Object.entries(value.sourceRootSnapshots).flatMap(([sourceId, snapshot]) => {
          if (!isRecord(snapshot))
            return []
          return [[sourceId, {
            libraries: Array.isArray(snapshot.libraries) ? snapshot.libraries.map(sanitizeDisplayLibrary) : [],
            homeSections: Array.isArray(snapshot.homeSections)
              ? snapshot.homeSections.map(sanitizeDisplayHomeSection).filter(section => section.items.length > 0)
              : [],
            updatedAt: 0,
          }] as const]
        }),
      )
    : {}

  return {
    homeSections,
    homeLoadedAt: homeSections.length > 0 ? 0 : safeTimestamp(value.homeLoadedAt),
    sourceRootSnapshots,
  }
}

function sanitizeDisplayHomeSection(value: unknown): HomeSection {
  const section = isRecord(value) ? value : {}
  const type = ['hero', 'continueWatching', 'recentlyAdded', 'recommended', 'libraryRow'].includes(String(section.type))
    ? section.type as HomeSection['type']
    : 'libraryRow'
  return {
    id: safeText(section.id, `cached-${type}`),
    sourceId: optionalText(section.sourceId),
    title: safeText(section.title, '媒体'),
    type,
    items: Array.isArray(section.items) ? section.items.map(sanitizeDisplayMediaItem) : [],
    providerIdentity: sanitizeIdentityText(section.providerIdentity),
    sourceLabel: optionalText(section.sourceLabel),
    refreshKey: sanitizeIdentityText(section.refreshKey),
    refreshable: section.refreshable === true,
    layout: ['hero', 'row', 'poster-grid', 'video-list'].includes(String(section.layout))
      ? section.layout as HomeSection['layout']
      : undefined,
    errorCode: sanitizeIdentityText(section.errorCode),
  }
}

function sanitizeDisplayMediaItem(value: unknown): MediaItem {
  const item = isRecord(value) ? value : {}
  const type = ['movie', 'series', 'season', 'episode', 'folder', 'file'].includes(String(item.type))
    ? item.type as MediaItem['type']
    : 'file'
  return {
    id: safeText(item.id, 'cached-item'),
    sourceId: safeText(item.sourceId, 'unknown'),
    originType: sanitizeDataSourceType(item.originType),
    libraryId: optionalText(item.libraryId),
    name: safeText(item.name, '未命名媒体'),
    originalTitle: optionalText(item.originalTitle),
    titleLogoUrl: sanitizeDisplayUrl(item.titleLogoUrl),
    type,
    posterUrl: sanitizeDisplayUrl(item.posterUrl),
    backdropUrl: sanitizeDisplayUrl(item.backdropUrl),
    year: optionalNumber(item.year),
    rating: optionalNumber(item.rating),
    overview: optionalText(item.overview),
    tagline: optionalText(item.tagline),
    duration: optionalNumber(item.duration),
    size: optionalNumber(item.size),
    modified: optionalText(item.modified),
    path: '',
    resumePosition: optionalNumber(item.resumePosition),
    progress: optionalNumber(item.progress),
    progressSource: item.progressSource === 'local' ? 'local' : undefined,
    played: item.played === true,
    favorite: item.favorite === true,
    seriesName: optionalText(item.seriesName),
    seasonNumber: optionalNumber(item.seasonNumber),
    episodeNumber: optionalNumber(item.episodeNumber),
    workIdentity: sanitizeMediaIdentity(item.workIdentity),
    exactIdentity: sanitizeIdentityText(item.exactIdentity),
    playbackTargets: sanitizePlaybackTargets(item.playbackTargets),
    siteActions: sanitizeSiteActions(item.siteActions),
  }
}

function sanitizeSiteActions(value: unknown): MediaItem['siteActions'] {
  if (!Array.isArray(value))
    return undefined
  const allowed = new Set(['like.add', 'like.remove', 'favorite.add', 'favorite.remove', 'watch-later.add', 'watch-later.remove', 'follow.add', 'follow.remove', 'history.remove'])
  const seen = new Set<string>()
  const actions = value.flatMap((raw): NonNullable<MediaItem['siteActions']>[number][] => {
    if (!isRecord(raw) || typeof raw.id !== 'string' || !allowed.has(raw.id) || seen.has(raw.id))
      return []
    const label = optionalText(raw.label)
    if (!label)
      return []
    seen.add(raw.id)
    return [{
      id: raw.id as NonNullable<MediaItem['siteActions']>[number]['id'],
      label,
      state: typeof raw.state === 'boolean' ? raw.state : undefined,
      requiresConfirmation: raw.requiresConfirmation === true,
      destructive: raw.destructive === true,
    }]
  })
  return actions.length > 0 ? actions : undefined
}

function sanitizeMediaIdentity(value: unknown): MediaItem['workIdentity'] {
  if (!isRecord(value))
    return undefined
  const scheme = value.scheme
  const mediaType = value.mediaType
  const identityValue = sanitizeIdentityText(value.value)
  if (!['tmdb', 'emby', 'server', 'plugin'].includes(String(scheme)) || !['movie', 'series', 'season', 'episode', 'file'].includes(String(mediaType)) || !identityValue)
    return undefined
  return { scheme: scheme as NonNullable<MediaItem['workIdentity']>['scheme'], mediaType: mediaType as NonNullable<MediaItem['workIdentity']>['mediaType'], value: identityValue }
}

function sanitizePlaybackTargets(value: unknown): MediaItem['playbackTargets'] {
  if (!Array.isArray(value))
    return undefined
  const targets = value.slice(0, 16).flatMap((target) => {
    if (!isRecord(target))
      return []
    const sourceId = optionalText(target.sourceId)
    const itemId = optionalText(target.itemId)
    const label = optionalText(target.label)
    if (!sourceId || !itemId || !label)
      return []
    return [{ sourceId, itemId, label, mediaSourceId: optionalText(target.mediaSourceId), exactIdentity: sanitizeIdentityText(target.exactIdentity) }]
  })
  return targets.length > 0 ? targets : undefined
}

function sanitizeIdentityText(value: unknown): string | undefined {
  const text = optionalText(value)
  return text && text.length <= 256 && !/[\r\n]/.test(text) ? text : undefined
}

function sanitizeDataSourceType(value: unknown): MediaItem['originType'] {
  return ['emby', 'jellyfin', 'alist', 'clouddrive2', 'webdav', 'server', '115', '123', 'quark', 'local'].includes(String(value))
    ? value as MediaItem['originType']
    : undefined
}

function sanitizeDisplayLibrary(value: unknown): MediaLibrary {
  const library = isRecord(value) ? value : {}
  const type = ['movies', 'series', 'anime', 'music', 'mixed', 'folders'].includes(String(library.type))
    ? library.type as MediaLibrary['type']
    : 'mixed'
  return {
    id: safeText(library.id, 'cached-library'),
    sourceId: safeText(library.sourceId, 'unknown'),
    name: safeText(library.name, '媒体库'),
    type,
    posterUrl: sanitizeDisplayUrl(library.posterUrl),
    backdropUrl: sanitizeDisplayUrl(library.backdropUrl),
    itemCount: optionalNumber(library.itemCount),
    providerIdentity: sanitizeIdentityText(library.providerIdentity),
  }
}

function sanitizeDisplayUrl(value: unknown): string | undefined {
  const text = optionalText(value)
  if (!text)
    return undefined
  try {
    const url = new URL(text)
    if (!['http:', 'https:'].includes(url.protocol))
      return undefined
    for (const key of url.searchParams.keys()) {
      if (isSensitiveUrlKey(key))
        return undefined
    }
    return url.toString()
  }
  catch {
    return undefined
  }
}

function isSensitiveUrlKey(key: string): boolean {
  const normalized = key.toLowerCase()
  return normalized.includes('token')
    || normalized.includes('key')
    || normalized.includes('auth')
    || normalized.includes('signature')
    || normalized === 'sig'
    || normalized === 'expires'
    || normalized === 'exp'
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function safeText(value: unknown, fallback: string): string {
  return optionalText(value) ?? fallback
}

function optionalText(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}

function optionalNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function safeTimestamp(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}

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
  const providerResumePosition = usableResumePosition(providerItem)
  const providerProgress = providerResumePosition == null
    ? undefined
    : providerItem.progress ?? progressRatio(providerResumePosition, providerItem.duration)

  return {
    ...localItem,
    ...providerItem,
    libraryId: providerItem.libraryId ?? localItem.libraryId,
    posterUrl: firstNonEmpty(providerItem.posterUrl, localItem.posterUrl),
    backdropUrl: firstNonEmpty(providerItem.backdropUrl, localItem.backdropUrl),
    titleLogoUrl: firstNonEmpty(providerItem.titleLogoUrl, localItem.titleLogoUrl),
    duration: providerItem.duration ?? localItem.duration,
    path: providerItem.path || localItem.path,
    resumePosition: providerResumePosition ?? localItem.resumePosition,
    progress: providerProgress ?? localItem.progress,
    progressSource: providerResumePosition != null ? providerItem.progressSource : localItem.progressSource,
    seriesName: firstNonEmpty(providerItem.seriesName, localItem.seriesName),
  }
}

function usableResumePosition(item: MediaItem): number | undefined {
  const position = item.resumePosition
  if (typeof position !== 'number' || !Number.isFinite(position) || position < 30)
    return undefined
  return isCompletedPosition(position, item.duration) ? undefined : position
}

function progressRatio(position: number, duration: number | undefined): number | undefined {
  if (typeof duration !== 'number' || !Number.isFinite(duration) || duration <= 0)
    return undefined
  return Math.max(0, Math.min(1, position / duration))
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
