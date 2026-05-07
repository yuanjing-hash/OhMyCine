<script setup lang="ts">
import DataSourceSidebar from './DataSourceSidebar.vue'
import FloatingControls from './FloatingControls.vue'
import WindowChrome from './WindowChrome.vue'
</script>

<template>
  <div class="app-window relative text-text font-sans">
    <!-- Content fills the full area -->
    <main class="cinema-scrollbar absolute inset-0 overflow-auto">
      <slot />
    </main>

    <!-- Floating glass sidebar -->
    <DataSourceSidebar />

    <!-- Floating glass top bar -->
    <WindowChrome />

    <!-- Bottom-right floating controls (player + theme) -->
    <FloatingControls />
  </div>
</template>
