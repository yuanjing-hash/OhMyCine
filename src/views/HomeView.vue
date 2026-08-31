<script setup lang="ts">
import type { DataSource, HomeSection, MediaItem, SiteActionDescriptor } from '@/services/datasource/types'
import type { HomeContributionPlacement, HomeContributionPreferences } from '@/services/homeContributionPreferences'
import type { LocalMediaCollection } from '@/services/mediaCollections'
import type { PlaybackHistoryEntry } from '@/services/playbackHistory'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import CachedImage from '@/components/media/CachedImage.vue'
import HeroCarousel from '@/components/media/HeroCarousel.vue'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { contributionPreferenceKey, loadHomeContributionPreferences, saveHomeContributionPreferences } from '@/services/homeContributionPreferences'
import { artworkCacheKey } from '@/services/imageCache'
import { beginMediaActionLongPress, cancelMediaActionLongPress, createMediaActionTarget, endMediaActionLongPress, handleMediaActionKeyboard, moveMediaActionLongPress, openMediaActionContextMenu, requestMediaActionConfirmation, suppressMediaActionClick } from '@/services/mediaActions'
import { annotateMissingCollectionSources, listLocalMediaCollections, removeLocalCollectionMember } from '@/services/mediaCollections'
import { createPlaybackQueue, savePlaybackMediaContext } from '@/services/playbackContext'
import { getPlaybackCompletionBatch, getPlaybackProgress, playbackCompletionKey, PLAYED_STATE_CHANGED_EVENT, shouldResumePlayback } from '@/services/playbackHistory'
import { createPlaybackRouteQuery } from '@/services/playbackRoute'
import { useDataSourceStore } from '@/stores/datasource'

const LOCAL_FILE_SOURCE_ID = 'local-file'

const router = useRouter()
const store = useDataSourceStore()

interface SeriesPlaybackTarget {
  item: MediaItem
  episodes: MediaItem[]
  resumePosition?: number
  canResume: boolean
}

const seriesPlaybackTargets = ref<Record<string, SeriesPlaybackTarget>>({})
const errorMessage = ref<string | null>(null)
const hasLoadedInitialHomeState = ref(false)
const completedItemKeys = ref<Set<string>>(new Set())
const localCollections = ref<LocalMediaCollection[]>([])
const contributionPreferences = ref<HomeContributionPreferences>({})
const isCustomizingHome = ref(false)
const refreshingSectionId = ref<string | null>(null)
const siteActionBusyKey = ref<string | null>(null)
let seriesTargetRefreshId = 0

const hasConfiguredSources = computed(() => store.configs.length > 0)
const hasHomeContent = computed(() => store.homeSections.some(section =>
  section.items.some(item => item.sourceId !== 'placeholder'),
))
const isFirstRunHome = computed(() => hasLoadedInitialHomeState.value && !hasConfiguredSources.value && !hasHomeContent.value)
const heroSection = computed(() => store.homeSections.find(s => s.type === 'hero' && !s.providerIdentity && s.items.length > 0))
const continueWatchingSection = computed(() => store.homeSections.find(s => s.type === 'continueWatching' && s.items.length > 0))
const recentlyAddedSection = computed(() => store.homeSections.find(s => s.type === 'recentlyAdded' && s.items.length > 0))
const contributionSections = computed(() => store.homeSections.filter(section => section.providerIdentity && section.items.length > 0))
const contributionErrors = computed(() => store.homeSections.filter(section => section.providerIdentity && section.errorCode))
const visibleContributionSections = computed(() => contributionSections.value
  .filter(section => contributionPreference(section).enabled)
  .sort((left, right) => contributionPreference(left).order - contributionPreference(right).order))
const contentContributionSections = computed(() => visibleContributionSections.value
  .filter(section => contributionPreference(section).placement === 'content'))
const heroItems = computed(() => dedupeHomeItems([
  ...(heroSection.value?.items ?? []),
  ...visibleContributionSections.value
    .filter(section => contributionPreference(section).placement === 'hero')
    .flatMap(section => section.items),
]).slice(0, 20))
const recentlyAddedItems = computed(() => recentlyAddedSection.value?.items.slice(0, 6) ?? [])
const recentlyAddedBrowseSourceId = computed(() => recentlyAddedSection.value?.sourceId)

function progressPercent(item: MediaItem): string {
  if (typeof item.progress === 'number' && Number.isFinite(item.progress))
    return `${Math.max(0, Math.min(100, item.progress * 100)).toFixed(1)}%`

  if (typeof item.resumePosition === 'number' && typeof item.duration === 'number' && item.duration > 0)
    return `${Math.max(0, Math.min(100, (item.resumePosition / item.duration) * 100)).toFixed(1)}%`

  return '0%'
}

function continueItemTitle(item: MediaItem): string {
  if (item.type !== 'episode')
    return item.name

  const seriesName = item.seriesName?.trim()
  if (!seriesName || item.name.includes(seriesName))
    return item.name

  return `${seriesName} - ${item.name}`
}

function continueSourceLabel(item: MediaItem): string {
  const config = store.configs.find(source => source.id === item.sourceId)
  const sourceName = item.sourceId === LOCAL_FILE_SOURCE_ID
    ? '本机文件'
    : (config?.displayName ?? config?.name ?? item.sourceId)

  return item.progressSource === 'local'
    ? `本机记录 · ${sourceName}`
    : sourceName
}

function itemArtworkUrl(item: MediaItem): string | undefined {
  return firstNonEmpty(item.backdropUrl, item.posterUrl)
}

function firstNonEmpty(...values: Array<string | undefined>): string | undefined {
  return values.find(value => typeof value === 'string' && value.trim().length > 0)
}

