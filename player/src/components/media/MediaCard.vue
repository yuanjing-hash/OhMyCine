<script setup lang="ts">
import type { MediaItem, MediaLibrary } from '@/services/datasource/types'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { artworkCacheKey } from '@/services/imageCache'
import { beginMediaActionLongPress, cancelMediaActionLongPress, createMediaActionTarget, endMediaActionLongPress, moveMediaActionLongPress, openMediaActionContextMenu, openMediaActionMenu, suppressMediaActionClick } from '@/services/mediaActions'
import { getPlaybackProgress, PLAYED_STATE_CHANGED_EVENT } from '@/services/playbackHistory'
import { useDataSourceStore } from '@/stores/datasource'
import { useDownloadStore } from '@/stores/downloads'
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
const downloads = useDownloadStore()
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
  return [...new Set(props.item.artworkCandidates ?? [])].filter(Boolean).slice(0, 9)
})
const hasLibraryCollage = computed(() => libraryArtworkCandidates.value.length > 0)
const filledStyle3ArtworkCandidates = computed(() => {
  const candidates = libraryArtworkCandidates.value
  if (candidates.length === 0)
    return []
  return Array.from({ length: 9 }, (_, index) => candidates[index % candidates.length])
})
const style3ArtworkColumns = computed(() => {
  const candidates = filledStyle3ArtworkCandidates.value
  const order = [2, 0, 4, 3, 1, 5, 8, 7, 6]
  const arranged = order.filter(index => index < candidates.length).map(index => candidates[index])
  candidates.forEach((candidate, index) => {
    if (!order.includes(index))
      arranged.push(candidate)
  })
  return [arranged.slice(0, 3), arranged.slice(3, 6), arranged.slice(6, 9)].filter(column => column.length > 0)
})
const usesStyle3Artwork = computed(() => props.kind === 'library' && props.item.artworkSource === 'generated')
const cardClass = computed(() => props.kind === 'library' ? 'library-card' : 'poster-card')
const imageClass = computed(() => props.kind === 'library' || (hasMediaPath(props.item) && props.item.type === 'episode') ? 'aspect-[16/9]' : 'aspect-[2/3]')
const imageRevisionKey = computed(() => {
  if (props.kind === 'library')
    return props.item.artworkRevision ?? 'initial'
  return hasMediaPath(props.item) ? 'media' : props.item.artworkRevision ?? 'initial'
})
const imageCacheKey = computed(() => artworkCacheKey(
  props.item.sourceId,
  `${props.item.id}:${imageRevisionKey.value}`,
  props.kind === 'library' || (hasMediaPath(props.item) && props.item.type === 'episode') ? 'backdrop' : 'poster',
))
const canPlay = computed(() => isMediaItem.value && !props.disabled && props.item.type !== 'folder' && props.item.type !== 'series' && props.item.type !== 'season')
const isPlayed = computed(() => hasMediaPath(props.item) && (props.item.played === true || locallyPlayed.value))
const offlineBadge = computed(() => hasMediaPath(props.item) ? downloads.badgeFor(props.item) : null)

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
    :data-media-card-id="item.id"
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
        class="library-artwork-style3"
      >
        <CachedImage
          :cache-key="`${imageCacheKey}:style3-background`"
          :src="libraryArtworkCandidates[0]"
          :alt="`${title} 背景`"
          loading="lazy"
          decoding="async"
          class="library-artwork-style3-background"
        >
          <template #fallback>
            <div class="h-full w-full bg-white/5" />
          </template>
        </CachedImage>
        <div class="library-artwork-style3-gradient" />
        <div class="library-artwork-style3-columns">
          <div
            v-for="(column, columnIndex) in style3ArtworkColumns"
            :key="`column:${columnIndex}`"
            class="library-artwork-style3-column"
          >
            <CachedImage
              v-for="(candidate, rowIndex) in column"
              :key="`${candidate}:${columnIndex}:${rowIndex}`"
              :cache-key="`${imageCacheKey}:candidate:${columnIndex}:${rowIndex}`"
              :src="candidate"
              :alt="`${title} 封面 ${columnIndex * 3 + rowIndex + 1}`"
              loading="lazy"
              decoding="async"
              class="library-artwork-style3-poster"
            >
              <template #fallback>
                <div class="h-full w-full bg-white/5" />
              </template>
            </CachedImage>
          </div>
        </div>
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

      <div
        v-if="!usesStyle3Artwork"
        class="absolute inset-0 bg-gradient-to-t from-black/86 via-black/10 to-transparent opacity-80"
      />

      <span v-if="isPlayed" class="media-card-played" aria-label="已播放" title="已播放">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6.5 12.5 3.4 3.4 7.6-8" /></svg>
      </span>

      <span v-if="offlineBadge" class="media-card-downloaded" :aria-label="offlineBadge.label" :title="offlineBadge.label">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3v10m0 0 4-4m-4 4L8 9M5 19h14" /></svg>
        <b v-if="offlineBadge.state === 'partial'">{{ offlineBadge.label }}</b>
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

      <div
        class="theme-immersive-dark library-artwork-copy absolute inset-x-0 bottom-0 p-4"
        :class="{ 'library-artwork-copy-style3': usesStyle3Artwork }"
      >
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

