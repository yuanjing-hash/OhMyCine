<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { mediaCatalogEndpoint, mediaCatalogOpenTargets, mediaCatalogPageCount, mediaCatalogPageSizes, type MediaCatalogPageSize, type MediaCatalogTypeFilter } from '@/media-catalog'
import type { ListResponse, MediaCatalogItem, MediaCatalogLibraryWork, MediaCatalogPage, MediaLibraryDetail } from '@/types/api'

const router = useRouter()
const libraries = ref<MediaLibraryDetail[]>([])
const selectedLibrary = ref<number | null>(null)
const mediaType = ref<MediaCatalogTypeFilter>('')
const category = ref('')
const query = ref('')
const page = ref(1)
const pageSize = ref<MediaCatalogPageSize>(20)
const result = ref<MediaCatalogPage>({ list: [], total: 0, page: 1, page_size: 20, categories: [] })
const loading = ref(true)
const error = ref('')
const pendingSelection = ref<{ title: string; works: MediaCatalogLibraryWork[] } | null>(null)
let controller: AbortController | null = null

const categories = computed(() => result.value.categories ?? [])
const pages = computed(() => mediaCatalogPageCount(result.value.total, pageSize.value))

async function loadLibraries() {
  const response = await api<ListResponse<MediaLibraryDetail>>('/api/v1/media-libraries')
  libraries.value = response.list.filter(item => item.enabled)
}

async function load() {
  controller?.abort(); controller = new AbortController(); loading.value = true; error.value = ''
  try {
    result.value = await api<MediaCatalogPage>(mediaCatalogEndpoint(selectedLibrary.value, { page: page.value, pageSize: pageSize.value, query: query.value, mediaType: mediaType.value, matchStatus: '', category: category.value }), { signal: controller.signal })
  } catch (reason) {
    if (reason instanceof DOMException && reason.name === 'AbortError') return
    error.value = reason instanceof Error ? reason.message : '媒体库内容加载失败'
  } finally { loading.value = false }
}

