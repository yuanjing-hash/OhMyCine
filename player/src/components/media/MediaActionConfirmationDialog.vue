<script setup lang="ts">
import type { MediaActionConfirmation, MediaActionConfirmationResult } from '@/services/mediaActions'
import { computed, nextTick, onMounted, ref } from 'vue'

const props = defineProps<{ confirmation: MediaActionConfirmation }>()
const emit = defineEmits<{ resolve: [result: MediaActionConfirmationResult] }>()
const cancelRef = ref<HTMLButtonElement | null>(null)
const verification = ref('')
const deleteSourceFiles = ref(false)
const canConfirm = computed(() => !props.confirmation.requiredText || verification.value === props.confirmation.requiredText)

onMounted(async () => {
  await nextTick()
  cancelRef.value?.focus()
})

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    emit('resolve', { confirmed: false, deleteSourceFiles: false })
  }
}
</script>

<template>
  <div class="media-confirm-layer theme-adaptive" @keydown="handleKeydown">
    <button class="media-confirm-scrim" type="button" aria-label="取消" @click="emit('resolve', { confirmed: false, deleteSourceFiles: false })" />
    <section class="media-confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="media-confirm-title" aria-describedby="media-confirm-message">
      <h2 id="media-confirm-title">
        {{ confirmation.title }}
      </h2>
      <p id="media-confirm-message">
        {{ confirmation.message }}
      </p>
      <div v-if="confirmation.sourceDelete" class="media-confirm-source">
        <label>
          <input v-model="deleteSourceFiles" type="checkbox" :disabled="!confirmation.sourceDelete.available">
          <span>{{ confirmation.sourceDelete.label }}</span>
        </label>
        <small v-if="confirmation.sourceDelete.disabledReason">{{ confirmation.sourceDelete.disabledReason }}</small>
        <details v-if="confirmation.sourceDelete.pathSummaries.length">
          <summary>{{ confirmation.sourceDelete.itemCount }} 个扫描归属文件</summary>
          <ul>
            <li v-for="path in confirmation.sourceDelete.pathSummaries" :key="path">
              {{ path }}
            </li>
          </ul>
        </details>
      </div>
      <label v-if="confirmation.requiredText" class="media-confirm-verification">
        <span>输入“{{ confirmation.requiredText }}”以确认</span>
        <input v-model="verification" type="text" autocomplete="off">
      </label>
      <div class="media-confirm-actions">
        <button ref="cancelRef" type="button" class="media-confirm-cancel" @click="emit('resolve', { confirmed: false, deleteSourceFiles: false })">
          {{ confirmation.cancelLabel ?? '取消' }}
        </button>
        <button type="button" class="media-confirm-submit" :class="`is-${confirmation.danger}`" :disabled="!canConfirm" @click="emit('resolve', { confirmed: true, deleteSourceFiles })">
          {{ confirmation.confirmLabel }}
        </button>
      </div>
    </section>
  </div>
</template>

<style scoped>
.media-confirm-layer { position: fixed; inset: 0; z-index: 1300; display: grid; place-items: center; padding: 1rem; }
.media-confirm-scrim { position: absolute; inset: 0; background: var(--chrome-scrim); backdrop-filter: blur(10px); }
.media-confirm-dialog { position: relative; width: min(30rem, 100%); border: 1px solid var(--chrome-border); border-radius: 12px; padding: 1.25rem; color: var(--color-text); background: var(--chrome-surface); box-shadow: var(--chrome-shadow); }
.media-confirm-dialog h2 { font-size: 1.05rem; font-weight: 800; }
.media-confirm-dialog p { margin-top: .65rem; color: var(--color-text-secondary); font-size: .82rem; line-height: 1.65; }
.media-confirm-verification { display: block; margin-top: 1rem; }
.media-confirm-source { margin-top: 1rem; border: 1px solid var(--color-divider); border-radius: 9px; padding: .8rem; background: var(--surface-soft); }
.media-confirm-source label { display: flex; align-items: center; gap: .55rem; font-size: .78rem; font-weight: 700; }
.media-confirm-source small { display: block; margin-top: .4rem; color: var(--color-text-tertiary); font-size: .68rem; }
.media-confirm-source details { margin-top: .65rem; color: var(--color-text-secondary); font-size: .7rem; }
.media-confirm-source ul { max-height: 8rem; overflow: auto; margin-top: .4rem; padding-left: 1rem; }
.media-confirm-source li { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.media-confirm-verification span { display: block; color: var(--color-text-tertiary); font-size: .72rem; }
.media-confirm-verification input { width: 100%; height: 2.7rem; margin-top: .4rem; border: 1px solid var(--control-border); border-radius: 8px; padding: 0 .75rem; color: var(--control-text); background: var(--control-bg); outline: none; }
.media-confirm-verification input:focus { border-color: var(--control-border-hover); box-shadow: 0 0 0 3px var(--control-focus-ring); }
.media-confirm-actions { display: flex; justify-content: flex-end; gap: .55rem; margin-top: 1.2rem; }
.media-confirm-actions button { min-height: 2.6rem; border-radius: 8px; padding: 0 1rem; font-size: .78rem; font-weight: 750; }
.media-confirm-cancel { color: var(--color-text); background: var(--surface-soft); }
.media-confirm-submit { color: var(--color-text-inverse); background: var(--color-text); }
.media-confirm-submit.is-destructive { color: #fff; background: var(--color-error); }
.media-confirm-submit.is-caution { color: #111; background: var(--color-warning); }
.media-confirm-submit:disabled { cursor: not-allowed; opacity: .45; }
</style>
