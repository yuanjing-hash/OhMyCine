<script setup lang="ts">
import type { MediaItem, MediaLibrary } from '@/services/datasource/types'
import type { LocalCollectionMember } from '@/services/mediaCollections'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import MediaGrid from '@/components/media/MediaGrid.vue'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { createMediaActionTarget, openMediaActionContextMenu } from '@/services/mediaActions'
import { annotateMissingCollectionSources, COLLECTIONS_CHANGED_EVENT, listLocalMediaCollections, removeLocalCollectionMember } from '@/services/mediaCollections'
import { useDataSourceStore } from '@/stores/datasource'

interface FavoriteGroup { id: string, title: string, owner: string, items: MediaItem[], missing: LocalCollectionMember[], error?: string }
const store = useDataSourceStore()
const router = useRouter()
const groups = ref<FavoriteGroup[]>([])
const loading = ref(true)
let generation = 0
const hasFavorites = computed(() => groups.value.some(group => group.items.length || group.missing.length || group.error))

onMounted(() => {
  window.addEventListener(COLLECTIONS_CHANGED_EVENT, refresh)
  void refresh()
})
onBeforeUnmount(() => window.removeEventListener(COLLECTIONS_CHANGED_EVENT, refresh))

async function refresh() {
  const current = ++generation
  loading.value = true
  store.loadConfigs()
  await store.syncManager().catch(() => undefined)
  const configs = store.orderedConfigs.filter(config => config.enabled !== false)
  const collections = annotateMissingCollectionSources(await listLocalMediaCollections().catch(() => []), new Set(configs.map(config => config.id)))
  const local = collections.find(collection => collection.kind === 'favorite')
  const next: FavoriteGroup[] = [{ id: 'player-local', title: 'Player 本地收藏', owner: 'Player 本地 · 跨来源', items: (local?.members ?? []).filter(member => !member.missing).map(memberToItem), missing: (local?.members ?? []).filter(member => member.missing) }]
  const providers = await Promise.all(configs.filter(config => config.type === 'emby' || config.type === 'jellyfin').map(async (config): Promise<FavoriteGroup> => {
    const source = store.getSource(config.id)
    try {
      if (!source?.listFavorites)
        throw new Error('当前媒体服务版本不支持收藏列表。')
      return { id: config.id, title: `${config.displayName ?? config.name} 收藏`, owner: '媒体服务原生', items: await source.listFavorites(), missing: [] }
    }
    catch (error) {
      return { id: config.id, title: `${config.displayName ?? config.name} 收藏`, owner: '媒体服务原生', items: [], missing: [], error: toSafeErrorMessage(error, '加载媒体服务收藏失败。') }
    }
  }))
  if (current === generation)
    groups.value = [...next, ...providers]
  loading.value = false
}
function memberToItem(member: LocalCollectionMember): MediaItem {
  return { id: member.itemId, sourceId: member.sourceId, name: member.title, type: mediaType(member.mediaType), path: member.itemId, posterUrl: member.posterUrl ?? undefined, backdropUrl: member.backdropUrl ?? undefined, favorite: true }
}
function mediaType(value: string): MediaItem['type'] {
  return ['movie', 'series', 'season', 'episode', 'folder', 'file'].includes(value) ? value as MediaItem['type'] : 'file'
}
function openItem(item: MediaItem | MediaLibrary) {
  if ('path' in item)
    void router.push({ name: 'media-detail', params: { sourceId: item.sourceId, itemId: item.id } })
}
function openActions(item: MediaItem | MediaLibrary, event: MouseEvent) {
  if (!('path' in item))
    return
  const config = store.orderedConfigs.find(entry => entry.id === item.sourceId)
  openMediaActionContextMenu(createMediaActionTarget(item, config?.type, config?.displayName ?? config?.name), event)
}
async function removeMissing(member: LocalCollectionMember) {
  const collection = (await listLocalMediaCollections()).find(item => item.kind === 'favorite')
  if (collection)
    await removeLocalCollectionMember(collection.id, member.sourceId, member.itemId)
  await refresh()
}
</script>

<template>
  <main class="mobile-nav-safe min-h-full px-5 pb-24 pt-20 sm:px-20">
    <div class="mx-auto max-w-7xl">
      <header class="mb-8">
        <p class="text-xs uppercase tracking-[.28em] text-white/38">
          Favorites
        </p><h1 class="mt-2 text-3xl font-bold text-white">
          我的收藏
        </h1><p class="mt-2 text-sm text-white/48">
          Player 本地收藏与媒体服务原生收藏按归属分别展示。
        </p>
      </header><div v-if="loading" class="text-sm text-white/45">
        正在加载收藏…
      </div><div v-else-if="hasFavorites" class="space-y-10">
        <section v-for="group in groups" v-show="group.items.length || group.missing.length || group.error" :key="group.id">
          <div class="mb-4">
            <h2 class="text-xl font-bold text-white">
              {{ group.title }}
            </h2><p class="mt-1 text-xs text-white/40">
              {{ group.owner }}
            </p>
          </div><div v-if="group.error" class="rounded-2xl border border-red-400/18 bg-red-400/7 p-4 text-sm text-red-100">
            {{ group.error }}
            <button type="button" class="ml-2 rounded-lg bg-white/8 px-3 py-1.5 text-xs" @click="refresh">
              重试
            </button>
          </div><MediaGrid v-else :items="group.items" empty-title="暂无收藏" @select="openItem" @play="openItem" @contextmenu="openActions" /><div v-if="group.missing.length" class="mt-4 rounded-2xl border border-amber-400/18 bg-amber-400/7 p-4">
            <p class="text-sm font-semibold text-amber-100">
              来源缺失
            </p><div v-for="member in group.missing" :key="`${member.sourceId}:${member.itemId}`" class="mt-2 flex items-center justify-between gap-3 text-sm text-white/58">
              <span class="truncate">{{ member.title }} · {{ member.sourceId }}</span><button class="rounded-lg bg-white/8 px-3 py-1.5 text-xs" @click="removeMissing(member)">
                移除记录
              </button>
            </div>
          </div>
        </section>
      </div><div v-else class="glass-panel rounded-3xl p-10 text-center text-white/48">
        还没有收藏任何媒体。
      </div>
    </div>
  </main>
</template>
