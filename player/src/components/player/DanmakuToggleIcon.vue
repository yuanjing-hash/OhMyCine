<script setup lang="ts">
defineProps<{ enabled: boolean }>()
</script>

<template>
  <svg class="danmaku-toggle-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <path d="M3.5 7.5h17M3.5 12h10M3.5 16.5h14" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
    <path v-if="enabled" d="m16.5 11.8 1.8 1.8 3.2-3.7" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" />
    <path v-else d="M4 4 20 20" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" />
  </svg>
</template>

<style scoped>
.danmaku-toggle-icon { display: block; }
</style>
