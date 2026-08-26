<script setup lang="ts">
defineProps<{ visible: boolean, busy?: boolean }>()
defineEmits<{ refresh: [] }>()
</script>

<template>
  <Transition name="server-library-update">
    <button
      v-if="visible"
      type="button"
      class="server-library-update-notice"
      aria-live="polite"
      :disabled="busy"
      @click="$emit('refresh')"
    >
      <strong>媒体库已更新</strong>
      <span>{{ busy ? '正在刷新当前列表…' : '点击刷新当前列表' }}</span>
    </button>
  </Transition>
</template>

<style scoped>
.server-library-update-notice {
  position: fixed;
  z-index: 1450;
  top: calc(1rem + env(safe-area-inset-top));
  left: 50%;
  display: grid;
  min-width: 12rem;
  transform: translateX(-50%);
  gap: .15rem;
  border: 1px solid var(--chrome-border);
  border-radius: 10px;
  padding: .7rem 1rem;
  color: var(--color-text);
  background: var(--chrome-surface);
  box-shadow: var(--chrome-shadow);
  text-align: center;
  cursor: pointer;
}

.server-library-update-notice span {
  color: var(--color-text-muted);
  font-size: .72rem;
}

.server-library-update-enter-active,
.server-library-update-leave-active {
  transition: opacity 160ms ease, transform 160ms ease;
}

.server-library-update-enter-from,
.server-library-update-leave-to {
  opacity: 0;
  transform: translate(-50%, -.5rem);
}

@media (prefers-reduced-motion: reduce) {
  .server-library-update-enter-active,
  .server-library-update-leave-active {
    transition: none;
  }
}
</style>
