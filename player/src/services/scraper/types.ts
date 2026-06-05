import type { ScrapeMediaType } from './classificationRules'
import type { TmdbEpisodeMetadata, TmdbMetadata } from './tmdb'
import type { DataSourceType } from '@/services/datasource/types'

export type RawFileSourceType = Extract<DataSourceType, 'alist' | 'clouddrive2' | 'local' | '115' | '123' | 'quark'>
export type RawScanModePreference = 'auto' | 'standard' | 'nonStandard'
export type RawFileStructureMode = Exclude<RawScanModePreference, 'auto'>
export type RawMediaCandidateKind = 'movie' | 'tv' | 'episode' | 'unresolved'
export type RawParseStatus = 'parsed' | 'partial' | 'unresolved'
export type RawTmdbMatchStatus = 'matched' | 'notConfigured' | 'notFound' | 'failed' | 'skipped'

export interface RawProviderScanItem {
  readonly name?: string
  readonly path?: string
  readonly providerPath?: string
  readonly parentPath?: string
  readonly isDirectory?: boolean
  readonly isDir?: boolean
  readonly type?: 'file' | 'folder' | 'directory' | 'dir'
  readonly size?: number
  readonly modifiedAt?: string
  readonly modified?: string
  readonly createdAt?: string
  readonly created?: string
}

export interface NormalizeRawFileRecordOptions {
  readonly sourceId: string
  readonly sourceType: RawFileSourceType
  readonly rootPath?: string
}

export interface RawFileRecord {
  readonly id: string
  readonly sourceId: string
  readonly sourceType: RawFileSourceType
  readonly rootPath: string
  readonly providerPath: string
  readonly relativePath: string
  readonly parentPath: string
  readonly fileName: string
  readonly extension: string
  readonly size?: number
  readonly modifiedAt?: string
}

export interface RawPathHints {
  readonly segments: string[]
  readonly parentSegments: string[]
  readonly depth: number
  readonly fileStem: string
  readonly cleanFileTitle: string
  readonly title?: string
  readonly seriesTitle?: string
  readonly categoryHint?: string
  readonly year?: number
  readonly seasonNumber?: number
  readonly episodeNumber?: number
  readonly titleYearFolder: boolean
  readonly titleYearFile: boolean
  readonly seasonFolder: boolean
  readonly episodePattern: boolean
  readonly chineseEpisodePattern: boolean
  readonly categoryTitleSeasonHierarchy: boolean
  readonly signals: string[]
}

export interface RawStructureDetectionScores {
  readonly videoCount: number
  readonly sampledCount: number
  readonly titleYearFolder: number
  readonly titleYearFile: number
  readonly seasonFolder: number
  readonly episodePattern: number
  readonly chineseEpisodePattern: number
  readonly categoryTitleSeasonHierarchy: number
  readonly sameSeriesEpisodeGroups: number
  readonly rootLevelVideos: number
  readonly mixedFolderAmbiguity: number
  readonly standardScore: number
  readonly nonStandardScore: number
}

export interface RawStructureDetectionResult {
  readonly mode: RawFileStructureMode
  readonly confidence: number
  readonly reasons: string[]
  readonly samplePaths: string[]
  readonly scores: RawStructureDetectionScores
}

export interface RawMediaCandidate {
  readonly kind: RawMediaCandidateKind
  readonly parseStatus: RawParseStatus
  readonly record: RawFileRecord
  readonly title: string
  readonly normalizedTitle: string
  readonly year?: number
  readonly seriesTitle?: string
  readonly seasonNumber?: number
  readonly episodeNumber?: number
  readonly categoryHint?: string
  readonly scrapeMetadata?: TmdbMetadata
  readonly categoryAssignment?: RawCategoryAssignment
  readonly confidence: number
  readonly signals: string[]
}

export type RawCategoryAssignmentSource = 'metadataRule' | 'pathHint' | 'kindFallback'

export interface RawCategoryAssignment {
  readonly categoryName: string
  readonly source: RawCategoryAssignmentSource
  readonly matchedRuleId?: string
  readonly matchedRuleName?: string
}

export interface RawScrapedMediaItem {
  readonly recordId: string
  readonly providerPath: string
  readonly matchStatus: RawTmdbMatchStatus
  readonly searchTitles: string[]
  readonly matchedSearchTitle?: string
  readonly metadata?: TmdbMetadata
  readonly episodeMetadata?: TmdbEpisodeMetadata
  readonly mediaType?: ScrapeMediaType
  readonly categoryName: string
  readonly matchedRuleId?: string
  readonly matchedRuleName?: string
  readonly categoryAssignment?: RawCategoryAssignment
  readonly errorMessage?: string
}

export interface RawScanPreview {
  readonly records: RawFileRecord[]
  readonly detection: RawStructureDetectionResult
  readonly candidates: RawMediaCandidate[]
}
