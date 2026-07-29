import { ref, watchEffect } from 'vue'
import { getAppSetting, setAppSetting } from '@/services/appSettings'

export type Theme = 'dark' | 'light'

const STORAGE_KEY = 'ohmycine-theme'
const theme = ref<Theme>('dark')

export function useTheme() {
  function load() {
    const saved = getAppSetting(STORAGE_KEY) as Theme | null
    if (saved === 'light' || saved === 'dark')
      theme.value = saved
  }

  function toggle() {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
    void setAppSetting(STORAGE_KEY, theme.value)
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
