<script setup lang="ts">
import type { DataSource, HomeSection, MediaItem, MediaLibrary } from '@/services/datasource/types'
import type { SourceBrowseNode } from '@/services/sourceBrowseContext'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import HeroCarousel from '@/components/media/HeroCarousel.vue'
import MediaGrid from '@/components/media/MediaGrid.vue'
import ServerLibraryUpdateNotice from '@/components/media/ServerLibraryUpdateNotice.vue'
import { requestAppScrollTop } from '@/services/appScroll'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { normalizeWorkLevelSearchResults } from '@/services/datasource/searchAggregation'
import { registerLayoutBackHandler } from '@/services/layoutBackNavigation'
import { createPlaybackQueue, savePlaybackMediaContext } from '@/services/playbackContext'
import { createPlaybackRouteQuery } from '@/services/playbackRoute'
import { loadSourceBrowseContext, saveSourceBrowseContext, sourceBrowseContextIdFromQuery } from '@/services/sourceBrowseContext'
import { useDataSourceStore } from '@/stores/datasource'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const sourceId = computed(() => String(route.params.sourceId ?? ''))
const sourceConfig = computed(() => store.orderedConfigs.find(config => config.id === sourceId.value))
const source = ref<DataSource | null>(null)
const libraries = ref<MediaLibrary[]>([])
const items = ref<MediaItem[]>([])
const sections = ref<HomeSection[]>([])
const selectedLibrary = ref<MediaLibrary | null>(null)
const navigationStack = ref<SourceBrowseNode[]>([])
const searchKeyword = ref('')
const isLoading = ref(false)
const isApplyingServerUpdate = ref(false)
const errorMessage = ref<string | null>(null)
const currentNode = computed(() => navigationStack.value.at(-1) ?? null)
const pageTitle = computed(() => currentNode.value?.name
  ?? sourceConfig.value?.displayName
  ?? sourceConfig.value?.name
  ?? '媒体库')
const heroItems = computed(() => sections.value.find(section => section.type === 'hero')?.items ?? [])
const historySection = computed<HomeSection | null>(() =>
  sections.value.find(section => section.type === 'continueWatching' && (section.items.length > 0 || section.viewAllRoute))
  ?? (sourceConfig.value?.type === 'server' && source.value?.listPlaybackHistory
    ? {
        id: `${sourceId.value}:history-navigation`,
        sourceId: sourceId.value,
        title: '继续观看',
        type: 'continueWatching',
        purpose: 'history',
        items: [],
        viewAllRoute: { kind: 'history', sourceId: sourceId.value },
      }
    : null),
)
const otherSections = computed(() => sections.value.filter(section => section.items.length > 0 && section.type !== 'hero' && section.type !== 'continueWatching'))
const pendingServerUpdate = computed(() => sourceConfig.value?.type === 'server'
  ? store.getPendingServerUpdate(sourceId.value)
  : null)
let loadGeneration = 0
let contextId: string | null = null
let unregisterBack: (() => void) | undefined

onMounted(async () => {
  store.loadConfigs()
  unregisterBack = registerLayoutBackHandler(Symbol('source-library'), backInPage)
  await initializeSource()
})

onBeforeUnmount(() => {
  loadGeneration++
  unregisterBack?.()
})

watch(sourceId, async () => {
  loadGeneration++
  contextId = null
  selectedLibrary.value = null
  navigationStack.value = []
  items.value = []
  libraries.value = []
  sections.value = []
  await initializeSource()
})

async function initializeSource() {
  source.value = null
  errorMessage.value = null
  if (!sourceConfig.value)
    return
  if (sourceConfig.value.enabled === false) {
    errorMessage.value = '该媒体来源已停用，请在设置中启用。'
    return
  }
  await store.syncManager()
  source.value = store.getSource(sourceId.value)
  if (!source.value) {
    errorMessage.value = store.lastError || '媒体来源不可用，请检查连接和登录状态。'
    return
  }
  await loadRoot()
  await restoreContext()
}

