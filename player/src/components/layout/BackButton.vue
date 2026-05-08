<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()

const shouldShowBackButton = computed(() => route.path !== '/')

function goBack() {
  if (window.history.state?.back)
    router.back()
  else
    router.push('/')
}
</script>

<template>
  <button
    v-if="shouldShowBackButton"
    class="glass-panel gp-back-btn fixed left-6 top-3 flex h-11 items-center gap-2 rounded-2xl px-4 text-sm font-semibold transition-all duration-200"
    type="button"
    title="返回"
    aria-label="返回上一页"
    @click="goBack"
  >
    <svg
      width="16"
      height="16"
      viewBox="0 0 20 20"
      fill="none"
      aria-hidden="true"
    >
      <path
        d="M12.5 4.5L7 10l5.5 5.5M8 10h8"
        stroke="currentColor"
        stroke-width="1.7"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>
    返回
  </button>
</template>

<style scoped>
.gp-back-btn {
  z-index: 1010;
  color: var(--gp-text);
}
.gp-back-btn:hover {
  color: var(--gp-text-full);
  background: var(--gp-hover);
  transform: translateX(-2px);
}
.gp-back-btn:active {
  color: var(--gp-text-full);
  background: var(--gp-active);
  transform: translateX(-1px) scale(0.98);
}
</style>
