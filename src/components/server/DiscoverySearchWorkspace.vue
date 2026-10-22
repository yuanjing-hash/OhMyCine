<script setup lang="ts">
import type { ServerDiscoveryTypePolicy, ServerResourceGroup, ServerResourceItem } from '@/services/serverDiscovery'
import type { ResourceSearchActivity, ResourceSearchState } from '@/services/serverDiscoverySearch'
import type { ServerResourceRecognition } from '@/services/serverResourceRecognition'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import DiscoveryResourceRecognition from '@/components/server/DiscoveryResourceRecognition.vue'
import { serverDiscoveryTypeOptions, serverResourceTypeLabel } from '@/services/serverDiscovery'
import { resourceGroupIsPrevious, resourceGroupPageIsChanging, searchIncompleteLabel, searchPhaseLabel } from '@/services/serverDiscoverySearch'
import { isServerCloudResource } from '@/services/serverResourceRecognition'

const props = defineProps<{
  open: boolean
  title: string
  state: ResourceSearchState
  activeSiteId?: number
  canAcquire: boolean
  busyToken?: string
  mixedSupported?: boolean
  typePolicy?: 'default' | ServerDiscoveryTypePolicy
  cloudTypes?: string[]
  copyingToken?: string
  recognitions?: Record<string, ServerResourceRecognition>
  recognizingTokens?: string[]
  recognitionErrors?: Record<string, string>
}>()

const emit = defineEmits<{
  close: []
  cancel: []
  retry: [siteId: number]
  selectSite: [siteId: number]
  selectResource: [item: ServerResourceItem]
  page: [siteId: number, page: number]
  copyResource: [item: ServerResourceItem]
  updateTypePolicy: [value: 'default' | ServerDiscoveryTypePolicy]
  updateCloudTypes: [value: string[]]
  refresh: []
  recognize: [token: string]
}>()

const searching = computed(() => props.state.outcome === 'running')
const progress = computed(() => props.state.progress)
const resultGroups = computed(() => props.state.groups.filter(group => group.items.length > 0))
const statusGroups = computed(() => props.state.groups.filter(group => group.items.length === 0 || group.status !== 'success' || group.partial || resourceGroupIsPrevious(props.state, group.siteId)))
const activeGroup = computed(() => resultGroups.value.find(group => group.siteId === props.activeSiteId) ?? resultGroups.value[0])
const typePolicy = computed({ get: () => props.typePolicy ?? 'default', set: value => emit('updateTypePolicy', value) })
const cloudTypes = computed({ get: () => props.cloudTypes ?? [], set: value => emit('updateCloudTypes', value) })
function unavailable(item: ServerResourceItem) {
  if (activeGroup.value?.unavailableReason)
    return activeGroup.value.unavailableReason
  if (item.expiresAt && Date.parse(item.expiresAt) <= Date.now())
    return '搜索结果已过期，请重新搜索。'
  return item.acquisitionAvailable === false ? item.acquisitionReason || '当前类型仅支持查看链接，尚不支持自动入库。' : ''
}
const progressPercent = computed(() => progress.value.total > 0 ? Math.min(100, Math.round(progress.value.completed / progress.value.total * 100)) : 0)
const resultCount = computed(() => props.state.groups.reduce((count, group) => count + group.items.length, 0))
const summary = computed(() => {
  switch (props.state.outcome) {
    case 'running': return `正在搜索 ${progress.value.total} 个站点 · 已显示 ${resultCount.value} 条结果`
    case 'complete': return props.state.groups.some(group => group.completion === 'received') ? `本轮已返回 ${resultCount.value} 条结果 · 上游尚未确认全部来源完成` : `本轮搜索完成 · 当前共 ${resultCount.value} 条结果`
    case 'cancelled': return `搜索已取消 · 已保留 ${resultCount.value} 条结果`
    case 'partial': return `搜索尚未完整完成 · 已保留 ${resultCount.value} 条结果`
    case 'failed': return '本次搜索失败'
    default: return '选择站点后开始搜索'
  }
})
const emptyMessage = computed(() => {
  if (props.state.outcome === 'complete')
    return '本轮查询已完成，没有找到可入库的资源。可以重选站点或使用直接搜索。'
  if (props.state.outcome === 'cancelled')
    return '搜索已取消，尚未收到可显示的结果。'
  return '查询未能完整完成，暂时没有可显示的结果。请查看站点状态后重试。'
})
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
watch(() => props.open && searching.value, (active) => {
  clearInterval(clock)
  clock = undefined
  now.value = Date.now()
  if (active)
    clock = setInterval(() => { now.value = Date.now() }, 1000)
}, { immediate: true })
onBeforeUnmount(() => clearInterval(clock))

