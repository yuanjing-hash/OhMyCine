<script setup lang="ts">
import type { AudioTrack, DataSource, MediaDetail, MediaItem, MediaLibrary, MediaSourceOption, SubtitleTrack } from '@/services/datasource/types'
import type { PlaybackQueueInput } from '@/services/playbackContext'
import type { PlaybackHistoryEntry } from '@/services/playbackHistory'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import MediaGrid from '@/components/media/MediaGrid.vue'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { createPlaybackQueue, getPlaybackMediaContext, savePlaybackMediaContext } from '@/services/playbackContext'
import { getPlaybackProgress, shouldResumePlayback } from '@/services/playbackHistory'
import { getContextFlatEpisodes, getContextSeriesSeasons, getPlayableSeasonChildren } from '@/services/scraper/rawSeriesGrouping'
import { useDataSourceStore } from '@/stores/datasource'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()

const sourceId = computed(() => route.params.sourceId as string)
const itemId = computed(() => route.params.itemId as string)
const detail = ref<MediaDetail | null>(null)
const seasons = ref<MediaItem[]>([])
const episodes = ref<MediaItem[]>([])
const selectedSeasonId = ref<string>('')
const isLoading = ref(false)
const isSeriesContentLoading = ref(false)
const isPlaying = ref(false)
const errorMessage = ref<string | null>(null)
const seriesErrorMessage = ref<string | null>(null)
const selectedMediaSourceId = ref<string>('')
const selectedAudioIndex = ref<number | null>(null)
const selectedSubtitleIndex = ref<number | null>(null)
const playbackProgressByItemId = ref<Record<string, PlaybackHistoryEntry>>({})

const heroStyle = computed(() => {
  const backdrop = detail.value?.backdropUrl
  return backdrop ? { backgroundImage: `url(${backdrop})` } : {}
})
const isSeriesDetail = computed(() => detail.value?.type === 'series')
const isPlayableDetail = computed(() => detail.value != null && !['series', 'season', 'folder'].includes(detail.value.type))
const mediaSources = computed(() => detail.value?.mediaSources ?? [])
const visibleMediaSources = computed(() => isSeriesDetail.value ? [] : mediaSources.value.filter(hasMeaningfulMediaSource))
const selectedMediaSource = computed(() => visibleMediaSources.value.find(source => source.id === selectedMediaSourceId.value) ?? visibleMediaSources.value[0])
const audioTracks = computed(() => isPlayableDetail.value ? (detail.value?.audioTracks ?? []) : [])
const subtitleTracks = computed(() => isPlayableDetail.value ? (detail.value?.subtitles ?? []) : [])
const runtimeLabel = computed(() => detail.value?.duration ? `${Math.round(detail.value.duration / 60)} 分钟` : '')
const sourceLabel = computed(() => selectedMediaSource.value ? describeMediaSource(selectedMediaSource.value) : '默认版本')
const mediaInfoRows = computed(() => {
  const current = detail.value
  if (!current)
    return []

  return [
    current.codec ? ['视频', current.codec] : undefined,
    current.audioCodec ? ['音频', current.audioCodec] : undefined,
    current.imdbId ? ['IMDB', current.imdbId] : undefined,
    current.tmdbId ? ['TMDB', String(current.tmdbId)] : undefined,
  ].filter((row): row is [string, string] => Boolean(row))
})
const EPISODE_WINDOW_SIZE = 8

const episodeWindowStart = ref(0)
const selectedEpisodeIndex = ref(0)
const isEpisodeIndicatorDragging = ref(false)
const episodeStripRef = ref<HTMLElement | null>(null)
const selectedSeason = computed(() => seasons.value.find(season => season.id === selectedSeasonId.value))
const maxEpisodeWindowStart = computed(() => Math.max(0, episodes.value.length - EPISODE_WINDOW_SIZE))
const visibleEpisodes = computed(() => episodes.value.slice(episodeWindowStart.value, episodeWindowStart.value + EPISODE_WINDOW_SIZE))
const selectedEpisode = computed(() => episodes.value[selectedEpisodeIndex.value])
const detailResumeEntry = computed(() => detail.value ? playbackProgressByItemId.value[detail.value.id] : undefined)
const detailCanResume = computed(() => Boolean(detail.value && hasResumeProgress(detail.value, detailResumeEntry.value)))
const seriesCanPlay = computed(() => Boolean(isSeriesDetail.value && selectedEpisode.value))
const primaryPlayTarget = computed(() => isSeriesDetail.value ? selectedEpisode.value : detail.value ?? undefined)
const primaryCanPlay = computed(() => isPlayableDetail.value || seriesCanPlay.value)
const primaryPlayLabel = computed(() => {
  if (isPlaying.value)
    return '正在准备…'
  if (isSeriesDetail.value)
    return selectedEpisode.value && hasResumeProgress(selectedEpisode.value) ? '继续播放' : '播放'
  return detailCanResume.value ? '继续播放' : '播放'
})
const canSelectEpisodePrev = computed(() => selectedEpisodeIndex.value > 0)
const canSelectEpisodeNext = computed(() => selectedEpisodeIndex.value < episodes.value.length - 1)
const episodeRangeLabel = computed(() => episodes.value.length > 0 ? `${selectedEpisodeIndex.value + 1} / ${episodes.value.length}` : '')
const selectedEpisodeDomId = computed(() => episodes.value.length > 0 ? `episode-card-${selectedEpisodeIndex.value}` : undefined)
const selectedEpisodeAriaValue = computed(() => episodes.value.length > 0 ? `第 ${selectedEpisodeIndex.value + 1} 集，共 ${episodes.value.length} 集` : '无分集')
const episodeIndicatorStyle = computed(() => {
  const total = episodes.value.length
  const percent = total > 0 ? ((selectedEpisodeIndex.value + 1) / total) * 100 : 0
  return { width: `${percent.toFixed(2)}%` }
})

