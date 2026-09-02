<script setup lang="ts">
import type { ServerAcquisitionStatus } from '@/services/serverDiscovery'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ServerDataSource } from '@/services/datasource/server'
import { getServerAcquisitions } from '@/services/serverDiscovery'
import { useAcquisitionWorkspaceStore } from '@/stores/acquisitionWorkspace'
import { useDataSourceStore } from '@/stores/datasource'

interface WorkspaceItem extends ServerAcquisitionStatus { sourceId: string, sourceName: string }

const router = useRouter()
const route = useRoute()
const workspace = useAcquisitionWorkspaceStore()
const sources = useDataSourceStore()
const items = ref<WorkspaceItem[]>([])
const loading = ref(false)
const error = ref('')
let refreshTimer: number | undefined
let generation = 0

const activeCount = computed(() => items.value.filter(item => !isTerminal(item)).length)

watch(() => workspace.open, (open) => {
  if (open)
    void refresh(true)
  else
    stopTimer()
})

onBeforeUnmount(stopTimer)

async function refresh(showLoading = false) {
  const current = ++generation
  stopTimer()
  if (showLoading)
    loading.value = true
  error.value = ''
  try {
    sources.loadConfigs()
    await sources.syncManager()
    const serverSources = sources.orderedConfigs.flatMap((config) => {
      const source = sources.getSource(config.id)
      return config.type === 'server' && config.enabled !== false && source instanceof ServerDataSource ? [{ config, source }] : []
    })
    if (!serverSources.length) {
      error.value = '还没有连接可用的 OhMyCine Server。'
      items.value = []
      return
    }
    const pages = await Promise.all(serverSources.map(async ({ config, source }) => {
      try {
        const capabilities = new Set(await source.refreshCapabilities())
        if (!capabilities.has('discovery_search'))
          return { items: [] as WorkspaceItem[], error: `${config.displayName ?? config.name}：当前账号没有影视搜索权限` }
        const page = await getServerAcquisitions(source)
        return { items: page.list.map(item => ({ ...item, sourceId: config.id, sourceName: config.displayName ?? config.name })), error: '' }
      }
      catch {
        return { items: [] as WorkspaceItem[], error: `${config.displayName ?? config.name}：无法读取任务，请确认 Server 已更新` }
      }
    }))
    if (current !== generation)
      return
    items.value = pages.flatMap(page => page.items).sort((left, right) => (Date.parse(right.updatedAt ?? '') || 0) - (Date.parse(left.updatedAt ?? '') || 0))
    const failures = pages.map(page => page.error).filter(Boolean)
    if (failures.length)
      error.value = failures.join('；')
  }
  catch (reason) {
    if (current === generation)
      error.value = reason instanceof Error ? reason.message : '读取入库任务失败'
  }
  finally {
    if (current === generation) {
      loading.value = false
      if (workspace.open && activeCount.value > 0)
        refreshTimer = window.setTimeout(() => void refresh(), 2500)
    }
  }
}

function stopTimer() {
  if (refreshTimer != null) {
    window.clearTimeout(refreshTimer)
    refreshTimer = undefined
  }
}

function isTerminal(item: ServerAcquisitionStatus) {
  return ['completed', 'failed', 'cancelled', 'canceled'].includes(item.status) || item.stage === 'library'
}

function stageLabel(item: ServerAcquisitionStatus) {
  const labels: Record<string, string> = { idle: '尚未开始', subscription: '订阅中', download: '下载中', organize: '整理中', transfer: '传输中', import: '刮削入库中', library: '已入库' }
  if (item.status === 'queued')
    return '等待执行'
  if (item.status === 'failed')
    return '需要处理'
  return labels[item.stage] ?? item.stage
}

function progressLabel(item: ServerAcquisitionStatus) {
  if (item.totalFiles > 0)
    return `${item.processedFiles} / ${item.totalFiles} 个文件`
  if (item.progress != null)
    return `${Math.round(item.progress)}%${item.downloadSpeed ? ` · ${formatSpeed(item.downloadSpeed)}` : ''}`
  return item.status
}

function progressPercent(item: ServerAcquisitionStatus) {
  if (item.totalFiles > 0)
    return Math.min(100, item.processedFiles / item.totalFiles * 100)
  return Math.min(100, Math.max(0, item.progress ?? 0))
}

function formatSpeed(bytesPerSecond: number) {
  if (bytesPerSecond >= 1024 ** 2)
    return `${(bytesPerSecond / 1024 ** 2).toFixed(1)} MB/s`
  if (bytesPerSecond >= 1024)
    return `${(bytesPerSecond / 1024).toFixed(0)} KB/s`
  return `${bytesPerSecond} B/s`
}

async function openItem(item: WorkspaceItem) {
  workspace.hide()
  const returnTo = route.name === 'server-discovery-detail' ? '/' : route.fullPath
  await router.push({
    name: 'server-discovery-detail',
    params: { sourceId: item.sourceId, provider: 'tmdb', mediaType: item.mediaType, providerId: item.tmdbId },
    query: { origin: 'acquisitions', return_to: returnTo },
  })
}
</script>

