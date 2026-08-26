<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { notify } from '@/toast'
import {
  confirmMediaReorganization,
  getMediaReorganization,
  mediaReorganizationPhaseLabel,
  previewMediaReorganization,
  searchReorganizationCandidates,
  type MediaReorganizationConflictPolicy,
  type MediaReorganizationPreview,
  type MediaReorganizationTask,
} from '@/media-reorganizations'
import type { TMDBCandidate } from '@/types/api'

const props = defineProps<{
  open: boolean
  transferTaskId: string
  downloadTaskId: string
  displayName: string
  currentTitle?: string
  currentMediaType?: string
}>()

const emit = defineEmits<{
  close: []
  queued: [task: MediaReorganizationTask]
}>()

const keyword = ref('')
const mediaType = ref<'' | 'movie' | 'tv'>('')
const tmdbID = ref<number | null>(null)
const conflictPolicy = ref<MediaReorganizationConflictPolicy>('rename')
const candidates = ref<TMDBCandidate[]>([])
const preview = ref<MediaReorganizationPreview | null>(null)
const task = ref<MediaReorganizationTask | null>(null)
const searching = ref(false)
const saving = ref(false)
let pollTimer: number | undefined

const canPreview = computed(() => Boolean(props.transferTaskId && tmdbID.value && mediaType.value))
const taskTerminal = computed(() => task.value?.phase === 'completed' || task.value?.phase === 'failed')

watch(() => props.open, open => {
  if (!open) {
    stopPolling()
    return
  }
  keyword.value = props.currentTitle?.trim() || props.displayName
  mediaType.value = props.currentMediaType === 'movie' || props.currentMediaType === 'tv' ? props.currentMediaType : ''
  tmdbID.value = null
  conflictPolicy.value = 'rename'
  candidates.value = []
  preview.value = null
  task.value = null
})

function selectCandidate(candidate: TMDBCandidate) {
  tmdbID.value = candidate.id
  mediaType.value = candidate.media_type
  preview.value = null
}

async function searchCandidates() {
  if (!keyword.value.trim()) {
    notify('请输入要搜索的影视名称', 'warning')
    return
  }
  searching.value = true
  candidates.value = []
  preview.value = null
  try {
    candidates.value = (await searchReorganizationCandidates(props.downloadTaskId, keyword.value, mediaType.value)).list
    if (candidates.value.length === 0) notify('TMDB 没有返回候选，可修改关键词或直接填写 ID', 'warning')
  } catch (reason) {
    notify(errorMessage(reason), 'error')
  } finally {
    searching.value = false
  }
}

async function buildPreview() {
  if (!canPreview.value || !tmdbID.value || !mediaType.value) return
  saving.value = true
  preview.value = null
  task.value = null
  try {
    preview.value = await previewMediaReorganization({
      transfer_task_id: props.transferTaskId,
      tmdb_id: tmdbID.value,
      media_type: mediaType.value,
      conflict_policy: conflictPolicy.value,
    })
  } catch (reason) {
    notify(errorMessage(reason), 'error')
  } finally {
    saving.value = false
  }
}

async function confirmPreview() {
  if (!preview.value?.confirmation_token) return
  saving.value = true
  try {
    task.value = await confirmMediaReorganization(preview.value.confirmation_token)
    preview.value = null
    emit('queued', task.value)
    notify('重新整理任务已入队，将只处理 OhMyCine 托管产物', 'success')
    schedulePoll()
  } catch (reason) {
    notify(errorMessage(reason), 'error')
  } finally {
    saving.value = false
  }
}

function schedulePoll() {
  stopPolling()
  if (!task.value || taskTerminal.value) return
  pollTimer = window.setTimeout(async () => {
    try {
      task.value = await getMediaReorganization(task.value!.id)
    } catch (reason) {
      notify(errorMessage(reason), 'error')
      return
    }
    schedulePoll()
  }, 1200)
}

function stopPolling() {
  if (pollTimer !== undefined) window.clearTimeout(pollTimer)
  pollTimer = undefined
}

function close() {
  if (saving.value) return
  emit('close')
}

function actionLabel(value: string): string {
  return ({ move: '移动 / 重命名', skip: '跳过', unchanged: '无需修改' } as Record<string, string>)[value] ?? value
}

function kindLabel(value: string): string {
  return ({ video: '视频', subtitle: '字幕', nfo: 'NFO', image: '图片', strm: 'STRM', sidecar: '伴随文件' } as Record<string, string>)[value] ?? value
}

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : '重新整理请求失败'
}

onBeforeUnmount(stopPolling)
</script>