onMounted(loadDetail)

watch([sourceId, itemId], loadDetail)

async function loadDetail() {
  isLoading.value = true
  errorMessage.value = null
  seriesErrorMessage.value = null
  detail.value = null
  seasons.value = []
  episodes.value = []
  selectedSeasonId.value = ''
  selectedMediaSourceId.value = ''
  selectedAudioIndex.value = null
  selectedSubtitleIndex.value = null
  playbackProgressByItemId.value = {}
  resetEpisodeWindow()

  try {
    const contextual = recoverContextualDetail()
    if (contextual) {
      detail.value = contextual.detail
      const selectableMediaSources = (contextual.detail.mediaSources ?? []).filter(hasMeaningfulMediaSource)
      selectedMediaSourceId.value = selectableMediaSources[0]?.id ?? ''
      selectedAudioIndex.value = contextual.detail.audioTracks?.find(track => track.isDefault)?.index ?? contextual.detail.audioTracks?.[0]?.index ?? null
      selectedSubtitleIndex.value = contextual.detail.subtitles?.find(track => track.isDefault)?.index ?? contextual.detail.subtitles?.[0]?.index ?? null

      if (contextual.detail.type === 'series') {
        const contextSeasons = getContextSeriesSeasons(contextual.detail)
        if (contextSeasons.length > 0) {
          seasons.value = contextSeasons
          selectedSeasonId.value = contextSeasons[0].id
          episodes.value = getPlayableSeasonChildren(contextSeasons[0])
        }
        else {
          episodes.value = getContextFlatEpisodes(contextual)
        }
        resetEpisodeWindow()
        await refreshPlaybackProgress(episodes.value)
        selectInitialEpisodeForSeason()
      }
      else {
        await refreshPlaybackProgress([contextual.detail])
      }
      return
    }

    const source = await resolveSource()
    const nextDetail = await source.getDetail(itemId.value)
    detail.value = nextDetail
    const selectableMediaSources = (nextDetail.mediaSources ?? []).filter(hasMeaningfulMediaSource)
    selectedMediaSourceId.value = selectableMediaSources[0]?.id ?? ''
    selectedAudioIndex.value = nextDetail.audioTracks?.find(track => track.isDefault)?.index ?? nextDetail.audioTracks?.[0]?.index ?? null
    selectedSubtitleIndex.value = nextDetail.subtitles?.find(track => track.isDefault)?.index ?? nextDetail.subtitles?.[0]?.index ?? null

    if (nextDetail.type === 'series')
      await loadSeriesSeasons(source, nextDetail.id)
    else
      await refreshPlaybackProgress([nextDetail])
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, '媒体详情加载失败。')
  }
  finally {
    isLoading.value = false
  }
}

async function resolveSource(): Promise<DataSource> {
  store.loadConfigs()
  await store.syncManager()
  const source = store.getSource(sourceId.value)
  if (!source)
    throw new Error('数据源不可用，请检查设置或重新登录。')
  return source
}

async function loadSeriesSeasons(source: DataSource, seriesId: string) {
  isSeriesContentLoading.value = true
  seriesErrorMessage.value = null
  try {
    const children = await source.list(seriesId)
    const seasonItems = children.filter(item => item.type === 'season' || item.type === 'folder')
    const episodeItems = children.filter(item => item.type === 'episode')
    seasons.value = seasonItems

    if (seasonItems.length > 0) {
      selectedSeasonId.value = seasonItems[0].id
      await loadSeasonEpisodes(source, seasonItems[0].id)
      return
    }

    episodes.value = episodeItems
    resetEpisodeWindow()
    await refreshPlaybackProgress(episodes.value)
    selectInitialEpisodeForSeason()
  }
  catch (error) {
    seriesErrorMessage.value = toSafeErrorMessage(error, '剧集季/集信息加载失败。')
  }
  finally {
    isSeriesContentLoading.value = false
  }
}

async function selectSeason(season: MediaItem) {
  if (season.id === selectedSeasonId.value && episodes.value.length > 0)
    return

  selectedSeasonId.value = season.id
  const contextEpisodes = getPlayableSeasonChildren(season)
  if (contextEpisodes.length > 0) {
    episodes.value = contextEpisodes
    resetEpisodeWindow()
    await refreshPlaybackProgress(episodes.value)
    selectInitialEpisodeForSeason()
    return
  }

  isSeriesContentLoading.value = true
  seriesErrorMessage.value = null
  try {
    episodes.value = []
    const source = await resolveSource()
    await loadSeasonEpisodes(source, season.id)
  }
  catch (error) {
    seriesErrorMessage.value = toSafeErrorMessage(error, '分集列表加载失败。')
  }
  finally {
    isSeriesContentLoading.value = false
  }
}

