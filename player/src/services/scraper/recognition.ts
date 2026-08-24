import type { ScrapeMediaType } from './classificationRules'
import type { RawMediaCandidate } from './types'
import { cleanMediaTitle, extractMediaSearchTitles } from './parser'
import { splitProviderPath, stripFileExtension } from './pathUtils'

export const PLAYER_RECOGNITION_ENGINE_VERSION = 'player-nextgen-v2'
export const MAX_TMDB_RECOGNITION_SEARCHES = 10
export const MAX_TMDB_RECOGNITION_DETAILS = 3

export type RecognitionDecisionReason = 'matched' | 'no_match' | 'low_confidence' | 'candidate_conflict'

export interface RecognitionTitleVariant {
  readonly title: string
  readonly source: 'resolved' | 'file' | 'parent' | 'grandparent'
  readonly stage: 'canonical' | 'fallback'
}

export interface RecognitionSearchRequest {
  readonly mediaType: ScrapeMediaType
  readonly title: string
  readonly year?: number
  readonly source: RecognitionTitleVariant['source']
}

export interface RecognitionRemoteCandidate {
  readonly id: number
  readonly mediaType: ScrapeMediaType
  readonly title: string
  readonly originalTitle?: string
  readonly alternativeTitles?: readonly string[]
  readonly translations?: readonly string[]
  readonly releaseYear?: number
  readonly seasonCount?: number
  readonly popularity?: number
}

export interface RecognitionRankedCandidate {
  readonly candidate: RecognitionRemoteCandidate
  readonly score: number
  readonly titleSimilarity: number
}

export interface RecognitionDecision {
  readonly reason: RecognitionDecisionReason
  readonly match?: RecognitionRemoteCandidate
  readonly ranked: readonly RecognitionRankedCandidate[]
  readonly confidence: number
  readonly runnerUpGap: number
}

const MATCH_THRESHOLD = 0.78
const CONFLICT_MARGIN = 0.06
const HAN_EQUIVALENCE: Readonly<Record<string, string>> = {
  後: '后',
  宮: '宫',
  傳: '传',
  國: '国',
  風: '风',
  雲: '云',
  劍: '剑',
  俠: '侠',
  龍: '龙',
  門: '门',
  臺: '台',
  灣: '湾',
  華: '华',
  語: '语',
  劇: '剧',
  電: '电',
  視: '视',
  體: '体',
}

export function buildRecognitionTitleVariants(candidate: RawMediaCandidate): RecognitionTitleVariant[] {
  const segments = splitProviderPath(candidate.record.relativePath || candidate.record.fileName)
  const sourceValues: Array<{ source: RecognitionTitleVariant['source'], value?: string }> = [
    { source: 'resolved', value: candidate.seriesTitle ?? candidate.title },
    { source: 'file', value: stripFileExtension(segments.at(-1) ?? candidate.record.fileName) },
    { source: 'parent', value: segments.at(-2) },
    { source: 'grandparent', value: segments.at(-3) },
  ]
  const canonical: RecognitionTitleVariant[] = []
  const fallback: RecognitionTitleVariant[] = []
  const seen = new Set<string>()

  for (const item of sourceValues) {
    const title = cleanMediaTitle(item.value ?? '')
    if (!isSearchableTitle(title))
      continue
    addVariant(canonical, seen, { title, source: item.source, stage: 'canonical' })
  }
  for (const item of sourceValues) {
    for (const title of extractMediaSearchTitles(item.value ?? '')) {
      if (!isSearchableTitle(title))
        continue
      addVariant(fallback, seen, { title, source: item.source, stage: 'fallback' })
    }
  }
  return [...canonical, ...fallback]
}

export function buildRecognitionSearchRequests(candidate: RawMediaCandidate): RecognitionSearchRequest[] {
  const variants = buildRecognitionTitleVariants(candidate)
  const canonical = variants.filter(item => item.stage === 'canonical').slice(0, 3)
  const fallback = variants.filter(item => item.stage === 'fallback')
  const preferred = preferredMediaTypes(candidate)
  const requests: RecognitionSearchRequest[] = []
  const seen = new Set<string>()
  const add = (variant: RecognitionTitleVariant, mediaType: ScrapeMediaType, year?: number) => {
    if (requests.length >= MAX_TMDB_RECOGNITION_SEARCHES)
      return
    const key = `${mediaType}:${comparisonKey(variant.title)}:${year ?? ''}`
    if (!comparisonKey(variant.title) || seen.has(key))
      return
    seen.add(key)
    requests.push({ mediaType, title: variant.title, year, source: variant.source })
  }

  for (const variant of canonical)
    add(variant, preferred[0], candidate.year)
  for (const variant of canonical)
    add(variant, preferred[1], candidate.year)

  const primary = canonical[0] ?? fallback[0]
  if (primary && candidate.year != null) {
    add(primary, preferred[0], candidate.year - 1)
    add(primary, preferred[0], candidate.year + 1)
  }
  if (primary)
    add(primary, preferred[0])

  for (const variant of fallback) {
    add(variant, preferred[0], candidate.year)
    add(variant, preferred[1], candidate.year)
  }
  return requests
}