function isHomeItemPlayed(item: MediaItem): boolean {
  const sourceType = store.configs.find(config => config.id === item.sourceId)?.type
  if (sourceType === 'emby' || sourceType === 'jellyfin')
    return item.played === true
  return item.played === true || completedItemKeys.value.has(playbackCompletionKey(item.sourceId, item.id))
}

function homeActionTarget(item: MediaItem, context?: 'continueWatching') {
  const source = store.configs.find(config => config.id === item.sourceId)
  return createMediaActionTarget({ ...item, played: isHomeItemPlayed(item) }, source?.type, source?.displayName ?? source?.name, context)
}

function beginHomeActionLongPress(item: MediaItem, event: PointerEvent, context?: 'continueWatching') {
  beginMediaActionLongPress(homeActionTarget(item, context), event)
}

function openHomeActionMenu(item: MediaItem, event: MouseEvent, context?: 'continueWatching') {
  openMediaActionContextMenu(homeActionTarget(item, context), event)
}

function handleHomeCardKey(item: MediaItem, event: KeyboardEvent, action: 'play' | 'detail', context?: 'continueWatching') {
  handleMediaActionKeyboard(homeActionTarget(item, context), event, () => {
    if (action === 'play')
      void handlePlay(item)
    else
      handleDetail(item)
  })
}

async function refreshHomePlayedStates() {
  const items = store.homeSections.flatMap(section => section.items).filter(item => item.sourceId !== 'placeholder')
  const entries = await getPlaybackCompletionBatch(items.map(item => ({ sourceId: item.sourceId, mediaIdentity: item.id })))
  completedItemKeys.value = new Set(entries.filter(entry => entry.completed).map(entry => playbackCompletionKey(entry.sourceId, entry.mediaIdentity)))
}

function handleHomeCardClick(item: MediaItem, event: MouseEvent, action: 'play' | 'detail') {
  if (suppressMediaActionClick(event))
    return
  if (action === 'play')
    void handlePlay(item)
  else
    handleDetail(item)
}

onMounted(async () => {
  window.addEventListener(PLAYED_STATE_CHANGED_EVENT, refreshHomePlayedStates)
  store.loadConfigs()
  contributionPreferences.value = loadHomeContributionPreferences()
  try {
    await store.loadHomeSections()
  }
  finally {
    hasLoadedInitialHomeState.value = true
  }
  await refreshHeroSeriesPlaybackTargets()
  await refreshHomePlayedStates()
  await refreshLocalCollections()
})

function contributionPreference(section: HomeSection) {
  const key = contributionKey(section)
  return contributionPreferences.value[key] ?? {
    enabled: true,
    order: contributionSections.value.findIndex(item => contributionKey(item) === key),
    placement: section.layout === 'hero' ? 'hero' as const : 'content' as const,
  }
}

function contributionKey(section: HomeSection): string {
  return contributionPreferenceKey(section.providerIdentity ?? 'unknown', section.id)
}

async function updateContributionPreference(section: HomeSection, patch: Partial<{ enabled: boolean, order: number, placement: HomeContributionPlacement }>) {
  const key = contributionKey(section)
  contributionPreferences.value = {
    ...contributionPreferences.value,
    [key]: { ...contributionPreference(section), ...patch },
  }
  await saveHomeContributionPreferences(contributionPreferences.value)
}

async function moveContribution(section: HomeSection, direction: -1 | 1) {
  const ordered = [...visibleContributionSections.value]
  const index = ordered.findIndex(item => contributionKey(item) === contributionKey(section))
  const target = index + direction
  if (index < 0 || target < 0 || target >= ordered.length)
    return
  const currentOrder = contributionPreference(ordered[index]).order
  const targetOrder = contributionPreference(ordered[target]).order
  await updateContributionPreference(ordered[index], { order: targetOrder })
  await updateContributionPreference(ordered[target], { order: currentOrder })
}

async function refreshContribution(section: HomeSection) {
  if (!section.refreshable || refreshingSectionId.value)
    return
  refreshingSectionId.value = section.id
  errorMessage.value = null
  try {
    await store.refreshHomeSection(section)
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, '栏目刷新失败，请稍后重试。')
  }
  finally {
    refreshingSectionId.value = null
  }
}

function dedupeHomeItems(items: readonly MediaItem[]): MediaItem[] {
  const seen = new Set<string>()
  return items.filter((item) => {
    const key = item.workIdentity
      ? `${item.workIdentity.scheme}:${item.workIdentity.mediaType}:${item.workIdentity.value}`
      : `${item.sourceId}:${item.id}`
    if (seen.has(key))
      return false
    seen.add(key)
    return true
  })
}

async function performSiteAction(item: MediaItem, action: SiteActionDescriptor) {
  const source = store.getSource(item.sourceId)
  if (!source?.performSiteAction)
    return
  const label = action.label
  let confirmed = false
  if (action.requiresConfirmation || action.destructive) {
    const confirmation = await requestMediaActionConfirmation({
      title: `确认${label}`,
      message: `确定要对“${item.name}”执行“${label}”吗？此操作会同步到远端站点。`,
      confirmLabel: label,
      cancelLabel: '取消',
      danger: action.destructive ? 'destructive' : 'caution',
    })
    if (!confirmation.confirmed)
      return
    confirmed = true
  }
  const busyKey = `${item.sourceId}:${item.id}:${action.id}`
  siteActionBusyKey.value = busyKey
  errorMessage.value = null
  try {
    await source.performSiteAction(item.id, action.id, action.state == null ? undefined : !action.state, confirmed)
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, `${label}失败，请稍后重试。`)
  }
  finally {
    siteActionBusyKey.value = null
  }
}