.library-artwork-style3 {
  position: relative;
  height: 100%;
  width: 100%;
  overflow: hidden;
  background: #241d24;
}

.library-artwork-style3-background,
.library-artwork-style3-background :deep(.cached-image-host),
.library-artwork-style3-background :deep(img) {
  position: absolute;
  inset: -18%;
  height: 100%;
  width: 100%;
  object-fit: cover;
  filter: blur(34px) saturate(1.25);
  opacity: 0.76;
  transform: scale(1.35);
}

.library-artwork-style3-gradient {
  position: absolute;
  inset: 0;
  background: linear-gradient(90deg, rgba(8, 7, 11, .92) 0%, rgba(15, 12, 17, .74) 31%, rgba(19, 15, 18, .18) 65%, rgba(255, 255, 255, .08) 100%);
}

.library-artwork-style3-columns {
  position: absolute;
  inset: 0;
}

.library-artwork-style3-column {
  position: absolute;
  left: 32.81%;
  top: -33.52%;
  width: 21.35%;
  height: 173.52%;
  transform: rotate(-15.8deg);
  transform-origin: 50% 50%;
}

.library-artwork-style3-column:nth-child(2) {
  left: 56.77%;
}

.library-artwork-style3-column:nth-child(3) {
  left: 82.81%;
  top: -47.87%;
}

.library-artwork-style3-poster,
.library-artwork-style3-poster :deep(.cached-image-host),
.library-artwork-style3-poster :deep(img) {
  display: block;
  width: 100%;
  aspect-ratio: 410 / 610;
  object-fit: cover;
  border-radius: 8%;
  box-shadow: .35rem .55rem 1.05rem rgba(0, 0, 0, .62);
}

.library-artwork-style3-poster {
  position: absolute;
  top: 0;
  left: 0;
}

.library-artwork-style3-poster:nth-child(2) {
  top: 33.72%;
}

.library-artwork-style3-poster:nth-child(3) {
  top: 67.45%;
}

.library-artwork-copy-style3 {
  top: 39.57%;
  right: auto;
  bottom: auto;
  z-index: 2;
  width: 44%;
  transform: none;
  padding-left: 3.82%;
}

.library-artwork-copy-style3 p:first-child {
  font-size: clamp(.88rem, 1.35vw, 1.45rem);
  line-height: 1.08;
  letter-spacing: -.035em;
  text-shadow: 0 .18rem .65rem rgba(0, 0, 0, .5);
}

.media-card-played { position: absolute; right: .55rem; bottom: .55rem; z-index: 2; display: flex; width: 1.7rem; height: 1.7rem; align-items: center; justify-content: center; border: 1px solid rgba(255,255,255,.22); border-radius: 50%; color: #fff; background: rgba(34,197,94,.88); box-shadow: 0 8px 18px rgba(0,0,0,.3); }
.media-card-played svg { width: 1rem; height: 1rem; fill: none; stroke: currentColor; stroke-width: 2.4; stroke-linecap: round; stroke-linejoin: round; }
.media-card-downloaded { position:absolute; left:.55rem; top:.55rem; z-index:3; display:flex; min-height:1.75rem; align-items:center; gap:.3rem; border:1px solid rgba(255,255,255,.24); border-radius:.65rem; color:#fff; background:rgba(37,99,235,.9); padding:.32rem .45rem; box-shadow:0 8px 18px rgba(0,0,0,.3); }
.media-card-downloaded svg { width:1rem; height:1rem; fill:none; stroke:currentColor; stroke-width:2; stroke-linecap:round; stroke-linejoin:round; }
.media-card-downloaded b { font-size:.62rem; line-height:1; }

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
