import type {
  NormalizeRawFileRecordOptions,
  RawFileRecord,
  RawProviderScanItem,
  RawScanPreview,
  RawStructureDetectionResult,
  RawStructureDetectionScores,
} from './types'
import { normalizeTitleKey, parseRawMediaCandidates } from './parser'
import { recognizePathAwareMedia } from './pathRecognition'
import {
  getVideoFileExtension,
  isLikelySensitiveProviderPath,
  isPathWithinRoot,
  isVideoFileName,
  joinProviderPath,
  normalizeProviderPath,
  providerBasename,
  providerParentPath,
  relativeProviderPath,
} from './pathUtils'

const DEFAULT_MAX_STRUCTURE_SAMPLES = 160
const DEFAULT_MAX_REPORTED_SAMPLES = 12

interface DetectionAccumulator {
  titleYearFolder: number
  titleYearFile: number
  seasonFolder: number
  episodePattern: number
  chineseEpisodePattern: number
  categoryTitleSeasonHierarchy: number
  rootLevelVideos: number
}

export function normalizeRawFileRecord(
  item: RawProviderScanItem,
  options: NormalizeRawFileRecordOptions,
): RawFileRecord | null {
  if (isDirectoryItem(item))
    return null

  const rootPath = normalizeProviderPath(options.rootPath)
  const providerPath = resolveItemPath(item)
  if (!providerPath || !isPathWithinRoot(providerPath, rootPath))
    return null

  const fileName = resolveFileName(item, providerPath)
  if (!fileName || !isVideoFileName(fileName))
    return null

  const extension = getVideoFileExtension(fileName)
  if (!extension)
    return null

  return {
    id: `${options.sourceId}:${providerPath}`,
    sourceId: options.sourceId,
    sourceType: options.sourceType,
    rootPath,
    providerPath,
    relativePath: relativeProviderPath(providerPath, rootPath),
    parentPath: providerParentPath(providerPath),
    fileName,
    extension,
    size: numberValue(item.size),
    modifiedAt: stringValue(item.modifiedAt) ?? stringValue(item.modified) ?? stringValue(item.createdAt) ?? stringValue(item.created),
  }
}

export function normalizeRawFileRecords(
  items: readonly RawProviderScanItem[],
  options: NormalizeRawFileRecordOptions,
): RawFileRecord[] {
  return items
    .map(item => normalizeRawFileRecord(item, options))
    .filter((record): record is RawFileRecord => record != null)
}

export function createRawScanPreview(
  items: readonly RawProviderScanItem[],
  options: NormalizeRawFileRecordOptions,
): RawScanPreview {
  const records = normalizeRawFileRecords(items, options)
  const detection = detectRawFileStructure(records)
  return {
    records,
    detection,
    candidates: parseRawMediaCandidates(records, detection),
  }
}

export function detectRawFileStructure(
  records: readonly RawFileRecord[],
  options: { maxSamples?: number, maxReportedSamples?: number } = {},
): RawStructureDetectionResult {
  const maxSamples = options.maxSamples ?? DEFAULT_MAX_STRUCTURE_SAMPLES
  const maxReportedSamples = options.maxReportedSamples ?? DEFAULT_MAX_REPORTED_SAMPLES
  const samples = records.slice(0, maxSamples)
  const accumulator: DetectionAccumulator = {
    titleYearFolder: 0,
    titleYearFile: 0,
    seasonFolder: 0,
    episodePattern: 0,
    chineseEpisodePattern: 0,
    categoryTitleSeasonHierarchy: 0,
    rootLevelVideos: 0,
  }
  const folderStats = new Map<string, { count: number, titles: Set<string>, strongSignals: number }>()
  const seriesEpisodeStats = new Map<string, number>()

  for (const record of samples) {
    const { hints } = recognizePathAwareMedia(record)
    if (hints.titleYearFolder)
      accumulator.titleYearFolder += 1
    if (hints.titleYearFile)
      accumulator.titleYearFile += 1
    if (hints.seasonFolder)
      accumulator.seasonFolder += 1
    if (hints.episodePattern)
      accumulator.episodePattern += 1
    if (hints.chineseEpisodePattern)
      accumulator.chineseEpisodePattern += 1
    if (hints.categoryTitleSeasonHierarchy)
      accumulator.categoryTitleSeasonHierarchy += 1
    if (hints.depth <= 1)
      accumulator.rootLevelVideos += 1

    const titleKey = normalizeTitleKey(hints.seriesTitle ?? hints.title ?? hints.cleanFileTitle)
    const folder = record.parentPath
    const current = folderStats.get(folder) ?? { count: 0, titles: new Set<string>(), strongSignals: 0 }
    current.count += 1
    if (titleKey)
      current.titles.add(titleKey)
    if (hints.titleYearFolder || hints.seasonFolder || hints.episodePattern)
      current.strongSignals += 1
    folderStats.set(folder, current)

    if (hints.seriesTitle && hints.episodeNumber != null) {
      const seriesKey = `${hints.categoryHint ?? ''}/${normalizeTitleKey(hints.seriesTitle)}`
      seriesEpisodeStats.set(seriesKey, (seriesEpisodeStats.get(seriesKey) ?? 0) + 1)
    }
  }

  const mixedFolderAmbiguity = [...folderStats.values()]
    .filter(stats => stats.count >= 4 && stats.strongSignals === 0 && stats.titles.size / stats.count >= 0.75)
    .length
  const sameSeriesEpisodeGroups = [...seriesEpisodeStats.values()].filter(count => count >= 2).length
  const scores = createDetectionScores(records.length, samples.length, accumulator, mixedFolderAmbiguity, sameSeriesEpisodeGroups)
  const mode = scores.standardScore >= scores.nonStandardScore + 0.08 ? 'standard' : 'nonStandard'
  const confidence = confidenceForScores(scores.standardScore, scores.nonStandardScore)

  return {
    mode,
    confidence,
    reasons: detectionReasons(scores, mode),
    samplePaths: samples.slice(0, maxReportedSamples).map(record => record.providerPath),
    scores,
  }
}

