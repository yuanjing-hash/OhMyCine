<script setup lang="ts">
import ThemeToggle from '@/components/ThemeToggle.vue'

defineProps<{ width?: 'medium' | 'large' }>()
</script>

<template>
  <main class="auth-shell">
    <div class="auth-shell__theme"><ThemeToggle /></div>
    <section class="auth-card" :class="{ 'auth-card--large': width === 'large' }"><slot /></section>
  </main>
</template>