onBeforeUnmount(() => window.removeEventListener(PLAYED_STATE_CHANGED_EVENT, refreshHomePlayedStates))

watch(heroItems, () => {
  void refreshHeroSeriesPlaybackTargets()
  void refreshHomePlayedStates()
})

function goToSettings() {
  void router.push({ name: 'settings', query: { section: 'datasources' } })
}

function goAddDataSource() {
  void router.push({ name: 'settings', query: { section: 'datasources', action: 'add' } })
}

async function refreshLocalCollections() {
  const collections = await listLocalMediaCollections().catch(() => [])
  localCollections.value = annotateMissingCollectionSources(collections, new Set(store.configs.map(config => config.id)))
}

async function removeManagedCollectionMember(collectionId: string, sourceId: string, itemId: string) {
  await removeLocalCollectionMember(collectionId, sourceId, itemId)
  await refreshLocalCollections()
}

function heroActionLabel(item: MediaItem): string {
  if (item.type === 'series')
    return seriesPlaybackTargets.value[itemKey(item)]?.canResume ? '继续播放' : '播放'

  if (isContainerItem(item))
    return '查看详情'

  return item.resumePosition ? '继续播放' : '播放'
}

async function handlePlay(item: MediaItem) {
  if (item.type === 'series') {
    await playSeriesFromHome(item)
    return
  }

  if (isContainerItem(item)) {
    handleDetail(item)
    return
  }

  await playResolvedItem(item, item.resumePosition)
}

async function playResolvedItem(item: MediaItem, resumePosition?: number, episodes: MediaItem[] = []) {
  errorMessage.value = null
  try {
    await store.syncManager()
    const source = store.getSource(item.sourceId)
    if (!source && item.sourceId !== 'placeholder' && item.sourceId !== LOCAL_FILE_SOURCE_ID) {
      handleDetail(item)
      return
    }

    const queue = episodes.length > 0 ? createPlaybackQueue(episodes, item.id) : undefined
    const contextId = savePlaybackMediaContext({
      sourceId: item.sourceId,
      itemId: item.id,
      title: continueItemTitle(item),
      currentItem: { ...item, resumePosition },
      locator: item.sourceId === LOCAL_FILE_SOURCE_ID || item.sourceId === 'placeholder'
        ? { kind: 'localPath', path: item.path }
        : undefined,
      queue,
    })

    await router.push({
      name: 'player',
      query: createPlaybackRouteQuery({
        sourceId: item.sourceId,
        itemId: item.id,
        contextId,
      }),
    })
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, '无法获取播放地址。')
  }
}

async function playSeriesFromHome(item: MediaItem) {
  errorMessage.value = null
  const target = seriesPlaybackTargets.value[itemKey(item)] ?? await resolveSeriesPlaybackTarget(item)
  if (!target) {
    errorMessage.value = '暂时无法找到可播放分集，请打开详情页查看可用内容。'
    return
  }

  seriesPlaybackTargets.value = {
    ...seriesPlaybackTargets.value,
    [itemKey(item)]: target,
  }
  await playResolvedItem(target.item, target.resumePosition, target.episodes)
}

async function refreshHeroSeriesPlaybackTargets() {
  const refreshId = ++seriesTargetRefreshId
  const seriesItems = heroItems.value.filter(item => item.type === 'series' && item.sourceId !== 'placeholder')
  const settled = await Promise.allSettled(seriesItems.map(async item => [itemKey(item), await resolveSeriesPlaybackTarget(item)] as const))
  if (refreshId !== seriesTargetRefreshId)
    return

  seriesPlaybackTargets.value = Object.fromEntries(
    settled
      .filter((result): result is PromiseFulfilledResult<readonly [string, SeriesPlaybackTarget | null]> => result.status === 'fulfilled')
      .map(result => result.value)
      .filter((entry): entry is readonly [string, SeriesPlaybackTarget] => entry[1] != null),
  )
}

async function resolveSeriesPlaybackTarget(item: MediaItem): Promise<SeriesPlaybackTarget | null> {
  try {
    await store.syncManager()
    const source = store.getSource(item.sourceId)
    if (!source)
      return null

    const episodes = await listSeriesEpisodes(source, item.id)
    if (episodes.length === 0)
      return null

    const providerResumeIndex = episodes.findIndex(episode => isResumePosition(episode.resumePosition, episode.duration))
    const progressEntries = await Promise.all(episodes.map(episode => getPlaybackProgress({ sourceId: episode.sourceId, mediaIdentity: episode.id })))
    const localResume = providerResumeIndex >= 0 ? null : newestLocalResume(progressEntries)
    const index = providerResumeIndex >= 0 ? providerResumeIndex : (localResume?.index ?? 0)
    const episode = episodes[index]
    const providerResumePosition = isResumePosition(episode.resumePosition, episode.duration) ? episode.resumePosition : undefined

    return {
      item: episode,
      episodes,
      resumePosition: providerResumePosition ?? localResume?.entry.position,
      canResume: providerResumeIndex >= 0 || Boolean(localResume),
    }
  }
  catch {
    return null
  }
}

async function listSeriesEpisodes(source: DataSource, seriesId: string): Promise<MediaItem[]> {
  const children = await source.list(seriesId)
  const directEpisodes = sortSeriesEpisodes(children.filter(isPlayableEpisodeItem))
  if (directEpisodes.length > 0)
    return directEpisodes

  const seasons = children.filter(item => item.type === 'season' || item.type === 'folder')
  const seasonEpisodeGroups = await Promise.all(seasons.map(async season => (await source.list(season.id)).filter(isPlayableEpisodeItem)))
  return sortSeriesEpisodes(seasonEpisodeGroups.flat())
}

