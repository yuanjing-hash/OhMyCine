import { ref } from 'vue'
import { getAppSetting, setAppSetting } from '@/services/appSettings'

export type Theme = 'dark' | 'light'

const STORAGE_KEY = 'ohmycine-theme'
const theme = ref<Theme>('dark')
let transitionCleanupFrame: number | null = null

function applyTheme(nextTheme: Theme, animate: boolean) {
  const root = document.documentElement
  const isChanging = root.getAttribute('data-theme') !== nextTheme

  if (animate && isChanging) {
    root.classList.add('theme-switching')
    void root.offsetWidth
  }

  root.setAttribute('data-theme', nextTheme)

  if (!animate || !isChanging)
    return

  if (transitionCleanupFrame != null)
    window.cancelAnimationFrame(transitionCleanupFrame)
  transitionCleanupFrame = window.requestAnimationFrame(() => {
    transitionCleanupFrame = window.requestAnimationFrame(() => {
      root.classList.remove('theme-switching')
      transitionCleanupFrame = null
    })
  })
}

export function useTheme() {
  function load() {
    const saved = getAppSetting(STORAGE_KEY) as Theme | null
    if (saved === 'light' || saved === 'dark')
      theme.value = saved
    applyTheme(theme.value, false)
  }

  function toggle() {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
    applyTheme(theme.value, true)
    void setAppSetting(STORAGE_KEY, theme.value)
  }

  return {
    theme,
    toggle,
    load,
  }
}
