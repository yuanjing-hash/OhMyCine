export type ScrapeMediaType = 'movie' | 'tv'

export interface TmdbGenreOption {
  id: number
  name: string
  label: string
}

export interface ScrapeNamedOption {
  value: string
  label: string
}

export interface ScrapeValueCondition<T extends string | number> {
  include: T[]
  exclude: T[]
}

export interface ScrapeYearRange {
  from?: number
  to?: number
}

export interface ScrapeCategoryConditions {
  genreIds: ScrapeValueCondition<number>
  originalLanguages: ScrapeValueCondition<string>
  productionCountries?: ScrapeValueCondition<string>
  originCountries?: ScrapeValueCondition<string>
  releaseYear: ScrapeYearRange | null
}

export interface ScrapeCategoryRule {
  id: string
  name: string
  conditions: ScrapeCategoryConditions
}

export interface ScrapeRuleGroup {
  mediaType: ScrapeMediaType
  categories: ScrapeCategoryRule[]
  fallbackCategoryName: string
}

export interface ScrapeClassificationRules {
  version: 1
  groups: ScrapeRuleGroup[]
}

export interface ScrapeClassifiableMetadata {
  readonly mediaType: ScrapeMediaType
  readonly genreIds: readonly number[]
  readonly originalLanguage?: string
  readonly productionCountries?: readonly string[]
  readonly originCountries?: readonly string[]
  readonly releaseYear?: number
}

export interface ScrapeClassificationResult {
  readonly categoryName: string
  readonly matchedRuleId?: string
  readonly matchedRuleName?: string
}

export const SCRAPE_CLASSIFICATION_RULES_STORAGE_KEY = 'ohmycine-scrape-classification-rules'
export const SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME = '未分类'
const LEGACY_FOREIGN_MOVIE_FALLBACK_CATEGORY_NAME = '外语电影'

export const TMDB_MOVIE_GENRES: TmdbGenreOption[] = [
  { id: 28, name: 'Action', label: '动作' },
  { id: 12, name: 'Adventure', label: '冒险' },
  { id: 16, name: 'Animation', label: '动画' },
  { id: 35, name: 'Comedy', label: '喜剧' },
  { id: 80, name: 'Crime', label: '犯罪' },
  { id: 99, name: 'Documentary', label: '纪录' },
  { id: 18, name: 'Drama', label: '剧情' },
  { id: 10751, name: 'Family', label: '家庭' },
  { id: 14, name: 'Fantasy', label: '奇幻' },
  { id: 36, name: 'History', label: '历史' },
  { id: 27, name: 'Horror', label: '恐怖' },
  { id: 10402, name: 'Music', label: '音乐' },
  { id: 9648, name: 'Mystery', label: '悬疑' },
  { id: 10749, name: 'Romance', label: '爱情' },
  { id: 878, name: 'Science Fiction', label: '科幻' },
  { id: 10770, name: 'TV Movie', label: '电视电影' },
  { id: 53, name: 'Thriller', label: '惊悚' },
  { id: 10752, name: 'War', label: '战争' },
  { id: 37, name: 'Western', label: '西部' },
]

export const TMDB_TV_GENRES: TmdbGenreOption[] = [
  { id: 10759, name: 'Action & Adventure', label: '动作冒险' },
  { id: 16, name: 'Animation', label: '动画' },
  { id: 35, name: 'Comedy', label: '喜剧' },
  { id: 80, name: 'Crime', label: '犯罪' },
  { id: 99, name: 'Documentary', label: '纪录片' },
  { id: 18, name: 'Drama', label: '剧情' },
  { id: 10751, name: 'Family', label: '家庭' },
  { id: 10762, name: 'Kids', label: '儿童' },
  { id: 9648, name: 'Mystery', label: '悬疑' },
  { id: 10763, name: 'News', label: '新闻' },
  { id: 10764, name: 'Reality', label: '真人秀' },
  { id: 10765, name: 'Sci-Fi & Fantasy', label: '科幻奇幻' },
  { id: 10766, name: 'Soap', label: '肥皂剧' },
  { id: 10767, name: 'Talk', label: '脱口秀' },
  { id: 10768, name: 'War & Politics', label: '战争政治' },
  { id: 37, name: 'Western', label: '西部' },
]