<template>
  <Teleport to="body">
    <Transition name="task-workspace">
      <div v-if="workspace.open" class="workspace-layer fixed inset-0 z-[1180]" @click.self="workspace.hide()">
        <aside class="task-panel glass-panel absolute bottom-5 right-5 top-5 flex w-[min(30rem,calc(100%-2rem))] flex-col overflow-hidden rounded-[28px]">
          <header class="flex items-start justify-between gap-4 border-b border-white/8 px-5 py-5">
            <div>
              <p class="text-[11px] font-bold tracking-[.2em] text-white/35 uppercase">
                OhMyCine Server
              </p><h2 class="mt-1 text-xl font-bold text-white">
                Player 入库任务
              </h2><p class="mt-1 text-xs text-white/42">
                {{ activeCount }} 个任务正在进行
              </p>
            </div>
            <div class="flex gap-2">
              <button class="icon-button" type="button" title="刷新" aria-label="刷新入库任务" @click="refresh(true)">
                ↻
              </button><button class="icon-button" type="button" aria-label="关闭" @click="workspace.hide()">
                ×
              </button>
            </div>
          </header>
          <main class="min-h-0 flex-1 overflow-y-auto p-4 cinema-scrollbar">
            <div v-if="loading && !items.length" class="py-16 text-center text-sm text-white/45">
              正在同步 Server 任务…
            </div>
            <div v-else-if="error" class="rounded-2xl border border-red-400/20 bg-red-400/8 p-4 text-sm text-red-100">
              {{ error }}
            </div>
            <div v-else-if="!items.length" class="py-16 text-center">
              <p class="text-sm font-semibold text-white/55">
                还没有 Player 发起的入库任务
              </p><p class="mt-2 text-xs text-white/32">
                从 Server 海报详情选择资源后会立即出现在这里。
              </p>
            </div>
            <button v-for="item in items" :key="`${item.sourceId}:${item.id}`" class="task-card" type="button" @click="openItem(item)">
              <div class="min-w-0 flex-1 text-left">
                <div class="flex items-center gap-2">
                  <span class="status-dot" :class="{ terminal: isTerminal(item), failed: item.status === 'failed' }" /><strong class="truncate text-sm text-white/90">{{ item.title || `TMDB ${item.tmdbId}` }}</strong>
                </div><p class="mt-2 text-xs text-white/40">
                  {{ item.sourceName }} · {{ item.mediaType === 'tv' ? '电视剧' : '电影' }}
                </p><div v-if="item.totalFiles > 0 || item.progress != null" class="mt-3 h-1.5 overflow-hidden rounded-full bg-white/8">
                  <div class="h-full rounded-full bg-white/75" :style="{ width: `${progressPercent(item)}%` }" />
                </div>
              </div>
              <div class="shrink-0 text-right">
                <b class="block text-xs text-white/70">{{ stageLabel(item) }}</b><small class="mt-1 block text-[11px] text-white/35">{{ progressLabel(item) }}</small>
              </div>
            </button>
          </main>
        </aside>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.workspace-layer{background:rgba(3,5,10,.5);backdrop-filter:blur(10px)}.task-panel{border:1px solid var(--chrome-border);background:color-mix(in srgb,var(--chrome-surface-translucent) 97%,transparent);box-shadow:var(--chrome-shadow),inset 0 1px rgba(255,255,255,.1)}.icon-button{display:grid;width:2.35rem;height:2.35rem;place-items:center;border-radius:50%;color:var(--color-text-secondary);background:var(--surface-soft);font-size:1.15rem}.task-card{display:flex;width:100%;align-items:center;gap:1rem;border:1px solid var(--color-border);border-radius:18px;padding:1rem;background:var(--surface-soft);transition:160ms ease}.task-card+.task-card{margin-top:.65rem}.task-card:hover{border-color:var(--control-border-hover);background:var(--surface-soft-hover);transform:translateX(-2px)}.status-dot{width:.55rem;height:.55rem;flex:0 0 auto;border-radius:50%;background:var(--color-primary);box-shadow:0 0 0 4px color-mix(in srgb,var(--color-primary) 16%,transparent)}.status-dot.terminal{background:#5fd39a;box-shadow:0 0 0 4px rgba(95,211,154,.13)}.status-dot.failed{background:#ff7187;box-shadow:0 0 0 4px rgba(255,113,135,.13)}.task-workspace-enter-active,.task-workspace-leave-active{transition:opacity 180ms ease}.task-workspace-enter-active .task-panel,.task-workspace-leave-active .task-panel{transition:transform 220ms ease}.task-workspace-enter-from,.task-workspace-leave-to{opacity:0}.task-workspace-enter-from .task-panel,.task-workspace-leave-to .task-panel{transform:translateX(28px)}@media(max-width:767px){.task-panel{inset:0;width:100%;border:0;border-radius:0;padding-top:env(safe-area-inset-top)}}
</style>