function isPlayableEpisodeItem(item: MediaItem): boolean {
  return item.type === 'episode' || item.type === 'file' || item.type === 'movie'
}

function newestLocalResume(entries: readonly (PlaybackHistoryEntry | null)[]): { index: number, entry: PlaybackHistoryEntry } | null {
  return entries.reduce<{ index: number, entry: PlaybackHistoryEntry } | null>((best, entry, index) => {
    if (!shouldResumePlayback(entry))
      return best
    if (!best || entry.updatedAt > best.entry.updatedAt)
      return { index, entry }
    return best
  }, null)
}

function sortSeriesEpisodes(episodes: readonly MediaItem[]): MediaItem[] {
  return episodes
    .map((item, index) => ({ item, index }))
    .sort((left, right) => compareEpisodeOrder(left.item, right.item) || left.index - right.index)
    .map(({ item }) => item)
}

function compareEpisodeOrder(left: MediaItem, right: MediaItem): number {
  const leftSeason = normalizedOrderNumber(left.seasonNumber)
  const rightSeason = normalizedOrderNumber(right.seasonNumber)
  if (leftSeason !== rightSeason)
    return leftSeason - rightSeason

  const leftEpisode = normalizedOrderNumber(left.episodeNumber)
  const rightEpisode = normalizedOrderNumber(right.episodeNumber)
  return leftEpisode - rightEpisode
}

function normalizedOrderNumber(value: number | undefined): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : Number.MAX_SAFE_INTEGER
}

function isResumePosition(position: number | undefined, duration: number | undefined): position is number {
  if (typeof position !== 'number' || !Number.isFinite(position) || position < 30)
    return false
  if (typeof duration !== 'number' || !Number.isFinite(duration) || duration <= 0)
    return true
  return position < duration * 0.92 && duration - position > 90
}

function itemKey(item: MediaItem): string {
  return `${item.sourceId}:${item.id}`
}

function handleDetail(item: MediaItem) {
  if (item.sourceId === 'placeholder')
    return
  void router.push({ name: 'media-detail', params: { sourceId: item.sourceId, itemId: item.id } })
}

function isContainerItem(item: MediaItem): boolean {
  return item.type === 'folder' || item.type === 'series' || item.type === 'season'
}
</script>

