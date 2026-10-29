<script setup lang="ts">
import type { ServerDataSource } from '@/services/datasource/server'
import type { ServerContentPreview, ServerContentSelection } from '@/services/serverResourceContent'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { runDiscoveryRequest } from '@/services/serverDiscoveryRequests'
import { previewServerResourceContent, selectServerResourceContent } from '@/services/serverResourceContent'
import { compactEpisodeSet, shareContentGroupLabel } from '@/services/serverShareContent'

const props = defineProps<{
  resultToken: string
  source: ServerDataSource | null
  preview?: ServerContentPreview
  loading?: boolean
  error?: string
  canSelect?: boolean
  autoLoad?: boolean
}>()
const emit = defineEmits<{
  retry: [
  ]
  change: [
        value: ServerContentSelection | null,
  ]
  preview: [
        token: string,
        value: ServerContentPreview,
  ]
}>()
const current = ref<ServerContentPreview>()
const expanded = ref(false)
const selected = ref<string[]>([])
const busy = ref(false)
const localError = ref('')
let generation = 0
let disposed = false
const files = computed(() => current.value?.files.slice(0, expanded.value ? undefined : 6) ?? [])
const selectedBytes = computed(() => (current.value?.files ?? []).filter(file => selected.value.includes(file.token)).reduce((sum, file) => sum + file.size, 0))
const selectedText = computed(() => `${selected.value.length} 个文件 · ${bytes(selectedBytes.value)}`)
function bytes(value: number) {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let at = 0
  while (value >= 1024 && at < units.length - 1) {
    value /= 1024
    at++
  }
  return `${value.toFixed(at > 1 ? 1 : 0)} ${units[at]}`
}
function owns(revision: number, source: ServerDataSource, token: string) {
  return !disposed && revision === generation && source === props.source && token === props.resultToken
}
function clearSelection() {
  selected.value = []
  emit('change', null)
}
watch(() => [props.source, props.resultToken] as const, () => {
  generation++
  current.value = props.preview
  busy.value = false
  expanded.value = false
  localError.value = ''
  clearSelection()
  if (props.autoLoad && !current.value)
    void load()
}, { immediate: true })
watch(() => props.preview, (preview) => {
  if (preview && preview !== current.value) {
    generation++
    current.value = preview
    busy.value = false
    localError.value = ''
    clearSelection()
  }
})
watch(selected, () => emit('change', null), { deep: true })
async function load(more = false) {
  const source = props.source
  if (!source || busy.value)
    return
  const preview = current.value
  const revision = ++generation
  const token = props.resultToken
  const offset = more ? preview?.nextOffset : undefined
  busy.value = true
  localError.value = ''
  emit('change', null)
  try {
    const value = await runDiscoveryRequest(source, 'content', () => previewServerResourceContent(source, { result_token: token, ...(more && preview ? { preview_token: preview.token, offset } : {}) }), () => owns(revision, source, token))
    if (!owns(revision, source, token))
      return
    if (more && (!preview || preview.token !== value.token || (value.nextOffset !== undefined && value.nextOffset <= (offset ?? 0))))
      throw new Error('文件清单已变化，请重新读取。')
    if (more && preview && value.files.some(file => preview.files.some(old => old.path === file.path || (old.token && old.token === file.token))))
      throw new Error('文件分页重复，请重新读取。')
    current.value = more && preview ? { ...value, files: [...preview.files, ...value.files] } : value
    if (current.value.files.length > 10000)
      throw new Error('文件数量超过显示上限，请缩小资源范围。')
    expanded.value = true
    if (!more)
      clearSelection()
    emit('preview', token, current.value)
  }
  catch (reason) {
    if (owns(revision, source, token))
      localError.value = reason instanceof Error ? reason.message : '实际文件暂不可用。'
  }
  finally {
    if (owns(revision, source, token))
      busy.value = false
  }
}
async function confirmSelection() {
  const source = props.source
  const preview = current.value
  if (!source || !preview?.complete || busy.value || !selected.value.length || selected.value.length > 500 || Date.parse(preview.expiresAt) <= Date.now())
    return
  const revision = ++generation
  const token = props.resultToken
  const entries = [...selected.value]
  busy.value = true
  localError.value = ''
  try {
    const value = await runDiscoveryRequest(source, 'content', () => selectServerResourceContent(source, { result_token: token, preview_token: preview.token, selected_entry_tokens: entries }), () => owns(revision, source, token))
    if (owns(revision, source, token))
      emit('change', value)
  }
  catch (reason) {
    if (owns(revision, source, token)) {
      emit('change', null)
      localError.value = reason instanceof Error ? reason.message : '选择未确认，请重试。'
    }
  }
  finally {
    if (owns(revision, source, token))
      busy.value = false
  }
}
onBeforeUnmount(() => {
  disposed = true
  generation++
})
</script>

