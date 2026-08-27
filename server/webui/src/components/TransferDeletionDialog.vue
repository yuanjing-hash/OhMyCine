<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { confirmTransferDeletion, previewTransferDeletion, transferDeletionLabels, type TransferDeletionPreview, type TransferDeletionScope, type TransferSummary } from '@/transfers'
import { notify } from '@/toast'

const props = defineProps<{ transfer: TransferSummary }>()
const emit = defineEmits<{ close: []; deleted: [] }>()
const preview = ref<TransferDeletionPreview | null>(null)
const loading = ref(false)
const error = ref('')
let activeRequest: AbortController | null = null
let requestTimer: ReturnType<typeof setTimeout> | null = null

function beginRequest(): AbortController {
  activeRequest?.abort()
  if (requestTimer) clearTimeout(requestTimer)
  const controller = new AbortController()
  activeRequest = controller
  requestTimer = setTimeout(() => controller.abort('timeout'), 50_000)
  return controller
}

function finishRequest(controller: AbortController) {
  if (activeRequest !== controller) return
  activeRequest = null
  if (requestTimer) clearTimeout(requestTimer)
  requestTimer = null
}

function close() {
  activeRequest?.abort('closed')
  emit('close')
}

onBeforeUnmount(() => activeRequest?.abort('unmounted'))

const options: Array<{ scope: TransferDeletionScope; description: string; risk: string }> = [
  { scope: 'record_only', description: '只清理 OhMyCine 中的整理记录和执行历史。', risk: '不删除任何文件' },
  { scope: 'record_and_source', description: '删除下载器任务及其来源数据，保留已经入库的媒体。', risk: '源文件会被删除' },
  { scope: 'record_and_library', description: '删除当前任务明确托管的媒体库文件，保留下载来源。', risk: '媒体库文件会被删除' },
  { scope: 'record_source_and_library', description: '同时删除来源数据和当前任务明确托管的媒体库文件。', risk: '两侧文件都会被删除' },
]

async function choose(scope: TransferDeletionScope) {
  const controller = beginRequest()
  loading.value = true
  error.value = ''
  try { preview.value = await previewTransferDeletion(props.transfer.id, scope, controller.signal) }
  catch (reason) { error.value = message(reason) }
  finally { finishRequest(controller); loading.value = false }
}

async function confirmDelete() {
  if (!preview.value) return
  const controller = beginRequest()
  loading.value = true
  error.value = ''
  try {
    const result = await confirmTransferDeletion(props.transfer.id, preview.value.confirmation_token, controller.signal)
    notify(result.scope === 'record_only' ? '媒体整理记录已删除' : `删除完成：源文件 ${result.source_removed} 项，媒体库文件 ${result.library_removed} 项`, 'success')
    emit('deleted')
  } catch (reason) { error.value = message(reason) }
  finally { finishRequest(controller); loading.value = false }
}

function formatSize(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let index = 0
  while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ }
  return `${amount.toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

function message(reason: unknown): string {
  if (reason instanceof DOMException && reason.name === 'AbortError') return '核对或删除请求已取消；未确认完成的记录和文件均会保留，可稍后重试。'
  return reason instanceof Error ? reason.message : '删除操作失败'
}
</script>

<template>
  <div class="modal-backdrop fixed inset-0 z-70 flex items-center justify-center p-4" @click.self="close">
    <section class="panel max-h-[90vh] w-full max-w-4xl overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="transfer-deletion-title">
      <header class="flex items-start justify-between gap-4">
        <div><h2 id="transfer-deletion-title" class="m-0 text-xl">删除媒体整理记录</h2><p class="page-description mt-2">{{ transfer.scrape_title || transfer.display_name }}</p></div>
        <button class="icon-button" type="button" aria-label="关闭删除窗口" @click="close">×</button>
      </header>

      <div v-if="!preview" class="deletion-options mt-5">
        <button v-for="option in options" :key="option.scope" class="deletion-option text-left" type="button" :disabled="loading" @click="choose(option.scope)">
          <strong>{{ transferDeletionLabels[option.scope] }}</strong>
          <span>{{ option.description }}</span>
          <small>{{ option.risk }}</small>
        </button>
      </div>

      <template v-else>
        <section class="semantic-warning mt-5 p-4">
          <strong>{{ transferDeletionLabels[preview.scope] }}</strong>
          <p class="mb-0 mt-2 text-sm">Server 已按当前任务、身份版本和托管清单重新核对删除边界。确认令牌 5 分钟内有效且只能使用一次。</p>
        </section>
        <dl class="deletion-counts mt-4">
          <div><dt>来源数据</dt><dd>{{ preview.source_items }} 项 · {{ formatSize(preview.source_bytes) }} · {{ preview.source_storage_type }}</dd><small v-if="preview.source_missing">其中 {{ preview.source_missing }} 项已不存在</small><small v-if="preview.source_detached">其中 {{ preview.source_detached }} 项已离开来源包或不存在，将保留</small></div>
          <div><dt>媒体库文件</dt><dd>{{ preview.library_items }} 项 · {{ formatSize(preview.library_bytes) }} · {{ preview.library_storage_type }}</dd><small v-if="preview.library_missing">其中 {{ preview.library_missing }} 项已不存在</small></div>
          <div><dt>下载提供方</dt><dd>{{ preview.provider_type || '无' }}</dd></div>
        </dl>
        <ul class="semantic-inset mt-4 grid gap-2 p-4 text-sm"><li v-for="warning in preview.warnings" :key="warning">{{ warning }}</li></ul>
        <ul v-if="preview.blockers.length" class="semantic-error mt-4 grid gap-2 p-4 text-sm"><li v-for="blocker in preview.blockers" :key="blocker">{{ blocker }}</li></ul>
        <label v-if="preview.requires_file_delete" class="semantic-danger-text mt-4 block text-sm"><input required type="checkbox" form="transfer-deletion-confirm" /> 我已理解选中的文件会被删除，并且 OhMyCine 无法撤销该操作。</label>
        <form id="transfer-deletion-confirm" class="mt-5 flex justify-end gap-3" @submit.prevent="confirmDelete">
          <button class="btn-secondary" type="button" :disabled="loading" @click="preview = null">返回修改范围</button>
          <button class="btn-danger" type="submit" :disabled="loading">{{ loading ? '正在安全删除…' : `确认${transferDeletionLabels[preview.scope]}` }}</button>
        </form>
      </template>
      <p v-if="loading && !preview" class="mt-4 text-sm text-muted">正在核对任务和来源边界（最多约 45 秒，可随时关闭取消）…</p>
      <p v-if="error" class="semantic-error mt-4 p-3" role="alert">{{ error }}</p>
    </section>
  </div>
</template>

<style scoped>
.deletion-options { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .75rem; }
.deletion-option { display: grid; gap: .45rem; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--surface); padding: 1rem; color: var(--text); }
.deletion-option:hover, .deletion-option:focus-visible { border-color: var(--accent); background: var(--surface-subtle); }
.deletion-option span { color: var(--text-muted); font-size: .85rem; }
.deletion-option small { color: var(--danger); font-weight: 700; }
.deletion-counts { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: .75rem; }
.deletion-counts div { border-left: 3px solid var(--border-strong); padding-left: .65rem; }
.deletion-counts dt { color: var(--text-subtle); font-size: .75rem; }
.deletion-counts dd { margin: .25rem 0 0; font-weight: 700; }
@media (max-width: 720px) { .deletion-options, .deletion-counts { grid-template-columns: 1fr; } }
</style>
