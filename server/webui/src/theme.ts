import { readonly, ref } from 'vue'

export type Theme = 'light' | 'dark'

export const THEME_STORAGE_KEY = 'omc:server-theme'

interface ThemeStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

interface ThemeRoot {
  dataset: DOMStringMap
  style: Pick<CSSStyleDeclaration, 'colorScheme'>
}

function browserStorage(): ThemeStorage | null {
  try { return window.localStorage } catch { return null }
}

export function readStoredTheme(storage: ThemeStorage | null = browserStorage()): Theme {
  if (!storage) return 'light'
  try { return storage.getItem(THEME_STORAGE_KEY) === 'dark' ? 'dark' : 'light' } catch { return 'light' }
}

export function applyTheme(theme: Theme, root: ThemeRoot = document.documentElement) {
  root.dataset.theme = theme
  root.style.colorScheme = theme
}

export function persistTheme(theme: Theme, storage: ThemeStorage | null = browserStorage()) {
  if (!storage) return
  try { storage.setItem(THEME_STORAGE_KEY, theme) } catch { /* Browser privacy settings may block storage. */ }
}

const currentTheme = ref<Theme>('light')

export function initializeTheme() {
  currentTheme.value = readStoredTheme()
  applyTheme(currentTheme.value)
  return currentTheme.value
}

export function useTheme() {
  function setTheme(theme: Theme) {
    currentTheme.value = theme
    applyTheme(theme)
    persistTheme(theme)
  }

  function toggleTheme() { setTheme(currentTheme.value === 'light' ? 'dark' : 'light') }

  return { theme: readonly(currentTheme), setTheme, toggleTheme }
}