async function loadRoot(force = false): Promise<void> {
  const current = source.value
  if (!current)
    return
  const generation = ++loadGeneration
  const snapshot = store.getSourceRootSnapshot(sourceId.value)
  if (snapshot) {
    libraries.value = snapshot.libraries
    sections.value = snapshot.homeSections
  }
  if (!force && snapshot && store.isSourceRootSnapshotFresh(sourceId.value))
    return

  isLoading.value = !snapshot
  errorMessage.value = null
  const pending = pendingServerUpdate.value
  const librariesRequest = pending && current.listLibrariesForMediaChangeRefresh
    ? current.listLibrariesForMediaChangeRefresh()
    : current.listLibraries?.() ?? Promise.resolve([])
  const [libraryResult, homeResult] = await Promise.allSettled([
    librariesRequest,
    current.getHomeSections?.() ?? Promise.resolve([]),
  ])
  if (generation !== loadGeneration || current !== source.value)
    return
  if (libraryResult.status === 'fulfilled')
    libraries.value = libraryResult.value
  else
    errorMessage.value = toSafeErrorMessage(libraryResult.reason, '媒体库加载失败。')
  if (homeResult.status === 'fulfilled')
    sections.value = homeResult.value
  if (libraryResult.status === 'fulfilled' || homeResult.status === 'fulfilled')
    store.setSourceRootSnapshot(sourceId.value, { libraries: libraries.value, homeSections: sections.value })
  if (libraryResult.status === 'fulfilled' && pending)
    store.acknowledgeServerUpdate(pending)
  isLoading.value = false
}

async function loadChildren(id: string) {
  const current = source.value
  if (!current)
    return
  const generation = ++loadGeneration
  isLoading.value = true
  errorMessage.value = null
  try {
    const next = await current.list(id)
    if (generation !== loadGeneration || current !== source.value)
      return
    items.value = next
    const pending = pendingServerUpdate.value
    const libraryId = selectedLibrary.value?.id
    if (pending && libraryId && /^\d+$/.test(libraryId))
      store.acknowledgeServerUpdate(pending, [libraryId])
  }
  catch (error) {
    if (generation === loadGeneration) {
      items.value = []
      errorMessage.value = toSafeErrorMessage(error, '媒体条目加载失败。')
    }
  }
  finally {
    if (generation === loadGeneration)
      isLoading.value = false
  }
}

async function selectLibrary(library: MediaLibrary) {
  selectedLibrary.value = library
  navigationStack.value = [{ id: library.id, name: library.name, type: library.type }]
  searchKeyword.value = ''
  requestAppScrollTop()
  await loadChildren(library.id)
  await persistContext()
}

async function selectItem(item: MediaItem | MediaLibrary) {
  if (!('path' in item)) {
    await selectLibrary(item)
    return
  }
  if (item.type === 'folder' || item.type === 'season') {
    if (!selectedLibrary.value)
      selectedLibrary.value = { id: item.id, sourceId: item.sourceId, name: item.name, type: 'folders' }
    navigationStack.value = [...navigationStack.value, { id: item.id, name: item.name, type: item.type }]
    requestAppScrollTop()
    await loadChildren(item.id)
    await persistContext()
    return
  }
  await openDetail(item)
}

async function runSearch() {
  const keyword = searchKeyword.value.trim()
  if (!keyword) {
    if (selectedLibrary.value)
      await loadChildren(navigationStack.value.at(-1)?.id ?? selectedLibrary.value.id)
    return
  }
  if (!source.value)
    return
  const generation = ++loadGeneration
  isLoading.value = true
  errorMessage.value = null
  try {
    const result = normalizeWorkLevelSearchResults(await source.value.search(keyword))
    if (generation !== loadGeneration)
      return
    items.value = result
    selectedLibrary.value = { id: 'search', sourceId: sourceId.value, name: `搜索：${keyword}`, type: 'mixed' }
    navigationStack.value = [{ id: 'search', name: `搜索：${keyword}`, type: 'mixed', isSearch: true }]
    requestAppScrollTop()
    await persistContext()
  }
  catch (error) {
    if (generation === loadGeneration)
      errorMessage.value = toSafeErrorMessage(error, '搜索失败。')
  }
  finally {
    if (generation === loadGeneration)
      isLoading.value = false
  }
}

