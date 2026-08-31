<script setup lang="ts">
import type { AudioTrack, DataSource, MediaDetail, MediaItem, MediaLibrary, MediaPerson, SubtitleTrack } from '@/services/datasource/types'
import type { PlaybackQueueInput } from '@/services/playbackContext'
import type { PlaybackHistoryEntry } from '@/services/playbackHistory'
import type { SeriesEpisodeSearchEntry } from '@/services/seriesEpisodeSearch'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import MediaGrid from '@/components/media/MediaGrid.vue'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { describeMediaSource, hasMeaningfulMediaSource } from '@/services/datasource/mediaSourceDisplay'
import { toOfflineMediaDetail } from '@/services/datasource/offline'
import { getOfflineDetail } from '@/services/downloads'
import { beginMediaActionLongPress, cancelMediaActionLongPress, createMediaActionTarget, endMediaActionLongPress, getMediaActionController, moveMediaActionLongPress, openMediaActionContextMenu, suppressMediaActionClick } from '@/services/mediaActions'
import { createPlaybackQueue, getPlaybackMediaContext, savePlaybackMediaContext } from '@/services/playbackContext'
import { areAllKnownPlayableChildrenCompleted, getPlaybackProgress, playbackCompletionKey, PLAYED_STATE_CHANGED_EVENT, shouldResumePlayback } from '@/services/playbackHistory'
import { createPlaybackRouteQuery } from '@/services/playbackRoute'
import { loadPlayerInteractionSettings } from '@/services/playerInteractionSettings'
import { isNativeAndroidRuntime } from '@/services/runtimePlatform'
import { getContextFlatEpisodes, getContextSeriesSeasons, getPlayableSeasonChildren } from '@/services/scraper/rawSeriesGrouping'
import { episodeSearchTitle, searchSeriesEpisodes } from '@/services/seriesEpisodeSearch'
import { useDataSourceStore } from '@/stores/datasource'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const isNativeAndroid = isNativeAndroidRuntime()
const mobileEpisodeLayout = loadPlayerInteractionSettings().mobileEpisodeLayout

const sourceId = computed(() => route.params.sourceId as string)
const itemId = computed(() => route.params.itemId as string)
const detail = ref<MediaDetail | null>(null)
const seasons = ref<MediaItem[]>([])
const episodes = ref<MediaItem[]>([])
const seasonEpisodeCache = new Map<string, MediaItem[]>()
const selectedSeasonId = ref<string>('')
const isLoading = ref(false)
const isSeriesContentLoading = ref(false)
const isPlaying = ref(false)
const isEnqueueingOnlineDownload = ref(false)
const downloadFeedback = ref<string | null>(null)
const errorMessage = ref<string | null>(null)
const seriesErrorMessage = ref<string | null>(null)
const isEpisodeSearchOpen = ref(false)
const isEpisodeSearchLoading = ref(false)
const episodeSearchKeyword = ref('')
const episodeSearchError = ref<string | null>(null)
const episodeSearchEntries = ref<SeriesEpisodeSearchEntry[]>([])
const episodeSearchInputRef = ref<HTMLInputElement | null>(null)
let episodeSearchGeneration = 0
const selectedMediaSourceId = ref<string>('')
const selectedAudioIndex = ref<number | null>(null)
const selectedSubtitleIndex = ref<number | null>(null)
const playbackProgressByItemId = ref<Record<string, PlaybackHistoryEntry>>({})
const failedTitleLogoUrls = ref<Set<string>>(new Set())
const detailPlayed = computed(() => {
  const current = detail.value
  if (!current)
    return false
  if (current.type === 'series' || current.type === 'season') {
    const completed = new Set(Object.values(playbackProgressByItemId.value).filter(entry => entry.completed).map(entry => playbackCompletionKey(entry.sourceId, entry.mediaIdentity)))
    return areAllKnownPlayableChildrenCompleted(episodes.value, completed)
  }
  const sourceType = store.configs.find(config => config.id === current.sourceId)?.type
  if (sourceType === 'emby' || sourceType === 'jellyfin')
    return current.played === true
  return current.played === true || playbackProgressByItemId.value[current.id]?.completed === true
})
const canToggleDetailPlayed = computed(() => {
  const current = detail.value
  if (!current)
    return false
  const sourceType = store.configs.find(config => config.id === current.sourceId)?.type
  return !['series', 'season', 'folder'].includes(current.type) || sourceType === 'emby' || sourceType === 'jellyfin'
})

async function toggleDetailPlayedState() {
  const target = detailActionTarget()
  if (!target)
    return
  const outcome = await getMediaActionController().execute(target, detailPlayed.value ? 'markUnplayed' : 'markPlayed')
  if (outcome.status === 'completed')
    await loadDetail()
}

async function enqueueOnlineDownload() {
  const current = detail.value
  const mediaSource = (current?.mediaSources ?? []).find(source => source.id === selectedMediaSourceId.value)
    ?? current?.mediaSources?.[0]
  if (!current || !mediaSource?.itemId || isEnqueueingOnlineDownload.value)
    return
  const source = store.getSource(current.sourceId)
  if (!source?.enqueueOnlineDownload)
    return
  isEnqueueingOnlineDownload.value = true
  errorMessage.value = null
  downloadFeedback.value = null
  try {
    await source.enqueueOnlineDownload({
      itemId: mediaSource.itemId,
      mediaSourceId: mediaSource.providerMediaSourceId ?? mediaSource.id,
    })
    downloadFeedback.value = '已加入 Server 下载队列，将按媒体库顺序自动选择入库目标。'
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, '无法创建 Server 下载任务。')
  }
  finally {
    isEnqueueingOnlineDownload.value = false
  }
}