export const SCRAPE_LANGUAGE_OPTIONS: ScrapeNamedOption[] = [
  { value: 'zh', label: '中文' },
  { value: 'cn', label: '中文' },
  { value: 'en', label: '英语' },
  { value: 'ja', label: '日语' },
  { value: 'ko', label: '韩语' },
  { value: 'fr', label: '法语' },
  { value: 'de', label: '德语' },
  { value: 'es', label: '西班牙语' },
  { value: 'it', label: '意大利语' },
  { value: 'ru', label: '俄语' },
  { value: 'th', label: '泰语' },
  { value: 'hi', label: '印地语' },
]

export const SCRAPE_COUNTRY_OPTIONS: ScrapeNamedOption[] = [
  { value: 'CN', label: '中国内地' },
  { value: 'TW', label: '中国台湾' },
  { value: 'HK', label: '中国香港' },
  { value: 'JP', label: '日本' },
  { value: 'KR', label: '韩国' },
  { value: 'US', label: '美国' },
  { value: 'GB', label: '英国' },
  { value: 'FR', label: '法国' },
  { value: 'DE', label: '德国' },
  { value: 'ES', label: '西班牙' },
  { value: 'IT', label: '意大利' },
  { value: 'NL', label: '荷兰' },
  { value: 'PT', label: '葡萄牙' },
  { value: 'RU', label: '俄罗斯' },
  { value: 'TH', label: '泰国' },
  { value: 'IN', label: '印度' },
  { value: 'SG', label: '新加坡' },
]

export const DEFAULT_SCRAPE_CLASSIFICATION_RULES: ScrapeClassificationRules = {
  version: 1,
  groups: [
    {
      mediaType: 'movie',
      fallbackCategoryName: SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME,
      categories: [
        createCategoryRule('动画电影', { genreIds: [16] }),
        createCategoryRule('华语电影', { originalLanguages: ['zh', 'cn'] }),
        createCategoryRule('外语电影', { excludedOriginalLanguages: ['zh', 'cn'] }),
      ],
    },
    {
      mediaType: 'tv',
      fallbackCategoryName: SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME,
      categories: [
        createCategoryRule('国漫', { genreIds: [16], originCountries: ['CN', 'TW', 'HK'] }),
        createCategoryRule('日番', { genreIds: [16], originCountries: ['JP'] }),
        createCategoryRule('动漫', { genreIds: [16] }),
        createCategoryRule('纪录片', { genreIds: [99] }),
        createCategoryRule('儿童', { genreIds: [10762] }),
        createCategoryRule('综艺', { genreIds: [10764, 10767] }),
        createCategoryRule('国产剧', { originCountries: ['CN', 'TW', 'HK'] }),
        createCategoryRule('欧美剧', { originCountries: ['US', 'FR', 'GB', 'DE', 'ES', 'IT', 'NL', 'PT', 'RU'] }),
        createCategoryRule('日韩剧', { originCountries: ['JP', 'KR'] }),
      ],
    },
  ],
}

export function loadScrapeClassificationRules(): ScrapeClassificationRules {
  try {
    const raw = localStorage.getItem(SCRAPE_CLASSIFICATION_RULES_STORAGE_KEY)
    if (!raw)
      return cloneScrapeClassificationRules(DEFAULT_SCRAPE_CLASSIFICATION_RULES)
    return sanitizeScrapeClassificationRules(JSON.parse(raw) as unknown)
  }
  catch {
    return cloneScrapeClassificationRules(DEFAULT_SCRAPE_CLASSIFICATION_RULES)
  }
}

export function saveScrapeClassificationRules(rules: ScrapeClassificationRules): void {
  localStorage.setItem(SCRAPE_CLASSIFICATION_RULES_STORAGE_KEY, JSON.stringify(sanitizeScrapeClassificationRules(rules)))
}

export function resetScrapeClassificationRules(): ScrapeClassificationRules {
  const rules = cloneScrapeClassificationRules(DEFAULT_SCRAPE_CLASSIFICATION_RULES)
  saveScrapeClassificationRules(rules)
  return rules
}

export function normalizeScrapeFallbackCategoryName(value: string | undefined): string {
  const trimmed = value?.trim()
  if (!trimmed || trimmed === LEGACY_FOREIGN_MOVIE_FALLBACK_CATEGORY_NAME)
    return SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME
  return trimmed
}

