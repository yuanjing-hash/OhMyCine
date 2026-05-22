import type { RawFileRecord, RawMediaCandidate, RawPathHints, RawStructureDetectionResult } from './types'
import { splitProviderPath, stripFileExtension } from './pathUtils'

interface TitleYearMatch {
  readonly title: string
  readonly year: number
}

interface EpisodeMatch {
  readonly seasonNumber?: number
  readonly episodeNumber: number
  readonly chinese: boolean
}

const YEAR_PATTERN = '(?:18|19|20|21)\\d{2}'
const BRACKETED_YEAR_TOKEN_RE = new RegExp(`[([【](${YEAR_PATTERN})[)\\]】]`)
const LOOSE_YEAR_TOKEN_RE = new RegExp(`(?:^|[\\s._-])(${YEAR_PATTERN})(?=$|[\\s._-])`)
const SXXEXX_RE = /\bS\s*0*(\d{1,2})\s*E\s*0*(\d{1,3})\b/i
const ONE_X_EPISODE_RE = /\b0*(\d{1,2})x0*(\d{1,3})\b/i
const CHINESE_EPISODE_RE = /第\s*([0-9一二三四五六七八九十百两〇零]+)\s*[集话話]/
const EPISODE_ONLY_RE = /\b(?:EP?|Episode)\s*0*(\d{1,3})\b/i
const SEASON_SEGMENT_RE = /^Season\s*0*(\d{1,2})$/i
const S_SEASON_SEGMENT_RE = /^S\s*0*(\d{1,2})$/i
const CHINESE_SEASON_RE = /第\s*([0-9一二三四五六七八九十百两〇零]+)\s*季/
const TECH_TOKEN_RE = /\b(?:2160p|1080p|720p|576p|480p|UHD|BluRay|BDRip|WEB[- .]?DL|WEBRip|HDTV|DVDRip|REMUX|x264|x265|H\.?264|H\.?265|HEVC|AV1|AAC|DTS(?:-HD)?|TrueHD|Atmos|DDP?5(?:\.1)?|HDR10?|DoVi|DV|10bit|8bit|Proper|Repack)\b/gi

export function parseRawMediaCandidates(
  records: readonly RawFileRecord[],
  detection?: RawStructureDetectionResult,
): RawMediaCandidate[] {
  return records.map(record => parseRawMediaCandidate(record, detection))
}

export function parseRawMediaCandidate(
  record: RawFileRecord,
  detection?: RawStructureDetectionResult,
): RawMediaCandidate {
  const hints = extractRawPathHints(record)
  const normalizedTitle = normalizeTitleKey(hints.title ?? hints.cleanFileTitle)
  const standardMode = detection?.mode === 'standard'

  if (hints.episodeNumber != null) {
    const title = hints.seriesTitle ?? hints.title ?? hints.cleanFileTitle
    return {
      kind: 'episode',
      parseStatus: 'parsed',
      record,
      title,
      normalizedTitle: normalizeTitleKey(title),
      year: hints.year,
      seriesTitle: hints.seriesTitle ?? title,
      seasonNumber: hints.seasonNumber,
      episodeNumber: hints.episodeNumber,
      categoryHint: hints.categoryHint,
      confidence: confidenceFromSignals(hints.signals, 0.78),
      signals: hints.signals,
    }
  }

  if (hints.seasonFolder && hints.seriesTitle) {
    return {
      kind: 'tv',
      parseStatus: 'partial',
      record,
      title: hints.seriesTitle,
      normalizedTitle: normalizeTitleKey(hints.seriesTitle),
      year: hints.year,
      seriesTitle: hints.seriesTitle,
      seasonNumber: hints.seasonNumber,
      categoryHint: hints.categoryHint,
      confidence: confidenceFromSignals(hints.signals, 0.56),
      signals: hints.signals,
    }
  }

  if (hints.titleYearFolder || hints.titleYearFile || (standardMode && hints.depth >= 2 && normalizedTitle)) {
    return {
      kind: 'movie',
      parseStatus: hints.titleYearFolder || hints.titleYearFile ? 'parsed' : 'partial',
      record,
      title: hints.title ?? hints.cleanFileTitle,
      normalizedTitle,
      year: hints.year,
      categoryHint: hints.categoryHint,
      confidence: confidenceFromSignals(hints.signals, hints.year ? 0.72 : 0.52),
      signals: hints.signals,
    }
  }

  return {
    kind: 'unresolved',
    parseStatus: 'unresolved',
    record,
    title: hints.cleanFileTitle || stripFileExtension(record.fileName),
    normalizedTitle,
    year: hints.year,
    categoryHint: hints.categoryHint,
    confidence: 0.2,
    signals: hints.signals,
  }
}