function detailActionTarget() {
  const current = detail.value
  if (!current)
    return null
  const source = store.configs.find(config => config.id === current.sourceId)
  const target = createMediaActionTarget({ ...current, played: detailPlayed.value }, source?.type, source?.displayName ?? source?.name)
  if (target.kind !== 'media')
    return target
  const selected = (current.mediaSources ?? []).find(candidate => candidate.id === selectedMediaSourceId.value)
    ?? current.mediaSources?.[0]
  return {
    ...target,
    mediaSourceId: selected?.providerMediaSourceId ?? selected?.id,
  }
}

function beginDetailActionLongPress(event: PointerEvent) {
  const target = detailActionTarget()
  if (target)
    beginMediaActionLongPress(target, event)
}

function openDetailActionMenu(event: MouseEvent) {
  const target = detailActionTarget()
  if (target)
    openMediaActionContextMenu(target, event)
  else
    event.preventDefault()
}

function handleDetailHeroClick(event: MouseEvent) {
  suppressMediaActionClick(event)
}

const heroStyle = computed(() => {
  const backdrop = detail.value?.backdropUrl
  return backdrop ? { backgroundImage: `url(${backdrop})` } : {}
})
const visibleTitleLogoUrl = computed(() => {
  const url = detail.value?.titleLogoUrl
  return url && !failedTitleLogoUrls.value.has(url) ? url : ''
})
const isSeriesDetail = computed(() => detail.value?.type === 'series')
const isPlayableDetail = computed(() => detail.value != null && !['series', 'season', 'folder'].includes(detail.value.type))
const mediaSources = computed(() => detail.value?.mediaSources ?? [])
const visibleMediaSources = computed(() => isSeriesDetail.value ? [] : mediaSources.value.filter(hasMeaningfulMediaSource))
const selectedMediaSource = computed(() => visibleMediaSources.value.find(source => source.id === selectedMediaSourceId.value) ?? visibleMediaSources.value[0])
const canEnqueueOnlineDownload = computed(() => {
  const current = detail.value
  const source = current ? store.getSource(current.sourceId) : null
  return Boolean(current && source?.enqueueOnlineDownload && selectedMediaSource.value?.itemId?.startsWith('online-version|'))
})
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
const isMobileEpisodeViewport = ref(isNativeAndroid)
let mobileEpisodeMediaQuery: MediaQueryList | null = null
const selectedSeason = computed(() => seasons.value.find(season => season.id === selectedSeasonId.value))
const maxEpisodeWindowStart = computed(() => Math.max(0, episodes.value.length - EPISODE_WINDOW_SIZE))
const visibleEpisodes = computed(() => episodes.value.slice(episodeWindowStart.value, episodeWindowStart.value + EPISODE_WINDOW_SIZE))
const renderedEpisodes = computed(() => isMobileEpisodeViewport.value ? episodes.value : visibleEpisodes.value)
const selectedEpisode = computed(() => episodes.value[selectedEpisodeIndex.value])
const episodeSearchResults = computed(() => searchSeriesEpisodes(episodeSearchEntries.value, episodeSearchKeyword.value))
const detailResumeEntry = computed(() => detail.value ? playbackProgressByItemId.value[detail.value.id] : undefined)
const detailCanResume = computed(() => Boolean(detail.value && hasResumeProgress(detail.value, detailResumeEntry.value)))
const seriesCanPlay = computed(() => Boolean(isSeriesDetail.value && selectedEpisode.value))
const primaryPlayTarget = computed(() => isSeriesDetail.value ? selectedEpisode.value : detail.value ?? undefined)
const primaryCanPlay = computed(() => seriesCanPlay.value || (isPlayableDetail.value && (detail.value?.originType !== 'server' || selectedMediaSource.value != null)))
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
const emptyEpisodesMessage = computed(() => {
  const current = detail.value
  const sourceType = store.configs.find(config => config.id === current?.sourceId)?.type ?? current?.originType
  if (sourceType === 'emby')
    return 'Emby 暂未返回可选择的分集。'
  if (sourceType === 'jellyfin')
    return 'Jellyfin 暂未返回可选择的分集。'
  if (sourceType === 'server')
    return 'Server 媒体库中暂时没有可播放的分集。'
  return '暂时没有可播放的分集。'
})
const visiblePeople = computed<MediaPerson[]>(() => {
  const current = detail.value
  if (!current)
    return []
  if (current.people?.length)
    return current.people.slice(0, 30)
  return [
    ...(current.directors ?? []).map(name => ({ name, role: 'Director' })),
    ...(current.cast ?? []).map(name => ({ name, role: 'Actor' })),
  ].slice(0, 30)
})

function personSubtitle(role?: string, character?: string): string {
  if (character)
    return character
  return ({ actor: '演员', director: '导演', writer: '编剧', creator: '主创' } as Record<string, string>)[role?.toLocaleLowerCase() ?? ''] ?? role ?? '演职员'
}
const selectedEpisodeDomId = computed(() => episodes.value.length > 0 ? `episode-card-${selectedEpisodeIndex.value}` : undefined)
const selectedEpisodeAriaValue = computed(() => episodes.value.length > 0 ? `第 ${selectedEpisodeIndex.value + 1} 集，共 ${episodes.value.length} 集` : '无分集')
const episodeIndicatorStyle = computed(() => {
  const total = episodes.value.length
  const percent = total > 0 ? ((selectedEpisodeIndex.value + 1) / total) * 100 : 0
  return { width: `${percent.toFixed(2)}%` }
})

onMounted(() => {
  window.addEventListener(PLAYED_STATE_CHANGED_EVENT, handlePlayedStateChanged)
  mobileEpisodeMediaQuery = window.matchMedia('(max-width: 767px)')
  updateMobileEpisodeViewport()
  mobileEpisodeMediaQuery.addEventListener('change', updateMobileEpisodeViewport)
  void loadDetail()
})

onBeforeUnmount(() => {
  episodeSearchGeneration += 1
  mobileEpisodeMediaQuery?.removeEventListener('change', updateMobileEpisodeViewport)
  window.removeEventListener(PLAYED_STATE_CHANGED_EVENT, handlePlayedStateChanged)
})

function handlePlayedStateChanged() {
  void loadDetail()
}

