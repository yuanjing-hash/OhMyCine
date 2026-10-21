import type { DataSource, MediaItem } from '@/services/datasource/types'
import type { PlaybackQueueInput } from '@/services/playbackContext'
import { parseOnlineItemID } from '@/services/datasource/serverOnline'
import { createPlaybackQueue } from '@/services/playbackContext'

export async function resolveEpisodePlaybackQueue(source: DataSource, item: MediaItem): Promise<PlaybackQueueInput | undefined> {
  if (item.type !== 'episode')
    return undefined

  const detail = await source.getDetail(item.id).catch(() => null)
  const children = detail?.children?.filter(isPlayableEpisodeItem) ?? []
  let episodes = children.some(child => child.id === item.id) ? sortSeriesEpisodes(uniqueEpisodeVersions(children, item.id)) : []

  // Some providers return a work-shaped detail for an exact entry route. Only the
  // matching playable child can replace the episode's own numbering and identity.
  const exactDetail = detail?.type === 'episode' && detail.id === item.id
    ? detail
    : children.find(child => child.id === item.id)
  const current = { ...item, ...exactDetail, resumePosition: item.resumePosition ?? exactDetail?.resumePosition }
  const seriesId = current.seriesId
  if (seriesId) {
    const fullSeries = await listSeriesEpisodes(source, seriesId, current).catch(() => [])
    if (fullSeries.some(episode => episode.id === item.id))
      episodes = fullSeries
  }

  episodes = episodes.map(episode => ({
    ...episode,
    ...(episode.id === item.id ? current : {}),
    seriesId: (episode.id === item.id ? current.seriesId : undefined) ?? episode.seriesId ?? seriesId,
  }))

  const queue = createPlaybackQueue(episodes, item.id)
  if (!queue)
    return undefined

  return {
    ...queue,
    items: queue.items.map((episode, index) => index === queue.currentIndex
      ? {
          ...episode,
          resumePosition: item.resumePosition ?? episode.resumePosition,
          historyIdentity: item.historyIdentity ?? episode.historyIdentity,
        }
      : episode),
  }
}

export async function listSeriesEpisodes(source: DataSource, seriesId: string, preferred?: MediaItem): Promise<MediaItem[]> {
  const children = await source.list(seriesId)
  const seasons = children.filter(item => item.type === 'season' || item.type === 'folder')
  const seasonEpisodeGroups = await Promise.all(seasons.map(async season => (await source.list(season.id).catch(() => [])).filter(isPlayableEpisodeItem)))
  const allEpisodes = [...children.filter(isPlayableEpisodeItem), ...seasonEpisodeGroups.flat()]
    .map(episode => ({ ...episode, seriesId: episode.seriesId ?? seriesId }))
  const edition = preferred?.edition
  const selectedEdition = preferred
    ? allEpisodes.filter(episode => (episode.edition ?? '') === (edition ?? ''))
    : allEpisodes
  return sortSeriesEpisodes(uniqueEpisodeVersions(selectedEdition, preferred?.id))
}

function isPlayableEpisodeItem(item: MediaItem): boolean {
  return item.type === 'episode' || item.type === 'file' || item.type === 'movie'
}

function uniqueEpisodeVersions(items: readonly MediaItem[], preferredId?: string): MediaItem[] {
  const selected = new Map<string, MediaItem>()
  for (const item of items) {
    const online = parseOnlineItemID(item.id)
    const key = item.seasonNumber != null && item.episodeNumber != null
      ? ['episode', item.seasonNumber, item.episodeNumber].join(':')
      : online?.kind === 'version'
        ? ['online-segment', online.libraryId, online.workId, online.segmentId].join(':')
        : item.historyIdentity || item.id
    if (!selected.has(key) || item.id === preferredId)
      selected.set(key, item)
  }
  return [...selected.values()]
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
