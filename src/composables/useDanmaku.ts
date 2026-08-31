import type { DanmakuComment, DanmakuMatch, DanmakuSearchEpisode, DanmakuSettings } from '@/services/danmaku/types'
import { onBeforeUnmount, ref } from 'vue'
import { fetchDanmakuComments, matchDanmaku, searchDanmaku } from '@/services/danmaku/client'
import { selectExactStructuredDanmakuMatch } from '@/services/danmaku/selection'
import { DANMAKU_SETTINGS_CHANGED_EVENT, loadDanmakuSettings, saveDanmakuSettings } from '@/services/danmaku/settings'
import { toSafeErrorMessage } from '@/services/datasource/errors'

const memoryCache = new Map<string, { matches: DanmakuMatch[], selected: DanmakuMatch, comments: DanmakuComment[] }>()
const providerTrackCache = new Map<string, DanmakuComment[]>()

export interface DanmakuLoadIdentity {
  matchName: string
  searchTitle: string
  episode: string
}

export function useDanmaku() {
  const settings = ref<DanmakuSettings>(loadDanmakuSettings())
  const comments = ref<DanmakuComment[]>([])
  const matches = ref<DanmakuMatch[]>([])
  const selectedEpisodeId = ref<number | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)
  let generation = 0
  let lastMedia: { identity: DanmakuLoadIdentity, duration: number } | null = null

  async function loadForMedia(identity: DanmakuLoadIdentity, duration: number, force = false) {
    const currentGeneration = ++generation
    lastMedia = { identity, duration }
    comments.value = []
    matches.value = []
    selectedEpisodeId.value = null
    error.value = null
    if (!settings.value.enabled)
      return
    if (settings.value.provider === 'custom' && !settings.value.customBaseUrl.trim()) {
      error.value = '请先在设置中填写兼容弹幕 API 地址。'
      return
    }
    const cacheKey = `${settings.value.provider}:${settings.value.customBaseUrl}:${identity.matchName}:${identity.searchTitle}:${identity.episode}:${Math.round(duration)}`
    const cached = !force ? memoryCache.get(cacheKey) : undefined
    if (cached) {
      matches.value = cached.matches
      selectedEpisodeId.value = cached.selected.episodeId
      comments.value = cached.comments
      return
    }
    loading.value = true
    try {
      let matchResponse: Awaited<ReturnType<typeof matchDanmaku>> | null = null
      let matchFailure: unknown = null
      let searchedBySeries = false
      try {
        matchResponse = await matchDanmaku(settings.value, identity.matchName, duration)
      }
      catch (reason) {
        matchFailure = reason
      }
      if (generation !== currentGeneration)
        return
      let nextMatches = matchResponse?.matches ?? []
      let selected = matchResponse?.exact ? nextMatches[0] : undefined
      if (!selected && identity.searchTitle && identity.episode) {
        searchedBySeries = true
        error.value = null
        const searchResponse = await searchDanmaku(settings.value, identity.searchTitle, identity.episode)
        if (generation !== currentGeneration)
          return
        nextMatches = searchResponse.animes.flatMap(anime => anime.episodes.map(episode => ({
          episodeId: episode.episodeId,
          animeId: anime.animeId,
          animeTitle: anime.animeTitle,
          episodeTitle: episode.episodeTitle,
          shift: 0,
        })))
        selected = selectExactStructuredDanmakuMatch(searchResponse, identity.searchTitle) ?? undefined
      }
      matches.value = nextMatches
      if (!selected) {
        error.value = nextMatches.length
          ? '找到了多个可能结果，请手动搜索确认。'
          : matchFailure && !searchedBySeries
            ? toSafeErrorMessage(matchFailure, '没有自动匹配到弹幕，请尝试手动搜索。')
            : '没有自动匹配到弹幕，请尝试手动搜索。'
        return
      }
      selectedEpisodeId.value = selected.episodeId
      const nextComments = await fetchDanmakuComments(settings.value, selected)
      if (generation !== currentGeneration)
        return
      error.value = null
      comments.value = nextComments
      memoryCache.set(cacheKey, { matches: nextMatches, selected, comments: nextComments })
      if (memoryCache.size > 20)
        memoryCache.delete(memoryCache.keys().next().value as string)
    }
    catch (reason) {
      if (generation === currentGeneration)
        error.value = toSafeErrorMessage(reason, '弹幕加载失败。')
    }
    finally {
      if (generation === currentGeneration)
        loading.value = false
    }
  }

  async function loadProviderComments(cacheKey: string, loader: () => Promise<DanmakuComment[]>, force = false): Promise<boolean> {
    const currentGeneration = ++generation
    comments.value = []
    matches.value = []
    selectedEpisodeId.value = null
    error.value = null
    if (!settings.value.enabled)
      return true
    const cached = force ? undefined : providerTrackCache.get(cacheKey)
    if (cached) {
      comments.value = cached
      return true
    }
    loading.value = true
    try {
      const nextComments = await loader()
      if (generation !== currentGeneration)
        return false
      if (nextComments.length === 0)
        return false
      comments.value = nextComments
      providerTrackCache.set(cacheKey, nextComments)
      if (providerTrackCache.size > 20)
        providerTrackCache.delete(providerTrackCache.keys().next().value as string)
      return true
    }
    catch (reason) {
      if (generation === currentGeneration)
        error.value = toSafeErrorMessage(reason, '来源弹幕加载失败。')
      return false
    }
    finally {
      if (generation === currentGeneration)
        loading.value = false
    }
  }

  async function selectSearchEpisode(animeId: number, animeTitle: string, episode: DanmakuSearchEpisode) {
    const currentGeneration = ++generation
    if (!settings.value.enabled)
      settings.value = await saveDanmakuSettings({ ...settings.value, enabled: true })
    if (generation !== currentGeneration)
      return
    const selected: DanmakuMatch = {
      episodeId: episode.episodeId,
      animeId,
      animeTitle,
      episodeTitle: episode.episodeTitle,
      shift: 0,
    }
    const existing = matches.value.filter(item => item.episodeId !== selected.episodeId)
    matches.value = [selected, ...existing]
    await selectMatch(selected.episodeId)
  }

  function resetForMediaChange() {
    generation++
    lastMedia = null
    comments.value = []
    matches.value = []
    selectedEpisodeId.value = null
    loading.value = false
    error.value = null
  }

  async function selectMatch(episodeId: number) {
    const selected = matches.value.find(item => item.episodeId === episodeId)
    if (!selected)
      return
    const currentGeneration = ++generation
    selectedEpisodeId.value = episodeId
    loading.value = true
    error.value = null
    comments.value = []
    try {
      const nextComments = await fetchDanmakuComments(settings.value, selected)
      if (generation === currentGeneration)
        comments.value = nextComments
    }
    catch (reason) {
      if (generation === currentGeneration)
        error.value = toSafeErrorMessage(reason, '弹幕加载失败。')
    }
    finally {
      if (generation === currentGeneration)
        loading.value = false
    }
  }

  async function updateSettings(next: DanmakuSettings) {
    const previous = settings.value
    settings.value = await saveDanmakuSettings(next)
    const providerChanged = previous.provider !== next.provider || previous.customBaseUrl !== next.customBaseUrl
    if ((providerChanged || (!previous.enabled && next.enabled)) && lastMedia)
      await loadForMedia(lastMedia.identity, lastMedia.duration, true)
    else if (!next.enabled)
      comments.value = []
  }

  async function toggleEnabled() {
    const next = { ...settings.value, enabled: !settings.value.enabled }
    settings.value = await saveDanmakuSettings(next)
    if (next.enabled && lastMedia)
      await loadForMedia(lastMedia.identity, lastMedia.duration)
    else
      comments.value = []
  }

  function reloadSettings(event: Event) {
    const detail = (event as CustomEvent<DanmakuSettings>).detail
    settings.value = detail ?? loadDanmakuSettings()
  }

  window.addEventListener(DANMAKU_SETTINGS_CHANGED_EVENT, reloadSettings)
  onBeforeUnmount(() => window.removeEventListener(DANMAKU_SETTINGS_CHANGED_EVENT, reloadSettings))

  return { settings, comments, loading, error, loadForMedia, loadProviderComments, selectSearchEpisode, resetForMediaChange, updateSettings, toggleEnabled }
}