async function loadSeasonEpisodes(source: DataSource, seasonId: string) {
  const items = await source.list(seasonId)
  episodes.value = items.filter(item => item.type === 'episode' || item.type === 'file' || item.type === 'movie')
  resetEpisodeWindow()
  await refreshPlaybackProgress(episodes.value)
  selectInitialEpisodeForSeason()
}

function resetEpisodeWindow() {
  selectedEpisodeIndex.value = 0
  episodeWindowStart.value = 0
  scrollSelectedEpisodeIntoView('auto')
}

function selectInitialEpisodeForSeason() {
  const resumeIndex = episodes.value.findIndex(item => hasResumeProgress(item))
  selectEpisodeIndex(resumeIndex >= 0 ? resumeIndex : 0, 'auto')
}

function selectEpisodeByOffset(direction: -1 | 1) {
  selectEpisodeIndex(selectedEpisodeIndex.value + direction)
}

function selectEpisodeIndex(index: number, behavior: ScrollBehavior = 'smooth') {
  if (episodes.value.length === 0)
    return

  selectedEpisodeIndex.value = Math.min(episodes.value.length - 1, Math.max(0, index))
  ensureSelectedEpisodeVisible(behavior)
}

function ensureSelectedEpisodeVisible(behavior: ScrollBehavior) {
  if (episodes.value.length === 0)
    return

  const selected = selectedEpisodeIndex.value
  if (selected < episodeWindowStart.value)
    episodeWindowStart.value = selected
  else if (selected >= episodeWindowStart.value + EPISODE_WINDOW_SIZE)
    episodeWindowStart.value = Math.min(maxEpisodeWindowStart.value, selected - EPISODE_WINDOW_SIZE + 1)
  else if (episodeWindowStart.value > maxEpisodeWindowStart.value)
    episodeWindowStart.value = maxEpisodeWindowStart.value

  scrollSelectedEpisodeIntoView(behavior)
}

function scrollSelectedEpisodeIntoView(behavior: ScrollBehavior) {
  window.requestAnimationFrame(() => {
    episodeStripRef.value
      ?.querySelector(`[data-episode-index="${selectedEpisodeIndex.value}"]`)
      ?.scrollIntoView({ behavior, block: 'nearest', inline: 'center' })
  })
}

function handleEpisodeCardClick(item: MediaItem, index: number) {
  if (selectedEpisodeIndex.value === index) {
    openRelated(item)
    return
  }

  selectEpisodeIndex(index)
}

function handleEpisodeRailKeydown(event: KeyboardEvent) {
  if (event.defaultPrevented)
    return

  if (event.key === 'ArrowLeft') {
    event.preventDefault()
    selectEpisodeByOffset(-1)
    return
  }

  if (event.key === 'ArrowRight') {
    event.preventDefault()
    selectEpisodeByOffset(1)
    return
  }

  const target = event.target instanceof HTMLElement ? event.target : null
  if (event.key === 'Enter' && !target?.closest('button')) {
    event.preventDefault()
    if (selectedEpisode.value)
      openRelated(selectedEpisode.value)
  }
}

function handleEpisodeIndicatorPointerDown(event: PointerEvent) {
  if (episodes.value.length === 0)
    return

  isEpisodeIndicatorDragging.value = true
  const target = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  target?.setPointerCapture(event.pointerId)
  selectEpisodeFromIndicatorPointer(event)
}

function handleEpisodeIndicatorPointerMove(event: PointerEvent) {
  if (!isEpisodeIndicatorDragging.value)
    return

  selectEpisodeFromIndicatorPointer(event)
}

function handleEpisodeIndicatorPointerEnd(event: PointerEvent) {
  isEpisodeIndicatorDragging.value = false
  const target = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  if (target?.hasPointerCapture(event.pointerId))
    target.releasePointerCapture(event.pointerId)
}

function selectEpisodeFromIndicatorPointer(event: PointerEvent) {
  const target = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  if (!target || episodes.value.length === 0)
    return

  const rect = target.getBoundingClientRect()
  if (rect.width <= 0)
    return

  const ratio = Math.max(0, Math.min(1, (event.clientX - rect.left) / rect.width))
  selectEpisodeIndex(Math.round(ratio * (episodes.value.length - 1)))
}

function handleEpisodeIndicatorKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowLeft') {
    event.preventDefault()
    selectEpisodeByOffset(-1)
    return
  }

  if (event.key === 'ArrowRight') {
    event.preventDefault()
    selectEpisodeByOffset(1)
    return
  }

  if (event.key === 'Home') {
    event.preventDefault()
    selectEpisodeIndex(0)
    return
  }

  if (event.key === 'End') {
    event.preventDefault()
    selectEpisodeIndex(episodes.value.length - 1)
  }
}

async function refreshPlaybackProgress(items: readonly MediaItem[]) {
  const playableItems = items.filter(item => !['series', 'season', 'folder'].includes(item.type))
  if (playableItems.length === 0)
    return

  const entries = await Promise.all(playableItems.map(async item => [item.id, await getPlaybackProgress({ sourceId: sourceId.value, mediaIdentity: item.id })] as const))
  playbackProgressByItemId.value = {
    ...playbackProgressByItemId.value,
    ...Object.fromEntries(entries.filter((entry): entry is readonly [string, PlaybackHistoryEntry] => entry[1] != null)),
  }
}

