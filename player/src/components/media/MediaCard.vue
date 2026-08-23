<script setup lang="ts">
import type { MediaItem, MediaLibrary } from '@/services/datasource/types'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { artworkCacheKey } from '@/services/imageCache'
import { beginMediaActionLongPress, cancelMediaActionLongPress, createMediaActionTarget, endMediaActionLongPress, moveMediaActionLongPress, openMediaActionContextMenu, openMediaActionMenu, suppressMediaActionClick } from '@/services/mediaActions'
import { getPlaybackProgress, PLAYED_STATE_CHANGED_EVENT } from '@/services/playbackHistory'
import { useDataSourceStore } from '@/stores/datasource'
import CachedImage from './CachedImage.vue'

const props = defineProps<{
  item: MediaItem | MediaLibrary
  kind?: 'poster' | 'library'
  disabled?: boolean
  contextMenuMode?: 'shared' | 'custom'
}>()

const emit = defineEmits<{
  select: [item: MediaItem | MediaLibrary]
  play: [item: MediaItem]
  contextmenu: [item: MediaItem | MediaLibrary, event: MouseEvent]
}>()

const store = useDataSourceStore()
const locallyPlayed = ref(false)

const isMediaItem = computed(() => hasMediaPath(props.item))
const title = computed(() => props.item.name)
const subtitle = computed(() => {
  const item = props.item
  if (!hasMediaPath(item))
    return item.itemCount == null ? '媒体库' : `${item.itemCount} 项内容`

  if (item.type === 'season')
    return item.seasonNumber == null ? '季' : `第 ${item.seasonNumber} 季`
  if (item.type === 'episode') {
    const episode = item.episodeNumber == null ? undefined : `第 ${item.episodeNumber} 集`
    const meta = [episode, item.duration ? `${Math.round(item.duration / 60)} 分钟` : undefined].filter(Boolean)
    return meta.join(' · ')
  }

  const meta = [item.year, item.duration ? `${Math.round(item.duration / 60)} 分钟` : undefined].filter(Boolean)
  return meta.join(' · ')
})
const posterUrl = computed(() => {
  if (props.kind === 'library')
    return props.item.backdropUrl ?? props.item.posterUrl
  if (hasMediaPath(props.item) && props.item.type === 'episode')
    return props.item.backdropUrl ?? props.item.posterUrl
  return props.item.posterUrl
})
const libraryArtworkCandidates = computed(() => {
  if (props.kind !== 'library' || hasMediaPath(props.item))
    return []
  return [...new Set(props.item.artworkCandidates ?? [])].filter(Boolean).slice(0, 4)
})
const hasLibraryCollage = computed(() => libraryArtworkCandidates.value.length > 1)
const cardClass = computed(() => props.kind === 'library' ? 'library-card' : 'poster-card')
const imageClass = computed(() => props.kind === 'library' || (hasMediaPath(props.item) && props.item.type === 'episode') ? 'aspect-[16/9]' : 'aspect-[2/3]')
const imageCacheKey = computed(() => artworkCacheKey(
  props.item.sourceId,
  `${props.item.id}:${!hasMediaPath(props.item) ? props.item.artworkRevision ?? 'initial' : 'media'}`,
  props.kind === 'library' || (hasMediaPath(props.item) && props.item.type === 'episode') ? 'backdrop' : 'poster',
))
const canPlay = computed(() => isMediaItem.value && !props.disabled && props.item.type !== 'folder' && props.item.type !== 'series' && props.item.type !== 'season')
const isPlayed = computed(() => hasMediaPath(props.item) && (props.item.played === true || locallyPlayed.value))

onMounted(() => {
  window.addEventListener(PLAYED_STATE_CHANGED_EVENT, refreshPlayedState)
  void refreshPlayedState()
})
onBeforeUnmount(() => window.removeEventListener(PLAYED_STATE_CHANGED_EVENT, refreshPlayedState))
watch(() => [props.item.sourceId, props.item.id] as const, () => void refreshPlayedState())

async function refreshPlayedState() {
  if (!hasMediaPath(props.item)) {
    locallyPlayed.value = false
    return
  }
  const sourceType = store.configs.find(config => config.id === props.item.sourceId)?.type
  if (sourceType === 'emby' || sourceType === 'jellyfin') {
    locallyPlayed.value = false
    return
  }
  locallyPlayed.value = (await getPlaybackProgress({ sourceId: props.item.sourceId, mediaIdentity: props.item.id }))?.completed === true
}

function hasMediaPath(item: MediaItem | MediaLibrary): item is MediaItem {
  return 'path' in item
}

function handleSelect() {
  if (!props.disabled)
    emit('select', props.item)
}

function actionTarget() {
  const source = store.configs.find(config => config.id === props.item.sourceId)
  return createMediaActionTarget(props.item, source?.type, source?.displayName ?? source?.name)
}

function handlePointerDown(event: PointerEvent) {
  if (!props.disabled)
    beginMediaActionLongPress(actionTarget(), event)
}

function handlePointerMove(event: PointerEvent) {
  moveMediaActionLongPress(event)
}

function handlePointerEnd(event: PointerEvent) {
  endMediaActionLongPress(event)
}

function handlePointerCancel(event: PointerEvent) {
  cancelMediaActionLongPress(event.pointerId)
}

function handleClick(event: MouseEvent) {
  if (!suppressMediaActionClick(event))
    handleSelect()
}

function handlePlay() {
  if (canPlay.value && hasMediaPath(props.item))
    emit('play', props.item)
}

