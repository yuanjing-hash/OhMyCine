<script setup lang="ts">
import { computed } from 'vue'
import { useTheme } from '@/theme'

const { theme, toggleTheme } = useTheme()
const targetLabel = computed(() => theme.value === 'light' ? '深色' : '白色')
const accessibleLabel = computed(() => `当前为${theme.value === 'light' ? '白色' : '深色'}主题，切换到${targetLabel.value}主题`)
</script>

<template>
  <button class="theme-toggle" type="button" :aria-label="accessibleLabel" :title="accessibleLabel" @click="toggleTheme">
    <svg v-if="theme === 'light'" viewBox="0 0 24 24" aria-hidden="true"><path d="M21 12.8A8.5 8.5 0 1 1 11.2 3 6.8 6.8 0 0 0 21 12.8Z" /></svg>
    <svg v-else viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></svg>
    <span class="theme-toggle__label">{{ targetLabel }}主题</span>
  </button>
</template>
