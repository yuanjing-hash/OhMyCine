<script setup lang="ts">
import { onBeforeUnmount, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import UpdateDialog from '@/components/layout/UpdateDialog.vue'
import MediaActionHost from '@/components/media/MediaActionHost.vue'
import MediaCollectionDialog from '@/components/media/MediaCollectionDialog.vue'
import MediaEditorHost from '@/components/media/MediaEditorHost.vue'
import { configureMediaActionController, createCollectionMediaActionAdapter, createDeleteMediaActionAdapter, createDownloadMediaActionAdapter, createMaintenanceMediaActionAdapter, createNavigationMediaActionAdapter, createPlayedStateMediaActionAdapter, MediaActionController, publishFeedback, requestMediaActionConfirmation } from '@/services/mediaActions'
import { COLLECTIONS_CHANGED_EVENT } from '@/services/mediaCollections'
import { PLAYED_STATE_CHANGED_EVENT } from '@/services/playbackHistory'
import { createRawSourceAutoIndexTargets, createRawSourceLocalWatcherController, rawSourceIndexScheduler } from '@/services/scraper'
import { useDataSourceStore } from '@/stores/datasource'
import { useDownloadStore } from '@/stores/downloads'
import { useUpdaterStore } from '@/stores/updater'

const store = useDataSourceStore()
const updater = useUpdaterStore()
const downloads = useDownloadStore()
const router = useRouter()
configureMediaActionController(new MediaActionController({
  adapters: [createDeleteMediaActionAdapter({ resolveSource: sourceId => store.getSource(sourceId), resolveConfig: sourceId => store.orderedConfigs.find(config => config.id === sourceId) }), createPlayedStateMediaActionAdapter({ resolveSource: sourceId => store.getSource(sourceId) }), createCollectionMediaActionAdapter(sourceId => store.getSource(sourceId)), createDownloadMediaActionAdapter(sourceId => store.getSource(sourceId)), createMaintenanceMediaActionAdapter(router, sourceId => store.getSource(sourceId), sourceId => store.orderedConfigs.find(config => config.id === sourceId)), createNavigationMediaActionAdapter(router)],
  confirm: requestMediaActionConfirmation,
  invalidate: async (invalidation) => {
    store.getSource(invalidation.sourceId)?.clearCache?.()
    store.invalidateSourceRootSnapshot(invalidation.sourceId)
    if (invalidation.scopes.includes('home')) {
      store.invalidateHomeCache()
      await store.loadHomeSections({ force: true, background: true })
    }
    window.dispatchEvent(new CustomEvent(PLAYED_STATE_CHANGED_EVENT, { detail: invalidation }))
    if (invalidation.scopes.includes('collections'))
      window.dispatchEvent(new CustomEvent(COLLECTIONS_CHANGED_EVENT, { detail: invalidation }))
  },
  onFeedback: publishFeedback,
}))
const localWatcherController = createRawSourceLocalWatcherController({
  resolveSource: sourceId => store.getSource(sourceId),
  markDirty: target => rawSourceIndexScheduler.markIncrementalDirty(target),
})

onMounted(() => {
  store.loadConfigs()
  document.addEventListener('contextmenu', suppressNativeContextMenu)
  rawSourceIndexScheduler.startAutoIndexing({
    getTargets: async () => {
      await store.syncManager().catch(() => undefined)
      return createRawSourceAutoIndexTargets(store.orderedConfigs, sourceId => store.getSource(sourceId))
    },
  })
  void store.syncManager().finally(() => localWatcherController.sync(store.orderedConfigs))
  void updater.initialize().then(() => updater.scheduleStartupCheck())
  void downloads.initialize()
})

watch(
  () => store.orderedConfigs,
  configs => void localWatcherController.sync(configs),
  { deep: true },
)

onBeforeUnmount(() => {
  document.removeEventListener('contextmenu', suppressNativeContextMenu)
  rawSourceIndexScheduler.stopAutoIndexing()
  store.stopMediaChangeWatchers()
  void localWatcherController.dispose()
  updater.cancelStartupCheck()
  downloads.dispose()
})

function suppressNativeContextMenu(event: MouseEvent) {
  event.preventDefault()
}
</script>

<template>
  <AppLayout>
    <RouterView />
  </AppLayout>
  <UpdateDialog />
  <MediaActionHost />
  <MediaCollectionDialog />
  <MediaEditorHost />
</template>
