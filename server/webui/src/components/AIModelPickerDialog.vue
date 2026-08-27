<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { filterAIModels } from '@/ai-model-picker'
import type { AIProviderModel } from '@/types/api'

const props = withDefaults(defineProps<{
  open: boolean
  models: AIProviderModel[]
  selectedModel: string
  loading?: boolean
}>(), { loading: false })
const emit = defineEmits<{ close: []; select: [modelID: string] }>()

const dialog = ref<HTMLElement | null>(null)
const searchInput = ref<HTMLInputElement | null>(null)
const query = ref('')
const filteredModels = computed(() => filterAIModels(props.models, query.value))
let previousFocus: HTMLElement | null = null

watch(() => props.open, async open => {
  if (!open) {
    query.value = ''
    await nextTick()
    previousFocus?.focus()
    previousFocus = null
    return
  }
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  query.value = ''
  await nextTick()
  searchInput.value?.focus()
}, { immediate: true })

onBeforeUnmount(() => previousFocus?.focus())

function close() {
  emit('close')
}

function select(modelID: string) {
  emit('select', modelID)
  emit('close')
}

function isSelected(model: AIProviderModel) {
  return props.selectedModel.trim() === model.id
}

function modelLabel(model: AIProviderModel) {
  return isSelected(model) ? `${model.id}，当前已选` : `选择模型 ${model.id}`
}

function showDisplayName(model: AIProviderModel) {
  const name = model.display_name.trim()
  return name !== '' && name !== model.id
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    close()
    return
  }
  if (event.key !== 'Tab' || !dialog.value) return
  const focusable = [...dialog.value.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), [href], [tabindex]:not([tabindex="-1"])')]
  if (focusable.length === 0) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="model-picker-overlay fixed inset-0 z-100 flex items-center justify-center p-4" @mousedown.self="close">
      <section ref="dialog" role="dialog" aria-modal="true" aria-labelledby="ai-model-picker-title" aria-describedby="ai-model-picker-description" class="panel flex max-h-[min(46rem,92vh)] w-full max-w-3xl flex-col overflow-hidden" @keydown="onKeydown">
        <header class="flex items-start justify-between gap-4">
          <div>
            <h2 id="ai-model-picker-title" class="m-0 text-xl">选择 AI 模型</h2>
            <p id="ai-model-picker-description" class="page-description mb-0 mt-2 text-sm">选择只会回填模型 ID；请返回设置页点击“保存 AI 设置”后再持久化。</p>
          </div>
          <button type="button" class="btn-secondary" aria-label="关闭 AI 模型选择器" @click="close">关闭</button>
        </header>

        <div class="mt-5">
          <label class="label" for="ai-model-search">搜索模型</label>
          <input id="ai-model-search" ref="searchInput" v-model="query" class="input" type="search" autocomplete="off" placeholder="输入模型 ID 或显示名称" :disabled="loading" />
          <p v-if="!loading && models.length" class="text-subtle mb-0 mt-2 text-xs">显示 {{ filteredModels.length }} / {{ models.length }} 个模型</p>
        </div>

        <div class="semantic-inset mt-4 min-h-56 flex-1 overflow-y-auto p-2">
          <div v-if="loading" role="status" class="text-muted flex min-h-52 items-center justify-center text-sm">正在读取模型列表…</div>
          <div v-else-if="models.length === 0" role="status" class="text-subtle flex min-h-52 items-center justify-center text-sm">Provider 没有返回可用模型，请关闭窗口后手动填写模型 ID。</div>
          <div v-else-if="filteredModels.length === 0" role="status" class="text-subtle flex min-h-52 items-center justify-center text-sm">没有匹配“{{ query.trim() }}”的模型</div>
          <template v-else>
            <button
              v-for="model in filteredModels"
              :key="model.id"
              type="button"
              class="semantic-list-item model-picker-item mb-1 flex w-full items-center justify-between gap-4 p-3 text-left"
              :class="{ 'semantic-list-item--selected': isSelected(model) }"
              :aria-label="modelLabel(model)"
              :aria-pressed="isSelected(model)"
              @click="select(model.id)"
            >
              <span class="min-w-0">
                <strong class="block break-all font-mono text-sm">{{ model.id }}</strong>
                <span v-if="showDisplayName(model)" class="text-subtle mt-1 block text-xs">{{ model.display_name }}</span>
              </span>
              <span v-if="isSelected(model)" class="status-chip status-chip--ready shrink-0">当前选择</span>
            </button>
          </template>
        </div>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.model-picker-overlay { background: var(--overlay); }
.model-picker-item { min-height: 3.65rem; }
</style>