<template>
  <div class="home-view relative min-h-full transition-colors duration-500">
    <div v-if="isFirstRunHome" class="first-run-home theme-adaptive mobile-nav-safe relative min-h-screen overflow-hidden px-4 pb-8 pt-20 sm:px-8 sm:pt-32 lg:px-12">
      <div class="first-run-scene" aria-hidden="true">
        <div class="first-run-screen" />
        <div class="first-run-shelf">
          <span class="first-run-poster first-run-poster--large" />
          <span class="first-run-poster first-run-poster--mid" />
          <span class="first-run-poster first-run-poster--small" />
        </div>
      </div>

      <section class="relative mx-auto flex min-h-[calc(100vh-10rem)] max-w-6xl items-center">
        <div class="max-w-xl">
          <p class="text-sm font-medium" style="color: var(--gp-text)">
            OhMyCine Player
          </p>
          <h1 class="mt-4 text-4xl font-bold leading-tight sm:text-5xl" style="color: var(--gp-text-full)">
            添加你的第一座影视库
          </h1>
          <p class="mt-5 max-w-lg text-base leading-7 sm:text-lg" style="color: var(--gp-text)">
            连接一个数据源后，首页会自动聚合海报、继续观看和最新入库。
          </p>

          <div class="mt-8 flex flex-wrap gap-3">
            <button
              type="button"
              class="first-run-primary-action inline-flex items-center gap-2 rounded-2xl px-5 py-3 text-sm font-semibold transition-all"
              @click="goAddDataSource"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
              </svg>
              添加数据源
            </button>
            <button
              type="button"
              class="first-run-secondary-action inline-flex items-center gap-2 rounded-2xl px-5 py-3 text-sm font-semibold transition-all"
              @click="goToSettings"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <path d="M4 7h16M4 12h16M4 17h10" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
              </svg>
              管理数据源
            </button>
          </div>

          <p class="mt-7 max-w-lg text-sm leading-6" style="color: var(--gp-text-dim)">
            当前可添加 Emby、OpenList/Alist；Jellyfin、本地文件、CloudDrive2 等来源会继续接入。
          </p>
        </div>
      </section>
    </div>

    <div v-else class="mobile-nav-safe flex min-h-screen flex-col gap-6 px-4 pb-6 sm:gap-8 sm:px-6 lg:px-8">
      <section class="home-hero-shell relative -mx-4 overflow-hidden rounded-b-[2rem] sm:-mx-6 lg:-mx-8">
        <HeroCarousel
          v-if="heroItems.length"
          :items="heroItems"
          :action-label="heroActionLabel"
          @play="handlePlay"
          @detail="handleDetail"
        />

        <div
          v-if="!hasConfiguredSources"
          class="pointer-events-none absolute inset-0 flex items-center justify-end p-4 sm:p-8 lg:p-10"
        >
          <div class="glass-panel pointer-events-auto w-full max-w-sm rounded-3xl p-5 sm:p-6 lg:max-w-md">
            <p class="text-xs font-medium uppercase tracking-[0.3em]" style="color: var(--gp-text)">
              OhMyCine Player
            </p>
            <h1 class="mt-3 text-2xl font-bold" style="color: var(--gp-text-full)">
              添加你的第一个影视库
            </h1>
            <p class="mt-3 text-sm leading-6" style="color: var(--gp-text)">
              绑定 Emby、Jellyfin、OpenList/Alist、CloudDrive2 或本地文件后，这里会聚合展示海报轮播、继续观看和最新影片。
            </p>
            <button
              class="mt-5 rounded-2xl px-5 py-3 text-sm font-semibold transition-colors"
              style="color: var(--gp-text-full); background: var(--gp-active)"
              @click="goToSettings"
            >
              去设置数据源
            </button>
          </div>
        </div>
      </section>

      <div
        v-if="errorMessage"
        class="theme-adaptive rounded-2xl border border-red-400/20 bg-red-400/10 px-5 py-4 text-sm text-red-100"
      >
        {{ errorMessage }}
      </div>

      <section v-if="contributionSections.length || contributionErrors.length" class="home-contribution-toolbar glass-panel rounded-2xl px-4 py-3 sm:px-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <strong class="text-sm" style="color: var(--gp-text-full)">在线推荐</strong>
            <span class="ml-2 text-xs" style="color: var(--gp-text-dim)">由 Server 插件提供 · 本机布局</span>
          </div>
          <button class="rounded-xl px-3 py-2 text-xs transition-colors" style="background: var(--gp-hover); color: var(--gp-text)" @click="isCustomizingHome = !isCustomizingHome">
            {{ isCustomizingHome ? '完成' : '自定义首页' }}
          </button>
        </div>
        <div v-if="contributionErrors.length" class="mt-3 grid gap-2">
          <div v-for="section in contributionErrors" :key="section.id" class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-amber-300/20 bg-amber-300/8 px-3 py-2 text-xs text-amber-100">
            <span>{{ section.sourceLabel ?? section.title }} 暂时不可用（{{ section.errorCode }}），其它媒体来源不受影响。</span>
            <button v-if="section.refreshable" type="button" class="rounded-lg bg-white/10 px-2 py-1 disabled:opacity-50" :disabled="refreshingSectionId != null" @click="refreshContribution(section)">
              {{ refreshingSectionId === section.id ? '重试中…' : '重试' }}
            </button>
          </div>
        </div>
        <div v-if="isCustomizingHome" class="mt-3 grid gap-2 lg:grid-cols-2">
          <div v-for="section in contributionSections" :key="contributionKey(section)" class="flex flex-wrap items-center gap-2 rounded-xl border border-white/10 px-3 py-2">
            <label class="flex min-w-0 flex-1 items-center gap-2 text-sm">
              <input type="checkbox" :checked="contributionPreference(section).enabled" @change="updateContributionPreference(section, { enabled: ($event.target as HTMLInputElement).checked })">
              <span class="truncate">{{ section.title }}</span>
            </label>
            <select
              class="rounded-lg border border-white/10 bg-black/20 px-2 py-1 text-xs"
              :value="contributionPreference(section).placement"
              @change="updateContributionPreference(section, { placement: ($event.target as HTMLSelectElement).value as HomeContributionPlacement })"
            >
              <option value="content">
                独立栏目
              </option>
              <option value="hero">
                顶部精选
              </option>
            </select>
            <button aria-label="上移栏目" class="rounded-lg px-2 py-1 text-xs" style="background: var(--gp-hover)" @click="moveContribution(section, -1)">
              ↑
            </button>
            <button aria-label="下移栏目" class="rounded-lg px-2 py-1 text-xs" style="background: var(--gp-hover)" @click="moveContribution(section, 1)">
              ↓
            </button>
          </div>
        </div>
      </section>

      <div class="theme-adaptive grid grid-cols-1 gap-6 pb-8 xl:grid-cols-2">
        <section class="home-feed-section glass-panel rounded-[1.75rem] p-6">
          <div class="mb-5 flex items-center justify-between">
            <div>
              <p class="text-xs uppercase tracking-[0.24em]" style="color: var(--gp-text-dim)">
                Resume
              </p>
              <h2 class="mt-1 text-xl font-bold" style="color: var(--gp-text-full)">
                {{ continueWatchingSection?.title ?? '继续观看' }}
              </h2>
            </div>
            <button class="text-xs transition-colors" style="color: var(--gp-text)">
              全部 >
            </button>
          </div>

          <div v-if="continueWatchingSection?.items.length" class="flex gap-3 overflow-x-auto cinema-scrollbar">
            <article
              v-for="item in continueWatchingSection.items"
              :key="`${item.sourceId}:${item.id}`"
              class="continue-card group w-48 flex-shrink-0 cursor-pointer overflow-hidden rounded-2xl transition-transform hover:scale-[1.03]"
              data-media-action-target
              tabindex="0"
              @pointerdown="beginHomeActionLongPress(item, $event, 'continueWatching')"
              @pointermove="moveMediaActionLongPress"
              @pointerup="endMediaActionLongPress"
              @pointercancel="cancelMediaActionLongPress($event.pointerId)"
              @pointerleave="cancelMediaActionLongPress($event.pointerId)"
              @click="handleHomeCardClick(item, $event, 'play')"
              @contextmenu="openHomeActionMenu(item, $event, 'continueWatching')"
              @keydown="handleHomeCardKey(item, $event, 'play', 'continueWatching')"
            >
              <div class="relative h-28 media-placeholder overflow-hidden">
                <CachedImage :cache-key="artworkCacheKey(item.sourceId, item.id, 'backdrop')" :src="itemArtworkUrl(item)" :alt="continueItemTitle(item)" class="h-full w-full object-cover" loading="lazy" decoding="async">
                  <template #fallback>
                    <div class="flex h-full w-full items-center justify-center bg-white/6 p-4 text-center text-xs font-semibold text-white/48">
                      {{ continueItemTitle(item) }}
                    </div>
                  </template>
                </CachedImage>
                <div class="progress-track absolute bottom-0 left-0 right-0 h-1">
                  <div class="progress-value h-full rounded-full" :style="{ width: progressPercent(item) }" />
                </div>
                <span v-if="isHomeItemPlayed(item)" class="home-played-badge" aria-label="已播放">✓</span>
              </div>
              <div class="px-2 py-3">
                <h3 class="truncate text-sm font-medium" style="color: var(--gp-text-full)">
                  {{ continueItemTitle(item) }}
                </h3>
                <p class="mt-1 truncate text-[0.68rem]" style="color: var(--gp-text-dim)">
                  {{ continueSourceLabel(item) }}
                </p>
              </div>
            </article>
          </div>

          <div v-else class="flex h-40 flex-col items-center justify-center rounded-3xl empty-panel text-center">
            <p class="text-sm font-medium" style="color: var(--gp-text-full)">
              还没有观看记录
            </p>
            <p class="mt-2 max-w-xs text-xs leading-5" style="color: var(--gp-text)">
              添加影视库并开始播放后，播放进度会出现在这里。
            </p>
          </div>
        </section>

        <section class="home-feed-section glass-panel rounded-[1.75rem] p-6">
          <div class="mb-5 flex items-center justify-between">
            <div>
              <p class="text-xs uppercase tracking-[0.24em]" style="color: var(--gp-text-dim)">
                Library
              </p>
              <h2 class="mt-1 text-xl font-bold" style="color: var(--gp-text-full)">
                最新影片
              </h2>
            </div>
            <button
              class="text-xs transition-colors disabled:opacity-30"
              style="color: var(--gp-text)"
              :disabled="!recentlyAddedBrowseSourceId"
              @click="recentlyAddedBrowseSourceId && router.push(`/source/${recentlyAddedBrowseSourceId}`)"
            >
              浏览全部 >
            </button>
          </div>

          <div v-if="recentlyAddedItems.length" class="flex gap-4 overflow-x-auto cinema-scrollbar">
            <article
              v-for="item in recentlyAddedItems"
              :key="`${item.sourceId}:${item.id}`"
              class="recent-card group w-28 flex-shrink-0 cursor-pointer overflow-hidden rounded-2xl transition-transform hover:scale-[1.04]"
              data-media-action-target
              tabindex="0"
              @pointerdown="beginHomeActionLongPress(item, $event)"
              @pointermove="moveMediaActionLongPress"
              @pointerup="endMediaActionLongPress"
              @pointercancel="cancelMediaActionLongPress($event.pointerId)"
              @pointerleave="cancelMediaActionLongPress($event.pointerId)"
              @click="handleHomeCardClick(item, $event, 'detail')"
              @contextmenu="openHomeActionMenu(item, $event)"
              @keydown="handleHomeCardKey(item, $event, 'detail')"
            >
              <div class="relative aspect-[2/3] media-placeholder">
                <CachedImage :cache-key="artworkCacheKey(item.sourceId, item.id, 'poster')" :src="item.posterUrl" :alt="continueItemTitle(item)" class="h-full w-full object-cover" loading="lazy" decoding="async">
                  <template #fallback>
                    <div class="poster-placeholder flex h-full items-center justify-center">
                      <svg width="28" height="28" viewBox="0 0 24 24" fill="none">
                        <rect x="3" y="3" width="18" height="18" rx="3" stroke="currentColor" stroke-width="1.5" />
                      </svg>
                    </div>
                  </template>
                </CachedImage>
                <div class="recent-play-overlay absolute inset-0 flex items-center justify-center bg-black/55 opacity-0 transition-opacity group-hover:opacity-100">
                  <button
                    class="recent-play-button flex h-9 w-9 items-center justify-center rounded-full bg-white text-black transition-transform hover:scale-110"
                    :aria-label="`${heroActionLabel(item)} ${continueItemTitle(item)}`"
                    @click.stop="handlePlay(item)"
                  >
                    <svg width="12" height="12" viewBox="0 0 14 14" fill="currentColor">
                      <path d="M3 1l9 6-9 6V1z" />
                    </svg>
                  </button>
                </div>
                <span v-if="isHomeItemPlayed(item)" class="home-played-badge" aria-label="已播放">✓</span>
              </div>
              <h3 class="truncate px-1 py-2 text-xs font-medium" style="color: var(--gp-text-full)">
                {{ continueItemTitle(item) }}
              </h3>
            </article>
          </div>

          <div v-else class="flex h-40 flex-col items-center justify-center rounded-3xl empty-panel text-center">
            <p class="text-sm font-medium" style="color: var(--gp-text-full)">
              等待影视库内容
            </p>
            <p class="mt-2 max-w-xs text-xs leading-5" style="color: var(--gp-text)">
              配置数据源后，最新入库和推荐内容会在这里横向展示。
            </p>
            <button
              v-if="!hasConfiguredSources"
              class="mt-4 rounded-2xl px-4 py-2 text-xs font-semibold transition-colors"
              style="color: var(--gp-text-full); background: var(--gp-hover)"
              @click="goAddDataSource"
            >
              添加数据源
            </button>
          </div>
        </section>
      </div>

      <section
        v-for="section in contentContributionSections"
        :key="contributionKey(section)"
        class="home-online-section glass-panel rounded-[1.75rem] p-5 sm:p-6"
      >
        <div class="mb-5 flex flex-wrap items-center justify-between gap-3">
          <div>
            <p class="text-xs uppercase tracking-[0.2em]" style="color: var(--gp-text-dim)">
              {{ section.sourceLabel ?? 'Server 在线来源' }}
            </p>
            <h2 class="mt-1 text-xl font-bold" style="color: var(--gp-text-full)">
              {{ section.title }}
            </h2>
          </div>
          <button
            v-if="section.refreshable"
            class="rounded-xl px-3 py-2 text-xs transition-colors disabled:opacity-50"
            style="background: var(--gp-hover); color: var(--gp-text)"
            :disabled="refreshingSectionId != null"
            @click="refreshContribution(section)"
          >
            {{ refreshingSectionId === section.id ? '刷新中…' : '换一批' }}
          </button>
        </div>
        <div class="flex gap-4 overflow-x-auto pb-2 cinema-scrollbar">
          <article
            v-for="item in section.items"
            :key="`${item.sourceId}:${item.id}`"
            class="online-media-card group w-40 flex-shrink-0 cursor-pointer overflow-hidden rounded-2xl"
            data-media-action-target
            tabindex="0"
            @pointerdown="beginHomeActionLongPress(item, $event)"
            @pointermove="moveMediaActionLongPress"
            @pointerup="endMediaActionLongPress"
            @pointercancel="cancelMediaActionLongPress($event.pointerId)"
            @pointerleave="cancelMediaActionLongPress($event.pointerId)"
            @click="handleHomeCardClick(item, $event, 'detail')"
            @contextmenu="openHomeActionMenu(item, $event)"
            @keydown="handleHomeCardKey(item, $event, 'detail')"
          >
            <div class="relative aspect-[16/10] media-placeholder overflow-hidden">
              <CachedImage :cache-key="artworkCacheKey(item.sourceId, item.id, 'backdrop')" :src="item.backdropUrl ?? item.posterUrl" :alt="item.name" class="h-full w-full object-cover" loading="lazy" decoding="async">
                <template #fallback>
                  <div class="flex h-full items-center justify-center p-3 text-center text-xs text-white/50">
                    {{ item.name }}
                  </div>
                </template>
              </CachedImage>
              <span v-if="item.duration" class="absolute bottom-2 right-2 rounded-md bg-black/70 px-1.5 py-0.5 text-[0.65rem] text-white">{{ Math.round(item.duration / 60) }} 分钟</span>
            </div>
            <div class="px-1 py-3">
              <h3 class="line-clamp-2 text-sm font-medium" style="color: var(--gp-text-full)">
                {{ item.name }}
              </h3>
              <p class="mt-1 truncate text-xs" style="color: var(--gp-text-dim)">
                {{ item.year ?? section.sourceLabel ?? '在线内容' }}
              </p>
              <div v-if="item.siteActions?.length" class="mt-2 flex gap-1 overflow-x-auto" @click.stop>
                <button
                  v-for="action in item.siteActions.slice(0, 3)"
                  :key="action.id"
                  type="button"
                  class="flex-shrink-0 rounded-lg border border-white/10 bg-white/6 px-2 py-1 text-[0.65rem] text-white/65 hover:bg-white/12 disabled:opacity-50"
                  :disabled="siteActionBusyKey != null"
                  @click="performSiteAction(item, action)"
                >
                  {{ siteActionBusyKey === `${item.sourceId}:${item.id}:${action.id}` ? '处理中…' : action.label }}
                </button>
              </div>
            </div>
          </article>
        </div>
      </section>
      <section v-if="localCollections.some(collection => collection.members.length)" class="glass-panel rounded-[1.75rem] p-6">
        <h2 class="text-xl font-bold">
          本地收藏与合集
        </h2>
        <p class="mt-1 text-sm text-white/40">
          Player 本地 · 可跨来源
        </p>
        <div class="mt-4 grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          <article v-for="collection in localCollections.filter(item => item.members.length)" :key="collection.id" class="rounded-xl border border-white/10 bg-white/5 p-3">
            <strong>{{ collection.name }}</strong><small class="ml-2 text-white/40">{{ collection.members.length }} 项</small>
            <div class="mt-2 grid gap-1">
              <div v-for="member in collection.members.slice(0, 8)" :key="`${member.sourceId}:${member.itemId}`" class="flex items-center gap-2 rounded-lg bg-white/5 px-2 py-1.5 text-xs">
                <span class="min-w-0 flex-1 truncate">{{ member.title }}</span><em v-if="member.missing" class="not-italic text-amber-300">来源缺失</em><button aria-label="移除成员" class="text-white/45" @click="removeManagedCollectionMember(collection.id, member.sourceId, member.itemId)">
                  ×
                </button>
              </div>
            </div>
          </article>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.home-view {
  background: var(--color-bg);
  color: var(--color-text);
}