function handleContextMenu(event: MouseEvent) {
  event.preventDefault()
  if (props.disabled)
    return
  if (props.contextMenuMode === 'custom') {
    emit('contextmenu', props.item, event)
    return
  }
  openMediaActionContextMenu(actionTarget(), event)
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    handleSelect()
    return
  }
  if (props.contextMenuMode === 'custom' || (event.key !== 'ContextMenu' && !(event.shiftKey && event.key === 'F10')))
    return
  event.preventDefault()
  const element = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  const bounds = element?.getBoundingClientRect()
  openMediaActionMenu({
    target: actionTarget(),
    anchor: bounds ? { x: bounds.left + Math.min(bounds.width, 48), y: bounds.top + Math.min(bounds.height, 48) } : undefined,
    presentation: 'popover',
  })
}
</script>

<template>
  <article
    class="media-card group overflow-hidden rounded-[1.4rem] border transition-all duration-300"
    :class="[cardClass, disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:-translate-y-1 hover:border-white/24']"
    data-media-action-target
    :tabindex="disabled ? -1 : 0"
    @pointerdown="handlePointerDown"
    @pointermove="handlePointerMove"
    @pointerup="handlePointerEnd"
    @pointercancel="handlePointerCancel"
    @pointerleave="handlePointerCancel"
    @click="handleClick"
    @contextmenu="handleContextMenu"
    @keydown="handleKeydown"
  >
    <div class="relative overflow-hidden bg-white/5" :class="imageClass">
      <div
        v-if="hasLibraryCollage"
        class="library-artwork-collage"
        :style="{ gridTemplateColumns: `repeat(${libraryArtworkCandidates.length}, minmax(0, 1fr))` }"
      >
        <CachedImage
          v-for="(candidate, index) in libraryArtworkCandidates"
          :key="candidate"
          :cache-key="`${imageCacheKey}:candidate:${index}`"
          :src="candidate"
          :alt="`${title} 封面 ${index + 1}`"
          loading="lazy"
          decoding="async"
          class="h-full min-w-0 object-cover transition-transform duration-500 group-hover:scale-105"
        >
          <template #fallback>
            <div class="h-full w-full bg-white/5" />
          </template>
        </CachedImage>
      </div>
      <CachedImage
        v-else
        :cache-key="imageCacheKey"
        :src="posterUrl"
        :alt="title"
        loading="lazy"
        decoding="async"
        class="h-full w-full object-cover transition-transform duration-500 group-hover:scale-105"
      >
        <template #fallback>
          <div class="flex h-full w-full flex-col items-center justify-center gap-3 p-5 text-center">
            <svg width="40" height="40" viewBox="0 0 24 24" fill="none" class="text-white/28">
              <rect x="3" y="3" width="18" height="18" rx="3" stroke="currentColor" stroke-width="1.5" />
              <path d="M8 14l2.5-2.5 2 2L15 11l2 3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
            <p class="line-clamp-3 text-sm font-semibold text-white/70">
              {{ title }}
            </p>
          </div>
        </template>
      </CachedImage>

      <div class="absolute inset-0 bg-gradient-to-t from-black/86 via-black/10 to-transparent opacity-80" />

      <span v-if="isPlayed" class="media-card-played" aria-label="已播放" title="已播放">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6.5 12.5 3.4 3.4 7.6-8" /></svg>
      </span>

      <button
        v-if="canPlay"
        class="media-card-play absolute left-1/2 top-1/2 flex h-12 w-12 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-white text-black opacity-0 shadow-2xl transition-all duration-200 hover:scale-110 group-hover:opacity-100"
        aria-label="Play media"
        title="Play media"
        @click.stop="handlePlay"
      >
        <svg width="15" height="15" viewBox="0 0 14 14" fill="currentColor">
          <path d="M3 1l9 6-9 6V1z" />
        </svg>
      </button>

      <div class="theme-immersive-dark absolute inset-x-0 bottom-0 p-4">
        <p class="line-clamp-2 text-sm font-semibold text-white drop-shadow">
          {{ title }}
        </p>
        <p v-if="subtitle" class="mt-1 truncate text-xs text-white/54">
          {{ subtitle }}
        </p>
      </div>
    </div>
  </article>
</template>

<style scoped>
.media-card {
  border-color: var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 40%, transparent);
  box-shadow: var(--shadow-sm);
}

.library-card {
  border-radius: 1.8rem;
  background: linear-gradient(135deg, color-mix(in srgb, var(--color-surface) 62%, transparent), color-mix(in srgb, var(--color-surface-hover) 34%, transparent));
}

.library-artwork-collage {
  display: grid;
  height: 100%;
  width: 100%;
}

.library-artwork-collage :deep(.cached-image-host),
.library-artwork-collage :deep(img) {
  height: 100%;
  min-width: 0;
  width: 100%;
  object-fit: cover;
}

.media-card-played { position: absolute; right: .55rem; bottom: .55rem; z-index: 2; display: flex; width: 1.7rem; height: 1.7rem; align-items: center; justify-content: center; border: 1px solid rgba(255,255,255,.22); border-radius: 50%; color: #fff; background: rgba(34,197,94,.88); box-shadow: 0 8px 18px rgba(0,0,0,.3); }
.media-card-played svg { width: 1rem; height: 1rem; fill: none; stroke: currentColor; stroke-width: 2.4; stroke-linecap: round; stroke-linejoin: round; }

@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .media-card,
  .library-card {
    border-radius: 8px;
  }

  .media-card:hover {
    transform: none;
  }

  .media-card :deep(img) {
    transform: none !important;
  }

  .media-card-play {
    top: auto;
    right: 0.55rem;
    bottom: 0.55rem;
    left: auto;
    width: 2.65rem;
    height: 2.65rem;
    transform: none;
    opacity: 1;
  }

  .media-card-play:hover {
    transform: none;
  }
}
</style>
