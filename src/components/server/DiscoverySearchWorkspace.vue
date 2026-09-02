<script setup lang="ts">
import type { ServerResourceGroup, ServerResourceItem, ServerSearchProgress } from '@/services/serverDiscovery'
import { computed } from 'vue'

const props = defineProps<{
  open: boolean
  title: string
  groups: ServerResourceGroup[]
  activeSiteId?: number
  searching: boolean
  progress: ServerSearchProgress
  canAcquire: boolean
  busyToken?: string
}>()

const emit = defineEmits<{
  close: []
  cancel: []
  retry: [siteId: number]
  selectSite: [siteId: number]
  selectResource: [item: ServerResourceItem]
  page: [siteId: number, page: number]
}>()

const resultGroups = computed(() => props.groups.filter(group => group.items.length > 0))
const statusGroups = computed(() => props.groups.filter(group => group.items.length === 0 || group.status !== 'success'))
const activeGroup = computed(() => resultGroups.value.find(group => group.siteId === props.activeSiteId) ?? resultGroups.value[0])
const progressPercent = computed(() => props.progress.total > 0 ? Math.round(props.progress.completed / props.progress.total * 100) : 0)

function formatBytes(value?: number) {
  if (!value)
    return '大小未知'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index++
  }
  return `${size.toFixed(index > 1 ? 1 : 0)} ${units[index]}`
}
</script>

<template>
  <Teleport to="body">
    <Transition name="search-workspace">
      <div v-if="open" class="workspace-layer fixed inset-0 z-[1205] grid place-items-center p-4" @click.self="emit('close')">
        <section class="workspace-card glass-panel flex max-h-[88vh] w-full max-w-6xl flex-col overflow-hidden rounded-[30px]">
          <header class="border-b border-white/8 px-6 pb-4 pt-5">
            <div class="flex items-start justify-between gap-5">
              <div class="min-w-0">
                <p class="text-[11px] font-bold tracking-[.22em] text-white/35 uppercase">
                  步骤 2 · 选择资源
                </p>
                <h2 class="mt-1 truncate text-xl font-bold text-white">
                  {{ title }}
                </h2>
                <p class="mt-1 text-sm text-white/45">
                  {{ searching ? `正在并行搜索 ${progress.total} 个站点` : `搜索完成 · 共 ${progress.resultCount} 个结果` }}
                </p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <button v-if="searching" class="glass-button rounded-full px-4 py-2 text-xs" type="button" @click="emit('cancel')">
                  取消搜索
                </button>
                <button class="icon-button" type="button" aria-label="关闭结果" @click="emit('close')">
                  ×
                </button>
              </div>
            </div>
            <div class="mt-4 h-1.5 overflow-hidden rounded-full bg-white/8">
              <div class="h-full rounded-full bg-white/85 transition-[width] duration-300" :style="{ width: `${progressPercent}%` }" />
            </div>
            <div v-if="resultGroups.length" class="site-tabs cinema-scrollbar mt-4 flex gap-2 overflow-x-auto pb-1">
              <button v-for="group in resultGroups" :key="group.siteId" class="site-tab" :class="{ active: activeGroup?.siteId === group.siteId }" type="button" @click="emit('selectSite', group.siteId)">
                <span>{{ group.siteName }}</span><b>{{ group.items.length }}</b>
              </button>
            </div>
          </header>

          <main class="min-h-0 flex-1 overflow-y-auto px-6 py-5 cinema-scrollbar">
            <div v-if="!resultGroups.length && searching" class="grid min-h-72 place-items-center text-center">
              <div>
                <div class="search-spinner mx-auto h-9 w-9 rounded-full border-2" /><p class="mt-4 text-sm text-white/55">
                  有结果的站点会立即出现在上方
                </p><p class="mt-1 text-xs text-white/32">
                  已完成 {{ progress.completed }} / {{ progress.total }}
                </p>
              </div>
            </div>
            <div v-else-if="!resultGroups.length" class="grid min-h-64 place-items-center text-center text-sm text-white/45">
              没有找到可入库的资源，可以返回重选站点或使用直接搜索。
            </div>
            <template v-else-if="activeGroup">
              <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h3 class="font-bold text-white/90">
                    {{ activeGroup.siteName }}
                  </h3><p class="mt-1 text-xs text-white/38">
                    第 {{ activeGroup.page }} 页 · {{ activeGroup.items.length }} 个结果<template v-if="activeGroup.skipped">
                      · 已过滤 {{ activeGroup.skipped }} 个不匹配结果
                    </template>
                  </p>
                </div>
                <div class="flex items-center gap-2">
                  <button class="page-button" type="button" :disabled="searching || activeGroup.page <= 1" @click="emit('page', activeGroup.siteId, activeGroup.page - 1)">
                    上一页
                  </button>
                  <button class="page-button" type="button" :disabled="searching || !activeGroup.hasNext" @click="emit('page', activeGroup.siteId, activeGroup.page + 1)">
                    下一页
                  </button>
                </div>
              </div>
              <div class="space-y-2.5">
                <article v-for="item in activeGroup.items" :key="item.token" class="resource-row">
                  <div class="min-w-0 flex-1">
                    <h4 class="break-words text-sm font-semibold leading-6 text-white/92">
                      {{ item.title }}
                    </h4>
                    <p v-if="item.subtitle" class="mt-1 line-clamp-1 text-xs text-white/35">
                      {{ item.subtitle }}
                    </p>
                    <p class="mt-2 text-xs text-white/42">
                      {{ [formatBytes(item.sizeBytes), item.seeders != null ? `${item.seeders} 做种` : '', item.promotion, item.quality, item.matchedName ? `命中：${item.matchedName}` : ''].filter(Boolean).join(' · ') }}
                    </p>
                  </div>
                  <button class="select-button" type="button" :disabled="!canAcquire || busyToken === item.token" @click="emit('selectResource', item)">
                    {{ busyToken === item.token ? '处理中…' : '选择入库' }}
                  </button>
                </article>
              </div>
            </template>

            <details v-if="statusGroups.length" class="status-box mt-5 rounded-2xl border border-white/8 bg-white/[.025] p-4">
              <summary class="cursor-pointer text-xs font-semibold text-white/55">
                {{ statusGroups.length }} 个站点暂无结果或搜索失败
              </summary>
              <div class="mt-3 space-y-2">
                <div v-for="group in statusGroups" :key="group.siteId" class="flex items-center justify-between gap-3 text-xs text-white/42">
                  <span>{{ group.siteName }} · {{ group.status === 'success' ? '暂无结果' : `失败 ${group.errorCode || ''}` }}</span>
                  <button v-if="group.status !== 'success' && !searching" class="text-white/70 hover:text-white" type="button" @click="emit('retry', group.siteId)">
                    重试
                  </button>
                </div>
              </div>
            </details>
          </main>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.workspace-layer { background:color-mix(in srgb,var(--color-bg) 70%,transparent); backdrop-filter:blur(18px); }