.home-played-badge { position: absolute; right: .45rem; bottom: .45rem; z-index: 2; display: grid; width: 1.55rem; height: 1.55rem; place-items: center; border-radius: 50%; color: #fff; background: rgba(34,197,94,.9); font-size: .72rem; font-weight: 900; box-shadow: 0 6px 16px rgba(0,0,0,.32); }

.first-run-home {
  --gp-text: var(--color-text-secondary);
  --gp-text-full: var(--color-text);
  --gp-text-dim: var(--color-text-tertiary);
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.055), transparent 24%),
    linear-gradient(118deg, rgba(74, 158, 255, 0.14), transparent 42%),
    linear-gradient(248deg, rgba(34, 197, 94, 0.08), transparent 38%),
    var(--color-bg);
}

.first-run-scene {
  position: absolute;
  inset: 0;
  overflow: hidden;
  pointer-events: none;
}

.first-run-scene::before {
  position: absolute;
  inset: 0;
  background:
    linear-gradient(90deg, rgba(255, 255, 255, 0.045) 1px, transparent 1px),
    linear-gradient(180deg, transparent 0%, rgba(255, 255, 255, 0.06) 58%, transparent 59%);
  background-size: 92px 100%, 100% 100%;
  content: '';
  mask-image: linear-gradient(90deg, transparent, black 34%, black 100%);
}