function createDetectionScores(
  videoCount: number,
  sampledCount: number,
  accumulator: DetectionAccumulator,
  mixedFolderAmbiguity: number,
  sameSeriesEpisodeGroups: number,
): RawStructureDetectionScores {
  if (sampledCount === 0) {
    return {
      videoCount,
      sampledCount: 0,
      ...accumulator,
      sameSeriesEpisodeGroups: 0,
      mixedFolderAmbiguity: 0,
      standardScore: 0,
      nonStandardScore: 1,
    }
  }

  const standardSignalWeight = accumulator.titleYearFolder * 1.4
    + accumulator.seasonFolder * 1.25
    + accumulator.episodePattern * 1.2
    + accumulator.chineseEpisodePattern * 0.9
    + accumulator.categoryTitleSeasonHierarchy * 1.1
    + sameSeriesEpisodeGroups * 1.3
    + accumulator.titleYearFile * 0.45
  const nonStandardSignalWeight = accumulator.rootLevelVideos * 0.85
    + mixedFolderAmbiguity * 1.45
    + Math.max(0, sampledCount - strongSignalCount(accumulator)) * 0.18

  const standardScore = clamp(standardSignalWeight / Math.max(1, sampledCount * 2.2), 0, 1)
  const nonStandardScore = clamp(nonStandardSignalWeight / Math.max(1, sampledCount * 1.35), 0, 1)

  return {
    videoCount,
    sampledCount,
    ...accumulator,
    sameSeriesEpisodeGroups,
    mixedFolderAmbiguity,
    standardScore,
    nonStandardScore,
  }
}

function strongSignalCount(accumulator: DetectionAccumulator): number {
  return accumulator.titleYearFolder
    + accumulator.seasonFolder
    + accumulator.episodePattern
    + accumulator.categoryTitleSeasonHierarchy
}

function confidenceForScores(standardScore: number, nonStandardScore: number): number {
  const gap = Math.abs(standardScore - nonStandardScore)
  const dominant = Math.max(standardScore, nonStandardScore)
  return clamp(0.45 + gap * 0.75 + dominant * 0.25, 0.35, 0.96)
}

function detectionReasons(scores: RawStructureDetectionScores, mode: 'standard' | 'nonStandard'): string[] {
  if (scores.sampledCount === 0)
    return ['没有可参与判断的视频文件样本。']

  const reasons: string[] = []
  if (scores.titleYearFolder > 0)
    reasons.push(`检测到 ${scores.titleYearFolder} 个片名年份目录信号。`)
  if (scores.seasonFolder > 0)
    reasons.push(`检测到 ${scores.seasonFolder} 个 Season/季目录信号。`)
  if (scores.episodePattern > 0)
    reasons.push(`检测到 ${scores.episodePattern} 个季集文件名信号。`)
  if (scores.chineseEpisodePattern > 0)
    reasons.push(`检测到 ${scores.chineseEpisodePattern} 个中文第 N 集信号。`)
  if (scores.categoryTitleSeasonHierarchy > 0)
    reasons.push(`检测到 ${scores.categoryTitleSeasonHierarchy} 个分类/标题/季集层级信号。`)
  if (scores.sameSeriesEpisodeGroups > 0)
    reasons.push(`检测到 ${scores.sameSeriesEpisodeGroups} 个同剧多集聚合信号。`)
  if (scores.rootLevelVideos > 0)
    reasons.push(`样本中有 ${scores.rootLevelVideos} 个根目录散文件。`)
  if (scores.mixedFolderAmbiguity > 0)
    reasons.push(`检测到 ${scores.mixedFolderAmbiguity} 个混合多标题目录。`)

  reasons.push(mode === 'standard'
    ? '标准目录信号强于混杂信号，建议按路径结构优先解析。'
    : '混杂或散文件信号更强，建议按非标准散文件解析。')
  return reasons
}

function resolveItemPath(item: RawProviderScanItem): string | null {
  const directPath = stringValue(item.providerPath) ?? stringValue(item.path)
  if (directPath) {
    if (isLikelySensitiveProviderPath(directPath))
      return null

    try {
      return normalizeProviderPath(directPath)
    }
    catch {
      return null
    }
  }

  const parentPath = stringValue(item.parentPath)
  const name = stringValue(item.name)
  if (!parentPath || !name)
    return null
  if (!isSafeProviderFileName(name))
    return null

  try {
    return joinProviderPath(parentPath, name)
  }
  catch {
    return null
  }
}

function resolveFileName(item: RawProviderScanItem, providerPath: string): string | null {
  const name = stringValue(item.name)
  if (name && isSafeProviderFileName(name))
    return name
  return providerBasename(providerPath) ?? null
}

function isSafeProviderFileName(value: string): boolean {
  const normalized = value.trim()
  if (!normalized || normalized.includes('/') || normalized.includes('\\'))
    return false

  try {
    normalizeProviderPath(normalized)
    return true
  }
  catch {
    return false
  }
}

function isDirectoryItem(item: RawProviderScanItem): boolean {
  if (item.isDirectory === true || item.isDir === true)
    return true
  return item.type === 'folder' || item.type === 'directory' || item.type === 'dir'
}

function stringValue(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}

function numberValue(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}