export function extractRawPathHints(record: RawFileRecord): RawPathHints {
  const segments = splitProviderPath(record.relativePath || record.fileName)
  const fileName = segments.at(-1) ?? record.fileName
  const parentSegments = segments.slice(0, -1)
  const fileStem = stripFileExtension(fileName)
  const cleanFileTitle = cleanMediaTitle(fileStem)
  const titleYearFile = parseTitleYear(fileStem)
  const titleYearFolder = findLastTitleYearSegment(parentSegments)
  const episode = parseEpisode(fileStem)
  const seasonFolder = findLastSeasonFolder(parentSegments)
  const seasonNumber = episode?.seasonNumber ?? seasonFolder?.seasonNumber
  const episodeNumber = episode?.episodeNumber
  const seriesIndex = seasonFolder ? seasonFolder.index - 1 : parentSegments.length - 1
  const seriesTitle = episodeNumber != null
    ? cleanMediaTitle(parentSegments[seriesIndex] ?? titleBeforeEpisode(fileStem))
    : seasonFolder
      ? cleanMediaTitle(parentSegments[seriesIndex] ?? '')
      : undefined
  const titleMatch = titleYearFolder?.match ?? titleYearFile
  const title = episodeNumber != null
    ? seriesTitle
    : titleMatch?.title ?? folderTitleFallback(parentSegments, cleanFileTitle)
  const categoryHint = inferCategoryHint(parentSegments, {
    titleFolderIndex: titleYearFolder?.index,
    seasonFolderIndex: seasonFolder?.index,
    seriesIndex,
  })
  const signals = collectSignals({
    titleYearFolder: titleYearFolder != null,
    titleYearFile: titleYearFile != null,
    seasonFolder: seasonFolder != null,
    episodePattern: episode != null,
    chineseEpisodePattern: episode?.chinese === true,
    categoryTitleSeasonHierarchy: categoryHint != null && (titleYearFolder != null || seasonFolder != null),
  })

  return {
    segments,
    parentSegments,
    depth: segments.length,
    fileStem,
    cleanFileTitle,
    title: title || cleanFileTitle,
    seriesTitle: seriesTitle || undefined,
    categoryHint,
    year: titleMatch?.year,
    seasonNumber,
    episodeNumber,
    titleYearFolder: titleYearFolder != null,
    titleYearFile: titleYearFile != null,
    seasonFolder: seasonFolder != null,
    episodePattern: episode != null,
    chineseEpisodePattern: episode?.chinese === true,
    categoryTitleSeasonHierarchy: categoryHint != null && (titleYearFolder != null || seasonFolder != null),
    signals,
  }
}

export function cleanMediaTitle(value: string): string {
  return value
    .replace(BRACKETED_YEAR_TOKEN_RE, ' ')
    .replace(LOOSE_YEAR_TOKEN_RE, ' ')
    .replace(SXXEXX_RE, ' ')
    .replace(ONE_X_EPISODE_RE, ' ')
    .replace(CHINESE_EPISODE_RE, ' ')
    .replace(EPISODE_ONLY_RE, ' ')
    .replace(/\[[^\]]+\]|\([^)]*\)|【[^】]+】/g, ' ')
    .replace(TECH_TOKEN_RE, ' ')
    .replace(/[._]+/g, ' ')
    .replace(/\s+-\s+[\da-z]+$/i, ' ')
    .replace(/[-—]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}

export function normalizeTitleKey(value: string): string {
  return cleanMediaTitle(value).toLocaleLowerCase()
}

function parseTitleYear(value: string): TitleYearMatch | null {
  const bracketed = BRACKETED_YEAR_TOKEN_RE.exec(value)
  if (bracketed)
    return createTitleYearMatch(value.slice(0, bracketed.index), bracketed[1])

  const loose = LOOSE_YEAR_TOKEN_RE.exec(value)
  if (loose)
    return createTitleYearMatch(value.slice(0, loose.index), loose[1])

  return null
}

function createTitleYearMatch(rawTitle: string, rawYear: string): TitleYearMatch | null {
  const title = cleanMediaTitle(rawTitle)
  const year = Number(rawYear)
  if (!title || !Number.isInteger(year))
    return null
  return { title, year }
}