.first-run-scene::after {
  position: absolute;
  right: -10vw;
  bottom: -18vh;
  width: 70vw;
  height: 42vh;
  background:
    repeating-linear-gradient(90deg, rgba(255, 255, 255, 0.12) 0 1px, transparent 1px 70px),
    repeating-linear-gradient(0deg, rgba(255, 255, 255, 0.12) 0 1px, transparent 1px 52px);
  content: '';
  opacity: 0.24;
  transform: perspective(640px) rotateX(66deg);
  transform-origin: center bottom;
}

.first-run-screen {
  position: absolute;
  top: 16vh;
  right: max(6vw, 56px);
  width: min(48vw, 620px);
  height: min(44vh, 360px);
  border: 1px solid rgba(255, 255, 255, 0.14);
  border-radius: var(--radius-2xl);
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.11), rgba(255, 255, 255, 0.02)),
    linear-gradient(135deg, rgba(74, 158, 255, 0.18), transparent 58%),
    linear-gradient(245deg, rgba(255, 255, 255, 0.055), transparent 46%);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.2),
    inset 0 -1px 0 rgba(255, 255, 255, 0.055),
    0 36px 120px rgba(0, 0, 0, 0.42);
  transform: perspective(900px) rotateY(-13deg) rotateX(3deg);
}

