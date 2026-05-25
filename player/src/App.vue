<script setup lang="ts">
import { onMounted } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { createRawSourceAutoIndexTargets, rawSourceIndexScheduler } from '@/services/scraper'
import { useDataSourceStore } from '@/stores/datasource'

const store = useDataSourceStore()

onMounted(() => {
  store.loadConfigs()
  rawSourceIndexScheduler.startAutoIndexing({
    getTargets: async () => {
      await store.syncManager().catch(() => undefined)
      return createRawSourceAutoIndexTargets(store.orderedConfigs, sourceId => store.getSource(sourceId))
    },
  })
})
</script>

<template>
  <AppLayout>
    <RouterView />
  </AppLayout>
</template>
