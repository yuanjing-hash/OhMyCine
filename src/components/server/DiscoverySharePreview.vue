<script setup lang="ts">
import type { ShareContentAnalysis } from '@/services/serverShareContent'
import type { ServerShareEntry, ServerSharePreview, ServerShareSelection } from '@/services/serverSharePreview'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { maxServerShareSelectedFiles, shareEntryLeaves, toggleServerShareEntry, validateServerShareSelection } from '@/services/serverSharePreview'

const props = defineProps<{
  resultToken: string
  downloaderId: string
  providerLabel: string
  load: (downloaderId: string) => Promise<ServerSharePreview>
}>()
const emit = defineEmits<{ change: [selection: ServerShareSelection | null], analysis: [token: string, analysis: ShareContentAnalysis] }>()
const preview = ref<ServerSharePreview | null>(null)
const selected = ref<string[]>([])
const loading = ref(false)
const error = ref('')
const expired = ref(false)
let generation = 0
let expiryTimer: ReturnType<typeof setTimeout> | undefined
const selectedFiles = computed(() => preview.value?.entries.filter(entry => !entry.isDirectory && selected.value.includes(entry.token)) ?? [])
const selectedSize = computed(() => selectedFiles.value.reduce((sum, item) => sum + item.size, 0))

function clearExpiry() {
  if (expiryTimer)
    clearTimeout(expiryTimer)
  expiryTimer = undefined
}
function expireAt(version: number, expiresAt: string) {
  expiryTimer = setTimeout(() => {
    if (version !== generation)
      return
    error.value = '分享预览已过期，请重新读取并选择文件。'
    expired.value = true
    selected.value = []
    emit('change', null)
  }, Math.min(2_147_483_647, Math.max(0, Date.parse(expiresAt) - Date.now())))
}
async function reload(preserve = true) {
  const version = ++generation
  const previous = preserve ? preview.value : null
  const previousSelection = preserve ? [...selected.value] : []
  clearExpiry()
  preview.value = null
  selected.value = []
  loading.value = true
  error.value = ''
  expired.value = false
  emit('change', null)
  try {
    const result = await props.load(props.downloaderId)
    if (version !== generation)
      return
    if (Date.parse(result.expiresAt) <= Date.now())
      throw new Error('分享预览已过期，请重新读取。')
    preview.value = result
    if (result.shareAnalysis)
      emit('analysis', props.resultToken, result.shareAnalysis)
    expireAt(version, result.expiresAt)
  }
  catch (reason) {
    if (version === generation) {
      error.value = reason instanceof Error ? reason.message : '分享预览失败，请重新读取'
      if (previous && Date.parse(previous.expiresAt) > Date.now()) {
        preview.value = previous
        selected.value = previousSelection
        error.value += '；已保留上次仍有效的目录和选择。'
        expireAt(version, previous.expiresAt)
        emit('change', validateServerShareSelection(previous, previousSelection))
      }
    }
  }
  finally {
    if (version === generation)
      loading.value = false
  }
}
function checked(entry: ServerShareEntry) {
  if (!preview.value)
    return false
  const leaves = shareEntryLeaves(preview.value, entry)
  return leaves.length > 0 && leaves.every(file => selected.value.includes(file.token))
}
function toggle(entry: ServerShareEntry) {
  if (!preview.value || expired.value)
    return
  selected.value = toggleServerShareEntry(preview.value, selected.value, entry)
  emit('change', validateServerShareSelection(preview.value, selected.value))
}
function selectAll(value: boolean) {
  if (!preview.value || expired.value)
    return
  selected.value = value ? preview.value.entries.filter(item => !item.isDirectory).map(item => item.token) : []
  emit('change', validateServerShareSelection(preview.value, selected.value))
}
function bytes(value: number) {
  return value >= 1024 ** 3 ? `${(value / 1024 ** 3).toFixed(2)} GB` : value >= 1024 ** 2 ? `${(value / 1024 ** 2).toFixed(1)} MB` : value >= 1024 ? `${(value / 1024).toFixed(1)} KB` : `${value} B`
}
watch(() => [props.resultToken, props.downloaderId], () => {
  void reload(false)
}, { immediate: true })
onBeforeUnmount(() => {
  generation++
  clearExpiry()
})
</script>

<template>
  <section class="mt-5 border border-white/10 rounded-2xl p-4" aria-label="分享文件选择">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h3 class="text-sm font-bold text-white/90">
        {{ providerLabel }} · 选择转存文件
      </h3>
      <button class="glass-button rounded-full px-3 py-1.5 text-xs" type="button" :disabled="loading" @click="reload()">
        重新读取目录
      </button>
    </div>
    <p v-if="loading" class="py-7 text-center text-sm text-white/55" role="status">
      Server 正在读取分享的真实目录、文件名和大小，请稍候…
    </p>
    <p v-if="error" class="mt-3 rounded-xl bg-red-400/10 p-3 text-sm text-red-200" role="alert">
      {{ error }}
    </p>
    <template v-if="preview && !loading">
      <p class="mt-2 text-xs text-white/45">
        共 {{ preview.fileCount }} 个文件 · {{ bytes(preview.totalSize) }}。只转存勾选的文件，目录选项会选择其全部子文件。
      </p>
      <div class="my-3 flex items-center gap-3 text-xs">
        <button type="button" class="glass-button rounded-full px-3 py-1.5" :disabled="expired" @click="selectAll(true)">
          全选文件
        </button>
        <button type="button" class="glass-button rounded-full px-3 py-1.5" @click="selectAll(false)">
          清空选择
        </button>
      </div>
      <div class="max-h-72 overflow-auto cinema-scrollbar">
        <label v-for="entry in preview.entries" :key="entry.token" class="flex items-start gap-3 border-t border-white/6 py-2.5 text-sm">
          <input type="checkbox" class="mt-1 shrink-0" :checked="checked(entry)" :disabled="expired || entry.isDirectory && !shareEntryLeaves(preview, entry).length" @change="toggle(entry)">
          <span class="min-w-0 flex-1"><span class="block break-words text-white/85">{{ entry.isDirectory ? '目录 · ' : '' }}{{ entry.name }}</span><span class="block break-all text-xs text-white/35">{{ entry.path }}</span></span>
          <span class="shrink-0 text-xs text-white/45">{{ entry.isDirectory ? '—' : bytes(entry.size) }}</span>
        </label>
      </div>
      <p class="mb-0 mt-3 text-xs text-white/65" role="status">
        已选 {{ selectedFiles.length }} 个文件 · {{ bytes(selectedSize) }}
      </p>
      <p v-if="selectedFiles.length > maxServerShareSelectedFiles" class="mt-2 text-xs text-amber-200" role="alert">
        单次最多转存 {{ maxServerShareSelectedFiles }} 个文件，请减少选择后提交。
      </p>
    </template>
  </section>
</template>