function elapsed(activity: ResourceSearchActivity): string {
  const base = activity.detail?.elapsedMs ?? Math.max(0, activity.detailReceivedAt - activity.startedAt)
  const end = activity.pending && searching.value ? now.value : activity.endedAt ?? activity.detailReceivedAt
  const duration = base + Math.max(0, end - activity.detailReceivedAt)
  const seconds = Math.floor(duration / 1000)
  return seconds >= 60 ? `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒` : `${seconds} 秒`
}

function activityLabel(activity: ResourceSearchActivity): string {
  if (activity.pending)
    return searchPhaseLabel(activity.detail?.phase)
  if (activity.previousResults)
    return '本次未取得完整新结果，保留上次结果'
  const group = props.state.groups.find(item => item.siteId === activity.siteId)
  if (group?.partial)
    return searchIncompleteLabel(group.incompleteReason)
  if (group?.status === 'success')
    return group.completion === 'received' ? '已收到本批结果，上游尚未确认全部来源完成' : group.items.length ? '搜索完成' : '查询完成，暂无结果'
  if (props.state.outcome === 'cancelled')
    return '已取消'
  if (group)
    return groupLabel(group)
  return '查询未完成，可重试'
}

function groupLabel(group: ServerResourceGroup): string {
  if (resourceGroupIsPrevious(props.state, group.siteId))
    return `上次保留的结果 · 第 ${group.page} 页`
  if (group.complete === false)
    return '已收到部分结果，搜索仍在继续'
  if (group.partial)
    return `${searchIncompleteLabel(group.incompleteReason)}，已保留收到的结果`
  if (group.status !== 'success')
    return `${searchIncompleteLabel(group.errorCode)}，请重新查询`
  if (group.completion === 'received')
    return '已收到本批结果，上游尚未确认全部来源完成'
  return group.items.length ? '本次查询已完成' : '本次查询已完成，暂无结果'
}

function queryCounts(activity: ResourceSearchActivity): string | undefined {
  const group = activity.previousResults ? undefined : props.state.groups.find(item => item.siteId === activity.siteId)
  const completed = activity.detail?.queriesCompleted == null ? group?.queriesCompleted : Math.max(group?.queriesCompleted ?? 0, activity.detail.queriesCompleted)
  const failed = activity.detail?.queriesFailed == null ? group?.queriesFailed : Math.max(group?.queriesFailed ?? 0, activity.detail.queriesFailed)
  const total = group?.queriesTotal ?? activity.detail?.queriesTotal
  if (completed == null && failed == null && total == null)
    return undefined
  return `查询完成 ${completed ?? 0}${total != null && total > 0 ? ` / ${total}` : ''}${failed ? ` · 失败 ${failed}` : ''}`
}

function activityCount(activity: ResourceSearchActivity): number {
  if (activity.previousResults)
    return activity.detail?.resultCount ?? 0
  return props.state.groups.find(item => item.siteId === activity.siteId)?.items.length ?? activity.detail?.resultCount ?? 0
}

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