export function decideRecognitionCandidate(
  candidate: Pick<RawMediaCandidate, 'kind' | 'year' | 'seasonNumber'>,
  titleVariants: readonly Pick<RecognitionTitleVariant, 'title'>[],
  remoteCandidates: readonly RecognitionRemoteCandidate[],
): RecognitionDecision {
  const identities = new Map<string, RecognitionRemoteCandidate>()
  for (const remote of remoteCandidates) {
    if (!Number.isInteger(remote.id) || remote.id <= 0 || !comparisonKey(remote.title))
      continue
    identities.set(`${remote.mediaType}:${remote.id}`, remote)
  }
  const ranked = [...identities.values()]
    .map(remote => scoreRecognitionCandidate(candidate, titleVariants, remote))
    .sort((left, right) => right.score - left.score
      || left.candidate.mediaType.localeCompare(right.candidate.mediaType)
      || left.candidate.id - right.candidate.id)

  const best = ranked[0]
  if (!best)
    return { reason: 'no_match', ranked, confidence: 0, runnerUpGap: 0 }
  const runnerUpGap = Math.max(0, best.score - (ranked[1]?.score ?? 0))
  if (best.score < MATCH_THRESHOLD)
    return { reason: 'low_confidence', ranked, confidence: best.score, runnerUpGap }
  if (ranked[1] && runnerUpGap < CONFLICT_MARGIN && ranked[1].score >= MATCH_THRESHOLD - CONFLICT_MARGIN)
    return { reason: 'candidate_conflict', ranked, confidence: best.score, runnerUpGap }
  return { reason: 'matched', match: best.candidate, ranked, confidence: best.score, runnerUpGap }
}

function scoreRecognitionCandidate(
  parsed: Pick<RawMediaCandidate, 'kind' | 'year' | 'seasonNumber'>,
  variants: readonly Pick<RecognitionTitleVariant, 'title'>[],
  remote: RecognitionRemoteCandidate,
): RecognitionRankedCandidate {
  const names = [remote.title, remote.originalTitle, ...(remote.alternativeTitles ?? []), ...(remote.translations ?? [])]
    .filter((name): name is string => Boolean(name?.trim()))
  let titleSimilarity = 0
  let strongVariantMatches = 0
  for (const variant of variants) {
    const best = names.reduce((score, name) => Math.max(score, titleSimilarityScore(variant.title, name)), 0)
    titleSimilarity = Math.max(titleSimilarity, best)
    if (best >= 0.9)
      strongVariantMatches += 1
  }

  let score = titleSimilarity * 0.68
  // A script-neutral exact/near-exact identity is strong evidence even when a
  // loose movie filename carries no year. Keep this bonus explicit instead of
  // weakening the global acceptance threshold.
  if (titleSimilarity >= 0.98)
    score += 0.06
  if (parsed.year != null && remote.releaseYear != null) {
    const difference = Math.abs(parsed.year - remote.releaseYear)
    score += difference === 0 ? 0.12 : difference === 1 ? 0.06 : -0.24
  }
  const expectedType = parsed.kind === 'episode' || parsed.kind === 'tv' ? 'tv' : parsed.kind === 'movie' ? 'movie' : undefined
  const typeStrength = parsed.kind === 'episode' ? 0.96 : parsed.kind === 'tv' ? 0.82 : parsed.kind === 'movie' ? 0.72 : 0
  if (expectedType) {
    score += remote.mediaType === expectedType ? 0.1 * typeStrength : typeStrength >= 0.8 ? -0.22 * typeStrength : 0
    if (remote.mediaType === expectedType && typeStrength >= 0.8)
      score += 0.05 * typeStrength
  }
  if (parsed.seasonNumber != null && remote.mediaType === 'tv' && remote.seasonCount != null && parsed.seasonNumber <= remote.seasonCount)
    score += 0.03
  if (strongVariantMatches >= 2)
    score += 0.03 * Math.min(1, (strongVariantMatches - 1) / 3)
  if ((remote.popularity ?? 0) > 0)
    score += 0.01 * Math.min(1, Math.log1p(remote.popularity ?? 0) / Math.log(1001))

  return { candidate: remote, titleSimilarity, score: clamp(score, 0, 1) }
}