function selectLibrary(id: number | null) { category.value = ''; selectedLibrary.value = id }
function applySearch() { page.value = 1; void load() }
function open(item: MediaCatalogItem) {
  const targets = mediaCatalogOpenTargets(item, selectedLibrary.value)
  if (targets.length === 1) return openTarget(targets[0])
  if (targets.length > 1) pendingSelection.value = { title: item.title, works: targets }
}
function openTarget(target: MediaCatalogLibraryWork) { pendingSelection.value = null; void router.push({ name: 'library-catalog-detail', params: { libraryID: String(target.library_id), workID: target.work_id } }) }
function formatBytes(value: number) { if (value < 1024) return `${value} B`; const units = ['KB', 'MB', 'GB', 'TB']; let size = value / 1024; let index = 0; while (size >= 1024 && index < units.length - 1) { size /= 1024; index++ }; return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[index]}` }

watch([selectedLibrary, mediaType, category, pageSize], () => { page.value = 1; void load() })
watch(page, load)
onMounted(async () => { try { await loadLibraries(); await load() } catch (reason) { error.value = reason instanceof Error ? reason.message : '媒体库加载失败'; loading.value = false } })
</script>

<template>
  <section class="space-y-5">
    <header><p class="text-xs font-700 uppercase tracking-widest text-[var(--text-subtle)]">Library</p><h1 class="mt-1 text-2xl font-800">媒体库</h1><p class="page-description mt-1">浏览 Server 已扫描入库的真实作品；“全部库”按可信 TMDB 身份去重分页。</p></header>
    <nav class="panel flex gap-2 overflow-x-auto p-2" aria-label="选择媒体库">
      <button class="btn-secondary shrink-0" :class="{ '!border-[var(--accent)] !bg-[var(--accent-soft)] !text-[var(--accent)]': selectedLibrary == null }" @click="selectLibrary(null)">全部库</button>
      <button v-for="library in libraries" :key="library.id" class="btn-secondary shrink-0" :class="{ '!border-[var(--accent)] !bg-[var(--accent-soft)] !text-[var(--accent)]': selectedLibrary === library.id }" @click="selectLibrary(library.id)">{{ library.name }}</button>
    </nav>
    <div class="panel grid gap-3 md:grid-cols-[minmax(12rem,1fr)_auto_auto_auto]">
      <form class="flex gap-2" @submit.prevent="applySearch"><input v-model="query" class="input min-w-0 flex-1" maxlength="200" placeholder="搜索标题" aria-label="搜索媒体库标题"><button class="btn-primary">搜索</button></form>
      <select v-model="mediaType" class="input" aria-label="媒体类型"><option value="">全部类型</option><option value="movie">电影</option><option value="series">电视剧</option></select>
      <select v-model="category" class="input" aria-label="媒体分类"><option value="">全部分类</option><option v-for="item in categories" :key="item" :value="item">{{ item }}</option></select>
      <select v-model="pageSize" class="input" aria-label="每页作品数"><option v-for="size in mediaCatalogPageSizes" :key="size" :value="size">每页 {{ size }}</option></select>
    </div>
    <div v-if="loading" class="panel py-14 text-center text-muted">正在读取媒体库海报墙…</div>
    <div v-else-if="error" class="semantic-error p-4"><strong>媒体库暂时不可用</strong><p class="mt-1 text-sm">{{ error }}</p><button class="btn-secondary mt-3" @click="load">重试</button></div>
    <template v-else>
      <div v-if="result.list.length" class="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5 2xl:grid-cols-7">
        <button v-for="item in result.list" :key="item.id" class="discovery-poster text-left" @click="open(item)">
          <div class="discovery-poster__image"><img v-if="item.poster_url" :src="item.poster_url" :alt="`${item.title} 海报`" loading="lazy"><span v-else>暂无海报</span></div>
          <strong :title="item.title">{{ item.title }}</strong>
          <small>{{ item.kind === 'series' ? `${item.season_count} 季 · ${item.episode_count} 集` : formatBytes(item.size) }}</small>
          <small>{{ item.library_works.length }} 个库 · {{ item.match_status === 'matched' ? '已匹配' : '待识别' }}</small>
        </button>
      </div>
      <div v-else class="panel py-14 text-center text-muted">当前筛选下没有已入库作品。</div>
      <footer v-if="result.total" class="panel flex flex-wrap items-center justify-between gap-3 py-3"><span class="text-sm text-muted">共 {{ result.total }} 部作品</span><div class="flex items-center gap-2"><button class="btn-secondary" :disabled="page <= 1" @click="page--">上一页</button><span class="text-sm">{{ page }} / {{ pages }}</span><button class="btn-secondary" :disabled="page >= pages" @click="page++">下一页</button></div></footer>
    </template>
    <div v-if="pendingSelection" class="modal-backdrop" @click.self="pendingSelection = null">
      <section class="panel modal-panel" role="dialog" aria-modal="true" aria-labelledby="library-selection-title">
        <header class="flex items-center justify-between gap-3"><div><h2 id="library-selection-title" class="m-0 text-lg">选择媒体库</h2><p class="page-description mt-1 text-sm">《{{ pendingSelection.title }}》存在于多个媒体库。请选择要查看和管理的单个库。</p></div><button class="btn-secondary" aria-label="关闭媒体库选择" @click="pendingSelection = null">关闭</button></header>
        <div class="mt-4 grid gap-2"><button v-for="work in pendingSelection.works" :key="`${work.library_id}:${work.work_id}`" class="semantic-list-item flex items-center justify-between gap-3 p-4 text-left" @click="openTarget(work)"><strong>{{ work.library_name }}</strong><span class="status-chip">{{ work.file_count }} 个文件</span></button></div>
      </section>
    </div>
  </section>
</template>

<style scoped>
.modal-backdrop { position: fixed; inset: 0; z-index: 80; display: grid; place-items: center; padding: 1rem; background: color-mix(in srgb, #000 64%, transparent); }
.modal-panel { width: min(36rem, 100%); }
</style>