function queryStringValue(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function recoverContextualDetail(): { detail: MediaDetail, relatedItems: MediaItem[] } | null {
  const contextId = queryStringValue(route.query.contextId)
  const playbackContext = contextId ? getPlaybackMediaContext(contextId) : null
  if (!playbackContext?.detail || playbackContext.sourceId !== sourceId.value || playbackContext.itemId !== itemId.value)
    return null

  const relatedItems = playbackContext.relatedItems?.map(cloneContextMediaItem)
    ?? playbackContext.queue?.items.map(item => ({
      id: item.id,
      sourceId: item.sourceId,
      libraryId: item.libraryId,
      name: item.name,
      type: item.type,
      posterUrl: item.posterUrl,
      backdropUrl: item.backdropUrl,
      overview: item.overview,
      duration: item.duration,
      path: item.path,
      seasonNumber: item.seasonNumber,
      episodeNumber: item.episodeNumber,
    }))
    ?? []

  return {
    detail: {
      ...playbackContext.detail,
      children: playbackContext.detail.children?.map(cloneContextMediaItem),
    },
    relatedItems,
  }
}

function cloneContextMediaItem(item: MediaItem): MediaItem {
  return {
    ...item,
    children: item.children?.map(cloneContextMediaItem),
  }
}

function recoverRoutePlaybackQueue(currentItemId: string): PlaybackQueueInput | undefined {
  const contextId = queryStringValue(route.query.contextId)
  const playbackContext = contextId ? getPlaybackMediaContext(contextId) : null
  const queue = playbackContext?.sourceId === sourceId.value ? playbackContext.queue : undefined
  if (!queue)
    return undefined

  const currentIndex = queue.items.findIndex(item => item.id === currentItemId)
  if (currentIndex < 0)
    return undefined

  return {
    items: queue.items.map(item => ({ ...item })),
    currentIndex,
  }
}

function saveQueueContextForDetail(item: MediaItem): string | undefined {
  const queue = createPlaybackQueue(episodes.value, item.id)
  if (!queue)
    return undefined

  return savePlaybackMediaContext({
    sourceId: sourceId.value,
    itemId: item.id,
    title: item.name,
    queue,
  })
}

function localResumeEntry(item: MediaItem): PlaybackHistoryEntry | undefined {
  return playbackProgressByItemId.value[item.id]
}

function hasResumeProgress(item: MediaItem, entry = localResumeEntry(item)): boolean {
  if (shouldResumePlayback(entry))
    return true

  return isResumePosition(item.resumePosition, item.duration)
}

function resumePositionForItem(item: MediaItem): number | undefined {
  const entry = localResumeEntry(item)
  if (shouldResumePlayback(entry))
    return entry.position

  return isResumePosition(item.resumePosition, item.duration) ? item.resumePosition : undefined
}

function isResumePosition(position: number | undefined, duration: number | undefined): position is number {
  if (typeof position !== 'number' || !Number.isFinite(position) || position < 30)
    return false
  if (typeof duration !== 'number' || !Number.isFinite(duration) || duration <= 0)
    return true
  return position < duration * 0.92 && duration - position > 90
}

function episodeActionLabel(item: MediaItem): string {
  return hasResumeProgress(item) ? '继续播放' : '播放本集'
}

function episodeProgressStyle(item: MediaItem): Record<string, string> {
  const entry = localResumeEntry(item)
  const progress = shouldResumePlayback(entry)
    ? entry.progress
    : item.progress
  const percent = typeof progress === 'number' && Number.isFinite(progress)
    ? Math.max(0, Math.min(100, progress * 100))
    : 0

  return { width: `${percent.toFixed(1)}%` }
}

async function playPrimaryTarget() {
  await playItem(isSeriesDetail.value ? selectedEpisode.value : undefined)
}

async function playItem(item?: MediaItem) {
  const target = item ?? primaryPlayTarget.value
  if (!target || ['series', 'season', 'folder'].includes(target.type))
    return

  isPlaying.value = true
  errorMessage.value = null
  try {
    const source = await resolveSource()
    const path = await source.getStreamURL(target.id)
    const isCurrentDetail = target.id === detail.value?.id
    const queue = (item ? createPlaybackQueue(episodes.value, item.id) : undefined) ?? recoverRoutePlaybackQueue(target.id)
    const playbackContextId = savePlaybackMediaContext({
      sourceId: sourceId.value,
      itemId: target.id,
      title: target.name,
      mediaSourceId: item ? undefined : selectedMediaSource.value?.id,
      subtitles: isCurrentDetail ? detail.value?.subtitles : undefined,
      audioTracks: isCurrentDetail ? detail.value?.audioTracks : undefined,
      queue,
    })
    await router.push({
      name: 'player',
      query: {
        title: target.name,
        path,
        sourceId: sourceId.value,
        itemId: target.id,
        libraryId: target.libraryId,
        mediaType: target.type,
        posterUrl: target.posterUrl,
        backdropUrl: target.backdropUrl,
        contextId: playbackContextId,
        mediaSourceId: item ? undefined : selectedMediaSource.value?.id,
        resumePosition: resumePositionForItem(target),
        audioIndex: item ? undefined : (selectedAudioIndex.value ?? undefined),
        subtitleIndex: item ? undefined : (selectedSubtitleIndex.value ?? undefined),
      },
    })
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, '无法获取播放地址。')
  }
  finally {
    isPlaying.value = false
  }
}

