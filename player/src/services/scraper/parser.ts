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
const SEASON_SEGMENT_RE = /^(?:Season|Seanson)\s*0*(\d{1,2})$/i
const S_SEASON_SEGMENT_RE = /^S\s*0*(\d{1,2})$/i
const CHINESE_SEASON_RE = /第\s*([0-9一二三四五六七八九十百两〇零]+)\s*季/
const TECH_TOKEN_RE = /\b(?:2160p|1080p|720p|576p|480p|UHD|BluRay|BDRip|WEB[- .]?DL|WEBRip|HDTV|DVDRip|REMUX|x264|x265|H\.?264|H\.?265|HEVC|AV1|AAC|DTS(?:-HD)?|TrueHD|Atmos|DDP?5(?:\.1)?|HDR10?|DoVi|DV|10bit|8bit|Proper|Repack)\b/gi
const SOURCE_TOKEN_RE = /\b(?:AMZN|Amazon|NF|Netflix|DSNP|Disney\+?|HMAX|HBO|Hulu|ATVP|AppleTV|iTunes|BiliBili|Baha|Crunchyroll|CR|Viu|U-?NEXT|ABEMA|TVING|PrimeVideo|MAX|Peacock|Paramount\+?)\b/g
const RELEASE_GROUP_TOKEN_RE = /\b(?:GrassTV|NTb|FLUX|PTerWEB|CMCT|CHD|FGT|YIFY|YTS|MeGusta|VARYG|LoliHouse|ANi|Lilith|U3|CatWEB|MTeam|MWeb|Hares|SweetSub|MagicStar|Skymoon|XiaYong|Nekomoe|DBD-Raws|GM-Team|NC-Raws)\b/gi
const SUBTITLE_TOKEN_RE = /\b(?:CHS|CHT|CHS&CHT|CHT&CHS|GB|BIG5|SUBS?|MULTI[- .]?SUB)\b|简繁|繁简|简中|繁中|简体|繁体|中文字幕|中字|中英双字|中英字幕|双语字幕|内封字幕|外挂字幕|字幕组|字幕/gi
const CHINESE_TITLE_RE = /[\u4E00-\u9FFF][\u4E00-\u9FFF\s·・、，：:《》“”"'\-—]*/g
const LATIN_TITLE_RE = /[a-z][a-z0-9\s'’:&.,!?+\-]*/gi
const GENERIC_TITLE_SEGMENT_KEYS = new Set([
  'download',
  'downloads',
  'complete',
  'completed',
  'incoming',
  'temp',
  'tmp',
  'media',
  'library',
  'libraries',
  'movie',
  'movies',
  'film',
  'films',
  'tv',
  'tv series',
  'series',
  'show',
  'shows',
  'video',
  'videos',
  '下载',
  '下载完成',
  '未整理',
  '待整理',
  '临时',
  '影视',
  '影视库',
  '媒体库',
  '片库',
  '视频',
].map(value => normalizeSegmentTitleKey(value)))
const STANDALONE_MOVIE_NOISE_TITLE_KEYS = new Set([
  'sample',
  'samples',
  'trailer',
  'trailers',
  'teaser',
  'preview',
  'clip',
  'clips',
  'extra',
  'extras',
  'bonus',
  'featurette',
  'behind the scenes',
  'bts',
  'test',
  'demo',
  'unknown',
  'untitled',
  '4k',
  '8k',
  'hd',
  'sd',
  'fhd',
  'uhd',
  'hdr',
  'dv',
  'cam',
  'tc',
  'ts',
  '合集',
  '样片',
  '预告',
  '预告片',
  '片花',
  '花絮',
  '测试',
  '示例',
  '未知',
  '未命名',
  '片头',
  '片尾',
].map(value => normalizeSegmentTitleKey(value)))
const CATEGORY_TITLE_SEGMENT_KEYS = new Set([
  '电影',
  '剧集',
  '电视剧',
  '华语电影',
  '外语电影',
  '动画电影',
  '纪录片',
  '纪录',
  '综艺',
  '儿童',
  '动漫',
  '国漫',
  '日番',
  '国产动漫',
  '国产剧',
  '欧美剧',
  '美剧',
  '日韩剧',
  '日剧',
  '韩剧',
  '短剧',
].map(value => normalizeSegmentTitleKey(value)))

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
    const title = hints.seriesTitle || hints.title || hints.cleanFileTitle || stripFileExtension(record.fileName)
    return {
      kind: 'episode',
      parseStatus: 'parsed',
      record,
      title,
      normalizedTitle: normalizeTitleKey(title),
      year: hints.year,
      seriesTitle: hints.seriesTitle || undefined,
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

  const standaloneMovieTitle = hasSearchableStandaloneMovieTitle(hints, false)
  const standardMovieTitle = standardMode
    && hints.depth >= 2
    && hasSearchableStandaloneMovieTitle(hints, true)
  const categorizedNestedMovieTitle = cleanExplicitCategorySegment(hints.categoryHint) != null
    && hasSearchableStandaloneMovieTitle(hints, true)
  if (hints.titleYearFolder || hints.titleYearFile || standardMovieTitle || standaloneMovieTitle || categorizedNestedMovieTitle) {
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
  const fileEpisodeTitle = cleanEpisodeTitleFromStem(fileStem)
  const titleYearFile = parseTitleYear(fileStem)
  const titleYearFolder = findLastTitleYearSegment(parentSegments)
  const episode = parseEpisode(fileStem)
  const seasonFolder = findLastSeasonFolder(parentSegments)
  const seasonNumber = episode?.seasonNumber ?? seasonFolder?.seasonNumber
  const episodeNumber = episode?.episodeNumber
  const seriesIndex = seasonFolder
    ? seasonFolder.index - 1
    : episodeNumber != null && fileEpisodeTitle
      ? -1
      : parentSegments.length - 1
  const seriesTitle = episodeNumber != null
    ? resolveEpisodeSeriesTitle({
        fileEpisodeTitle,
        parentSegments,
        seasonFolderIndex: seasonFolder?.index,
      })
    : seasonFolder
      ? cleanWorkTitleSegment(parentSegments[seasonFolder.index - 1])
      : undefined
  const titleMatch = titleYearFolder?.match ?? titleYearFile
  const title = episodeNumber != null
    ? seriesTitle ?? fileEpisodeTitle
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
    .replace(SOURCE_TOKEN_RE, ' ')
    .replace(RELEASE_GROUP_TOKEN_RE, ' ')
    .replace(SUBTITLE_TOKEN_RE, ' ')
    .replace(/[._]+/g, ' ')
    .replace(/\s+-\s+[\da-z]+$/i, ' ')
    .replace(/[-—]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}

export function normalizeTitleKey(value: string): string {
  return cleanMediaTitle(value).toLocaleLowerCase()
}

export function extractMediaSearchTitles(value: string): string[] {
  const cleaned = cleanSearchTitle(value)
  const candidates: string[] = []

  for (const match of cleaned.matchAll(CHINESE_TITLE_RE))
    addSearchTitleCandidate(candidates, match[0])
  for (const match of cleaned.matchAll(LATIN_TITLE_RE))
    addSearchTitleCandidate(candidates, match[0])
  addSearchTitleCandidate(candidates, cleaned)

  return candidates
}

function hasSearchableStandaloneMovieTitle(hints: RawPathHints, allowNested: boolean): boolean {
  if (hints.episodePattern || hints.seasonFolder)
    return false

  if (!allowNested && hints.depth > 2)
    return false

  const title = cleanWorkTitleSegment(hints.title ?? hints.cleanFileTitle)
  if (!title || isObviousStandaloneMovieNoiseTitle(title))
    return false

  return extractMediaSearchTitles(title).some(searchTitle => !isObviousStandaloneMovieNoiseTitle(searchTitle))
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

function cleanEpisodeTitleFromStem(value: string): string | undefined {
  return cleanWorkTitleSegment(titleBeforeEpisode(value))
}

function resolveEpisodeSeriesTitle(input: {
  fileEpisodeTitle?: string
  parentSegments: readonly string[]
  seasonFolderIndex?: number
}): string | undefined {
  if (input.seasonFolderIndex != null) {
    const grandparentTitle = cleanWorkTitleSegment(input.parentSegments[input.seasonFolderIndex - 1])
    return grandparentTitle ?? input.fileEpisodeTitle
  }

  if (input.fileEpisodeTitle)
    return input.fileEpisodeTitle

  return cleanWorkTitleSegment(input.parentSegments.at(-1))
}

function cleanWorkTitleSegment(value: string | undefined): string | undefined {
  if (!value)
    return undefined

  const title = cleanMediaTitle(value)
  if (!title || isReservedStructureSegment(title))
    return undefined

  return title
}

function isReservedStructureSegment(value: string): boolean {
  const key = normalizeSegmentTitleKey(value)
  if (!key)
    return true
  if (GENERIC_TITLE_SEGMENT_KEYS.has(key) || CATEGORY_TITLE_SEGMENT_KEYS.has(key))
    return true
  if (parseSeasonFolder(value) != null)
    return true
  if (parseEpisode(value) != null && !titleBeforeEpisode(value))
    return true
  return false
}

function isObviousStandaloneMovieNoiseTitle(value: string): boolean {
  const key = normalizeSegmentTitleKey(value)
  return !key || isReservedStructureSegment(value) || STANDALONE_MOVIE_NOISE_TITLE_KEYS.has(key)
}

function normalizeSegmentTitleKey(value: string): string {
  return cleanMediaTitle(value).toLocaleLowerCase().replace(/[^a-z0-9\u4E00-\u9FFF]+/g, '')
}

function folderTitleFallback(parentSegments: readonly string[], cleanFileTitle: string): string | undefined {
  const parent = parentSegments.at(-1)
  if (!parent)
    return cleanFileTitle || undefined

  const cleanParent = cleanWorkTitleSegment(parent)
  if (cleanParent && cleanFileTitle && cleanFileTitle.includes(cleanParent))
    return cleanParent
  return cleanFileTitle || cleanParent || undefined
}

function inferCategoryHint(
  parentSegments: readonly string[],
  indexes: { titleFolderIndex?: number, seasonFolderIndex?: number, seriesIndex?: number },
): string | undefined {
  const directCategoryHint = parentSegments.length === 1
    ? cleanExplicitCategorySegment(parentSegments[0])
    : undefined
  const hintIndex = indexes.titleFolderIndex != null
    ? indexes.titleFolderIndex - 1
    : indexes.seasonFolderIndex != null && indexes.seriesIndex != null
      ? indexes.seriesIndex - 1
      : parentSegments.length > 1
        ? parentSegments.length - 2
        : undefined

  if (hintIndex == null || hintIndex < 0)
    return directCategoryHint

  const hint = cleanMediaTitle(parentSegments[hintIndex])
  return hint || directCategoryHint
}

function cleanExplicitCategorySegment(value: string | undefined): string | undefined {
  if (!value)
    return undefined

  const hint = cleanMediaTitle(value)
  if (!hint || !CATEGORY_TITLE_SEGMENT_KEYS.has(normalizeSegmentTitleKey(hint)))
    return undefined

  return hint
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

function addSearchTitleCandidate(candidates: string[], value: string) {
  const title = cleanSearchTitle(value)
  if (!title || isLikelyNoiseTitle(title))
    return

  const normalized = normalizeTitleKey(title)
  if (!normalized || candidates.some(candidate => normalizeTitleKey(candidate) === normalized))
    return

  candidates.push(title)
}

function cleanSearchTitle(value: string): string {
  return cleanMediaTitle(value)
    .replace(/^[\s:：,，.·・《》"']+|[\s:：,，.·・《》"']+$/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

function isLikelyNoiseTitle(value: string): boolean {
  const normalized = value.trim()
  if (normalized.length < 2)
    return true
  if (/^\d+$/.test(normalized))
    return true
  if (/^(?:S\d+|EP?\d+)$/i.test(normalized))
    return true
  return false
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}
