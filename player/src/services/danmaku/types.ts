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

export interface DanmakuStatus {
  loading: boolean
  error: string | null
  commentCount: number
  matches: readonly DanmakuMatch[]
  selectedEpisodeId: number | null
}