watch([sourceId, itemId], loadDetail)

async function loadDetail() {
  episodeSearchGeneration += 1
  closeEpisodeSearch()
  isLoading.value = true
  errorMessage.value = null
  downloadFeedback.value = null
  seriesErrorMessage.value = null
  detail.value = null
  seasons.value = []
  episodes.value = []
  seasonEpisodeCache.clear()
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
          for (const season of contextSeasons) {
            const seasonEpisodes = getPlayableSeasonChildren(season)
            if (seasonEpisodes.length > 0)
              seasonEpisodeCache.set(season.id, seasonEpisodes)
          }
          selectedSeasonId.value = contextSeasons[0].id
          episodes.value = getPlayableSeasonChildren(contextSeasons[0])
        }
        else {
          episodes.value = getContextFlatEpisodes(contextual)
          seasonEpisodeCache.set(contextual.detail.id, episodes.value)
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
    const offline = await getOfflineDetail(sourceId.value, itemId.value).catch(() => undefined)
    if (offline) {
      const offlineDetail = await toOfflineMediaDetail(offline)
      detail.value = offlineDetail
      selectedMediaSourceId.value = offlineDetail.mediaSources?.[0]?.id ?? ''
      await refreshPlaybackProgress([offlineDetail])
      errorMessage.value = null
      return
    }
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
    seasonEpisodeCache.set(seriesId, episodeItems)
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
    seasonEpisodeCache.set(season.id, contextEpisodes)
    episodes.value = contextEpisodes
    resetEpisodeWindow()
    await refreshPlaybackProgress(episodes.value)
    selectInitialEpisodeForSeason()
    return
  }

  const cachedEpisodes = seasonEpisodeCache.get(season.id)
  if (cachedEpisodes) {
    episodes.value = cachedEpisodes
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
  episodes.value = playableEpisodeItems(items)
  seasonEpisodeCache.set(seasonId, episodes.value)
  resetEpisodeWindow()
  await refreshPlaybackProgress(episodes.value)
  selectInitialEpisodeForSeason()
}

function playableEpisodeItems(items: readonly MediaItem[]): MediaItem[] {
  return items.filter(item => item.type === 'episode' || item.type === 'file' || item.type === 'movie')
}

async function openEpisodeSearch() {
  isEpisodeSearchOpen.value = true
  episodeSearchKeyword.value = ''
  episodeSearchError.value = null
  episodeSearchEntries.value = []
  const generation = ++episodeSearchGeneration

  await nextTick()
  episodeSearchInputRef.value?.focus()

  isEpisodeSearchLoading.value = true
  try {
    if (seasons.value.length === 0) {
      const currentEpisodes = seasonEpisodeCache.get(detail.value?.id ?? '') ?? episodes.value
      if (generation === episodeSearchGeneration)
        episodeSearchEntries.value = currentEpisodes.map(episode => ({ season: null, episode }))
      return
    }

    let sourcePromise: Promise<DataSource> | undefined
    const entriesBySeason = await Promise.all(seasons.value.map(async (season) => {
      let seasonEpisodes = seasonEpisodeCache.get(season.id)
      if (!seasonEpisodes) {
        const contextualEpisodes = getPlayableSeasonChildren(season)
        if (contextualEpisodes.length > 0) {
          seasonEpisodes = contextualEpisodes
        }
        else {
          sourcePromise ??= resolveSource()
          seasonEpisodes = playableEpisodeItems(await (await sourcePromise).list(season.id))
        }
        if (generation !== episodeSearchGeneration)
          return []
        seasonEpisodeCache.set(season.id, seasonEpisodes)
      }
      return seasonEpisodes.map(episode => ({ season, episode }))
    }))

    if (generation === episodeSearchGeneration)
      episodeSearchEntries.value = entriesBySeason.flat()
  }
  catch (error) {
    if (generation === episodeSearchGeneration)
      episodeSearchError.value = toSafeErrorMessage(error, '全部分集加载失败，请稍后重试。')
  }
  finally {
    if (generation === episodeSearchGeneration)
      isEpisodeSearchLoading.value = false
  }
}

function closeEpisodeSearch() {
  episodeSearchGeneration += 1
  isEpisodeSearchOpen.value = false
  isEpisodeSearchLoading.value = false
  episodeSearchKeyword.value = ''
  episodeSearchError.value = null
  episodeSearchEntries.value = []
}

async function locateEpisodeSearchResult(entry: SeriesEpisodeSearchEntry) {
  if (entry.season)
    await selectSeason(entry.season)

  const index = episodes.value.findIndex(episode => episode.id === entry.episode.id)
  if (index < 0) {
    episodeSearchError.value = '无法在当前季定位这一集，请重新打开搜索后重试。'
    return
  }

  closeEpisodeSearch()
  await nextTick()
  selectEpisodeIndex(index)
}

function episodeSearchSeasonLabel(entry: SeriesEpisodeSearchEntry): string {
  if (entry.season?.name.trim())
    return entry.season.name
  const seasonNumber = entry.episode.seasonNumber ?? entry.season?.seasonNumber
  return seasonNumber == null ? '本剧' : `第 ${seasonNumber} 季`
}

function episodeSearchNumberLabel(episode: MediaItem): string {
  return episode.episodeNumber == null ? '单集' : `第 ${episode.episodeNumber} 集`
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

  if (isMobileEpisodeViewport.value) {
    scrollSelectedEpisodeIntoView(behavior)
    return
  }

  const selected = selectedEpisodeIndex.value
  if (selected < episodeWindowStart.value)
    episodeWindowStart.value = selected
  else if (selected >= episodeWindowStart.value + EPISODE_WINDOW_SIZE)
    episodeWindowStart.value = Math.min(maxEpisodeWindowStart.value, selected - EPISODE_WINDOW_SIZE + 1)
  else if (episodeWindowStart.value > maxEpisodeWindowStart.value)
    episodeWindowStart.value = maxEpisodeWindowStart.value

  scrollSelectedEpisodeIntoView(behavior)
}