function openRelated(item: MediaItem | MediaLibrary) {
  if (!('path' in item))
    return

  const contextId = saveQueueContextForDetail(item)
  void router.push({
    name: 'media-detail',
    params: { sourceId: sourceId.value, itemId: item.id },
    query: contextId ? { contextId } : undefined,
  })
}

function describeMediaSource(source: MediaSourceOption): string {
  return [source.container?.toUpperCase(), source.size ? formatBytes(source.size) : undefined, source.isStrm ? 'STRM' : undefined, source.isRemote ? '远程' : undefined]
    .filter(Boolean)
    .join(' · ') || source.name
}

function hasMeaningfulMediaSource(source: MediaSourceOption): boolean {
  return Boolean(source.container || source.size || source.bitrate || source.isRemote || source.isStrm || (source.name && !/^default$|^source-\d+$/i.test(source.name)))
}

function formatBytes(value: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  return `${size.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

function trackLabel(track: AudioTrack | SubtitleTrack): string {
  const language = track.language && track.language !== 'Unknown' ? track.language : undefined
  const codec = 'codec' in track && track.codec !== 'unknown' ? ` · ${track.codec}` : ''
  const channels = 'channels' in track && track.channels ? ` · ${track.channels}ch` : ''
  const title = 'title' in track ? track.title : undefined
  return `${title ?? language ?? '未命名轨道'}${codec}${channels}${track.isDefault ? ' · 默认' : ''}`
}

function episodeTitle(item: MediaItem): string {
  return item.episodeNumber == null ? item.name : `第 ${item.episodeNumber} 集 · ${item.name}`
}

function itemRuntime(item: MediaItem): string {
  return item.duration ? `${Math.round(item.duration / 60)} 分钟` : ''
}
</script>

<template>
  <div class="detail-view min-h-screen bg-[var(--color-bg)] text-white">
    <div v-if="isLoading && !detail" class="pointer-events-none flex min-h-screen items-center justify-center text-white/45" aria-live="polite">
      正在加载媒体详情…
    </div>

    <div v-else-if="errorMessage && !detail" class="flex min-h-screen flex-col items-center justify-center gap-4 px-6 text-center">
      <p class="text-lg font-semibold text-white">
        详情不可用
      </p>
      <p class="max-w-xl text-sm leading-6 text-white/50">
        {{ errorMessage }}
      </p>
      <button class="rounded-2xl bg-white/10 px-5 py-3 text-sm text-white transition-colors hover:bg-white/16" @click="router.back()">
        返回
      </button>
    </div>

    <template v-else-if="detail">
      <section class="relative min-h-[68vh] overflow-hidden bg-cover bg-center" :style="heroStyle">
        <div class="pointer-events-none absolute inset-0 bg-gradient-to-r from-black/94 via-black/62 to-black/20" />
        <div class="pointer-events-none absolute inset-0 bg-gradient-to-t from-[var(--color-bg)] via-transparent to-black/40" />
        <div class="relative flex min-h-[68vh] items-end gap-8 px-6 pb-12 pl-24 pt-24 lg:px-12 lg:pl-28">
          <div class="hidden w-56 flex-shrink-0 overflow-hidden rounded-[1.8rem] border border-white/12 bg-white/6 shadow-2xl md:block">
            <img v-if="detail.posterUrl" :src="detail.posterUrl" :alt="detail.name" class="aspect-[2/3] w-full object-cover" loading="eager" decoding="async">
            <div v-else class="flex aspect-[2/3] items-center justify-center p-6 text-center text-sm text-white/45">
              {{ detail.name }}
            </div>
          </div>

          <div class="max-w-4xl">
            <p class="text-xs uppercase tracking-[0.28em] text-white/42">
              {{ isSeriesDetail ? 'OhMyCine Series' : 'OhMyCine Detail' }}
            </p>
            <h1 class="mt-3 text-4xl font-bold leading-tight drop-shadow-2xl lg:text-6xl">
              {{ detail.name }}
            </h1>
            <div class="mt-4 flex flex-wrap items-center gap-3 text-sm text-white/68">
              <span v-if="detail.rating" class="rounded-full bg-yellow-400/16 px-3 py-1 text-yellow-100">★ {{ detail.rating.toFixed(1) }}</span>
              <span v-if="detail.year">{{ detail.year }}</span>
              <span v-if="runtimeLabel">{{ runtimeLabel }}</span>
              <span v-if="detail.resolution && !isSeriesDetail">{{ detail.resolution }}</span>
              <span v-if="detail.genres?.length">{{ detail.genres.slice(0, 4).join(' / ') }}</span>
            </div>
            <p v-if="detail.overview" class="mt-5 max-w-3xl text-base leading-8 text-white/68 line-clamp-5">
              {{ detail.overview }}
            </p>
            <div class="mt-7 flex flex-wrap items-center gap-3">
              <button v-if="primaryCanPlay" class="flex items-center gap-2 rounded-full bg-white px-7 py-3 text-sm font-bold text-black shadow-xl transition-transform hover:scale-105 disabled:cursor-not-allowed disabled:opacity-60" :disabled="isPlaying" @click="playPrimaryTarget">
                <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M4 2l10 6-10 6V2z" /></svg>
                {{ primaryPlayLabel }}
              </button>
              <span v-if="isSeriesDetail && selectedEpisode" class="rounded-full border border-white/12 bg-white/8 px-4 py-3 text-xs text-white/58">{{ episodeTitle(selectedEpisode) }}</span>
              <span v-else-if="visibleMediaSources.length" class="rounded-full border border-white/12 bg-white/8 px-4 py-3 text-xs text-white/58">{{ sourceLabel }}</span>
            </div>
          </div>
        </div>
      </section>

      <main class="space-y-10 px-6 pb-14 pl-24 lg:px-12 lg:pl-28">
        <div v-if="errorMessage" class="rounded-2xl border border-red-400/20 bg-red-400/10 px-5 py-4 text-sm text-red-100">
          {{ errorMessage }}
        </div>

        <section v-if="isSeriesDetail" class="episode-rail-shell relative overflow-hidden rounded-[2rem] border border-white/10 bg-white/[0.045] p-6 shadow-2xl">
          <div class="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(255,255,255,0.13),transparent_36%),linear-gradient(135deg,rgba(255,255,255,0.08),transparent_42%)]" />
          <div class="relative flex flex-wrap items-end justify-between gap-4">
            <div>
              <p class="text-xs uppercase tracking-[0.24em] text-white/36">
                Seasons & Episodes
              </p>
              <h2 class="mt-2 text-2xl font-bold">
                分集
              </h2>
            </div>
            <div class="flex items-center gap-3 text-sm text-white/46">
              <span v-if="selectedSeason">{{ selectedSeason.name }} · {{ episodes.length }} 集</span>
              <span v-if="episodeRangeLabel" class="rounded-full border border-white/10 bg-white/6 px-3 py-1 text-xs text-white/50" aria-live="polite">{{ episodeRangeLabel }}</span>
            </div>
          </div>

          <div v-if="seriesErrorMessage" class="relative mt-5 rounded-2xl border border-red-400/20 bg-red-400/10 px-4 py-3 text-sm text-red-100">
            {{ seriesErrorMessage }}
          </div>

          <div v-if="seasons.length" class="relative mt-6 flex gap-3 overflow-x-auto cinema-scrollbar pb-2">
            <button
              v-for="season in seasons"
              :key="season.id"
              type="button"
              class="flex-shrink-0 rounded-full border px-5 py-3 text-sm transition-colors"
              :class="selectedSeasonId === season.id ? 'border-white/36 bg-white/16 text-white shadow-lg shadow-black/20' : 'border-white/10 bg-white/6 text-white/58 hover:bg-white/10'"
              :aria-pressed="selectedSeasonId === season.id"
              @click="selectSeason(season)"
            >
              {{ season.name }}
            </button>
          </div>

          <div v-if="isSeriesContentLoading" class="relative mt-7 flex gap-4 overflow-hidden" aria-hidden="true">
            <div v-for="i in 4" :key="i" class="h-72 min-w-[20rem] animate-pulse rounded-[1.7rem] bg-white/6" />
          </div>

          <div v-else-if="episodes.length" class="episode-rail group relative mt-7">
            <div class="pointer-events-none absolute inset-y-0 left-0 z-10 w-16 bg-gradient-to-r from-[var(--color-bg)] to-transparent" />
            <div class="pointer-events-none absolute inset-y-0 right-0 z-10 w-20 bg-gradient-to-l from-[var(--color-bg)] to-transparent" />
            <button
              class="episode-nav-button left-2"
              type="button"
              aria-label="选择上一集"
              title="选择上一集"
              :disabled="!canSelectEpisodePrev"
              @click="selectEpisodeByOffset(-1)"
            >
              <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="M12.5 4.5 7 10l5.5 5.5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
            </button>
            <button
              class="episode-nav-button right-2"
              type="button"
              aria-label="选择下一集"
              title="选择下一集"
              :disabled="!canSelectEpisodeNext"
              @click="selectEpisodeByOffset(1)"
            >
              <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="m7.5 4.5 5.5 5.5-5.5 5.5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
            </button>

            <div
              ref="episodeStripRef"
              class="episode-card-strip flex snap-x snap-mandatory gap-4 overflow-x-auto pr-10 pb-3"
              role="listbox"
              tabindex="0"
              aria-label="分集选择，左右方向键切换，回车进入当前分集详情"
              :aria-activedescendant="selectedEpisodeDomId"
              @keydown="handleEpisodeRailKeydown"
            >
              <article
                v-for="(episode, visibleIndex) in visibleEpisodes"
                :id="`episode-card-${episodeWindowStart + visibleIndex}`"
                :key="episode.id"
                class="episode-card group/card flex min-w-[20rem] max-w-[20rem] cursor-pointer flex-col overflow-hidden rounded-[1.7rem] border shadow-xl shadow-black/24 outline-none transition-all duration-300 hover:-translate-y-1 hover:border-white/24 hover:bg-white/[0.075] focus-visible:-translate-y-1 focus-visible:border-white/42 focus-visible:ring-2 focus-visible:ring-white/22 md:min-w-[23rem] md:max-w-[23rem]"
                :class="selectedEpisodeIndex === episodeWindowStart + visibleIndex ? '-translate-y-1 border-white/46 bg-white/[0.105] ring-2 ring-white/18' : 'border-white/10 bg-black/22'"
                :data-episode-index="episodeWindowStart + visibleIndex"
                role="option"
                tabindex="0"
                :aria-selected="selectedEpisodeIndex === episodeWindowStart + visibleIndex"
                :aria-label="`${selectedEpisodeIndex === episodeWindowStart + visibleIndex ? '当前选中' : '选择'} ${episodeTitle(episode)}`"
                @click="handleEpisodeCardClick(episode, episodeWindowStart + visibleIndex)"
                @keydown.enter.self.prevent="handleEpisodeCardClick(episode, episodeWindowStart + visibleIndex)"
              >
                <div class="relative block overflow-hidden text-left">
                  <img v-if="episode.backdropUrl || episode.posterUrl" :src="episode.backdropUrl ?? episode.posterUrl" :alt="episode.name" class="aspect-video w-full object-cover transition-transform duration-700 group-hover/card:scale-105" loading="lazy" decoding="async">
                  <div v-else class="flex aspect-video w-full items-center justify-center bg-white/6 p-5 text-center text-sm text-white/42">
                    {{ episodeTitle(episode) }}
                  </div>
                  <div class="absolute inset-0 bg-gradient-to-t from-black/88 via-black/8 to-transparent" />
                  <span class="absolute left-4 top-4 rounded-full border border-white/12 bg-black/42 px-3 py-1 text-xs font-semibold text-white/76 backdrop-blur-xl">
                    {{ episode.episodeNumber == null ? 'Episode' : `第 ${episode.episodeNumber} 集` }}
                  </span>
                  <span v-if="selectedEpisodeIndex === episodeWindowStart + visibleIndex" class="absolute right-4 top-4 rounded-full border border-white/18 bg-white/18 px-3 py-1 text-xs font-semibold text-white shadow-lg backdrop-blur-xl">
                    已选中
                  </span>
                  <div v-if="hasResumeProgress(episode)" class="absolute inset-x-4 bottom-4 h-1 overflow-hidden rounded-full bg-white/18">
                    <div class="h-full rounded-full bg-white" :style="episodeProgressStyle(episode)" />
                  </div>
                </div>

                <div class="flex flex-1 flex-col justify-between gap-5 p-5">
                  <div>
                    <div class="flex items-center justify-between gap-3 text-xs text-white/42">
                      <span>{{ itemRuntime(episode) || '单集' }}</span>
                      <span v-if="hasResumeProgress(episode)" class="rounded-full border border-white/12 bg-white/8 px-2 py-1 text-white/62">可继续播放</span>
                    </div>
                    <h3 class="mt-3 line-clamp-2 text-lg font-semibold leading-snug text-white">
                      {{ episodeTitle(episode) }}
                    </h3>
                    <p v-if="episode.overview" class="mt-3 line-clamp-3 text-sm leading-6 text-white/52">
                      {{ episode.overview }}
                    </p>
                    <p v-else class="mt-3 text-sm leading-6 text-white/36">
                      暂无本集简介。
                    </p>
                  </div>
                  <div class="flex items-center gap-2">
                    <button type="button" class="rounded-full bg-white px-4 py-2 text-xs font-bold text-black transition-transform hover:scale-105" :aria-label="`${episodeActionLabel(episode)}：${episodeTitle(episode)}`" @click.stop="playItem(episode)">
                      {{ episodeActionLabel(episode) }}
                    </button>
                    <button type="button" class="rounded-full border border-white/10 bg-white/6 px-4 py-2 text-xs text-white/62 transition-colors hover:bg-white/10" :aria-label="`查看 ${episodeTitle(episode)} 详情`" @click.stop="openRelated(episode)">
                      详情
                    </button>
                  </div>
                </div>
              </article>
            </div>

            <div class="mt-3 flex items-center gap-3 text-xs text-white/42" aria-live="polite">
              <div
                class="episode-position-slider h-3 flex-1 cursor-pointer touch-none rounded-full px-0 py-1 outline-none focus-visible:ring-2 focus-visible:ring-white/28"
                role="slider"
                tabindex="0"
                aria-label="分集快速定位"
                aria-valuemin="1"
                :aria-valuemax="episodes.length"
                :aria-valuenow="selectedEpisodeIndex + 1"
                :aria-valuetext="selectedEpisodeAriaValue"
                @pointerdown.prevent="handleEpisodeIndicatorPointerDown"
                @pointermove.prevent="handleEpisodeIndicatorPointerMove"
                @pointerup="handleEpisodeIndicatorPointerEnd"
                @pointercancel="handleEpisodeIndicatorPointerEnd"
                @keydown="handleEpisodeIndicatorKeydown"
              >
                <div class="h-1 overflow-hidden rounded-full bg-white/10">
                  <div class="h-full rounded-full bg-white/72 transition-all duration-300" :style="episodeIndicatorStyle" />
                </div>
              </div>
              <span class="min-w-16 text-right">{{ episodeRangeLabel }}</span>
            </div>
          </div>

          <div v-else class="relative mt-6 rounded-[1.5rem] border border-white/10 bg-white/5 p-8 text-center text-sm text-white/45">
            Emby 暂未返回可选择的分集。
          </div>
        </section>

        <section v-if="!isSeriesDetail && (visibleMediaSources.length || audioTracks.length || subtitleTracks.length)" class="grid gap-5 lg:grid-cols-3">
          <div v-if="visibleMediaSources.length" class="glass-panel rounded-[1.6rem] p-5">
            <h2 class="text-base font-semibold">
              版本
            </h2>
            <div class="mt-4 space-y-2">
              <button
                v-for="source in visibleMediaSources"
                :key="source.id"
                class="w-full rounded-2xl border px-4 py-3 text-left text-sm transition-colors"
                :class="selectedMediaSourceId === source.id ? 'border-white/34 bg-white/14 text-white' : 'border-white/8 bg-white/5 text-white/62 hover:bg-white/10'"
                @click="selectedMediaSourceId = source.id"
              >
                <span class="block font-medium">版本 {{ mediaSources.findIndex(candidate => candidate.id === source.id) + 1 }}</span>
                <span class="mt-1 block text-xs text-white/42">{{ describeMediaSource(source) }}</span>
              </button>
            </div>
          </div>

          <div v-if="audioTracks.length" class="glass-panel rounded-[1.6rem] p-5">
            <h2 class="text-base font-semibold">
              音轨
            </h2>
            <select v-model="selectedAudioIndex" class="mt-4 w-full rounded-2xl border border-white/10 bg-black/20 px-4 py-3 text-sm text-white outline-none">
              <option v-for="track in audioTracks" :key="track.index" :value="track.index">
                {{ trackLabel(track) }}
              </option>
            </select>
          </div>

          <div v-if="subtitleTracks.length" class="glass-panel rounded-[1.6rem] p-5">
            <h2 class="text-base font-semibold">
              字幕
            </h2>
            <select v-model="selectedSubtitleIndex" class="mt-4 w-full rounded-2xl border border-white/10 bg-black/20 px-4 py-3 text-sm text-white outline-none">
              <option :value="null">
                不预选字幕
              </option>
              <option v-for="track in subtitleTracks" :key="track.index" :value="track.index">
                {{ trackLabel(track) }}
              </option>
            </select>
          </div>
        </section>

        <section v-if="detail.stills?.length">
          <h2 class="mb-4 text-xl font-bold">
            剧照 / 截图
          </h2>
          <div class="flex gap-4 overflow-x-auto cinema-scrollbar">
            <img v-for="still in detail.stills" :key="still" :src="still" :alt="detail.name" class="h-40 w-72 flex-shrink-0 rounded-3xl object-cover" loading="lazy" decoding="async">
          </div>
        </section>

        <section class="grid gap-6 lg:grid-cols-[1.5fr_1fr]">
          <div class="glass-panel rounded-[1.6rem] p-6">
            <h2 class="text-xl font-bold">
              演职员
            </h2>
            <p class="mt-4 text-sm leading-7 text-white/58">
              <span v-if="detail.directors?.length">导演：{{ detail.directors.join('、') }}</span>
              <br v-if="detail.directors?.length && detail.cast?.length">
              <span v-if="detail.cast?.length">演员：{{ detail.cast.slice(0, 12).join('、') }}</span>
              <span v-if="!detail.directors?.length && !detail.cast?.length">暂无演职员信息。</span>
            </p>
          </div>
          <div v-if="mediaInfoRows.length" class="glass-panel rounded-[1.6rem] p-6">
            <h2 class="text-xl font-bold">
              媒体信息
            </h2>
            <dl class="mt-4 space-y-2 text-sm text-white/58">
              <div v-for="row in mediaInfoRows" :key="row[0]" class="flex justify-between gap-4">
                <dt>{{ row[0] }}</dt><dd>{{ row[1] }}</dd>
              </div>
            </dl>
          </div>
        </section>

        <section v-if="detail.collections?.length">
          <h2 class="mb-4 text-xl font-bold">
            合集
          </h2>
          <MediaGrid :items="detail.collections" @select="openRelated" @play="playItem" />
        </section>

        <section v-if="detail.similarItems?.length">
          <h2 class="mb-4 text-xl font-bold">
            相似内容
          </h2>
          <MediaGrid :items="detail.similarItems" @select="openRelated" @play="playItem" />
        </section>
      </main>
    </template>
  </div>
</template>

<style scoped>
.episode-rail-shell {
  backdrop-filter: blur(28px);
  -webkit-backdrop-filter: blur(28px);
}

.episode-rail {
  isolation: isolate;
}

.episode-card-strip {
  -ms-overflow-style: none;
  scrollbar-width: none;
  scroll-behavior: smooth;
}

.episode-card-strip::-webkit-scrollbar {
  display: none;
}

.episode-card {
  scroll-snap-align: start;
}

.episode-nav-button {
  position: absolute;
  top: 50%;
  z-index: 20;
  display: inline-flex;
  width: 2.75rem;
  height: 2.75rem;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--glass-border-hover);
  border-radius: 999px;
  color: var(--color-text);
  background: var(--glass-bg-hover);
  box-shadow: var(--glass-shadow-elevated);
  backdrop-filter: blur(var(--glass-blur));
  -webkit-backdrop-filter: blur(var(--glass-blur));
  transform: translateY(-50%);
  transition: opacity var(--duration-fast) var(--ease-out), transform var(--duration-fast) var(--ease-out), background var(--duration-fast) var(--ease-out);
}

.episode-nav-button:hover:not(:disabled),
.episode-nav-button:focus-visible:not(:disabled) {
  background: var(--glass-bg-active);
  transform: translateY(-50%) scale(1.05);
}

.episode-nav-button:disabled {
  cursor: default;
  opacity: 0.26;
}
</style>
