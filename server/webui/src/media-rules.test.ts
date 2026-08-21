import { reactive } from 'vue'
import { describe, expect, it } from 'vitest'
import { cloneRecognitionRules, cloneRules, emptyRules, moveItem, newCategory, newRecognitionRule } from '@/media-rules'

describe('controlled media rule drafts', () => {
  it('creates exactly movie and tv empty groups', () => {
    const rules = emptyRules()
    expect(rules.groups.map(group => group.media_type)).toEqual(['movie', 'tv'])
    expect(rules.groups.every(group => group.fallback_category_name === '未分类')).toBe(true)
  })
  it('creates provider-specific country dimensions and moves without mutation', () => {
    expect(newCategory('movie').conditions.production_countries).toBeDefined()
    expect(newCategory('movie').conditions.origin_countries).toBeUndefined()
    expect(newCategory('tv').conditions.origin_countries).toBeDefined()
    const source = ['a', 'b', 'c']; expect(moveItem(source, 1, -1)).toEqual(['b', 'a', 'c']); expect(source).toEqual(['a', 'b', 'c'])
  })
  it('deep-clones Vue reactive DTOs without losing optional condition fields', () => {
    const source = reactive(emptyRules())
    const movie = source.groups[0]!
    const category = newCategory('movie')
    category.conditions.release_year = { from: 2000 }
    movie.categories.push(category)

    const clone = cloneRules(source)

    expect(clone).toEqual(source)
    expect(clone).not.toBe(source)
    expect(clone.groups[0]).not.toBe(movie)
    expect(clone.groups[0]!.categories[0]!.conditions.production_countries).toEqual({ include: [], exclude: [] })
    expect(clone.groups[0]!.categories[0]!.conditions.release_year).toEqual({ from: 2000 })
    clone.groups[0]!.categories[0]!.conditions.genre_ids.include.push(16)
    expect(category.conditions.genre_ids.include).toEqual([])
  })
  it('creates and clones ordered recognition preprocessors independently', () => {
    const first = newRecognitionRule()
    first.pattern = '^【[^】]*发布[^】]*】'
    const source = reactive([first])
    const clone = cloneRecognitionRules(source)
    expect(clone).toEqual([{ enabled: true, media_type: 'all', pattern: '^【[^】]*发布[^】]*】', replacement: '' }])
    clone[0]!.pattern = 'changed'
    expect(source[0]!.pattern).toBe('^【[^】]*发布[^】]*】')
  })
})
