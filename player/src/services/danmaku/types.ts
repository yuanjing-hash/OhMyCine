export type DanmakuMode = 'scroll' | 'bottom' | 'top'
export type DanmakuProvider = 'official' | 'custom'

export interface DanmakuComment {
  id: string
  time: number
  mode: DanmakuMode
  color: string
  text: string
}

export interface DanmakuMatch {
  episodeId: number
  animeId: number
  animeTitle: string
  episodeTitle: string
  shift: number
}

export interface DanmakuMatchResponse {
  exact: boolean
  matches: DanmakuMatch[]
}

export interface DanmakuSearchAnime {
  animeId: number
  animeTitle: string
  typeDescription: string
  episodes: DanmakuSearchEpisode[]
}

export interface DanmakuSearchEpisode {
  episodeId: number
  episodeTitle: string
}

export interface DanmakuSearchResponse {
  hasMore: boolean
  animes: DanmakuSearchAnime[]
}

export interface DanmakuSettings {
  enabled: boolean
  provider: DanmakuProvider
  customBaseUrl: string
  opacity: number
  fontScale: number
  speed: number
  displayArea: number
  density: number
  showScroll: boolean
  showTop: boolean
  showBottom: boolean
  bold: boolean
  blockKeywords: string[]
}
