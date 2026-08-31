import type { RawFileRecord, RawPathHints } from './types'
import { extractMediaSearchTitles, extractRawPathHints } from './parser'
import { splitProviderPath } from './pathUtils'

export interface PathAwareMediaRecognition {
  readonly record: RawFileRecord
  readonly hints: RawPathHints
  readonly fileSegment: string
  readonly parentSegment?: string
  readonly grandparentSegment?: string
  readonly mergeOrder: readonly ['file', 'parent', 'grandparent']
  readonly parentIsSeason: boolean
  readonly fileIsEpisodeOnly: boolean
  readonly seriesTitle?: string
  readonly searchTitles: string[]
}

export function recognizePathAwareMedia(record: RawFileRecord): PathAwareMediaRecognition {
  const hints = extractRawPathHints(record)
  const segments = splitProviderPath(record.relativePath || record.fileName)
  const fileSegment = segments.at(-1) ?? record.fileName
  const parentSegment = segments.at(-2)
  const grandparentSegment = segments.at(-3)
  const fileIsEpisodeOnly = hints.episodeNumber != null && !hints.cleanFileTitle
  const titleSource = hints.seriesTitle ?? hints.title ?? hints.cleanFileTitle

  return {
    record,
    hints,
    fileSegment,
    parentSegment,
    grandparentSegment,
    mergeOrder: ['file', 'parent', 'grandparent'],
    parentIsSeason: hints.seasonFolder && parentSegment === hints.parentSegments.at(-1),
    fileIsEpisodeOnly,
    seriesTitle: hints.seriesTitle,
    searchTitles: titleSource ? extractMediaSearchTitles(titleSource) : [],
  }
}