async function navigateToCrumb(index: number) {
  const crumb = navigationStack.value[index]
  if (!crumb || crumb.isSearch)
    return
  navigationStack.value = navigationStack.value.slice(0, index + 1)
  searchKeyword.value = ''
  await loadChildren(crumb.id)
  await persistContext()
}

async function backInPage(): Promise<boolean> {
  if (!selectedLibrary.value)
    return false
  if (navigationStack.value.length > 1)
    await navigateToCrumb(navigationStack.value.length - 2)
  else
    backToLibraries()
  return true
}

function backToLibraries() {
  selectedLibrary.value = null
  navigationStack.value = []
  items.value = []
  searchKeyword.value = ''
  requestAppScrollTop()
  void persistContext()
}

async function openDetail(item: MediaItem) {
  await persistContext(true)
  const queue = createPlaybackQueue(items.value, item.id)
  const id = queue ? savePlaybackMediaContext({ sourceId: sourceId.value, itemId: item.id, title: item.name, queue }) : undefined
  await router.push({
    name: 'media-detail',
    params: { sourceId: sourceId.value, itemId: item.id },
    query: id ? { contextId: id } : undefined,
  })
}

async function handlePlay(item: MediaItem) {
  if (item.type === 'folder' || item.type === 'season' || item.type === 'series') {
    await openDetail(item)
    return
  }
  await persistContext(true)
  const id = savePlaybackMediaContext({
    sourceId: sourceId.value,
    itemId: item.id,
    title: item.name,
    currentItem: item,
    queue: createPlaybackQueue(items.value, item.id),
  })
  await router.push({
    name: 'player',
    query: createPlaybackRouteQuery({ sourceId: sourceId.value, itemId: item.id, contextId: id }),
  })
}

async function refreshCurrentServerView() {
  const pending = pendingServerUpdate.value
  if (!pending || !source.value || isApplyingServerUpdate.value)
    return
  isApplyingServerUpdate.value = true
  errorMessage.value = null
  try {
    source.value.clearCache?.()
    store.invalidateSourceRootSnapshot(sourceId.value)
    if (selectedLibrary.value)
      await loadChildren(currentNode.value?.id ?? selectedLibrary.value.id)
    else
      await loadRoot(true)
    const libraryId = selectedLibrary.value?.id
    store.acknowledgeServerUpdate(pending, libraryId && /^\d+$/.test(libraryId) ? [libraryId] : undefined)
  }
  finally {
    isApplyingServerUpdate.value = false
  }
}

async function persistContext(captureScroll = false) {
  if (route.name !== 'source')
    return
  contextId = saveSourceBrowseContext({
    sourceId: sourceId.value,
    selectedLibrary: selectedLibrary.value,
    navigationStack: navigationStack.value,
    searchKeyword: searchKeyword.value,
    scrollTop: captureScroll ? document.querySelector<HTMLElement>('main.cinema-scrollbar')?.scrollTop ?? 0 : 0,
  }, contextId)
  await router.replace({ name: 'source', params: { sourceId: sourceId.value }, query: { ...route.query, browseContextId: contextId } })
}

async function restoreContext() {
  contextId = sourceBrowseContextIdFromQuery(route.query.browseContextId)
  const context = loadSourceBrowseContext(contextId, sourceId.value)
  if (!context?.selectedLibrary)
    return
  selectedLibrary.value = context.selectedLibrary
  navigationStack.value = [...context.navigationStack]
  searchKeyword.value = context.searchKeyword
  if (context.searchKeyword.trim() && currentNode.value?.isSearch)
    await runSearch()
  else
    await loadChildren(currentNode.value?.id ?? context.selectedLibrary.id)
  if (context.scrollTop > 0)
    requestAnimationFrame(() => document.querySelector<HTMLElement>('main.cinema-scrollbar')?.scrollTo({ top: context.scrollTop }))
}

async function openHomeSection(section: HomeSection) {
  const target = section.viewAllRoute
  if (!target)
    return
  if (target.kind === 'history') {
    await router.push({ name: 'history', query: { sourceId: target.sourceId, ...(target.libraryId ? { libraryId: target.libraryId } : {}) } })
    return
  }
  await selectItem({ id: target.path, sourceId: sourceId.value, name: section.title, type: 'folder', path: '' })
}
</script>

