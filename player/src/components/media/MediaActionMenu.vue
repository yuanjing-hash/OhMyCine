<script setup lang="ts">
import type { MediaActionGroup, MediaActionTarget, ResolvedMediaAction } from '@/services/mediaActions'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import MediaActionIcon from './MediaActionIcon.vue'

const props = defineProps<{
  target: MediaActionTarget
  actions: readonly ResolvedMediaAction[]
  presentation: 'popover' | 'sheet'
  loading?: boolean
}>()

const emit = defineEmits<{
  close: []
  execute: [action: ResolvedMediaAction]
}>()

const rootRef = ref<HTMLElement | null>(null)
const groupedActions = computed(() => {
  const groups: Array<{ group: MediaActionGroup, actions: ResolvedMediaAction[] }> = []
  for (const action of props.actions) {
    const existing = groups.find(entry => entry.group === action.group)
    if (existing)
      existing.actions.push(action)
    else
      groups.push({ group: action.group, actions: [action] })
  }
  return groups
})

watch(() => props.actions, focusFirstAction, { flush: 'post' })
onMounted(() => {
  document.addEventListener('keydown', handleDocumentKeydown)
  void focusFirstAction()
})
onBeforeUnmount(() => document.removeEventListener('keydown', handleDocumentKeydown))

async function focusFirstAction() {
  await nextTick()
  rootRef.value?.querySelector<HTMLButtonElement>('button.media-action-row:not(:disabled)')?.focus()
}

function handleDocumentKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    emit('close')
    return
  }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key))
    return
  const enabled = [...(rootRef.value?.querySelectorAll<HTMLButtonElement>('button.media-action-row:not(:disabled)') ?? [])]
  if (!enabled.length)
    return
  event.preventDefault()
  const current = enabled.indexOf(document.activeElement as HTMLButtonElement)
  const nextIndex = event.key === 'Home'
    ? 0
    : event.key === 'End'
      ? enabled.length - 1
      : event.key === 'ArrowDown'
        ? (current + 1 + enabled.length) % enabled.length
        : (current - 1 + enabled.length) % enabled.length
  enabled[nextIndex]?.focus()
}
</script>

<template>
  <section
    ref="rootRef"
    class="media-action-menu theme-adaptive"
    :class="`media-action-menu--${presentation}`"
    :role="presentation === 'sheet' ? 'dialog' : 'menu'"
    :aria-modal="presentation === 'sheet' ? 'true' : undefined"
    :aria-label="`${target.display.name} 的操作`"
    @pointerdown.stop
  >
    <div v-if="presentation === 'sheet'" class="media-action-handle" aria-hidden="true" />
    <header class="media-action-header">
      <div class="media-action-heading">
        <span>{{ target.kind === 'library' ? '媒体库操作' : '媒体操作' }}</span>
        <strong>{{ target.display.name }}</strong>
        <small v-if="target.display.sourceName">{{ target.display.sourceName }}</small>
      </div>
      <button class="media-action-close" type="button" aria-label="关闭操作菜单" @click="emit('close')">
        ×
      </button>
    </header>

    <div class="media-action-groups" :role="presentation === 'sheet' ? 'menu' : undefined" :aria-busy="loading">
      <p v-if="loading" class="media-action-empty">
        正在读取可用操作…
      </p>
      <template v-else-if="groupedActions.length">
        <div v-for="entry in groupedActions" :key="entry.group" class="media-action-group">
          <button
            v-for="action in entry.actions"
            :key="action.action"
            class="media-action-row"
            :class="{ 'is-danger': action.danger !== 'none' }"
            type="button"
            role="menuitem"
            :disabled="action.availability !== 'available' || action.busy"
            :aria-describedby="action.disabledReason ? `media-action-reason-${action.action}` : undefined"
            @click="emit('execute', action)"
          >
            <span class="media-action-icon"><MediaActionIcon :action="action.action" /></span>
            <span class="media-action-copy">
              <strong>{{ action.label }}</strong>
              <small v-if="action.disabledReason" :id="`media-action-reason-${action.action}`">{{ action.disabledReason }}</small>
              <small v-else-if="action.description">{{ action.description }}</small>
            </span>
            <span v-if="action.busy" class="media-action-spinner" aria-label="执行中" />
          </button>
        </div>
      </template>
      <p v-else class="media-action-empty">
        此对象当前没有可用操作。
      </p>
    </div>
  </section>