export function titleSimilarityScore(left: string, right: string): number {
  const leftKey = comparisonKey(left)
  const rightKey = comparisonKey(right)
  if (!leftKey || !rightKey)
    return 0
  if (leftKey === rightKey)
    return 1
  const edit = 1 - levenshtein([...leftKey], [...rightKey]) / Math.max([...leftKey].length, [...rightKey].length)
  const dice = bigramDice([...leftKey], [...rightKey])
  const tokens = tokenJaccard(comparisonTokens(left), comparisonTokens(right))
  return clamp(Math.max(edit, dice, tokens), 0, 1)
}

function comparisonKey(value: string): string {
  return [...value.normalize('NFKD').toLocaleLowerCase()]
    .map(character => HAN_EQUIVALENCE[character] ?? character)
    .join('')
    .replace(/\p{M}/gu, '')
    .replace(/[^\p{L}\p{N}]+/gu, '')
}

function comparisonTokens(value: string): string[] {
  return [...value.normalize('NFKD').toLocaleLowerCase()]
    .map(character => HAN_EQUIVALENCE[character] ?? character)
    .join('')
    .replace(/\p{M}/gu, '')
    .split(/[^\p{L}\p{N}]+/gu)
    .filter(Boolean)
}

function levenshtein(left: readonly string[], right: readonly string[]): number {
  let previous = Array.from({ length: right.length + 1 }, (_, index) => index)
  for (let leftIndex = 0; leftIndex < left.length; leftIndex += 1) {
    const current = [leftIndex + 1]
    for (let rightIndex = 0; rightIndex < right.length; rightIndex += 1) {
      current.push(Math.min(
        previous[rightIndex + 1] + 1,
        current[rightIndex] + 1,
        previous[rightIndex] + (left[leftIndex] === right[rightIndex] ? 0 : 1),
      ))
    }
    previous = current
  }
  return previous[right.length]
}

function bigramDice(left: readonly string[], right: readonly string[]): number {
  if (left.length < 2 || right.length < 2)
    return 0
  const counts = new Map<string, number>()
  for (let index = 0; index < left.length - 1; index += 1) {
    const key = `${left[index]}\0${left[index + 1]}`
    counts.set(key, (counts.get(key) ?? 0) + 1)
  }
  let intersection = 0
  for (let index = 0; index < right.length - 1; index += 1) {
    const key = `${right[index]}\0${right[index + 1]}`
    if ((counts.get(key) ?? 0) > 0) {
      intersection += 1
      counts.set(key, (counts.get(key) ?? 0) - 1)
    }
  }
  return (2 * intersection) / (left.length + right.length - 2)
}

function tokenJaccard(left: readonly string[], right: readonly string[]): number {
  const leftSet = new Set(left)
  const rightSet = new Set(right)
  const union = new Set([...leftSet, ...rightSet])
  if (union.size === 0)
    return 0
  return [...leftSet].filter(token => rightSet.has(token)).length / union.size
}

function isSearchableTitle(value: string): boolean {
  const key = comparisonKey(value)
  if (key.length < 2)
    return false
  if (/^\d+$/.test(key))
    return key.length === 4 && Number(key) >= 1000
  return !/^(?:s\d+(?:e\d+)?|ep?\d+|(?:season|seanson|saison|staffel|temporada|stagione|seizoen|sasong|sesong|kausi|sezon|serie|episode|folge|episodio|aflevering|avsnitt|jakso|odcinek|bolum)\d+|(?:第|제)?[零〇一二三四五六七八九十百千两兩\d]+(?:[季期集话話화회]|시즌))$/iu.test(key)
}

function addVariant(target: RecognitionTitleVariant[], seen: Set<string>, variant: RecognitionTitleVariant): void {
  const key = comparisonKey(variant.title)
  if (!key || seen.has(key))
    return
  seen.add(key)
  target.push(variant)
}

function preferredMediaTypes(candidate: Pick<RawMediaCandidate, 'kind'>): [ScrapeMediaType, ScrapeMediaType] {
  return candidate.kind === 'episode' || candidate.kind === 'tv' ? ['tv', 'movie'] : ['movie', 'tv']
}

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value))
}