export function classifyScrapeMetadata(
  metadata: ScrapeClassifiableMetadata,
  rules: ScrapeClassificationRules = loadScrapeClassificationRules(),
): ScrapeClassificationResult {
  const group = rules.groups.find(item => item.mediaType === metadata.mediaType)
  const fallbackCategoryName = normalizeScrapeFallbackCategoryName(group?.fallbackCategoryName)

  if (!group)
    return { categoryName: fallbackCategoryName }

  for (const category of group.categories) {
    if (!matchesScrapeCategoryConditions(metadata, category.conditions))
      continue

    return {
      categoryName: category.name,
      matchedRuleId: category.id,
      matchedRuleName: category.name,
    }
  }

  return { categoryName: fallbackCategoryName }
}

export function cloneScrapeClassificationRules(rules: ScrapeClassificationRules): ScrapeClassificationRules {
  return {
    version: 1,
    groups: rules.groups.map(group => ({
      mediaType: group.mediaType,
      fallbackCategoryName: group.fallbackCategoryName,
      categories: group.categories.map(category => ({
        id: category.id,
        name: category.name,
        conditions: cloneConditions(category.conditions),
      })),
    })),
  }
}

export function createEmptyScrapeCategoryRule(name = '新分类'): ScrapeCategoryRule {
  return createCategoryRule(name, {})
}

function createCategoryRule(name: string, input: {
  genreIds?: number[]
  excludedGenreIds?: number[]
  originalLanguages?: string[]
  excludedOriginalLanguages?: string[]
  productionCountries?: string[]
  excludedProductionCountries?: string[]
  originCountries?: string[]
  excludedOriginCountries?: string[]
  releaseYear?: ScrapeYearRange | null
}): ScrapeCategoryRule {
  return {
    id: createRuleId(name),
    name,
    conditions: {
      genreIds: createCondition(input.genreIds ?? [], input.excludedGenreIds ?? []),
      originalLanguages: createCondition(input.originalLanguages ?? [], input.excludedOriginalLanguages ?? []),
      productionCountries: createCondition(input.productionCountries ?? [], input.excludedProductionCountries ?? []),
      originCountries: createCondition(input.originCountries ?? [], input.excludedOriginCountries ?? []),
      releaseYear: input.releaseYear ?? null,
    },
  }
}

function sanitizeScrapeClassificationRules(value: unknown): ScrapeClassificationRules {
  if (!isRecord(value) || value.version !== 1 || !Array.isArray(value.groups))
    return cloneScrapeClassificationRules(DEFAULT_SCRAPE_CLASSIFICATION_RULES)

  const groups: ScrapeRuleGroup[] = []
  for (const mediaType of ['movie', 'tv'] satisfies ScrapeMediaType[]) {
    const rawGroup = value.groups.find(group => isRecord(group) && group.mediaType === mediaType)
    if (!isRecord(rawGroup)) {
      groups.push(cloneScrapeClassificationRules(DEFAULT_SCRAPE_CLASSIFICATION_RULES).groups.find(group => group.mediaType === mediaType)!)
      continue
    }

    groups.push({
      mediaType,
      fallbackCategoryName: normalizeScrapeFallbackCategoryName(
        typeof rawGroup.fallbackCategoryName === 'string' ? rawGroup.fallbackCategoryName : undefined,
      ),
      categories: Array.isArray(rawGroup.categories)
        ? rawGroup.categories.map(category => sanitizeCategoryRule(category, mediaType)).filter((category): category is ScrapeCategoryRule => category != null)
        : [],
    })
  }

  return { version: 1, groups }
}

function sanitizeCategoryRule(value: unknown, mediaType: ScrapeMediaType): ScrapeCategoryRule | null {
  if (!isRecord(value))
    return null
  const name = typeof value.name === 'string' && value.name.trim() ? value.name.trim() : '未命名分类'
  const conditions = isRecord(value.conditions) ? value.conditions : {}
  return {
    id: typeof value.id === 'string' && value.id.trim() ? value.id : createRuleId(name),
    name,
    conditions: {
      genreIds: sanitizeCondition(conditions.genreIds, genreIdsForMediaType(mediaType)),
      originalLanguages: sanitizeCondition(conditions.originalLanguages, SCRAPE_LANGUAGE_OPTIONS.map(option => option.value)),
      productionCountries: mediaType === 'movie'
        ? sanitizeCondition(conditions.productionCountries, SCRAPE_COUNTRY_OPTIONS.map(option => option.value))
        : createCondition([]),
      originCountries: mediaType === 'tv'
        ? sanitizeCondition(conditions.originCountries, SCRAPE_COUNTRY_OPTIONS.map(option => option.value))
        : createCondition([]),
      releaseYear: sanitizeYearRange(conditions.releaseYear),
    },
  }
}

