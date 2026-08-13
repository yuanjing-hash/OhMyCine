import { describe, expect, it } from 'vitest'
import { applyTheme, persistTheme, readStoredTheme, THEME_STORAGE_KEY } from './theme'

describe('server theme preference', () => {
  it('defaults malformed and missing values to light', () => {
    expect(readStoredTheme(null)).toBe('light')
    expect(readStoredTheme({ getItem: () => 'system', setItem: () => undefined })).toBe('light')
  })

  it('reads and persists a supported theme', () => {
    const values = new Map<string, string>([[THEME_STORAGE_KEY, 'dark']])
    const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value) }
    expect(readStoredTheme(storage)).toBe('dark')
    persistTheme('light', storage)
    expect(values.get(THEME_STORAGE_KEY)).toBe('light')
  })

  it('applies the theme and native color scheme together', () => {
    const root = { dataset: {} as DOMStringMap, style: { colorScheme: '' } }
    applyTheme('dark', root)
    expect(root.dataset.theme).toBe('dark')
    expect(root.style.colorScheme).toBe('dark')
  })

  it('does not fail when storage is unavailable', () => {
    const blocked = { getItem: () => { throw new Error('blocked') }, setItem: () => { throw new Error('blocked') } }
    expect(readStoredTheme(blocked)).toBe('light')
    expect(() => persistTheme('dark', blocked)).not.toThrow()
  })
})
