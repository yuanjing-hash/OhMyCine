<script setup lang="ts">
import type { DataSource, MediaItem } from '@/services/datasource/types'
import type { PlaybackHistoryEntry } from '@/services/playbackHistory'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import HeroCarousel from '@/components/media/HeroCarousel.vue'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { createPlaybackQueue, savePlaybackMediaContext } from '@/services/playbackContext'
import { getPlaybackProgress, shouldResumePlayback } from '@/services/playbackHistory'
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
let seriesTargetRefreshId = 0
let settledRefreshTimer: number | undefined

const hasSources = computed(() => store.configs.length > 0)
const heroSection = computed(() => store.homeSections.find(s => s.type === 'hero'))
const continueWatchingSection = computed(() => store.homeSections.find(s => s.type === 'continueWatching'))
const recentlyAddedSection = computed(() => store.homeSections.find(s => s.type === 'recentlyAdded'))
const heroItems = computed(() => heroSection.value?.items ?? [])
const recentlyAddedItems = computed(() => recentlyAddedSection.value?.items.slice(0, 6) ?? [])

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

onMounted(async () => {
  store.loadConfigs()
  await store.loadHomeSections()
  scheduleSettledContinueWatchingRefresh()
  await refreshHeroSeriesPlaybackTargets()
})

onBeforeUnmount(() => {
  if (settledRefreshTimer)
    window.clearTimeout(settledRefreshTimer)
})

watch(heroItems, () => {
  void refreshHeroSeriesPlaybackTargets()
})

function goToSettings() {
  void router.push({ name: 'settings', query: { section: 'datasources' } })
}

function scheduleSettledContinueWatchingRefresh() {
  if (settledRefreshTimer)
    window.clearTimeout(settledRefreshTimer)

  settledRefreshTimer = window.setTimeout(() => {
    settledRefreshTimer = undefined
    void store.loadHomeSections()
  }, 1800)
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

    const path = source && item.sourceId !== 'placeholder'
      ? await source.getStreamURL(item.id)
      : item.path
    if (!path)
      throw new Error('播放地址不可用。')

    const queue = episodes.length > 0 ? createPlaybackQueue(episodes, item.id) : undefined
    const contextId = queue
      ? savePlaybackMediaContext({
          sourceId: item.sourceId,
          itemId: item.id,
          title: continueItemTitle(item),
          queue,
        })
      : undefined

    await router.push({
      name: 'player',
      query: {
        title: continueItemTitle(item),
        path,
        sourceId: item.sourceId,
        itemId: item.id,
        libraryId: item.libraryId,
        mediaType: item.type,
        posterUrl: item.posterUrl,
        backdropUrl: item.backdropUrl,
        contextId,
        resumePosition,
      },
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
  const entries = await Promise.all(seriesItems.map(async item => [itemKey(item), await resolveSeriesPlaybackTarget(item)] as const))
  if (refreshId !== seriesTargetRefreshId)
    return

  seriesPlaybackTargets.value = Object.fromEntries(entries.filter((entry): entry is readonly [string, SeriesPlaybackTarget] => entry[1] != null))
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

    const progressEntries = await Promise.all(episodes.map(episode => getPlaybackProgress({ sourceId: episode.sourceId, mediaIdentity: episode.id })))
    const localResume = newestLocalResume(progressEntries)
    const providerResumeIndex = localResume ? -1 : episodes.findIndex(episode => isResumePosition(episode.resumePosition, episode.duration))
    const index = localResume?.index ?? (providerResumeIndex >= 0 ? providerResumeIndex : 0)
    const episode = episodes[index]
    const providerResumePosition = isResumePosition(episode.resumePosition, episode.duration) ? episode.resumePosition : undefined

    return {
      item: episode,
      episodes,
      resumePosition: localResume?.entry.position ?? providerResumePosition,
      canResume: Boolean(localResume) || providerResumeIndex >= 0,
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
    <div class="flex min-h-screen flex-col gap-6 px-4 pb-6 sm:gap-8 sm:px-6 lg:px-8">
      <section class="relative -mx-4 overflow-hidden rounded-b-[2rem] sm:-mx-6 lg:-mx-8">
        <HeroCarousel
          v-if="heroItems.length"
          :items="heroItems"
          :action-label="heroActionLabel"
          @play="handlePlay"
          @detail="handleDetail"
        />

        <div
          v-if="!hasSources"
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
        class="rounded-2xl border border-red-400/20 bg-red-400/10 px-5 py-4 text-sm text-red-100"
      >
        {{ errorMessage }}
      </div>

      <div class="grid grid-cols-1 gap-6 pb-8 xl:grid-cols-2">
        <section class="glass-panel rounded-[1.75rem] p-6">
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
              class="group w-48 flex-shrink-0 cursor-pointer overflow-hidden rounded-2xl transition-transform hover:scale-[1.03]"
              @click="handlePlay(item)"
            >
              <div class="relative h-28 media-placeholder overflow-hidden">
                <img v-if="itemArtworkUrl(item)" :src="itemArtworkUrl(item)" :alt="continueItemTitle(item)" class="h-full w-full object-cover" loading="lazy" decoding="async">
                <div v-else class="flex h-full w-full items-center justify-center bg-white/6 p-4 text-center text-xs font-semibold text-white/48">
                  {{ continueItemTitle(item) }}
                </div>
                <div class="progress-track absolute bottom-0 left-0 right-0 h-1">
                  <div class="progress-value h-full rounded-full" :style="{ width: progressPercent(item) }" />
                </div>
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

        <section class="glass-panel rounded-[1.75rem] p-6">
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
              :disabled="!recentlyAddedSection"
              @click="recentlyAddedSection && router.push(`/source/${recentlyAddedSection.sourceId}`)"
            >
              浏览全部 >
            </button>
          </div>

          <div v-if="recentlyAddedItems.length" class="flex gap-4 overflow-x-auto cinema-scrollbar">
            <article
              v-for="item in recentlyAddedItems"
              :key="item.id"
              class="group w-28 flex-shrink-0 cursor-pointer overflow-hidden rounded-2xl transition-transform hover:scale-[1.04]"
              @click="handleDetail(item)"
            >
              <div class="relative aspect-[2/3] media-placeholder">
                <img v-if="item.posterUrl" :src="item.posterUrl" :alt="continueItemTitle(item)" class="h-full w-full object-cover" loading="lazy" decoding="async">
                <div v-else class="poster-placeholder flex h-full items-center justify-center">
                  <svg width="28" height="28" viewBox="0 0 24 24" fill="none">
                    <rect x="3" y="3" width="18" height="18" rx="3" stroke="currentColor" stroke-width="1.5" />
                  </svg>
                </div>
                <div class="absolute inset-0 flex items-center justify-center bg-black/55 opacity-0 transition-opacity group-hover:opacity-100">
                  <button
                    class="flex h-9 w-9 items-center justify-center rounded-full bg-white text-black transition-transform hover:scale-110"
                    :aria-label="`${heroActionLabel(item)} ${continueItemTitle(item)}`"
                    @click.stop="handlePlay(item)"
                  >
                    <svg width="12" height="12" viewBox="0 0 14 14" fill="currentColor">
                      <path d="M3 1l9 6-9 6V1z" />
                    </svg>
                  </button>
                </div>
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
              v-if="!hasSources"
              class="mt-4 rounded-2xl px-4 py-2 text-xs font-semibold transition-colors"
              style="color: var(--gp-text-full); background: var(--gp-hover)"
              @click="goToSettings"
            >
              添加数据源
            </button>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.home-view {
  background: var(--color-bg);
  color: var(--color-text);
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
</style>