.first-run-screen::before {
  position: absolute;
  inset: 18px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: var(--radius-xl);
  background:
    linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.08), transparent),
    repeating-linear-gradient(90deg, rgba(255, 255, 255, 0.09) 0 1px, transparent 1px 54px);
  content: '';
  opacity: 0.62;
}

.first-run-screen::after {
  position: absolute;
  left: 26px;
  right: 26px;
  bottom: 24px;
  height: 7px;
  border-radius: var(--radius-full);
  background: rgba(255, 255, 255, 0.16);
  content: '';
}

.first-run-shelf {
  position: absolute;
  right: max(10vw, 96px);
  bottom: 14vh;
  display: flex;
  align-items: flex-end;
  gap: 18px;
  transform: perspective(820px) rotateY(-16deg);
}

.first-run-poster {
  display: block;
  width: 86px;
  border: 1px solid rgba(255, 255, 255, 0.16);
  border-radius: var(--radius-lg);
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.18), rgba(255, 255, 255, 0.035)),
    linear-gradient(145deg, rgba(74, 158, 255, 0.28), rgba(34, 197, 94, 0.1));
  box-shadow: 0 22px 60px rgba(0, 0, 0, 0.34);
}

.first-run-poster--large {
  height: 132px;
}

.first-run-poster--mid {
  height: 112px;
  opacity: 0.78;
}

.first-run-poster--small {
  height: 92px;
  opacity: 0.58;
}

.first-run-primary-action {
  color: var(--color-text-inverse);
  background: color-mix(in srgb, var(--color-text) 94%, transparent);
  box-shadow: 0 16px 42px rgba(255, 255, 255, 0.12);
}

.first-run-primary-action:hover {
  transform: translateY(-1px);
  background: var(--color-text);
}

.first-run-secondary-action {
  color: var(--gp-text-full);
  border: 1px solid rgba(255, 255, 255, 0.16);
  background: rgba(255, 255, 255, 0.08);
  backdrop-filter: blur(24px) saturate(1.5);
  -webkit-backdrop-filter: blur(24px) saturate(1.5);
}

.first-run-secondary-action:hover {
  transform: translateY(-1px);
  border-color: rgba(255, 255, 255, 0.24);
  background: rgba(255, 255, 255, 0.12);
}

.empty-panel {
  border: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 42%, transparent);
}

.media-placeholder {
  background: color-mix(in srgb, var(--color-surface) 42%, transparent);
}

.poster-placeholder {
  color: var(--color-text-tertiary);
}

.progress-track {
  background: var(--color-surface-hover);
}

.progress-value {
  background: color-mix(in srgb, var(--color-text) 60%, transparent);
}

@media (max-width: 900px) {
  .first-run-screen {
    top: 20vh;
    right: -36vw;
    width: 88vw;
    opacity: 0.34;
  }

  .first-run-shelf {
    display: none;
  }
}

@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .home-view > .mobile-nav-safe {
    gap: 1.4rem;
  }

  .home-hero-shell {
    border-radius: 0 0 8px 8px;
  }

  .home-feed-section {
    border: 0;
    border-radius: 0;
    padding: 0;
    background: transparent;
    box-shadow: none;
    backdrop-filter: none;
    -webkit-backdrop-filter: none;
  }

  .home-feed-section > div:first-child {
    margin-bottom: 0.8rem;
  }

  .home-feed-section h2 {
    font-size: 1.05rem;
  }

  .continue-card {
    width: min(78vw, 19rem);
    border-radius: 8px;
    background: var(--surface-soft);
  }

  .continue-card > div:first-child {
    height: auto;
    aspect-ratio: 16 / 9;
  }

  .recent-card {
    width: min(38vw, 9.5rem);
    border-radius: 8px;
  }

  .recent-play-overlay {
    align-items: flex-end;
    justify-content: flex-end;
    padding: 0.55rem;
    background: linear-gradient(to top, rgba(0, 0, 0, 0.68), transparent 55%);
    opacity: 1;
  }

  .recent-play-button {
    width: 2.55rem;
    height: 2.55rem;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.4);
  }

  .first-run-home section {
    align-items: flex-end;
    padding-bottom: 5rem;
  }

  .first-run-home h1 {
    font-size: 2rem;
  }

  .first-run-primary-action,
  .first-run-secondary-action {
    min-height: 3rem;
    border-radius: 8px;
  }
}
</style>
