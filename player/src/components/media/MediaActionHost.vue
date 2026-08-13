<script setup lang="ts">
import type { ResolvedMediaAction } from '@/services/mediaActions'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import {
  closeMediaActionMenu,
  getMediaActionController,
  publishFeedback,
  resolveMediaActionConfirmation,
  useMediaActionRuntime,
} from '@/services/mediaActions'
import MediaActionConfirmationDialog from './MediaActionConfirmationDialog.vue'
import MediaActionMenu from './MediaActionMenu.vue'

const runtime = useMediaActionRuntime()
const actions = ref<ResolvedMediaAction[]>([])
const loading = ref(false)
const viewportWidth = ref(window.innerWidth)
const feedbackVisible = ref(false)
let resolveGeneration = 0
let feedbackTimer: number | undefined

const presentation = computed<'popover' | 'sheet'>(() => {
  const requested = runtime.menuRequest.value?.presentation ?? 'auto'
  if (requested !== 'auto')
    return requested
  return viewportWidth.value <= 767 || window.matchMedia('(hover: none) and (pointer: coarse)').matches ? 'sheet' : 'popover'
})
const popoverStyle = computed(() => {
  const anchor = runtime.menuRequest.value?.anchor
  if (!anchor || presentation.value !== 'popover')
    return undefined
  const width = 336
  const margin = 12
  const left = Math.min(Math.max(margin, anchor.x), Math.max(margin, viewportWidth.value - width - margin))
  const top = Math.min(Math.max(margin, anchor.y), Math.max(margin, window.innerHeight - 420))
  return { left: `${left}px`, top: `${top}px` }
})

watch(() => runtime.menuRequest.value, (request) => {
  actions.value = []
  if (!request)
    return
  const generation = ++resolveGeneration
  loading.value = true
  void getMediaActionController().resolve(request.target).then((resolved) => {
    if (generation === resolveGeneration)
      actions.value = resolved
  }).catch((error) => {
    if (generation !== resolveGeneration)
      return
    publishFeedback({
      id: Date.now(),
      kind: 'error',
      message: toSafeErrorMessage(error, '读取媒体操作失败，请稍后重试。'),
    })
  }).finally(() => {
    if (generation === resolveGeneration)
      loading.value = false
  })
}, { immediate: true })

watch(() => runtime.feedback.value, (next) => {
  if (!next)
    return
  if (feedbackTimer)
    window.clearTimeout(feedbackTimer)
  feedbackVisible.value = true
  feedbackTimer = window.setTimeout(() => {
    feedbackTimer = undefined
    feedbackVisible.value = false
  }, 3200)
})

onMounted(() => window.addEventListener('resize', updateViewport))
onBeforeUnmount(() => {
  window.removeEventListener('resize', updateViewport)
  if (feedbackTimer)
    window.clearTimeout(feedbackTimer)
})

function updateViewport() {
  viewportWidth.value = window.innerWidth
}

async function execute(action: ResolvedMediaAction) {
  const request = runtime.menuRequest.value
  if (!request || action.availability !== 'available' || action.busy)
    return
  const target = request.target
  actions.value = actions.value.map(entry => entry.action === action.action ? { ...entry, busy: true } : entry)
  const outcome = await getMediaActionController().execute(target, action.action)
  if (outcome.status === 'completed') {
    closeMediaActionMenu()
    return
  }
  actions.value = await getMediaActionController().resolve(target)
}
</script>

<template>
  <Teleport to="body">
    <Transition name="media-action-fade">
      <div v-if="runtime.menuRequest.value" class="media-action-layer" :class="`is-${presentation}`">
        <button class="media-action-scrim" type="button" aria-label="关闭操作菜单" @click="closeMediaActionMenu" />
        <div class="media-action-position" :style="popoverStyle">
          <MediaActionMenu
            :target="runtime.menuRequest.value.target"
            :actions="actions"
            :presentation="presentation"
            :loading="loading"
            @close="closeMediaActionMenu"
            @execute="execute"
          />
        </div>
      </div>
    </Transition>

    <MediaActionConfirmationDialog
      v-if="runtime.pendingConfirmation.value"
      :confirmation="runtime.pendingConfirmation.value.confirmation"
      @resolve="resolveMediaActionConfirmation"
    />

    <Transition name="media-feedback">
      <p v-if="runtime.feedback.value && feedbackVisible" class="media-action-feedback" :class="`is-${runtime.feedback.value.kind}`" role="status" aria-live="polite">
        {{ runtime.feedback.value.message }}
      </p>
    </Transition>
  </Teleport>
</template>

<style scoped>
.media-action-layer { position: fixed; inset: 0; z-index: 1200; }
.media-action-scrim { position: absolute; inset: 0; background: transparent; }
.media-action-position { position: absolute; }
.media-action-layer.is-sheet .media-action-scrim { background: var(--chrome-scrim); backdrop-filter: blur(8px); }
.media-action-layer.is-sheet .media-action-position { right: 0; bottom: 0; left: 0; }
.media-action-feedback { position: fixed; z-index: 1400; right: 1rem; bottom: 1rem; max-width: min(24rem, calc(100vw - 2rem)); border: 1px solid var(--chrome-border); border-radius: 9px; padding: .75rem 1rem; color: var(--color-text); background: var(--chrome-surface); box-shadow: var(--chrome-shadow); font-size: .78rem; }
.media-action-feedback.is-error { border-color: color-mix(in srgb, var(--color-error) 52%, transparent); }
.media-action-fade-enter-active,.media-action-fade-leave-active,.media-feedback-enter-active,.media-feedback-leave-active { transition: opacity 160ms ease; }
.media-action-fade-enter-active .media-action-position,.media-action-fade-leave-active .media-action-position { transition: transform 190ms var(--ease-out); }
.media-action-fade-enter-from,.media-action-fade-leave-to,.media-feedback-enter-from,.media-feedback-leave-to { opacity: 0; }
.media-action-layer.is-sheet.media-action-fade-enter-from .media-action-position,.media-action-layer.is-sheet.media-action-fade-leave-to .media-action-position { transform: translateY(1rem); }
@media (max-width: 767px), (hover: none) and (pointer: coarse) { .media-action-feedback { right: .75rem; bottom: calc(.75rem + env(safe-area-inset-bottom)); left: .75rem; max-width: none; text-align: center; } }
@media (prefers-reduced-motion: reduce) { .media-action-fade-enter-active,.media-action-fade-leave-active,.media-feedback-enter-active,.media-feedback-leave-active,.media-action-fade-enter-active .media-action-position,.media-action-fade-leave-active .media-action-position { transition: none; } }
</style>
