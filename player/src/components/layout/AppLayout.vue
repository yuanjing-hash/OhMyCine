<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import BackButton from './BackButton.vue'
import DataSourceSidebar from './DataSourceSidebar.vue'
import FloatingControls from './FloatingControls.vue'
import WindowChrome from './WindowChrome.vue'

const route = useRoute()
const isPlayerRoute = computed(() => route.name === 'player')
</script>

<template>
  <div
    class="app-window relative text-text font-sans"
    :class="isPlayerRoute ? 'app-window--player' : 'app-window--cinema'"
  >
    <!-- Content fills the full area -->
    <main class="cinema-scrollbar absolute inset-0 z-0 overflow-auto">
      <slot />
    </main>

    <!-- Floating glass sidebar — hidden on player page for immersive layout -->
    <DataSourceSidebar v-if="!isPlayerRoute" />

    <!-- Floating back navigation for non-home pages — hidden on player page -->
    <BackButton v-if="!isPlayerRoute" />

    <!-- Floating glass top bar: always visible (drag region + window controls).
         On player page, hide center nav buttons only. -->
    <WindowChrome :hide-nav="isPlayerRoute" />

    <!-- Bottom-right floating controls (player + theme) -->
    <FloatingControls />
  </div>
</template>

<style scoped>
.app-window--cinema {
  background: var(--color-bg);
}

.app-window--player {
  background: transparent;
}
</style>