function parseEpisode(value: string): EpisodeMatch | null {
  const sxxexx = SXXEXX_RE.exec(value)
  if (sxxexx) {
    return {
      seasonNumber: Number(sxxexx[1]),
      episodeNumber: Number(sxxexx[2]),
      chinese: false,
    }
  }

  const oneX = ONE_X_EPISODE_RE.exec(value)
  if (oneX) {
    return {
      seasonNumber: Number(oneX[1]),
      episodeNumber: Number(oneX[2]),
      chinese: false,
    }
  }

  const chinese = CHINESE_EPISODE_RE.exec(value)
  if (chinese) {
    const episodeNumber = parseNumberText(chinese[1])
    return episodeNumber == null ? null : { episodeNumber, chinese: true }
  }

  const episodeOnly = EPISODE_ONLY_RE.exec(value)
  if (episodeOnly)
    return { episodeNumber: Number(episodeOnly[1]), chinese: false }

  return null
}

function parseSeasonFolder(value: string): number | null {
  const season = SEASON_SEGMENT_RE.exec(value) ?? S_SEASON_SEGMENT_RE.exec(value)
  if (season)
    return Number(season[1])

  const chinese = CHINESE_SEASON_RE.exec(value)
  return chinese ? parseNumberText(chinese[1]) : null
}

function parseNumberText(value: string): number | null {
  const normalized = value.trim()
  if (/^\d+$/.test(normalized))
    return Number(normalized)

  const digits: Record<string, number> = {
    零: 0,
    〇: 0,
    一: 1,
    二: 2,
    两: 2,
    三: 3,
    四: 4,
    五: 5,
    六: 6,
    七: 7,
    八: 8,
    九: 9,
  }

  if (normalized === '十')
    return 10
  if (normalized.includes('十')) {
    const [tensRaw, onesRaw = ''] = normalized.split('十')
    const tens = tensRaw ? digits[tensRaw] : 1
    const ones = onesRaw ? digits[onesRaw] : 0
    return tens == null || ones == null ? null : tens * 10 + ones
  }

  return digits[normalized] ?? null
}

function findLastTitleYearSegment(segments: readonly string[]): { index: number, match: TitleYearMatch } | null {
  for (let index = segments.length - 1; index >= 0; index -= 1) {
    const match = parseTitleYear(segments[index])
    if (match)
      return { index, match }
  }
  return null
}

function findLastSeasonFolder(segments: readonly string[]): { index: number, seasonNumber: number } | null {
  for (let index = segments.length - 1; index >= 0; index -= 1) {
    const seasonNumber = parseSeasonFolder(segments[index])
    if (seasonNumber != null)
      return { index, seasonNumber }
  }
  return null
}

function titleBeforeEpisode(value: string): string {
  return cleanMediaTitle(value
    .replace(SXXEXX_RE, ' ')
    .replace(ONE_X_EPISODE_RE, ' ')
    .replace(CHINESE_EPISODE_RE, ' ')
    .replace(EPISODE_ONLY_RE, ' '))
}

function folderTitleFallback(parentSegments: readonly string[], cleanFileTitle: string): string | undefined {
  const parent = parentSegments.at(-1)
  if (!parent)
    return cleanFileTitle || undefined

  const cleanParent = cleanMediaTitle(parent)
  if (cleanParent && cleanFileTitle && cleanFileTitle.includes(cleanParent))
    return cleanParent
  return cleanFileTitle || cleanParent || undefined
}

function inferCategoryHint(
  parentSegments: readonly string[],
  indexes: { titleFolderIndex?: number, seasonFolderIndex?: number, seriesIndex?: number },
): string | undefined {
  const hintIndex = indexes.titleFolderIndex != null
    ? indexes.titleFolderIndex - 1
    : indexes.seasonFolderIndex != null && indexes.seriesIndex != null
      ? indexes.seriesIndex - 1
      : parentSegments.length > 1
        ? parentSegments.length - 2
        : undefined

  if (hintIndex == null || hintIndex < 0)
    return undefined

  const hint = cleanMediaTitle(parentSegments[hintIndex])
  return hint || undefined
}

function collectSignals(input: {
  titleYearFolder: boolean
  titleYearFile: boolean
  seasonFolder: boolean
  episodePattern: boolean
  chineseEpisodePattern: boolean
  categoryTitleSeasonHierarchy: boolean
}): string[] {
  const signals: string[] = []
  if (input.titleYearFolder)
    signals.push('title-year-folder')
  if (input.titleYearFile)
    signals.push('title-year-file')
  if (input.seasonFolder)
    signals.push('season-folder')
  if (input.episodePattern)
    signals.push('episode-pattern')
  if (input.chineseEpisodePattern)
    signals.push('chinese-episode')
  if (input.categoryTitleSeasonHierarchy)
    signals.push('category-title-season-hierarchy')
  return signals
}

function confidenceFromSignals(signals: readonly string[], base: number): number {
  return clamp(base + Math.min(signals.length * 0.06, 0.18), 0.1, 0.96)
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}