</template>

<style scoped>
.media-action-menu { display: flex; min-height: 0; flex-direction: column; overflow: hidden; color: var(--color-text); background: var(--chrome-surface); box-shadow: var(--chrome-shadow); backdrop-filter: blur(32px) saturate(1.35); }
.media-action-menu--popover { width: min(21rem, calc(100vw - 1.5rem)); max-height: min(38rem, var(--media-action-popover-max-height, calc(100vh - 1.5rem))); border: 1px solid var(--chrome-border); border-radius: 10px; }
.media-action-menu--sheet { width: 100%; max-height: min(82vh, 42rem); border: 1px solid var(--chrome-border); border-width: 1px 0 0; border-radius: 16px 16px 0 0; padding-bottom: max(1rem, env(safe-area-inset-bottom)); }
.media-action-handle { width: 2.7rem; height: .25rem; margin: .55rem auto .1rem; border-radius: 999px; background: var(--chrome-handle); }
.media-action-header { display: flex; flex: 0 0 auto; align-items: flex-start; justify-content: space-between; gap: 1rem; padding: .9rem 1rem .75rem; border-bottom: 1px solid var(--color-divider); }
.media-action-heading { min-width: 0; }
.media-action-heading span,.media-action-heading strong,.media-action-heading small { display: block; }
.media-action-heading span { color: var(--color-text-tertiary); font-size: .66rem; font-weight: 800; text-transform: uppercase; }
.media-action-heading strong { margin-top: .2rem; overflow: hidden; font-size: .92rem; text-overflow: ellipsis; white-space: nowrap; }
.media-action-heading small { margin-top: .15rem; color: var(--color-text-tertiary); font-size: .68rem; }
.media-action-close { display: flex; width: 2rem; height: 2rem; flex: 0 0 auto; align-items: center; justify-content: center; border-radius: 50%; color: var(--color-text-secondary); background: var(--surface-soft); font-size: 1.25rem; line-height: 1; }
.media-action-groups { min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: .4rem; }
.media-action-group + .media-action-group { margin-top: .35rem; padding-top: .35rem; border-top: 1px solid var(--color-divider); }
.media-action-row { display: grid; width: 100%; min-height: 3.2rem; grid-template-columns: 2rem minmax(0,1fr) 1rem; align-items: center; gap: .65rem; border-radius: 8px; padding: .42rem .55rem; color: var(--color-text); text-align: left; }
.media-action-row:hover:not(:disabled),.media-action-row:focus-visible { background: var(--surface-soft-hover); outline: none; }
.media-action-row:focus-visible { box-shadow: 0 0 0 2px var(--control-focus-ring); }
.media-action-row:disabled { cursor: not-allowed; opacity: .52; }
.media-action-row.is-danger { color: var(--color-error); }
.media-action-icon { display: flex; width: 2rem; height: 2rem; align-items: center; justify-content: center; border-radius: 8px; background: var(--surface-soft); }
.media-action-copy { min-width: 0; }
.media-action-copy strong,.media-action-copy small { display: block; }
.media-action-copy strong { font-size: .8rem; }
.media-action-copy small { margin-top: .12rem; overflow: hidden; color: var(--color-text-tertiary); font-size: .65rem; text-overflow: ellipsis; white-space: nowrap; }
.media-action-spinner { width: .85rem; height: .85rem; border: 2px solid var(--color-border); border-top-color: currentColor; border-radius: 50%; animation: media-action-spin .7s linear infinite; }
.media-action-empty { padding: 1.5rem 1rem; color: var(--color-text-tertiary); text-align: center; font-size: .78rem; }
@keyframes media-action-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .media-action-spinner { animation: none; } }
</style>