function updateMobileEpisodeViewport() {
  const nextMobile = isNativeAndroid || mobileEpisodeMediaQuery?.matches === true
  const wasMobile = isMobileEpisodeViewport.value
  isMobileEpisodeViewport.value = nextMobile
  if (wasMobile && !nextMobile)
    ensureSelectedEpisodeVisible('auto')
  else
    scrollSelectedEpisodeIntoView('auto')
}

function renderedEpisodeIndex(index: number): number {
  return isMobileEpisodeViewport.value ? index : episodeWindowStart.value + index
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
      titleLogoUrl: item.titleLogoUrl,
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
  if (isResumePosition(item.resumePosition, item.duration))
    return item.resumePosition

  const entry = localResumeEntry(item)
  if (shouldResumePlayback(entry))
    return entry.position

  return undefined
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
  const progress = isResumePosition(item.resumePosition, item.duration)
    ? item.progress
    : shouldResumePlayback(entry)
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
    const selectedRoute = item ? undefined : selectedMediaSource.value
    const playbackSourceId = selectedRoute?.sourceId ?? target.sourceId ?? sourceId.value
    const playbackItemId = selectedRoute?.itemId ?? target.id
    const playbackMediaSourceId = item
      ? undefined
      : selectedRoute?.providerMediaSourceId ?? (selectedRoute?.id.startsWith('alternate-') ? undefined : selectedRoute?.id)
    store.loadConfigs()
    await store.syncManager()
    if (!store.getSource(playbackSourceId))
      throw new Error('所选播放线路不可用，请检查对应数据源。')
    const isCurrentDetail = target.id === detail.value?.id
    const queue = (item ? createPlaybackQueue(episodes.value, item.id) : undefined) ?? recoverRoutePlaybackQueue(target.id)
    const playbackContextId = savePlaybackMediaContext({
      sourceId: playbackSourceId,
      itemId: playbackItemId,
      title: target.name,
      currentItem: { ...target, id: playbackItemId, sourceId: playbackSourceId, resumePosition: resumePositionForItem(target) },
      mediaSourceId: playbackMediaSourceId,
      locator: {
        kind: 'dataSource',
        sourceId: playbackSourceId,
        itemId: playbackItemId,
        mediaSourceId: playbackMediaSourceId,
      },
      subtitles: isCurrentDetail ? detail.value?.subtitles : undefined,
      audioTracks: isCurrentDetail ? detail.value?.audioTracks : undefined,
      queue,
    })
    await router.push({
      name: 'player',
      query: createPlaybackRouteQuery({
        sourceId: playbackSourceId,
        itemId: playbackItemId,
        contextId: playbackContextId,
        mediaSourceId: playbackMediaSourceId,
      }),
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

function trackLabel(track: AudioTrack | SubtitleTrack): string {
  const language = track.language && track.language !== 'Unknown' ? track.language : undefined
  const codec = 'codec' in track && track.codec !== 'unknown' ? ` · ${track.codec}` : ''
  const channels = 'channels' in track && track.channels ? ` · ${track.channels}ch` : ''
  const title = 'title' in track ? track.title : undefined
  return `${title ?? language ?? '未命名轨道'}${codec}${channels}${track.isDefault ? ' · 默认' : ''}`
}

function episodeTitle(item: MediaItem): string {
  if (item.episodeNumber == null)
    return item.name
  const prefix = `第 ${item.episodeNumber} 集`
  const compactPrefix = `第${item.episodeNumber}集`
  const name = item.name.trim()
  return !name || name === prefix || name === compactPrefix ? prefix : `${prefix} · ${name}`
}

function itemRuntime(item: MediaItem): string {
  return item.duration ? `${Math.round(item.duration / 60)} 分钟` : ''
}

function markTitleLogoFailed(url: string) {
  failedTitleLogoUrls.value = new Set([...failedTitleLogoUrls.value, url])
}
</script>

<template>
  <div class="detail-view theme-adaptive min-h-screen bg-[var(--color-bg)]">
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
      <section
        class="detail-hero theme-immersive-dark relative min-h-[68vh] overflow-hidden bg-cover bg-center"
        :style="heroStyle"
        data-media-action-target
        @pointerdown="beginDetailActionLongPress"
        @pointermove="moveMediaActionLongPress"
        @pointerup="endMediaActionLongPress"
        @pointercancel="cancelMediaActionLongPress($event.pointerId)"
        @pointerleave="cancelMediaActionLongPress($event.pointerId)"
        @click="handleDetailHeroClick"
        @contextmenu="openDetailActionMenu"
      >
        <div class="pointer-events-none absolute inset-0 bg-gradient-to-r from-black/94 via-black/62 to-black/20" />
        <div class="pointer-events-none absolute inset-0 bg-gradient-to-t from-[var(--color-bg)] via-transparent to-black/40" />
        <div class="detail-hero-content relative flex min-h-[68vh] items-end gap-8 px-4 pb-10 pt-20 md:px-6 md:pb-12 md:pl-24 md:pt-24 lg:px-12 lg:pl-28">
          <div class="hidden w-56 flex-shrink-0 overflow-hidden rounded-[1.8rem] border border-white/12 bg-white/6 shadow-2xl md:block">
            <img v-if="detail.posterUrl" :src="detail.posterUrl" :alt="detail.name" class="aspect-[2/3] w-full object-cover" loading="eager" decoding="async">
            <div v-else class="flex aspect-[2/3] items-center justify-center p-6 text-center text-sm text-white/45">
              {{ detail.name }}
            </div>
          </div>

          <div class="max-w-4xl">
            <p v-if="!visibleTitleLogoUrl" class="text-xs uppercase tracking-[0.28em] text-white/42">
              {{ isSeriesDetail ? 'OhMyCine Series' : 'OhMyCine Detail' }}
            </p>
            <img
              v-if="visibleTitleLogoUrl"
              :src="visibleTitleLogoUrl"
              :alt="detail.name"
              class="max-h-28 max-w-[min(30rem,78vw)] object-contain object-left drop-shadow-2xl"
              loading="eager"
              decoding="async"
              @error="markTitleLogoFailed(visibleTitleLogoUrl)"
            >
            <h1 :class="visibleTitleLogoUrl ? 'sr-only' : 'mt-3 text-3xl font-bold leading-tight drop-shadow-2xl sm:text-4xl lg:text-6xl'">
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
              <button
                v-if="canEnqueueOnlineDownload"
                type="button"
                class="rounded-full border border-white/16 bg-white/10 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/16 disabled:opacity-50"
                :disabled="isEnqueueingOnlineDownload"
                @click="enqueueOnlineDownload"
              >
                {{ isEnqueueingOnlineDownload ? '正在提交…' : '下载并入库' }}
              </button>
              <span v-if="isSeriesDetail && selectedEpisode" class="rounded-full border border-white/12 bg-white/8 px-4 py-3 text-xs text-white/58">{{ episodeTitle(selectedEpisode) }}</span>
              <span v-else-if="visibleMediaSources.length" class="rounded-full border border-white/12 bg-white/8 px-4 py-3 text-xs text-white/58">{{ sourceLabel }}</span>
              <span class="detail-played-state" :class="{ 'is-played': detailPlayed }">{{ detailPlayed ? '✓ 已播放' : '未播放' }}</span>
              <button v-if="canToggleDetailPlayed" type="button" class="detail-played-toggle" @click.stop="toggleDetailPlayedState">
                {{ detailPlayed ? '标记为未播放' : '标记为已播放' }}
              </button>
            </div>
          </div>
        </div>
      </section>

      <main class="detail-content mobile-nav-safe space-y-10 px-4 pb-14 md:px-6 md:pl-24 lg:px-12 lg:pl-28">
        <div v-if="downloadFeedback" class="rounded-2xl border border-emerald-400/20 bg-emerald-400/10 px-5 py-4 text-sm text-emerald-100">
          {{ downloadFeedback }}
        </div>
        <div v-if="errorMessage" class="rounded-2xl border border-red-400/20 bg-red-400/10 px-5 py-4 text-sm text-red-100">
          {{ errorMessage }}
        </div>

        <section
          v-if="isSeriesDetail"
          class="episode-rail-shell relative overflow-hidden rounded-[2rem] border border-white/10 bg-white/[0.045] p-4 shadow-2xl sm:p-6"
          :class="[{ 'is-mobile-episode-surface': isMobileEpisodeViewport }, `is-${mobileEpisodeLayout}`]"
        >
          <div class="episode-shell-decoration pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(255,255,255,0.13),transparent_36%),linear-gradient(135deg,rgba(255,255,255,0.08),transparent_42%)]" />
          <div class="episode-rail-heading relative flex flex-wrap items-end justify-between gap-4">
            <div>
              <p class="text-xs uppercase tracking-[0.24em] text-white/36">
                Seasons & Episodes
              </p>
              <h2 class="mt-2 text-2xl font-bold">
                分集
              </h2>
            </div>
            <div class="flex flex-wrap items-center justify-end gap-3 text-sm text-white/46">
              <button
                type="button"
                class="episode-search-trigger"
                aria-label="搜索全部分集标题"
                @click="openEpisodeSearch"
              >
                <svg width="16" height="16" viewBox="0 0 20 20" fill="none" aria-hidden="true"><circle cx="8.75" cy="8.75" r="5.75" stroke="currentColor" stroke-width="1.8" /><path d="m13 13 4 4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" /></svg>
                搜索分集
              </button>
              <span v-if="selectedSeason">{{ selectedSeason.name }} · {{ episodes.length }} 集</span>
              <span v-if="episodeRangeLabel" class="rounded-full border border-white/10 bg-white/6 px-3 py-1 text-xs text-white/50" aria-live="polite">{{ episodeRangeLabel }}</span>
            </div>
          </div>

          <div v-if="seriesErrorMessage" class="relative mt-5 rounded-2xl border border-red-400/20 bg-red-400/10 px-4 py-3 text-sm text-red-100">
            {{ seriesErrorMessage }}
          </div>

          <div v-if="seasons.length" class="episode-season-strip relative mt-6 flex gap-3 overflow-x-auto cinema-scrollbar pb-2">
            <button
              v-for="season in seasons"
              :key="season.id"
              type="button"
              class="flex-shrink-0 rounded-full border px-5 py-3 text-sm transition-colors"
              :class="{ 'is-selected': selectedSeasonId === season.id }"
              :aria-pressed="selectedSeasonId === season.id"
              @click="selectSeason(season)"
            >
              {{ season.name }}
            </button>
          </div>

          <div v-if="isSeriesContentLoading" class="episode-loading-strip relative mt-7 flex gap-4 overflow-hidden" aria-hidden="true">
            <div v-for="i in 4" :key="i" class="h-72 min-w-[min(20rem,calc(100vw-4rem))] animate-pulse rounded-[1.7rem] bg-white/6 md:min-w-[23rem]" />
          </div>

          <div v-else-if="episodes.length" class="episode-rail group relative mt-7">
            <div class="episode-edge-fade pointer-events-none absolute inset-y-0 left-0 z-10 w-16 bg-gradient-to-r from-[var(--color-bg)] to-transparent" />
            <div class="episode-edge-fade pointer-events-none absolute inset-y-0 right-0 z-10 w-20 bg-gradient-to-l from-[var(--color-bg)] to-transparent" />
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
                v-for="(episode, visibleIndex) in renderedEpisodes"
                :id="`episode-card-${renderedEpisodeIndex(visibleIndex)}`"
                :key="episode.id"
                class="episode-card group/card flex min-w-[min(20rem,calc(100vw-4rem))] max-w-[min(20rem,calc(100vw-4rem))] cursor-pointer flex-col overflow-hidden rounded-[1.7rem] border shadow-xl shadow-black/24 outline-none transition-all duration-300 hover:-translate-y-1 hover:border-white/24 hover:bg-white/[0.075] focus-visible:-translate-y-1 focus-visible:border-white/42 focus-visible:ring-2 focus-visible:ring-white/22 md:min-w-[23rem] md:max-w-[23rem]"
                :class="{ 'is-selected': selectedEpisodeIndex === renderedEpisodeIndex(visibleIndex) }"
                :data-episode-index="renderedEpisodeIndex(visibleIndex)"
                role="option"
                tabindex="0"
                :aria-selected="selectedEpisodeIndex === renderedEpisodeIndex(visibleIndex)"
                :aria-label="`${selectedEpisodeIndex === renderedEpisodeIndex(visibleIndex) ? '当前选中' : '选择'} ${episodeTitle(episode)}`"
                @click="handleEpisodeCardClick(episode, renderedEpisodeIndex(visibleIndex))"
                @keydown.enter.self.prevent="handleEpisodeCardClick(episode, renderedEpisodeIndex(visibleIndex))"
              >
                <div class="episode-artwork theme-immersive-dark relative block overflow-hidden text-left">
                  <img v-if="episode.backdropUrl || episode.posterUrl" :src="episode.backdropUrl ?? episode.posterUrl" :alt="episode.name" class="aspect-video w-full object-cover transition-transform duration-700 group-hover/card:scale-105" loading="lazy" decoding="async">
                  <div v-else class="episode-artwork-fallback flex aspect-video w-full items-center justify-center p-5 text-center text-sm text-white/42">
                    {{ episodeTitle(episode) }}
                  </div>
                  <div class="absolute inset-0 bg-gradient-to-t from-black/88 via-black/8 to-transparent" />
                  <span class="absolute left-4 top-4 rounded-full border border-white/12 bg-black/42 px-3 py-1 text-xs font-semibold text-white/76 backdrop-blur-xl">
                    {{ episode.episodeNumber == null ? 'Episode' : `第 ${episode.episodeNumber} 集` }}
                  </span>
                  <span v-if="selectedEpisodeIndex === renderedEpisodeIndex(visibleIndex)" class="absolute right-4 top-4 rounded-full border border-white/18 bg-white/18 px-3 py-1 text-xs font-semibold text-white shadow-lg backdrop-blur-xl">
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

            <div class="episode-position-row mt-3 flex items-center gap-3 text-xs text-white/42" aria-live="polite">
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
            {{ emptyEpisodesMessage }}
          </div>
        </section>

        <Teleport to="body">
          <div
            v-if="isEpisodeSearchOpen"
            class="episode-search-backdrop theme-adaptive"
            role="presentation"
            @click.self="closeEpisodeSearch"
            @keydown.esc="closeEpisodeSearch"
          >
            <section
              class="episode-search-dialog"
              role="dialog"
              aria-modal="true"
              aria-labelledby="episode-search-title"
            >
              <header class="flex items-start justify-between gap-4">
                <div>
                  <p class="text-xs uppercase tracking-[0.22em] text-white/38">
                    Series Search
                  </p>
                  <h2 id="episode-search-title" class="mt-2 text-2xl font-bold text-white">
                    搜索全部分集
                  </h2>
                  <p class="mt-2 text-sm text-white/48">
                    按单集标题查找，选择后只定位到该集，不会自动播放。
                  </p>
                </div>
                <button type="button" class="episode-search-close" aria-label="关闭分集搜索" @click="closeEpisodeSearch">
                  <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="m4 4 12 12M16 4 4 16" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" /></svg>
                </button>
              </header>

              <label class="episode-search-field mt-6">
                <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true"><circle cx="8.75" cy="8.75" r="5.75" stroke="currentColor" stroke-width="1.8" /><path d="m13 13 4 4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" /></svg>
                <span class="sr-only">单集标题</span>
                <input
                  ref="episodeSearchInputRef"
                  v-model="episodeSearchKeyword"
                  type="search"
                  autocomplete="off"
                  placeholder="输入单集标题"
                >
                <button v-if="episodeSearchKeyword" type="button" aria-label="清空搜索内容" @click="episodeSearchKeyword = ''">
                  清空
                </button>
              </label>

              <div class="episode-search-results mt-5" aria-live="polite">
                <div v-if="isEpisodeSearchLoading" class="episode-search-state">
                  正在载入全部季的分集…
                </div>
                <div v-else-if="episodeSearchError" class="episode-search-state is-error">
                  <p>{{ episodeSearchError }}</p>
                  <button type="button" class="mt-3 rounded-full bg-white/10 px-4 py-2 text-xs text-white" @click="openEpisodeSearch">
                    重试
                  </button>
                </div>
                <div v-else-if="!episodeSearchKeyword.trim()" class="episode-search-state">
                  已载入 {{ episodeSearchEntries.length }} 集，输入标题开始查找。
                </div>
                <div v-else-if="episodeSearchResults.length === 0" class="episode-search-state">
                  没有找到标题包含“{{ episodeSearchKeyword.trim() }}”的分集。
                </div>
                <template v-else>
                  <p class="mb-3 px-1 text-xs text-white/42">
                    找到 {{ episodeSearchResults.length }} 个结果
                  </p>
                  <button
                    v-for="entry in episodeSearchResults"
                    :key="`${entry.season?.id ?? 'series'}:${entry.episode.id}`"
                    type="button"
                    class="episode-search-result"
                    @click="locateEpisodeSearchResult(entry)"
                  >
                    <span class="episode-search-result-index">{{ episodeSearchSeasonLabel(entry) }} · {{ episodeSearchNumberLabel(entry.episode) }}</span>
                    <span class="episode-search-result-title">{{ episodeSearchTitle(entry.episode) }}</span>
                    <span v-if="entry.episode.overview" class="episode-search-result-overview">{{ entry.episode.overview }}</span>
                  </button>
                </template>
              </div>
            </section>
          </div>
        </Teleport>

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
          <div class="glass-panel min-w-0 rounded-[1.6rem] p-6">
            <h2 class="text-xl font-bold">
              演职员
            </h2>
            <div v-if="visiblePeople.length" class="people-strip cinema-scrollbar mt-4">
              <article v-for="person in visiblePeople" :key="`${person.id || person.name}:${person.role || ''}:${person.character || ''}`" class="person-tile">
                <div class="person-avatar">
                  <img v-if="person.imageUrl" :src="person.imageUrl" :alt="`${person.name} 照片`" loading="lazy" decoding="async">
                  <span v-else>{{ person.name.slice(0, 1) }}</span>
                </div>
                <strong :title="person.name">{{ person.name }}</strong>
                <small :title="personSubtitle(person.role, person.character)">{{ personSubtitle(person.role, person.character) }}</small>
              </article>
            </div>
            <p v-else class="mt-4 text-sm leading-7 text-white/58">
              暂无演职员信息。
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
.detail-played-state,.detail-played-toggle { border: 1px solid rgba(255,255,255,.14); border-radius: 999px; padding: .7rem 1rem; color: rgba(255,255,255,.68); background: rgba(255,255,255,.08); font-size: .75rem; font-weight: 700; }
.detail-played-state.is-played { border-color: rgba(34,197,94,.42); color: #dcfce7; background: rgba(34,197,94,.18); }
.detail-played-toggle:hover { background: rgba(255,255,255,.14); }
.people-strip { display: grid; grid-auto-flow: column; grid-auto-columns: 7.25rem; gap: 1rem; overflow-x: auto; padding-bottom: .55rem; }
.person-tile { min-width: 0; text-align: center; }
.person-avatar { display: grid; width: 5.75rem; height: 5.75rem; margin: 0 auto .7rem; place-items: center; overflow: hidden; border: 1px solid rgba(255,255,255,.12); border-radius: 999px; background: rgba(255,255,255,.07); color: rgba(255,255,255,.52); font-size: 1.35rem; }
.person-avatar img { width: 100%; height: 100%; object-fit: cover; }
.person-tile strong,.person-tile small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.person-tile strong { font-size: .82rem; }
.person-tile small { margin-top: .22rem; color: rgba(255,255,255,.46); font-size: .7rem; }
.episode-search-trigger,
.episode-search-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-border);
  color: var(--color-text-secondary);
  background: var(--surface-soft);
  transition: color var(--duration-fast) var(--ease-out), background var(--duration-fast) var(--ease-out), border-color var(--duration-fast) var(--ease-out);
}

.episode-search-trigger {
  gap: 0.45rem;
  border-radius: 999px;
  padding: 0.55rem 0.85rem;
  font-size: 0.75rem;
  font-weight: 700;
}

.episode-search-close {
  width: 2.6rem;
  height: 2.6rem;
  flex: 0 0 auto;
  border-radius: 999px;
}

.episode-search-trigger:hover,
.episode-search-trigger:focus-visible,
.episode-search-close:hover,
.episode-search-close:focus-visible {
  border-color: var(--control-border-hover);
  color: var(--color-text);
  background: var(--surface-soft-hover);
}

.episode-search-backdrop {
  position: fixed;
  z-index: 180;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1.25rem;
  background: rgba(0, 0, 0, 0.68);
  backdrop-filter: blur(18px);
  -webkit-backdrop-filter: blur(18px);
}

.episode-search-dialog {
  width: min(42rem, 100%);
  max-height: min(46rem, calc(100svh - 2.5rem));
  overflow: hidden;
  border: 1px solid var(--glass-border-hover);
  border-radius: 2rem;
  padding: 1.5rem;
  color: var(--color-text);
  background: color-mix(in srgb, var(--color-bg) 88%, transparent);
  box-shadow: var(--glass-shadow-elevated);
  backdrop-filter: blur(32px);
  -webkit-backdrop-filter: blur(32px);
}

.episode-search-field {
  display: flex;
  min-height: 3.25rem;
  align-items: center;
  gap: 0.75rem;
  border: 1px solid var(--color-border);
  border-radius: 1rem;
  padding: 0 1rem;
  color: var(--color-text-muted);
  background: var(--surface-soft);
}

.episode-search-field:focus-within {
  border-color: var(--control-border-hover);
  box-shadow: 0 0 0 3px rgba(255, 255, 255, 0.08);
}

.episode-search-field input {
  min-width: 0;
  flex: 1;
  border: 0;
  outline: 0;
  color: var(--color-text);
  background: transparent;
}

.episode-search-field input::placeholder {
  color: var(--color-text-muted);
}

.episode-search-field button {
  flex: 0 0 auto;
  color: var(--color-text-secondary);
  font-size: 0.75rem;
}

.episode-search-results {
  max-height: min(29rem, calc(100svh - 14rem));
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
}

.episode-search-state {
  border: 1px solid var(--color-border);
  border-radius: 1rem;
  padding: 2.5rem 1.25rem;
  color: var(--color-text-muted);
  background: var(--surface-soft);
  text-align: center;
}

.episode-search-state.is-error {
  border-color: rgba(248, 113, 113, 0.24);
  color: #fecaca;
  background: rgba(248, 113, 113, 0.1);
}

.episode-search-result {
  display: flex;
  width: 100%;
  flex-direction: column;
  align-items: flex-start;
  gap: 0.35rem;
  border: 1px solid var(--color-border);
  border-radius: 1rem;
  padding: 0.9rem 1rem;
  color: var(--color-text);
  background: var(--surface-soft);
  text-align: left;
  transition: border-color var(--duration-fast) var(--ease-out), background var(--duration-fast) var(--ease-out), transform var(--duration-fast) var(--ease-out);
}

.episode-search-result + .episode-search-result {
  margin-top: 0.6rem;
}

.episode-search-result:hover,
.episode-search-result:focus-visible {
  border-color: var(--control-border-hover);
  background: var(--surface-soft-hover);
  transform: translateY(-1px);
}

.episode-search-result-index {
  color: var(--color-text-muted);
  font-size: 0.72rem;
}

.episode-search-result-title {
  font-size: 0.95rem;
  font-weight: 700;
}

.episode-search-result-overview {
  display: -webkit-box;
  overflow: hidden;
  color: var(--color-text-secondary);
  font-size: 0.78rem;
  line-height: 1.5;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.episode-rail-shell {
  border-color: var(--color-border);
  background: var(--surface-soft);
  backdrop-filter: blur(28px);
  -webkit-backdrop-filter: blur(28px);
}

.episode-season-strip button {
  border-color: var(--color-border);
  color: var(--color-text-secondary);
  background: var(--surface-soft);
}

.episode-season-strip button:hover,
.episode-season-strip button.is-selected {
  color: var(--color-text);
  background: var(--surface-soft-hover);
}

.episode-season-strip button.is-selected {
  border-color: var(--control-border-hover);
  box-shadow: var(--glass-shadow);
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
  border-color: var(--color-border);
  background: var(--surface-soft);
  scroll-snap-align: start;
}

.episode-card:hover,
.episode-card:focus-visible,
.episode-card.is-selected {
  border-color: var(--control-border-hover);
  background: var(--surface-soft-hover);
}

.episode-card.is-selected {
  transform: translateY(-0.25rem);
  box-shadow: var(--glass-shadow-elevated);
}

.episode-artwork-fallback {
  background: #171720;
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

.episode-rail-shell.is-mobile-episode-surface {
  margin-inline: -1rem;
  border-right: 0;
  border-left: 0;
  border-radius: 0;
  padding: 0.9rem 0 1.1rem;
  background: var(--surface-soft);
  box-shadow: none;
}

.is-mobile-episode-surface .episode-shell-decoration,
.is-mobile-episode-surface .episode-edge-fade,
.is-mobile-episode-surface .episode-nav-button,
.is-mobile-episode-surface .episode-position-row {
  display: none;
}

.is-mobile-episode-surface .episode-rail-heading,
.is-mobile-episode-surface .episode-season-strip,
.is-mobile-episode-surface .episode-loading-strip {
  margin-right: 1rem;
  margin-left: 1rem;
}

.is-mobile-episode-surface .episode-rail {
  margin-top: 1rem;
}

.is-mobile-episode-surface .episode-card-strip {
  -webkit-overflow-scrolling: touch;
  scroll-padding-inline: 1rem;
}

.is-mobile-episode-surface.is-horizontal .episode-card-strip {
  gap: 0.75rem;
  padding: 0.15rem 1rem 0.8rem;
  scroll-snap-type: x proximity;
  touch-action: pan-x;
  overscroll-behavior-x: contain;
}

.is-mobile-episode-surface.is-horizontal .episode-card {
  min-width: calc(100vw - 2rem);
  max-width: calc(100vw - 2rem);
  scroll-snap-align: center;
  transform: none !important;
}

.is-mobile-episode-surface.is-vertical .episode-card-strip {
  display: grid;
  overflow: visible;
  gap: 0.75rem;
  padding: 0 1rem;
  scroll-snap-type: none;
  touch-action: pan-y;
}

.is-mobile-episode-surface.is-vertical .episode-card {
  display: grid;
  min-width: 0;
  max-width: none;
  grid-template-columns: minmax(8.5rem, 38vw) minmax(0, 1fr);
  transform: none !important;
  content-visibility: auto;
  contain-intrinsic-size: 10rem;
}

.is-mobile-episode-surface.is-vertical .episode-card > div:first-child {
  min-height: 10rem;
}

.is-mobile-episode-surface.is-vertical .episode-card > div:first-child > img,
.is-mobile-episode-surface.is-vertical .episode-card > div:first-child > div:first-child {
  width: 100%;
  height: 100%;
  aspect-ratio: auto;
  object-fit: cover;
}

.is-mobile-episode-surface.is-vertical .episode-card > div:last-child {
  gap: 0.75rem;
  padding: 0.85rem;
}

.is-mobile-episode-surface.is-vertical .episode-card h3 {
  margin-top: 0.45rem;
  font-size: 0.92rem;
}

.is-mobile-episode-surface.is-vertical .episode-card p {
  display: none;
}

.is-mobile-episode-surface.is-vertical .episode-card button {
  border-radius: 6px;
  padding: 0.5rem 0.65rem;
}

@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .episode-search-backdrop {
    align-items: stretch;
    padding: 0;
  }

  .episode-search-dialog {
    width: 100%;
    max-height: 100svh;
    border: 0;
    border-radius: 0;
    padding: max(1rem, env(safe-area-inset-top)) 1rem max(1.25rem, env(safe-area-inset-bottom));
  }

  .episode-search-results {
    max-height: calc(100svh - max(1rem, env(safe-area-inset-top)) - max(1.25rem, env(safe-area-inset-bottom)) - 11.5rem);
  }

  .detail-hero,
  .detail-hero-content {
    min-height: min(68svh, 38rem);
  }

  .detail-hero-content {
    padding-top: max(5rem, calc(env(safe-area-inset-top) + 4rem));
    padding-bottom: 2rem;
  }

  .detail-hero-content h1 {
    font-size: 2rem;
  }

  .detail-hero-content p.line-clamp-5 {
    -webkit-line-clamp: 3;
    font-size: 0.88rem;
    line-height: 1.65;
  }

  .detail-hero-content button,
  .detail-hero-content span.rounded-full {
    min-height: 2.9rem;
    border-radius: 8px;
  }

  .detail-content {
    row-gap: 1.8rem;
  }

  .detail-content :deep(.glass-panel) {
    border-radius: 8px;
  }

  .is-mobile-episode-surface.is-horizontal .episode-card > div:last-child {
    gap: 1rem;
    padding: 0.9rem;
  }

  .is-mobile-episode-surface.is-horizontal .episode-card h3 {
    font-size: 1rem;
  }
}
</style>
