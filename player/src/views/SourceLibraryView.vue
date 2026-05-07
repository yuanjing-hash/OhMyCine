<script setup lang="ts">
import type { DataSource, MediaItem, MediaLibrary } from '@/services/datasource/types'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import MediaGrid from '@/components/media/MediaGrid.vue'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { useDataSourceStore } from '@/stores/datasource'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()

const sourceId = computed(() => route.params.sourceId as string)
const sourceConfig = computed(() =>
  store.configs.find(c => c.id === sourceId.value),
)
const source = ref<DataSource | null>(null)
const libraries = ref<MediaLibrary[]>([])
const items = ref<MediaItem[]>([])
const selectedLibrary = ref<MediaLibrary | null>(null)
const searchKeyword = ref('')
const isLoading = ref(false)
const errorMessage = ref<string | null>(null)

const displayItems = computed(() => selectedLibrary.value ? items.value : libraries.value)
const pageTitle = computed(() => selectedLibrary.value?.name ?? (sourceConfig.value?.displayName ?? sourceConfig.value?.name ?? 'Data Source'))

onMounted(async () => {
  store.loadConfigs()
  await ensureSource()
  await loadLibraries()
})

watch(sourceId, async () => {
  selectedLibrary.value = null
  items.value = []
  await ensureSource()
  await loadLibraries()
})

async function ensureSource() {
  await store.syncManager()
  source.value = store.getSource(sourceId.value)
}

async function loadLibraries() {
  if (!source.value)
    return

  isLoading.value = true
  errorMessage.value = null
  try {
    libraries.value = source.value.listLibraries
      ? await source.value.listLibraries()
      : []
  }
  catch (error) {
    libraries.value = []
    errorMessage.value = toSafeErrorMessage(error, '媒体库加载失败。')
  }
  finally {
    isLoading.value = false
  }
}

async function loadLibrary(library: MediaLibrary) {
  if (!source.value)
    return

  selectedLibrary.value = library
  searchKeyword.value = ''
  isLoading.value = true
  errorMessage.value = null
  try {
    items.value = await source.value.list(library.id)
  }
  catch (error) {
    items.value = []
    errorMessage.value = toSafeErrorMessage(error, '媒体条目加载失败。')
  }
  finally {
    isLoading.value = false
  }
}

async function runSearch() {
  if (!source.value)
    return

  const keyword = searchKeyword.value.trim()
  if (!keyword) {
    if (selectedLibrary.value)
      await loadLibrary(selectedLibrary.value)
    return
  }

  isLoading.value = true
  errorMessage.value = null
  try {
    items.value = await source.value.search(keyword)
    selectedLibrary.value = {
      id: 'search',
      sourceId: sourceId.value,
      name: `搜索：${keyword}`,
      type: 'mixed',
    }
  }
  catch (error) {
    items.value = []
    errorMessage.value = toSafeErrorMessage(error, '搜索失败。')
  }
  finally {
    isLoading.value = false
  }
}

function backToLibraries() {
  selectedLibrary.value = null
  items.value = []
  searchKeyword.value = ''
}

async function handleSelect(item: MediaItem | MediaLibrary) {
  if ('path' in item) {
    if (item.type === 'folder' || item.type === 'series') {
      selectedLibrary.value = {
        id: item.id,
        sourceId: item.sourceId,
        name: item.name,
        type: item.type === 'series' ? 'series' : 'folders',
      }
      await loadNestedItems(item.id)
    }
    return
  }

  await loadLibrary(item)
}

async function loadNestedItems(parentId: string) {
  if (!source.value)
    return

  isLoading.value = true
  errorMessage.value = null
  try {
    items.value = await source.value.list(parentId)
  }
  catch (error) {
    items.value = []
    errorMessage.value = toSafeErrorMessage(error, '子项目加载失败。')
  }
  finally {
    isLoading.value = false
  }
}

async function handlePlay(item: MediaItem) {
  if (!source.value)
    return

  isLoading.value = true
  errorMessage.value = null
  try {
    const streamUrl = await source.value.getStreamURL(item.id)
    await router.push({
      name: 'player',
      query: {
        title: item.name,
        path: streamUrl,
        sourceId: sourceId.value,
        itemId: item.id,
      },
    })
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, '无法获取播放地址。')
  }
  finally {
    isLoading.value = false
  }
}
</script>

<template>
  <div class="source-view relative min-h-full">
    <div class="space-y-8 p-6 pl-20 pt-16">
      <div v-if="!sourceConfig" class="flex flex-col items-center justify-center py-24">
        <p class="text-lg text-white/40">
          Data source not found
        </p>
        <button
          class="mt-4 rounded-full bg-white/10 px-6 py-2 text-sm text-white/70 transition-colors hover:bg-white/20"
          @click="router.push('/')"
        >
          Back to Home
        </button>
      </div>

      <template v-else>
        <div class="flex flex-wrap items-center justify-between gap-4">
          <div class="flex items-center gap-4">
            <div class="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/18 text-lg font-bold text-primary">
              <img v-if="sourceConfig.iconUrl" :src="sourceConfig.iconUrl" class="h-8 w-8 rounded" :alt="sourceConfig.name">
              <span v-else>{{ sourceConfig.type[0].toUpperCase() }}</span>
            </div>
            <div>
              <p class="text-xs uppercase tracking-[0.24em] text-white/34">
                {{ sourceConfig.type }} source
              </p>
              <h1 class="mt-1 text-2xl font-bold text-white">
                {{ pageTitle }}
              </h1>
            </div>
          </div>

          <form class="flex min-w-72 gap-2" @submit.prevent="runSearch">
            <input
              v-model="searchKeyword"
              class="min-w-0 flex-1 rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none placeholder:text-white/25 focus:border-primary/60"
              placeholder="搜索 Emby 媒体"
            >
            <button class="rounded-2xl bg-white/10 px-4 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/16">
              搜索
            </button>
          </form>
        </div>

        <div v-if="selectedLibrary" class="flex items-center gap-3">
          <button
            class="rounded-2xl bg-white/8 px-4 py-2 text-sm text-white/70 transition-colors hover:bg-white/14"
            @click="backToLibraries"
          >
            返回媒体库
          </button>
          <p class="text-sm text-white/36">
            {{ selectedLibrary.name }}
          </p>
        </div>

        <div
          v-if="errorMessage"
          class="rounded-2xl border border-red-400/20 bg-red-400/10 px-5 py-4 text-sm text-red-100"
        >
          {{ errorMessage }}
        </div>

        <section>
          <div class="mb-4 flex items-end justify-between">
            <div>
              <h2 class="text-xl font-bold text-white">
                {{ selectedLibrary ? '媒体项目' : '媒体库' }}
              </h2>
              <p class="mt-1 text-sm text-white/36">
                {{ selectedLibrary ? '选择视频条目即可进入现有播放加载流程。' : '选择一个 Emby 媒体库开始浏览。' }}
              </p>
            </div>
          </div>

          <MediaGrid
            :items="displayItems"
            :loading="isLoading"
            :empty-title="selectedLibrary ? '此媒体库暂无可显示项目' : '未找到 Emby 媒体库'"
            :empty-description="selectedLibrary ? '请检查 Emby 用户权限、媒体库内容或搜索条件。' : '请确认设置中的 URL、用户 ID 和访问令牌有效。'"
            @select="handleSelect"
            @play="handlePlay"
          />
        </section>
      </template>
    </div>
  </div>
</template>

<style scoped>
.source-view {
  background: var(--color-bg);
}
</style>
