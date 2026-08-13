import type { ClassificationCategory, ClassificationConditions, ClassificationRulesV1, MediaType } from '@/types/api'

export const movieGenreOptions = [12, 14, 16, 18, 27, 28, 35, 36, 37, 53, 80, 99, 878, 9648, 10402, 10749, 10751, 10752, 10770]
export const tvGenreOptions = [16, 18, 35, 37, 80, 99, 9648, 10751, 10759, 10762, 10763, 10764, 10765, 10766, 10767, 10768]
export const languageOptions = ['zh', 'cn', 'en', 'ja', 'ko', 'fr', 'de', 'es', 'it', 'ru', 'th', 'hi']
export const countryOptions = ['CN', 'TW', 'HK', 'JP', 'KR', 'US', 'GB', 'FR', 'DE', 'ES', 'IT', 'NL', 'PT', 'RU', 'TH', 'IN', 'SG']

export function emptyRules(): ClassificationRulesV1 {
  return { version: 1, groups: [
    { media_type: 'movie', categories: [], fallback_category_name: '未分类' },
    { media_type: 'tv', categories: [], fallback_category_name: '未分类' },
  ] }
}

export function cloneRules(rules: ClassificationRulesV1): ClassificationRulesV1 {
  return {
    version: 1,
    groups: rules.groups.map(group => ({
      media_type: group.media_type,
      fallback_category_name: group.fallback_category_name,
      categories: group.categories.map(category => ({
        id: category.id,
        name: category.name,
        conditions: {
          genre_ids: cloneCondition(category.conditions.genre_ids),
          original_languages: cloneCondition(category.conditions.original_languages),
          ...(category.conditions.production_countries
            ? { production_countries: cloneCondition(category.conditions.production_countries) }
            : {}),
          ...(category.conditions.origin_countries
            ? { origin_countries: cloneCondition(category.conditions.origin_countries) }
            : {}),
          release_year: category.conditions.release_year
            ? {
                ...(category.conditions.release_year.from === undefined ? {} : { from: category.conditions.release_year.from }),
                ...(category.conditions.release_year.to === undefined ? {} : { to: category.conditions.release_year.to }),
              }
            : null,
        },
      })),
    })),
  }
}

function cloneCondition<T>(condition: { include: T[]; exclude: T[] }) {
  return { include: [...condition.include], exclude: [...condition.exclude] }
}

export function newCategory(mediaType: MediaType): ClassificationCategory {
  const conditions: ClassificationConditions = { genre_ids: { include: [], exclude: [] }, original_languages: { include: [], exclude: [] }, release_year: null }
  if (mediaType === 'movie') conditions.production_countries = { include: [], exclude: [] }
  else conditions.origin_countries = { include: [], exclude: [] }
  return { id: crypto.randomUUID().replaceAll('-', ''), name: '新分类', conditions }
}

export function moveItem<T>(items: T[], index: number, direction: -1 | 1): T[] {
  const target = index + direction
  if (target < 0 || target >= items.length) return items
  const copy = [...items]; [copy[index], copy[target]] = [copy[target]!, copy[index]!]
  return copy
}
