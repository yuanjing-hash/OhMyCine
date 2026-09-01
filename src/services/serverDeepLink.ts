import type { UnlistenFn } from '@tauri-apps/api/event'
import type { Router } from 'vue-router'
import type { useDataSourceStore } from '@/stores/datasource'
import { invoke } from '@tauri-apps/api/core'
import { listen } from '@tauri-apps/api/event'
import { normalizeServerBaseUrl, ServerDataSource } from '@/services/datasource/server'
import { publishFeedback } from '@/services/mediaActions'
import { createPlaybackQueue, savePlaybackMediaContext } from '@/services/playbackContext'
import { createPlaybackRouteQuery } from '@/services/playbackRoute'
import { isTauriRuntime } from '@/services/runtimePlatform'

type DataSourceStore = ReturnType<typeof useDataSourceStore>

let stopListening: UnlistenFn | undefined
let lastHandled = ''
let lastHandledAt = 0

export async function initializeServerDeepLinks(router: Router, store: DataSourceStore): Promise<() => void> {
  if (!isTauriRuntime())
    return () => undefined
  stopListening?.()
  stopListening = await listen<string[]>('ohmycine-deep-link', event => void handleMany(event.payload, router, store))
  const pending = await invoke<string[]>('player_take_pending_deep_links').catch(() => [])
  await handleMany(pending, router, store)
  return () => {
    stopListening?.()
    stopListening = undefined
  }
}

async function handleMany(values: unknown, router: Router, store: DataSourceStore) {
  if (!Array.isArray(values))
    return
  for (const value of values.slice(-4)) {
    if (typeof value === 'string')
      await openServerMedia(value, router, store)
  }
}

async function openServerMedia(raw: string, router: Router, store: DataSourceStore) {
  const now = Date.now()
  if (raw === lastHandled && now - lastHandledAt < 2_000)
    return
  lastHandled = raw
  lastHandledAt = now

  try {
    const target = parseServerMediaDeepLink(raw)
    store.loadConfigs()
    await store.syncManager()
    const config = store.orderedConfigs.find((candidate) => {
      if (candidate.type !== 'server' || candidate.enabled === false)
        return false
      try {
        return normalizeServerBaseUrl(candidate.url) === target.server
      }
      catch {
        return false
      }
    })
    if (!config) {
      await router.push('/settings')
      throw new Error('Player 尚未连接这个 Server，已为你打开设置。请先添加并登录对应 Server。')
    }
    const source = store.getSource(config.id)
    if (!(source instanceof ServerDataSource)) {
      await router.push('/settings')
      throw new Error('Player 尚未建立可用的 Server 连接，已为你打开设置。请先重新连接。')
    }
    try {
      if (!await source.test())
        throw new Error('Server 鉴权未通过。')
    }
    catch {
      await router.push('/settings')
      throw new Error('这个 Server 连接或凭据已失效，已为你打开设置。请重新登录后再播放。')
    }

    const itemId = `work|${target.library}|${target.work}`
    const detail = await source.getDetail(itemId)
    if (target.autoplay && detail.type === 'movie') {
      const mediaSource = detail.mediaSources?.[0]
      if (!mediaSource)
        throw new Error('该作品当前没有可播放版本。')
      const contextId = savePlaybackMediaContext({ sourceId: config.id, itemId, title: detail.name, currentItem: detail, queue: createPlaybackQueue([detail], detail.id) })
      await router.push({ name: 'player', query: createPlaybackRouteQuery({ sourceId: config.id, itemId, mediaSourceId: mediaSource.id, contextId }) })
      publishFeedback({ id: Date.now(), kind: 'success', message: `已通过 ${config.displayName ?? config.name} 打开《${detail.name}》` })
      return
    }
    await router.push({ name: 'media-detail', params: { sourceId: config.id, itemId } })
    publishFeedback({ id: Date.now(), kind: 'success', message: `已通过 ${config.displayName ?? config.name} 打开《${detail.name}》` })
  }
  catch (error) {
    publishFeedback({ id: Date.now(), kind: 'error', message: error instanceof Error ? error.message : '无法使用 Player 打开这个 Server 媒体。' })
  }
}

interface ServerMediaDeepLink {
  server: string
  library: string
  work: string
  autoplay: boolean
}

export function parseServerMediaDeepLink(raw: string): ServerMediaDeepLink {
  if (raw.length > 4_096)
    throw new Error('Player 打开链接无效。')
  const url = new URL(raw)
  if (url.protocol !== 'ohmycine:' || url.hostname !== 'open' || url.username || url.password || (url.pathname !== '' && url.pathname !== '/') || url.hash)
    throw new Error('Player 打开链接无效。')
  const allowed = new Set(['server', 'library', 'work', 'autoplay'])
  if ([...url.searchParams.keys()].some(key => !allowed.has(key)))
    throw new Error('Player 打开链接包含未知参数。')
  const server = normalizeServerBaseUrl(url.searchParams.get('server') ?? '')
  const library = (url.searchParams.get('library') ?? '').trim()
  const work = (url.searchParams.get('work') ?? '').trim()
  if (!/^\d{1,10}$/.test(library) || !work || work.length > 512 || /[\r\n]/.test(work))
    throw new Error('Player 打开链接中的媒体身份无效。')
  return { server, library, work, autoplay: url.searchParams.get('autoplay') === '1' }
}
