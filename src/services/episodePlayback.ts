import type { DataSource, MediaItem } from '@/services/datasource/types'
import type { PlaybackQueueInput } from '@/services/playbackContext'
import { createPlaybackQueue } from '@/services/playbackContext'

export async function resolveEpisodePlaybackQueue(source: DataSource, item: MediaItem): Promise<PlaybackQueueInput | undefined> {
  if (item.type !== 'episode')
    return undefined

  const detail = await source.getDetail(item.id).catch(() => null)
  const children = detail?.children?.filter(isPlayableEpisodeItem) ?? []
  let episodes = children.some(child => child.id === item.id) ? sortSeriesEpisodes(uniqueEpisodeVersions(children, item.id)) : []

  const seriesId = detail?.seriesId || item.seriesId
  if (episodes.length === 0 && seriesId)
    episodes = await listSeriesEpisodes(source, seriesId).catch(() => [])

  if (episodes.length === 0) {
    const seriesName = detail?.seriesName || item.seriesName || item.name
    const candidates = await source.search(seriesName).catch(() => [])
    for (const series of candidates.filter(candidate => candidate.type === 'series'
      && (!item.libraryId || !candidate.libraryId || candidate.libraryId === item.libraryId)
      && candidate.name.trim().toLocaleLowerCase() === seriesName.trim().toLocaleLowerCase()).slice(0, 5)) {
      const matches = await listSeriesEpisodes(source, series.id).catch(() => [])
      if (matches.some(episode => episode.id === item.id)) {
        episodes = matches
        break
      }
    }
  }

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

export async function listSeriesEpisodes(source: DataSource, seriesId: string): Promise<MediaItem[]> {
  const children = await source.list(seriesId)
  const directEpisodes = sortSeriesEpisodes(uniqueEpisodeVersions(children.filter(isPlayableEpisodeItem)))
  if (directEpisodes.length > 0)
    return directEpisodes

  const seasons = children.filter(item => item.type === 'season' || item.type === 'folder')
  const seasonEpisodeGroups = await Promise.all(seasons.map(async season => (await source.list(season.id)).filter(isPlayableEpisodeItem)))
  return sortSeriesEpisodes(uniqueEpisodeVersions(seasonEpisodeGroups.flat()))
}

function isPlayableEpisodeItem(item: MediaItem): boolean {
  return item.type === 'episode' || item.type === 'file' || item.type === 'movie'
}

function uniqueEpisodeVersions(items: readonly MediaItem[], preferredId?: string): MediaItem[] {
  const selected = new Map<string, MediaItem>()
  for (const item of items) {
    const key = item.historyIdentity
      || (item.seasonNumber != null && item.episodeNumber != null
        ? ['episode', item.seasonNumber, item.episodeNumber].join(':')
        : item.id)
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