<template>
  <section class="mt-3 rounded-xl border border-white/10 bg-white/[.025] p-3 text-xs leading-5 text-[var(--color-text-secondary)]" aria-label="实际文件内容">
    <div class="flex items-center justify-between gap-2">
      <strong>{{ current?.complete ? '已读取实际文件' : loading || busy ? '正在读取实际文件…' : '实际文件尚未确认' }}</strong>
      <button type="button" class="rounded-lg border border-white/15 px-2 py-1 disabled:opacity-40" :disabled="loading || busy" @click="autoLoad ? load() : emit('retry')">
        重新读取文件
      </button>
    </div>
    <template v-if="current">
      <p class="mt-1">
        共 {{ current.totalCount }} 项 · {{ bytes(current.totalSize) }}
      </p>
      <p v-for="(group, index) in current.analysis?.groups ?? []" :key="index" class="mt-1">
        {{ shareContentGroupLabel(group) }}
      </p>
      <ul class="mt-2 space-y-2" aria-label="实际文件名">
        <li v-for="file in files" :key="file.path" class="min-w-0 border-t border-white/8 pt-2">
          <label class="flex items-start gap-2"><input v-if="canSelect && !file.isDirectory && file.token" v-model="selected" type="checkbox" :value="file.token" :disabled="busy || !current.complete"><span class="min-w-0"><b class="break-all">{{ file.name }}</b><small class="block break-all">{{ file.path }} · {{ bytes(file.size) }} · {{ ({ video: '视频', subtitle: '字幕', archive: '压缩包', directory: '目录', other: '其他文件' })[file.kind] }}</small><small v-if="file.episodes.length" class="block">{{ file.season !== undefined ? `第 ${file.season} 季` : '季待确认' }} · 第 {{ compactEpisodeSet(file.episodes) }} 集<template v-if="file.workStatus !== 'matched'"> · 作品待确认</template></small><small v-else-if="file.kind === 'video'" class="block">集数或作品身份待确认</small><small v-for="(subtitle, index) in file.subtitles" :key="index" class="block">{{ subtitle.language === 'zh' ? subtitle.script === 'Hans' ? '简体中文' : subtitle.script === 'Hant' ? '繁体中文' : '中文' : subtitle.language }} · {{ subtitle.source === 'sidecar_filename' ? '实际字幕文件的名称标注' : '发布名称声明，未确认内嵌字幕' }}</small></span></label>
        </li>
      </ul>
      <div class="mt-2 flex flex-wrap gap-2">
        <button v-if="current.files.length > 6" type="button" class="rounded-lg border border-white/15 px-2 py-1" @click="expanded = !expanded">
          {{ expanded ? '收起列表' : `展开已读取的 ${current.files.length} 项` }}
        </button>
        <button v-if="current.nextOffset !== undefined" type="button" class="rounded-lg border border-white/15 px-2 py-1 disabled:opacity-40" :disabled="busy" @click="load(true)">
          读取更多文件
        </button>
        <button v-if="canSelect" type="button" class="rounded-lg border border-white/15 px-2 py-1 disabled:opacity-40" :disabled="busy || !selected.length || selected.length > 500 || !current.complete" @click="confirmSelection">
          {{ busy ? '正在核对…' : `确认选择 · ${selectedText}` }}
        </button>
      </div>
    </template>
    <p v-if="localError || error" class="mt-2 text-amber-200" role="status">
      {{ localError || error }}
    </p>
    <p v-else-if="!current && !loading && !busy" class="mt-2">
      尚未取得实际文件；标题不能证明包内集数。可独立重新读取。
    </p>
  </section>
</template>
