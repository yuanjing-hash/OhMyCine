<script setup lang="ts">
import type { MediaItem } from '@/services/datasource/types'
import { computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import HeroCarousel from '@/components/media/HeroCarousel.vue'
import { useDataSourceStore } from '@/stores/datasource'

const router = useRouter()
const store = useDataSourceStore()

const hasSources = computed(() => store.configs.length > 0)
const heroSection = computed(() => store.homeSections.find(s => s.type === 'hero'))
const continueWatchingSection = computed(() => store.homeSections.find(s => s.type === 'continueWatching'))
const recentlyAddedSection = computed(() => store.homeSections.find(s => s.type === 'recentlyAdded'))
const heroItems = computed(() => heroSection.value?.items ?? [])
const recentlyAddedItems = computed(() => recentlyAddedSection.value?.items.slice(0, 6) ?? [])

onMounted(() => {
  store.loadConfigs()
  store.loadHomeSections()
})

function goToSettings() {
  void router.push({ name: 'settings', query: { section: 'datasources' } })
}

async function handlePlay(item: MediaItem) {
  const source = store.getSource(item.sourceId)
  const path = source && item.sourceId !== 'placeholder'
    ? await source.getStreamURL(item.id)
    : item.path

  await router.push({
    name: 'player',
    query: {
      title: item.name,
      path,
      sourceId: item.sourceId,
      itemId: item.id,
    },
  })
}

function handleDetail(item: MediaItem) {
  if (item.sourceId === 'placeholder')
    return
  void router.push({ name: 'media-detail', params: { sourceId: item.sourceId, itemId: item.id } })
}
</script>

<template>
  <div class="home-view relative min-h-full transition-colors duration-500">
    <div class="flex min-h-screen flex-col gap-6 px-4 pb-6 sm:gap-8 sm:px-6 lg:px-8">
      <section class="relative -mx-4 overflow-hidden rounded-b-[2rem] sm:-mx-6 lg:-mx-8">
        <HeroCarousel
          v-if="heroItems.length"
          :items="heroItems"
          @play="handlePlay"
          @detail="handleDetail"
        />

        <div
          v-if="!hasSources"
          class="pointer-events-none absolute inset-0 flex items-center justify-end p-4 sm:p-8 lg:p-10"
        >
          <div class="glass-panel pointer-events-auto w-full max-w-sm rounded-3xl p-5 sm:p-6 lg:max-w-md">
            <p class="text-xs font-medium uppercase tracking-[0.3em]" style="color: var(--gp-text)">
              OhMyCine Player
            </p>
            <h1 class="mt-3 text-2xl font-bold" style="color: var(--gp-text-full)">
              添加你的第一个影视库
            </h1>
            <p class="mt-3 text-sm leading-6" style="color: var(--gp-text)">
              绑定 Emby、Jellyfin、OpenList/Alist、CloudDrive2 或本地文件后，这里会聚合展示海报轮播、继续观看和最新影片。
            </p>
            <button
              class="mt-5 rounded-2xl px-5 py-3 text-sm font-semibold transition-colors"
              style="color: var(--gp-text-full); background: var(--gp-active)"
              @click="goToSettings"
            >
              去设置数据源
            </button>
          </div>
        </div>
      </section>

      <div class="grid grid-cols-1 gap-6 pb-8 xl:grid-cols-2">
        <section class="glass-panel rounded-[1.75rem] p-6">
          <div class="mb-5 flex items-center justify-between">
            <div>
              <p class="text-xs uppercase tracking-[0.24em]" style="color: var(--gp-text-dim)">
                Resume
              </p>
              <h2 class="mt-1 text-xl font-bold" style="color: var(--gp-text-full)">
                继续观看
              </h2>
            </div>
            <button class="text-xs transition-colors" style="color: var(--gp-text)">
              全部 >
            </button>
          </div>

          <div v-if="continueWatchingSection?.items.length" class="flex gap-3 overflow-x-auto cinema-scrollbar">
            <article
              v-for="item in continueWatchingSection.items"
              :key="item.id"
              class="group w-48 flex-shrink-0 cursor-pointer overflow-hidden rounded-2xl transition-transform hover:scale-[1.03]"
              @click="handleDetail(item)"
            >
              <div class="relative h-28 media-placeholder">
                <img v-if="item.backdropUrl" :src="item.backdropUrl" :alt="item.name" class="h-full w-full object-cover" loading="lazy" decoding="async">
                <div class="progress-track absolute bottom-0 left-0 right-0 h-1">
                  <div class="progress-value h-full w-1/3 rounded-full" />
                </div>
              </div>
              <h3 class="truncate px-2 py-3 text-sm font-medium" style="color: var(--gp-text-full)">
                {{ item.name }}
              </h3>
            </article>
          </div>

          <div v-else class="flex h-40 flex-col items-center justify-center rounded-3xl empty-panel text-center">
            <p class="text-sm font-medium" style="color: var(--gp-text-full)">
              还没有观看记录
            </p>
            <p class="mt-2 max-w-xs text-xs leading-5" style="color: var(--gp-text)">
              添加影视库并开始播放后，播放进度会出现在这里。
            </p>
          </div>
        </section>

        <section class="glass-panel rounded-[1.75rem] p-6">
          <div class="mb-5 flex items-center justify-between">
            <div>
              <p class="text-xs uppercase tracking-[0.24em]" style="color: var(--gp-text-dim)">
                Library
              </p>
              <h2 class="mt-1 text-xl font-bold" style="color: var(--gp-text-full)">
                最新影片
              </h2>
            </div>
            <button
              class="text-xs transition-colors disabled:opacity-30"
              style="color: var(--gp-text)"
              :disabled="!recentlyAddedSection"
              @click="recentlyAddedSection && router.push(`/source/${recentlyAddedSection.sourceId}`)"
            >
              浏览全部 >
            </button>
          </div>

          <div v-if="recentlyAddedItems.length" class="flex gap-4 overflow-x-auto cinema-scrollbar">
            <article
              v-for="item in recentlyAddedItems"
              :key="item.id"
              class="group w-28 flex-shrink-0 cursor-pointer overflow-hidden rounded-2xl transition-transform hover:scale-[1.04]"
              @click="handleDetail(item)"
            >
              <div class="relative aspect-[2/3] media-placeholder">
                <img v-if="item.posterUrl" :src="item.posterUrl" :alt="item.name" class="h-full w-full object-cover" loading="lazy" decoding="async">
                <div v-else class="poster-placeholder flex h-full items-center justify-center">
                  <svg width="28" height="28" viewBox="0 0 24 24" fill="none">
                    <rect x="3" y="3" width="18" height="18" rx="3" stroke="currentColor" stroke-width="1.5" />
                  </svg>
                </div>
                <div class="absolute inset-0 flex items-center justify-center bg-black/55 opacity-0 transition-opacity group-hover:opacity-100">
                  <button
                    class="flex h-9 w-9 items-center justify-center rounded-full bg-white text-black transition-transform hover:scale-110"
                    @click.stop="handlePlay(item)"
                  >
                    <svg width="12" height="12" viewBox="0 0 14 14" fill="currentColor">
                      <path d="M3 1l9 6-9 6V1z" />
                    </svg>
                  </button>
                </div>
              </div>
              <h3 class="truncate px-1 py-2 text-xs font-medium" style="color: var(--gp-text-full)">
                {{ item.name }}
              </h3>
            </article>
          </div>

          <div v-else class="flex h-40 flex-col items-center justify-center rounded-3xl empty-panel text-center">
            <p class="text-sm font-medium" style="color: var(--gp-text-full)">
              等待影视库内容
            </p>
            <p class="mt-2 max-w-xs text-xs leading-5" style="color: var(--gp-text)">
              配置数据源后，最新入库和推荐内容会在这里横向展示。
            </p>
            <button
              v-if="!hasSources"
              class="mt-4 rounded-2xl px-4 py-2 text-xs font-semibold transition-colors"
              style="color: var(--gp-text-full); background: var(--gp-hover)"
              @click="goToSettings"
            >
              添加数据源
            </button>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.home-view {
  background: var(--color-bg);
  color: var(--color-text);
}

.empty-panel {
  border: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 42%, transparent);
}

.media-placeholder {
  background: color-mix(in srgb, var(--color-surface) 42%, transparent);
}

.poster-placeholder {
  color: var(--color-text-tertiary);
}

.progress-track {
  background: var(--color-surface-hover);
}

.progress-value {
  background: color-mix(in srgb, var(--color-text) 60%, transparent);
}
</style>