function sanitizeCondition<T extends string | number>(value: unknown, allowedValues: readonly T[]): ScrapeValueCondition<T> {
  if (!isRecord(value))
    return createCondition([])
  const allowed = new Set<T>(allowedValues)
  return {
    include: uniqueArray(Array.isArray(value.include) ? value.include.filter((item): item is T => allowed.has(item as T)) : []),
    exclude: uniqueArray(Array.isArray(value.exclude) ? value.exclude.filter((item): item is T => allowed.has(item as T)) : []),
  }
}

function sanitizeYearRange(value: unknown): ScrapeYearRange | null {
  if (!isRecord(value))
    return null
  const from = sanitizeYear(value.from)
  const to = sanitizeYear(value.to)
  if (from == null && to == null)
    return null
  return { from, to }
}

function sanitizeYear(value: unknown): number | undefined {
  if (typeof value !== 'number' || !Number.isInteger(value))
    return undefined
  if (value < 1888 || value > 2200)
    return undefined
  return value
}

function cloneConditions(conditions: ScrapeCategoryConditions): ScrapeCategoryConditions {
  return {
    genreIds: cloneCondition(conditions.genreIds),
    originalLanguages: cloneCondition(conditions.originalLanguages),
    productionCountries: cloneCondition(conditions.productionCountries ?? createCondition([])),
    originCountries: cloneCondition(conditions.originCountries ?? createCondition([])),
    releaseYear: conditions.releaseYear ? { ...conditions.releaseYear } : null,
  }
}

function createCondition<T extends string | number>(include: T[], exclude: T[] = []): ScrapeValueCondition<T> {
  return { include: uniqueArray(include), exclude: uniqueArray(exclude) }
}

function cloneCondition<T extends string | number>(condition: ScrapeValueCondition<T>): ScrapeValueCondition<T> {
  return {
    include: [...condition.include],
    exclude: [...condition.exclude],
  }
}

function matchesScrapeCategoryConditions(
  metadata: ScrapeClassifiableMetadata,
  conditions: ScrapeCategoryConditions,
): boolean {
  const countries = metadata.mediaType === 'movie'
    ? metadata.productionCountries ?? []
    : metadata.originCountries ?? []

  return matchesValueCondition(metadata.genreIds, conditions.genreIds)
    && matchesValueCondition(metadata.originalLanguage ? [metadata.originalLanguage] : [], conditions.originalLanguages)
    && matchesValueCondition(countries, metadata.mediaType === 'movie'
      ? conditions.productionCountries ?? createCondition([])
      : conditions.originCountries ?? createCondition([]))
    && matchesYearRange(metadata.releaseYear, conditions.releaseYear)
}

function matchesValueCondition<T extends string | number>(
  actualValues: readonly T[],
  condition: ScrapeValueCondition<T>,
): boolean {
  const normalizedActual = new Set(actualValues.map(normalizeConditionComparable))
  const includes = condition.include.map(normalizeConditionComparable)
  const excludes = condition.exclude.map(normalizeConditionComparable)

  if (excludes.some(value => normalizedActual.has(value)))
    return false
  if (includes.length > 0 && !includes.some(value => normalizedActual.has(value)))
    return false
  return true
}

function matchesYearRange(year: number | undefined, range: ScrapeYearRange | null): boolean {
  if (!range)
    return true
  if (year == null)
    return false
  if (range.from != null && year < range.from)
    return false
  if (range.to != null && year > range.to)
    return false
  return true
}

function normalizeConditionComparable<T extends string | number>(value: T): T {
  if (typeof value === 'string')
    return value.toUpperCase() as T
  return value
}

function genreIdsForMediaType(mediaType: ScrapeMediaType): number[] {
  return (mediaType === 'movie' ? TMDB_MOVIE_GENRES : TMDB_TV_GENRES).map(genre => genre.id)
}

function uniqueArray<T>(items: readonly T[]): T[] {
  return [...new Set(items)]
}

function createRuleId(name: string): string {
  return `${slugify(name)}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
}

function slugify(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9\u4E00-\u9FFF]+/g, '-')
    .replace(/^-+|-+$/g, '')
    || 'category'
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null
}
