<script setup lang="ts">
import { onBeforeUnmount, onMounted, watch } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import UpdateDialog from '@/components/layout/UpdateDialog.vue'
import { createRawSourceAutoIndexTargets, createRawSourceLocalWatcherController, rawSourceIndexScheduler } from '@/services/scraper'
import { useDataSourceStore } from '@/stores/datasource'
import { useUpdaterStore } from '@/stores/updater'

const store = useDataSourceStore()
const updater = useUpdaterStore()
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
  void updater.initialize().then(() => updater.scheduleStartupCheck())
})

watch(
  () => store.orderedConfigs,
  configs => void localWatcherController.sync(configs),
  { deep: true },
)

onBeforeUnmount(() => {
  rawSourceIndexScheduler.stopAutoIndexing()
  void localWatcherController.dispose()
  updater.cancelStartupCheck()
})
</script>

<template>
  <AppLayout>
    <RouterView />
  </AppLayout>
  <UpdateDialog />
</template>
