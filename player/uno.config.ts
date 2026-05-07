import { defineConfig, presetIcons, presetUno } from 'unocss'

export default defineConfig({
  presets: [
    presetUno(),
    presetIcons(),
  ],
  theme: {
    colors: {
      primary: {
        DEFAULT: '#4A9EFF',
        hover: '#6BB3FF',
        active: '#3A8EEF',
      },
      accent: {
        DEFAULT: '#A855F7',
        hover: '#C084FC',
        active: '#9333EA',
      },
      bg: {
        DEFAULT: '#0A0A0F',
        secondary: '#12121A',
        tertiary: '#1A1A25',
      },
      surface: {
        DEFAULT: '#1E1E2A',
        hover: '#252535',
      },
      success: '#22C55E',
      warning: '#F59E0B',
      error: '#EF4444',
      info: '#3B82F6',
    },
  },
  shortcuts: {
    'glass': 'bg-[rgba(30,30,42,0.6)] backdrop-blur-16px border border-[rgba(255,255,255,0.1)]',
    'glass-card': 'glass rounded-lg shadow-[0_8px_32px_rgba(0,0,0,0.3)] transition-all duration-250',
    'glass-card-hover': 'glass-card hover:bg-[rgba(37,37,53,0.7)] hover:border-[rgba(255,255,255,0.18)]',
  },
})