<template>
  <div v-if="open" class="modal-backdrop fixed inset-0 z-70 flex items-center justify-center p-4" @click.self="close">
    <section class="panel max-h-[92vh] w-full max-w-5xl overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="media-reorganization-title">
      <header class="flex items-start justify-between gap-4">
        <div>
          <h2 id="media-reorganization-title" class="m-0 text-xl">修正识别并重新整理</h2>
          <p class="page-description mb-0 mt-2">{{ displayName }}</p>
        </div>
        <button class="icon-button" type="button" aria-label="关闭重新整理" :disabled="saving" @click="close">×</button>
      </header>

      <template v-if="!task">
        <p class="semantic-warning mt-4 p-3 text-sm">Server 会先预览旧位置到新位置，确认后只移动 manifest 中明确由 OhMyCine 管理的媒体、字幕和元数据。非托管文件不会被扫描、猜测或删除。</p>
        <div class="mt-4 grid gap-3 md:grid-cols-[minmax(0,1fr)_10rem_auto]">
          <input v-model="keyword" class="input" maxlength="256" placeholder="中文、英文、原名或别名" @keyup.enter="searchCandidates" />
          <select v-model="mediaType" class="input" @change="preview = null"><option value="">电影 + 剧集</option><option value="movie">电影</option><option value="tv">剧集</option></select>
          <button class="btn-secondary" type="button" :disabled="searching || saving" @click="searchCandidates">{{ searching ? '搜索中…' : '搜索 TMDB' }}</button>
        </div>
        <div v-if="candidates.length" class="mt-3 grid gap-2 md:grid-cols-2">
          <button v-for="candidate in candidates" :key="`${candidate.media_type}-${candidate.id}`" class="semantic-list-item flex items-center justify-between gap-3 p-3 text-left" :class="{ 'semantic-list-item--selected': tmdbID === candidate.id && mediaType === candidate.media_type }" type="button" @click="selectCandidate(candidate)">
            <span><strong>{{ candidate.title }}</strong><small v-if="candidate.original_title && candidate.original_title !== candidate.title" class="text-subtle mt-1 block">{{ candidate.original_title }}</small><small class="text-subtle mt-1 block">{{ candidate.media_type === 'tv' ? '剧集' : '电影' }} · {{ candidate.release_year || '年份未知' }} · TMDB {{ candidate.id }}</small></span>
            <span>{{ Math.round(candidate.confidence * 100) }}%</span>
          </button>
        </div>
        <div class="mt-4 grid gap-3 md:grid-cols-[10rem_12rem_minmax(12rem,1fr)_auto]">
          <select v-model="mediaType" class="input" aria-label="媒体类型" @change="preview = null"><option value="" disabled>选择类型</option><option value="movie">电影</option><option value="tv">剧集</option></select>
          <input v-model.number="tmdbID" class="input" type="number" min="1" step="1" placeholder="TMDB ID" @input="preview = null" />
          <select v-model="conflictPolicy" class="input" aria-label="冲突处理" @change="preview = null"><option value="rename">冲突时自动重命名（推荐）</option><option value="skip">冲突时跳过</option><option value="ask">发现冲突则停止预览</option><option value="overwrite">覆盖已对账的同名托管项</option></select>
          <button class="btn-primary" type="button" :disabled="saving || !canPreview" @click="buildPreview">{{ saving ? '正在校验…' : '生成安全预览' }}</button>
        </div>

        <section v-if="preview" class="semantic-inset mt-5 p-4">
          <div class="flex flex-wrap items-start justify-between gap-3"><div><h3 class="m-0 text-lg">{{ preview.title }}</h3><p class="text-subtle mb-0 mt-1 text-xs">{{ preview.media_type === 'tv' ? '剧集' : '电影' }} · 新身份 revision {{ preview.identity_revision }} · {{ preview.items.length }} 个托管产物</p></div><span :class="preview.conflict_count ? 'status-chip status-chip--warning' : 'status-chip status-chip--ready'">{{ preview.conflict_count }} 个冲突</span></div>
          <div class="mt-4 max-h-80 overflow-auto"><table class="semantic-table w-full text-sm"><thead><tr><th>类型</th><th>原相对位置</th><th>新相对位置</th><th>动作</th></tr></thead><tbody><tr v-for="(item, index) in preview.items" :key="`${index}-${item.old_relative_path}`"><td>{{ kindLabel(item.kind) }}</td><td><code>{{ item.old_relative_path }}</code></td><td><code>{{ item.new_relative_path }}</code></td><td>{{ actionLabel(item.action) }}</td></tr></tbody></table></div>
          <p class="text-subtle mb-0 mt-3 text-xs">预览令牌短时有效，并绑定当前用户、媒体库、身份 revision、规则 revision 与托管清单摘要；任何一项变化都会要求重新预览。</p>
          <div class="mt-4 flex justify-end gap-3"><button class="btn-secondary" type="button" :disabled="saving" @click="preview = null">返回修改</button><button class="btn-primary" type="button" :disabled="saving" @click="confirmPreview">{{ saving ? '正在入队…' : '确认并开始重新整理' }}</button></div>
        </section>
      </template>

      <section v-else class="semantic-inset mt-5 p-5">
        <div class="flex flex-wrap items-center justify-between gap-3"><div><h3 class="m-0 text-lg">{{ mediaReorganizationPhaseLabel(task.phase) }}</h3><p class="text-subtle mb-0 mt-1 text-xs">身份 r{{ task.source_identity_revision }} → r{{ task.target_identity_revision }} · {{ task.processed_items }} / {{ task.total_items }}</p></div><span :class="task.phase === 'completed' ? 'status-chip status-chip--ready' : task.phase === 'failed' ? 'status-chip status-chip--error' : 'status-chip status-chip--warning'">{{ task.phase }}</span></div>
        <div class="mt-4 h-2 overflow-hidden rounded-full bg-[var(--surface-muted)]"><div class="h-full bg-[var(--accent)] transition-all" :style="{ width: `${task.total_items ? Math.min(100, task.processed_items * 100 / task.total_items) : 0}%` }" /></div>
        <p v-if="task.last_error_code" class="semantic-error mt-4 p-3 text-sm">{{ task.last_error_code }}</p>
        <p v-else-if="task.phase === 'completed'" class="semantic-success mt-4 p-3 text-sm">新身份、托管清单和媒体库 revision 已更新；后续会按新的 dirty generation 重建 NFO / 图片 / STRM 并通知下游刷新。</p>
        <div class="mt-5 flex justify-end"><button class="btn-primary" type="button" :disabled="saving" @click="close">{{ taskTerminal ? '完成' : '关闭并在任务中心查看' }}</button></div>
      </section>
    </section>
  </div>
</template>
