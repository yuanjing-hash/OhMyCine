<script setup lang="ts">
import type { LayoutContextActionIcon } from '@/services/layoutContextActions'

defineProps<{
  name: LayoutContextActionIcon
}>()
</script>

<template>
  <svg v-if="name === 'rescrape'" viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <path d="M20 7v5h-5" />
    <path d="M18.2 17.2A8 8 0 1 1 20 12" />
  </svg>
  <svg v-else-if="name === 'scan'" viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <path d="M4 7h16M4 12h16M4 17h10" />
    <circle cx="18" cy="17" r="2.5" />
  </svg>
  <svg v-else viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <path d="M3.5 7.5A2.5 2.5 0 0 1 6 5h3.2l2 2H18a2.5 2.5 0 0 1 2.5 2.5v7A2.5 2.5 0 0 1 18 19H6a2.5 2.5 0 0 1-2.5-2.5v-9Z" />
  </svg>
</template>

<style scoped>
svg {
  width: 1.125rem;
  height: 1.125rem;
  stroke: currentColor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}
</style>
