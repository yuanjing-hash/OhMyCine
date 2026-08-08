import type { DanmakuComment, DanmakuMatch, DanmakuSettings } from '@/services/danmaku/types'
import { onBeforeUnmount, ref } from 'vue'
import { fetchDanmakuComments, matchDanmaku } from '@/services/danmaku/client'
import { DANMAKU_SETTINGS_CHANGED_EVENT, loadDanmakuSettings, saveDanmakuSettings } from '@/services/danmaku/settings'
import { toSafeErrorMessage } from '@/services/datasource/errors'

const memoryCache = new Map<string, { matches: DanmakuMatch[], selected: DanmakuMatch, comments: DanmakuComment[] }>()

export function useDanmaku() {
  const settings = ref<DanmakuSettings>(loadDanmakuSettings())
  const comments = ref<DanmakuComment[]>([])
  const matches = ref<DanmakuMatch[]>([])
  const selectedEpisodeId = ref<number | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)
  let generation = 0
  let lastMedia: { name: string, duration: number } | null = null

  async function loadForMedia(name: string, duration: number, force = false) {
    const currentGeneration = ++generation
    lastMedia = { name, duration }
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
    const cacheKey = `${settings.value.provider}:${settings.value.customBaseUrl}:${name}:${Math.round(duration)}`
    const cached = !force ? memoryCache.get(cacheKey) : undefined
    if (cached) {
      matches.value = cached.matches
      selectedEpisodeId.value = cached.selected.episodeId
      comments.value = cached.comments
      return
    }
    loading.value = true
    try {
      const nextMatches = await matchDanmaku(settings.value, name, duration)
      if (generation !== currentGeneration)
        return
      matches.value = nextMatches
      const selected = nextMatches[0]
      if (!selected) {
        error.value = '没有匹配到此影片的弹幕。'
        return
      }
      selectedEpisodeId.value = selected.episodeId
      const nextComments = await fetchDanmakuComments(settings.value, selected)
      if (generation !== currentGeneration)
        return
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
      await loadForMedia(lastMedia.name, lastMedia.duration, true)
    else if (!next.enabled)
      comments.value = []
  }

  async function toggleEnabled() {
    const next = { ...settings.value, enabled: !settings.value.enabled }
    settings.value = await saveDanmakuSettings(next)
    if (next.enabled && lastMedia)
      await loadForMedia(lastMedia.name, lastMedia.duration)
    else
      comments.value = []
  }

  function reloadSettings(event: Event) {
    const detail = (event as CustomEvent<DanmakuSettings>).detail
    settings.value = detail ?? loadDanmakuSettings()
  }

  window.addEventListener(DANMAKU_SETTINGS_CHANGED_EVENT, reloadSettings)
  onBeforeUnmount(() => window.removeEventListener(DANMAKU_SETTINGS_CHANGED_EVENT, reloadSettings))

  return { settings, comments, matches, selectedEpisodeId, loading, error, loadForMedia, selectMatch, updateSettings, toggleEnabled }
}
