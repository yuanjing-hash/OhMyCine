<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import UpdateDialog from '@/components/layout/UpdateDialog.vue'
import MediaActionHost from '@/components/media/MediaActionHost.vue'
import MediaCollectionDialog from '@/components/media/MediaCollectionDialog.vue'
import MediaEditorHost from '@/components/media/MediaEditorHost.vue'
import { configureMediaActionController, createCollectionMediaActionAdapter, createDeleteMediaActionAdapter, createDownloadMediaActionAdapter, createMaintenanceMediaActionAdapter, createNavigationMediaActionAdapter, createPlayedStateMediaActionAdapter, MediaActionController, publishFeedback, requestMediaActionConfirmation } from '@/services/mediaActions'
import { COLLECTIONS_CHANGED_EVENT } from '@/services/mediaCollections'
import { PLAYED_STATE_CHANGED_EVENT } from '@/services/playbackHistory'
import { startPlaybackHistorySync } from '@/services/playbackHistorySync'
import { initializeServerDeepLinks } from '@/services/serverDeepLink'
import { useDataSourceStore } from '@/stores/datasource'
import { useDownloadStore } from '@/stores/downloads'
import { useUpdaterStore } from '@/stores/updater'

const store = useDataSourceStore()
const updater = useUpdaterStore()
const downloads = useDownloadStore()
const router = useRouter()
configureMediaActionController(new MediaActionController({
  adapters: [createDeleteMediaActionAdapter(), createPlayedStateMediaActionAdapter({ resolveSource: sourceId => store.getSource(sourceId) }), createCollectionMediaActionAdapter(sourceId => store.getSource(sourceId)), createDownloadMediaActionAdapter(sourceId => store.getSource(sourceId), requestMediaActionConfirmation), createMaintenanceMediaActionAdapter(router, sourceId => store.getSource(sourceId), sourceId => store.orderedConfigs.find(config => config.id === sourceId)), createNavigationMediaActionAdapter(router)],
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
let disposeServerDeepLinks: (() => void) | undefined
let disposePlaybackHistorySync: (() => void) | undefined

onMounted(() => {
  store.loadConfigs()
  document.addEventListener('contextmenu', suppressNativeContextMenu)
  void updater.initialize().then(() => updater.scheduleStartupCheck())
  void downloads.initialize()
  void initializeServerDeepLinks(router, store).then((dispose) => {
    disposeServerDeepLinks = dispose
  })
  disposePlaybackHistorySync = startPlaybackHistorySync(store)
})

onBeforeUnmount(() => {
  document.removeEventListener('contextmenu', suppressNativeContextMenu)
  store.stopMediaChangeWatchers()
  updater.cancelStartupCheck()
  downloads.dispose()
  disposeServerDeepLinks?.()
  disposePlaybackHistorySync?.()
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
