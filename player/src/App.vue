<script setup lang="ts">
import { onBeforeUnmount, onMounted, watch } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { createRawSourceAutoIndexTargets, createRawSourceLocalWatcherController, rawSourceIndexScheduler } from '@/services/scraper'
import { useDataSourceStore } from '@/stores/datasource'

const store = useDataSourceStore()
const localWatcherController = createRawSourceLocalWatcherController({
  resolveSource: sourceId => store.getSource(sourceId),
  markDirty: target => rawSourceIndexScheduler.markIncrementalDirty(target),
})

onMounted(() => {
  store.loadConfigs()
  rawSourceIndexScheduler.startAutoIndexing({
    getTargets: async () => {
      await store.syncManager().catch(() => undefined)
      return createRawSourceAutoIndexTargets(store.orderedConfigs, sourceId => store.getSource(sourceId))
    },
  })
  void store.syncManager().finally(() => localWatcherController.sync(store.orderedConfigs))
})

watch(
  () => store.orderedConfigs,
  configs => void localWatcherController.sync(configs),
  { deep: true },
)

onBeforeUnmount(() => {
  rawSourceIndexScheduler.stopAutoIndexing()
  void localWatcherController.dispose()
})
</script>

<template>
  <AppLayout>
    <RouterView />
  </AppLayout>
</template>