.workspace-card { border:1px solid var(--chrome-border); background:color-mix(in srgb,var(--chrome-surface-translucent) 96%,transparent); box-shadow:var(--chrome-shadow),inset 0 1px rgba(255,255,255,.1); }
.icon-button { display:grid; width:2.4rem; height:2.4rem; place-items:center; border-radius:50%; color:var(--color-text-secondary); background:var(--surface-soft); font-size:1.35rem; }
.site-tabs { scrollbar-width:none; }.site-tabs::-webkit-scrollbar{display:none}
.site-tab { display:flex; flex:0 0 auto; align-items:center; gap:.55rem; border:1px solid var(--color-border); border-radius:999px; padding:.55rem .85rem; color:var(--color-text-secondary); background:var(--surface-soft); font-size:.78rem; font-weight:700; }
.site-tab b { min-width:1.35rem; border-radius:999px; padding:.08rem .35rem; text-align:center; color:var(--color-text); background:rgba(255,255,255,.08); font-size:.65rem; }
.site-tab.active { border-color:color-mix(in srgb,var(--color-primary) 55%,transparent); color:#fff; background:color-mix(in srgb,var(--color-primary) 72%,transparent); }
.resource-row { display:flex; align-items:center; gap:1rem; border:1px solid var(--color-border); border-radius:20px; padding:1rem 1.1rem; background:var(--surface-soft); transition:160ms ease; }
.resource-row:hover { border-color:var(--control-border-hover); background:var(--surface-soft-hover); transform:translateY(-1px); }
.select-button { flex:0 0 auto; border-radius:999px; padding:.65rem 1rem; color:#080a0f; background:#fff; font-size:.78rem; font-weight:800; }
.select-button:disabled,.page-button:disabled { cursor:not-allowed; opacity:.4; }
.page-button { border:1px solid var(--color-border); border-radius:999px; padding:.5rem .8rem; color:var(--color-text-secondary); background:var(--surface-soft); font-size:.72rem; }
.search-spinner { border-color:var(--color-border); border-top-color:var(--color-text); animation:spin .75s linear infinite; }
@keyframes spin { to { transform:rotate(360deg); } }
.search-workspace-enter-active,.search-workspace-leave-active{transition:opacity 180ms ease}.search-workspace-enter-active .workspace-card,.search-workspace-leave-active .workspace-card{transition:transform 180ms ease,opacity 180ms ease}.search-workspace-enter-from,.search-workspace-leave-to{opacity:0}.search-workspace-enter-from .workspace-card,.search-workspace-leave-to .workspace-card{opacity:0;transform:translateY(16px) scale(.985)}
@media(max-width:767px){.workspace-layer{padding:0}.workspace-card{max-height:100vh;height:100%;border:0;border-radius:0;padding-top:env(safe-area-inset-top)}.resource-row{align-items:flex-start;flex-direction:column}.select-button{align-self:flex-end}}
</style>
