import type { DataSourceConfig, HomeSection, MediaItem } from '@/services/datasource/types'
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

const STORAGE_KEY = 'ohmycine-datasources'

export const useDataSourceStore = defineStore('datasource', () => {
  const configs = ref<DataSourceConfig[]>([])
  const activeSourceId = ref<string | null>(null)
  const homeSections = ref<HomeSection[]>([])
  const isLoading = ref(false)

  const orderedConfigs = computed(() =>
    [...configs.value].sort((a, b) => a.order - b.order),
  )

  const activeSource = computed(() =>
    configs.value.find(c => c.id === activeSourceId.value) ?? null,
  )

  function loadConfigs() {
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (raw)
        configs.value = JSON.parse(raw)
    }
    catch {
      configs.value = []
    }
  }

  function saveConfigs() {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(configs.value))
  }

  function addConfig(config: Omit<DataSourceConfig, 'id' | 'order'>) {
    const id = `${config.type}-${Date.now()}`
    const order = configs.value.length
    configs.value.push({ ...config, id, order })
    saveConfigs()
    return id
  }

  function updateConfig(id: string, patch: Partial<DataSourceConfig>) {
    const idx = configs.value.findIndex(c => c.id === id)
    if (idx === -1)
      return
    configs.value[idx] = { ...configs.value[idx], ...patch }
    saveConfigs()
  }

  function removeConfig(id: string) {
    configs.value = configs.value.filter(c => c.id !== id)
    configs.value.forEach((c, i) => c.order = i)
    saveConfigs()
  }

  function reorderConfigs(ids: string[]) {
    const map = new Map(configs.value.map(c => [c.id, c]))
    configs.value = ids
      .map((id, order) => {
        const c = map.get(id)
        if (c)
          c.order = order
        return c
      })
      .filter((c): c is DataSourceConfig => c != null)
    saveConfigs()
  }

  async function loadHomeSections() {
    isLoading.value = true
    try {
      // MVP: generate placeholder sections from configs
      const sections: HomeSection[] = []

      // Hero section - placeholder items for now
      sections.push({
        id: 'hero',
        title: 'Featured',
        type: 'hero',
        items: generatePlaceholderHeroItems(),
      })

      // Continue watching - empty until playback history exists
      sections.push({
        id: 'continue-watching',
        title: 'Continue Watching',
        type: 'continueWatching',
        items: [],
      })

      // Recently added per source
      for (const config of orderedConfigs.value) {
        sections.push({
          id: `recent-${config.id}`,
          sourceId: config.id,
          title: `Latest from ${config.displayName ?? config.name}`,
          type: 'recentlyAdded',
          items: [],
        })
      }

      homeSections.value = sections
    }
    finally {
      isLoading.value = false
    }
  }

  return {
    configs,
    orderedConfigs,
    activeSourceId,
    activeSource,
    homeSections,
    isLoading,
    loadConfigs,
    addConfig,
    updateConfig,
    removeConfig,
    reorderConfigs,
    loadHomeSections,
  }
})

function generatePlaceholderHeroItems(): MediaItem[] {
  const items: MediaItem[] = [
    {
      id: 'placeholder-1',
      sourceId: 'placeholder',
      name: 'Dune: Part Two',
      type: 'movie',
      tagline: 'Long live the fighters.',
      overview: 'Paul Atreides unites with the Fremen while on a warpath of revenge against the conspirators who destroyed his family.',
      year: 2024,
      rating: 8.5,
      duration: 166,
      path: '/placeholder/dune-part-two',
      posterUrl: '',
      backdropUrl: '',
    },
    {
      id: 'placeholder-2',
      sourceId: 'placeholder',
      name: 'Blade Runner 2049',
      type: 'movie',
      tagline: 'The key to the future is finally unearthed.',
      overview: 'A young blade runner discovers a long-buried secret that leads him to track down former blade runner Rick Deckard.',
      year: 2017,
      rating: 8.0,
      duration: 164,
      path: '/placeholder/blade-runner-2049',
    },
    {
      id: 'placeholder-3',
      sourceId: 'placeholder',
      name: 'Interstellar',
      type: 'movie',
      tagline: 'Mankind was born on Earth. It was never meant to die here.',
      overview: 'A team of explorers travel through a wormhole in space in an attempt to ensure humanity\'s survival.',
      year: 2014,
      rating: 8.7,
      duration: 169,
      path: '/placeholder/interstellar',
    },
    {
      id: 'placeholder-4',
      sourceId: 'placeholder',
      name: 'The Batman',
      type: 'movie',
      tagline: 'Unmask the truth.',
      overview: 'Batman ventures into Gotham City\'s underworld when a sadistic killer leaves behind cryptic clues.',
      year: 2022,
      rating: 7.8,
      duration: 176,
      path: '/placeholder/the-batman',
    },
    {
      id: 'placeholder-5',
      sourceId: 'placeholder',
      name: 'Arrival',
      type: 'movie',
      tagline: 'Why are they here?',
      overview: 'A linguist works with the military to communicate with alien lifeforms after twelve mysterious spacecraft land.',
      year: 2016,
      rating: 7.9,
      duration: 116,
      path: '/placeholder/arrival',
    },
  ]

  const backdrops = [
    'linear-gradient(135deg, #0c1a2e 0%, #1a3a5c 40%, #2d1b4e 70%, #0a0a1a 100%)',
    'linear-gradient(135deg, #1a0a2e 0%, #2d1b6e 40%, #0a2a3a 70%, #0a0a1a 100%)',
    'linear-gradient(135deg, #0a1a0a 0%, #1a3a1a 40%, #2d4e1b 70%, #0a0a1a 100%)',
    'linear-gradient(135deg, #2e0a0a 0%, #4e1b1b 40%, #1a0a2e 70%, #0a0a1a 100%)',
    'linear-gradient(135deg, #0a2e2e 0%, #1b4e4e 40%, #2e0a2e 70%, #0a0a1a 100%)',
  ]

  return items.map((item, i) => ({
    ...item,
    backdropUrl: backdrops[i],
  }))
}