function origins(item: ServerResourceItem) {
  const seen = new Set<string>()
  return (item.origins ?? []).filter((origin) => {
    const key = `${origin.kind}:${origin.name}`
    if (seen.has(key))
      return false
    seen.add(key)
    return true
  })
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
                  {{ summary }}
                </p>
                <p v-if="searching && progress.phase" class="mt-1 text-xs text-white/65">
                  {{ searchPhaseLabel(progress.phase) }}
                </p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <button v-if="searching" class="glass-button rounded-full px-4 py-2 text-xs text-white/75" type="button" @click="emit('cancel')">
                  取消搜索
                </button>
                <button class="icon-button" type="button" aria-label="关闭结果" @click="emit('close')">
                  ×
                </button>
              </div>
            </div>
            <div class="mt-3 flex items-center justify-between gap-3 text-xs text-white/40">
              <span>本轮站点完成 {{ progress.completed }} / {{ progress.total }}</span>
              <span v-if="searching && resultCount">已有结果可以先选择入库</span>
            </div>
            <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-white/8" role="progressbar" aria-label="本轮已完成站点" :aria-valuenow="progress.completed" :aria-valuemax="progress.total || 1" aria-valuemin="0">
              <div class="h-full rounded-full bg-white/85 transition-[width] duration-300" :style="{ width: `${progressPercent}%` }" />
            </div>
            <div v-if="resultGroups.length" class="site-tabs cinema-scrollbar mt-4 flex gap-2 overflow-x-auto pb-1">
              <button v-for="group in resultGroups" :key="group.siteId" class="site-tab" :class="{ active: activeGroup?.siteId === group.siteId }" type="button" @click="emit('selectSite', group.siteId)">
                <span>{{ group.siteName }}</span><b>{{ group.items.length }}</b>
              </button>
            </div>
          </header>

          <main class="min-h-0 flex-1 overflow-y-auto px-6 py-5 cinema-scrollbar">
            <section v-if="mixedSupported" class="type-controls mb-4 space-y-3 rounded-2xl p-4" aria-label="PanSou 结果类型">
              <div class="flex flex-wrap items-end gap-3">
                <label class="min-w-56 flex-1"><span class="mb-2 block text-xs">PanSou 结果类型</span><select v-model="typePolicy" class="w-full rounded-xl px-3 py-2 text-sm" :disabled="searching"><option value="default">使用站点默认设置</option><option value="auto">自动：已接入网盘＋磁力</option><option value="all">全部类型</option><option value="manual">指定类型</option></select></label><button class="page-button" type="button" :disabled="searching || typePolicy === 'manual' && !cloudTypes.length" @click="emit('refresh')">
                  按此类型重新搜索
                </button>
              </div>
              <fieldset v-if="typePolicy === 'manual'" class="flex flex-wrap gap-3 text-xs">
                <legend class="mb-2">
                  搜索类型
                </legend><label v-for="option in serverDiscoveryTypeOptions" :key="option[0]"><input v-model="cloudTypes" type="checkbox" :value="option[0]"> {{ option[1] }}</label>
              </fieldset>
              <p class="text-xs">
                类型只影响 PanSou；自动模式始终包含磁力。其他网盘可展示，但是否能入库取决于 Server 的接收能力。
              </p>
            </section>
            <p v-else class="mb-4 text-xs text-[var(--color-text-secondary)]">
              当前 Server 未提供混合搜索能力，继续使用原有搜索和入库功能。
            </p>
            <details v-if="state.activities.length" open class="activity-panel mb-5 rounded-2xl border border-white/8 bg-white/[.025] p-4">
              <summary class="cursor-pointer text-xs font-semibold text-white/65">
                站点搜索详情
              </summary>
              <div class="activity-list cinema-scrollbar mt-3 space-y-3 overflow-y-auto">
                <div v-for="activity in state.activities" :key="activity.siteId" class="activity-row">
                  <div class="flex min-w-0 items-center gap-2">
                    <span v-if="activity.pending" class="search-spinner h-3 w-3 shrink-0 rounded-full border" aria-hidden="true" />
                    <b class="truncate text-xs text-white/80">{{ activity.siteName }}</b>
                    <span class="ml-auto shrink-0 text-[11px] text-white/38">{{ elapsed(activity) }}</span>
                  </div>
                  <p class="mt-1 text-xs text-white/55">
                    {{ activityLabel(activity) }}
                  </p>
                  <p v-if="activity.detail?.queryName && activity.pending" class="mt-1 truncate text-[11px] text-white/38" :title="activity.detail.queryName">
                    正在查询<template v-if="activity.detail.queryIndex">
                      {{ activity.detail.queryIndex }}
                    </template>：{{ activity.detail.queryName }}
                  </p>
                  <p class="mt-1 text-[11px] text-white/38">
                    <template v-if="queryCounts(activity)">
                      {{ queryCounts(activity) }} ·
                    </template>本次已收到 {{ activityCount(activity) }} 条
                  </p>
                  <button v-if="!activity.pending && !searching && !state.groups.some(group => group.siteId === activity.siteId)" class="mt-2 text-xs text-white/75 hover:text-white" type="button" @click="emit('retry', activity.siteId)">
                    重新搜索
                  </button>
                </div>
              </div>
              <p v-if="searching" class="mt-3 text-[11px] leading-5 text-white/35">
                站点可能需要较长时间；有新结果会自动显示，已有结果可先选择。
              </p>
            </details>
            <p v-if="state.error" role="alert" class="mb-4 rounded-2xl border border-amber-300/20 bg-amber-300/8 px-4 py-3 text-sm text-amber-100">
              {{ state.error }}
            </p>
            <div v-if="!resultGroups.length && searching" class="grid min-h-36 place-items-center text-center">
              <div>
                <div class="search-spinner mx-auto h-9 w-9 rounded-full border-2" /><p class="mt-4 text-sm text-white/55">
                  正在等待站点返回，收到结果后会立即显示
                </p><p class="mt-1 text-xs text-white/32">
                  已完成 {{ progress.completed }} / {{ progress.total }}
                </p>
              </div>
            </div>
            <div v-else-if="!resultGroups.length" class="grid min-h-64 place-items-center text-center text-sm text-white/45">
              {{ emptyMessage }}
            </div>
            <template v-else-if="activeGroup">
              <p v-if="activeGroup.unavailableReason" class="result-warning mb-3 rounded-xl p-3 text-xs" role="status">
                {{ activeGroup.unavailableReason }}
              </p>
              <p v-for="warning in activeGroup.warnings || []" :key="warning.code" class="result-warning mb-3 rounded-xl p-3 text-xs" role="status">
                {{ warning.message }}
              </p>
              <p v-if="activeGroup.truncated" class="result-warning mb-3 rounded-xl p-3 text-xs">
                本次结果达到保存上限，尚未完整展示；请缩小范围后重新搜索。
              </p>
              <p v-if="activeGroup.completion === 'received' && !activeGroup.warnings?.length" class="mb-3 text-xs text-[var(--color-text-secondary)]">
                已收到本批结果，上游尚未确认全部来源完成。
              </p>
              <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h3 class="font-bold text-white/90">
                    {{ activeGroup.siteName }}
                  </h3><p class="mt-1 text-xs text-white/38">
                    第 {{ activeGroup.page }} 页 · {{ activeGroup.items.length }} 个结果<template v-if="activeGroup.skipped">
                      · 已过滤 {{ activeGroup.skipped }} 个不匹配结果
                    </template>
                  </p>
                  <p class="mt-1 text-xs text-white/50">
                    {{ groupLabel(activeGroup) }}
                  </p>
                </div>
                <div class="flex items-center gap-2">
                  <button class="page-button" type="button" :disabled="searching" @click="emit('retry', activeGroup.siteId)">
                    重新搜索
                  </button>
                  <button class="page-button" type="button" :disabled="searching || !!activeGroup.unavailableReason || activeGroup.complete === false || activeGroup.page <= 1" @click="emit('page', activeGroup.siteId, activeGroup.page - 1)">
                    上一页
                  </button>
                  <button class="page-button" type="button" :disabled="searching || !!activeGroup.unavailableReason || activeGroup.complete === false || activeGroup.page >= 20 || !activeGroup.hasNext" @click="emit('page', activeGroup.siteId, activeGroup.page + 1)">
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
                    <div class="mt-2 flex flex-wrap gap-2 text-xs text-[var(--color-text-secondary)]">
                      <span v-if="serverResourceTypeLabel(item)" class="resource-type">{{ serverResourceTypeLabel(item) }}</span><span v-for="origin in origins(item)" :key="`${origin.kind}:${origin.name}`">PanSou · {{ origin.kind === 'tg' ? 'TG' : '插件' }} · {{ origin.name }}</span>
                    </div>
                    <p class="mt-2 text-xs text-white/42">
                      {{ [formatBytes(item.sizeBytes), item.seeders != null ? `${item.seeders} 做种` : '', item.promotion, item.quality, item.matchedName ? `命中：${item.matchedName}` : ''].filter(Boolean).join(' · ') }}
                    </p>
                    <p v-if="unavailable(item)" class="mt-2 text-xs text-[var(--color-text-secondary)]" role="status">
                      {{ unavailable(item) }}
                    </p>
                    <DiscoveryResourceRecognition :cloud="isServerCloudResource(item)" :result="recognitions?.[item.token]" :loading="recognizingTokens?.includes(item.token)" :error="recognitionErrors?.[item.token]" />
                  </div>
                  <div class="flex shrink-0 flex-wrap justify-end gap-2">
                    <button class="page-button" type="button" :disabled="recognizingTokens?.includes(item.token) || !!activeGroup.unavailableReason" @click="emit('recognize', item.token)">
                      重新检测
                    </button>
                    <button v-if="mixedSupported && item.resultType" class="page-button" type="button" :disabled="copyingToken === item.token || !!activeGroup.unavailableReason" @click="emit('copyResource', item)">
                      {{ copyingToken === item.token ? '正在读取…' : '复制链接' }}
                    </button><button class="select-button" type="button" :disabled="!canAcquire || busyToken === item.token || !!unavailable(item) || resourceGroupPageIsChanging(state, activeGroup)" :title="unavailable(item)" @click="emit('selectResource', item)">
                      {{ busyToken === item.token ? '处理中…' : item.acquisitionAvailable === false ? '仅供查看' : item.sourceKind === 'quark_share' || item.sourceKind === '115_share' ? '转存入库' : '选择入库' }}
                    </button>
                  </div>
                </article>
              </div>
            </template>

            <details v-if="statusGroups.length" class="status-box mt-5 rounded-2xl border border-white/8 bg-white/[.025] p-4">
              <summary class="cursor-pointer text-xs font-semibold text-white/55">
                {{ statusGroups.length }} 个站点需要查看状态
              </summary>
              <div class="mt-3 space-y-2">
                <div v-for="group in statusGroups" :key="group.siteId" class="flex items-center justify-between gap-3 text-xs text-white/42">
                  <span>{{ group.siteName }} · {{ groupLabel(group) }}</span>
                  <button v-if="!searching" class="shrink-0 text-white/70 hover:text-white" type="button" @click="emit('retry', group.siteId)">
                    重新搜索
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
.type-controls { border:1px solid var(--color-border); background:var(--surface-soft); color:var(--color-text-secondary); }
.result-warning { border:1px solid var(--color-border); background:var(--surface-soft); color:var(--color-text-secondary); }
.resource-type { padding:.1rem .45rem; border:1px solid var(--color-border); border-radius:999px; }
.activity-list { max-height:14rem; }.activity-row + .activity-row { border-top:1px solid var(--color-border); padding-top:.7rem; }
.search-spinner { border-color:var(--color-border); border-top-color:var(--color-text); animation:spin .75s linear infinite; }
@keyframes spin { to { transform:rotate(360deg); } }
.search-workspace-enter-active,.search-workspace-leave-active{transition:opacity 180ms ease}.search-workspace-enter-active .workspace-card,.search-workspace-leave-active .workspace-card{transition:transform 180ms ease,opacity 180ms ease}.search-workspace-enter-from,.search-workspace-leave-to{opacity:0}.search-workspace-enter-from .workspace-card,.search-workspace-leave-to .workspace-card{opacity:0;transform:translateY(16px) scale(.985)}
@media(max-width:767px){.workspace-layer{padding:0}.workspace-card{max-height:100vh;height:100%;border:0;border-radius:0;padding-top:env(safe-area-inset-top)}.resource-row{align-items:flex-start;flex-direction:column}.select-button{align-self:flex-end}.select-button,.page-button{min-height:2.75rem}}
</style>
