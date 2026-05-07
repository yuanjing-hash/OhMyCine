import { ref, watchEffect } from 'vue'

export type Theme = 'dark' | 'light'

const STORAGE_KEY = 'ohmycine-theme'
const theme = ref<Theme>('dark')

export function useTheme() {
  function load() {
    const saved = localStorage.getItem(STORAGE_KEY) as Theme | null
    if (saved === 'light' || saved === 'dark')
      theme.value = saved
  }

  function toggle() {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
    localStorage.setItem(STORAGE_KEY, theme.value)
  }

  watchEffect(() => {
    document.documentElement.setAttribute('data-theme', theme.value)
  })

  return {
    theme,
    toggle,
    load,
  }
}