<template>
  <div class="source-page-content mobile-nav-safe space-y-8 px-4 pb-8 pt-20 sm:p-6 sm:pl-20 sm:pt-20">
    <ServerLibraryUpdateNotice
      :visible="pendingServerUpdate != null"
      :busy="isApplyingServerUpdate"
      @refresh="refreshCurrentServerView"
    />
    <div v-if="!sourceConfig" class="glass-panel p-8 text-center text-white/65">
      媒体来源不存在。请在设置中重新添加。
    </div>
    <template v-else>
      <section v-if="!selectedLibrary && heroItems.length" class="-mx-4 -mt-20 overflow-hidden rounded-b-[2.4rem] sm:-mx-6 md:-ml-20">
        <HeroCarousel :items="heroItems" @play="handlePlay" @detail="openDetail" />
      </section>
      <div class="flex flex-wrap items-center justify-between gap-4">
        <div>
          <p class="text-xs uppercase tracking-[0.2em] text-white/40">
            媒体来源
          </p>
          <h1 class="mt-1 text-2xl font-bold text-white">
            {{ pageTitle }}
          </h1>
        </div>
        <form class="flex min-w-72 gap-2" @submit.prevent="runSearch">
          <input v-model="searchKeyword" class="min-w-0 flex-1 rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none" placeholder="搜索媒体">
          <button class="rounded-2xl bg-primary px-4 py-3 text-sm font-semibold text-black">
            搜索
          </button>
        </form>
      </div>
      <div v-if="selectedLibrary" class="flex flex-wrap items-center gap-3 text-sm">
        <button class="rounded-2xl bg-white/8 px-4 py-2 text-white/70" @click="backToLibraries">
          媒体库
        </button>
        <template v-for="(crumb, index) in navigationStack" :key="`${crumb.id}-${index}`">
          <span class="text-white/35">/</span>
          <button :disabled="index === navigationStack.length - 1 || crumb.isSearch" class="text-white/70 disabled:text-white/35" @click="navigateToCrumb(index)">
            {{ crumb.name }}
          </button>
        </template>
      </div>
      <p v-if="errorMessage" role="alert" class="rounded-2xl border border-red-400/25 bg-red-400/10 p-4 text-sm text-red-200">
        {{ errorMessage }}
      </p>
      <template v-if="!selectedLibrary">
        <section v-if="libraries.length" class="space-y-4">
          <h2 class="text-xl font-semibold text-white">
            媒体库
          </h2>
          <MediaGrid :items="libraries" :loading="isLoading" @select="selectItem" @play="handlePlay" />
        </section>
        <section v-if="historySection" class="space-y-4">
          <div class="flex items-center justify-between gap-3">
            <h2 class="text-xl font-semibold text-white">
              {{ historySection.title }}
            </h2>
            <button v-if="historySection.viewAllRoute" class="text-sm text-primary" @click="openHomeSection(historySection)">
              查看完整历史
            </button>
          </div>
          <MediaGrid v-if="historySection.items.length" :items="historySection.items" @select="selectItem" @play="handlePlay" />
        </section>
        <section v-for="section in otherSections" :key="section.id" class="space-y-4">
          <div class="flex items-center justify-between gap-3">
            <h2 class="text-xl font-semibold text-white">
              {{ section.title }}
            </h2>
            <button v-if="section.viewAllRoute" class="text-sm text-primary" @click="openHomeSection(section)">
              查看全部
            </button>
          </div>
          <MediaGrid :items="section.items" @select="selectItem" @play="handlePlay" />
        </section>
        <MediaGrid v-if="!libraries.length && !otherSections.length && !historySection" :items="[]" :loading="isLoading" empty-title="暂无媒体库" empty-description="请检查来源连接和媒体库权限。" />
      </template>
      <section v-else class="space-y-4">
        <h2 class="text-xl font-semibold text-white">
          {{ selectedLibrary.name }}
        </h2>
        <MediaGrid :items="items" :loading="isLoading" empty-title="暂无媒体项目" empty-description="当前目录没有可显示的媒体。" @select="selectItem" @play="handlePlay" />
      </section>
    </template>
  </div>
</template>
