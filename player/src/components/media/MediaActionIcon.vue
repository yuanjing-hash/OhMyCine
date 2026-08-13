<script setup lang="ts">
import type { MediaActionId } from '@/services/mediaActions'

defineProps<{ action: MediaActionId }>()
</script>

<template>
  <svg v-if="action === 'play'" viewBox="0 0 24 24" aria-hidden="true"><path d="m8 5 11 7-11 7V5Z" /></svg>
  <svg v-else-if="action === 'viewDetails' || action === 'openLibrary'" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5.5h6l2 2h8v11H4v-13Z" /><path v-if="action === 'viewDetails'" d="M12 11h.01M9 15h6" /></svg>
  <svg v-else-if="action === 'markPlayed' || action === 'markUnplayed'" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8" /><path d="m8.5 12 2.2 2.2 4.8-5" /></svg>
  <svg v-else-if="action === 'favorite' || action === 'unfavorite'" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19.5 4.8 12.8A4.8 4.8 0 0 1 11.6 6l.4.5.4-.5a4.8 4.8 0 0 1 6.8 6.8L12 19.5Z" /></svg>
  <svg v-else-if="action === 'deleteMedia'" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 7h14M9 7V4h6v3M7.5 7l.7 13h7.6l.7-13M10 11v5M14 11v5" /></svg>
  <svg v-else viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8" /><path d="M12 8v8M8 12h8" /></svg>
</template>

<style scoped>
svg { width: 1.15rem; height: 1.15rem; fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
svg:first-child { fill: currentColor; stroke: none; }
</style>
