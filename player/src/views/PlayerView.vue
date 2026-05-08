<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import PlayerControls from '@/components/player/PlayerControls.vue'
import VideoPlayer from '@/components/player/VideoPlayer.vue'
import { useMpv } from '@/composables/useMpv'
import { redactSensitiveText } from '@/services/datasource/errors'

const AUTO_HIDE_DELAY = 2800

const route = useRoute()
const mediaTitle = ref('未命名影片')
const mediaPath = ref('')
const displayMediaPath = computed(() => redactSensitiveText(mediaPath.value))
const chromeVisible = ref(true)
const controlsInteracting = ref(false)
const isWindowFocused = ref(true)
let hideTimer: number | undefined

const {
  isPlaying,
  currentTime,
  duration,
  volume,
  renderStatus,
  renderError,
  initializeRender,
  load,
  togglePause,
  seek,
  seekRelative,
  setVolume,
} = useMpv()

const hasMedia = computed(() => mediaPath.value.length > 0)
const shouldShowChrome = computed(() => chromeVisible.value || !hasMedia.value || !isPlaying.value || controlsInteracting.value)

function clearHideTimer() {
  if (!hideTimer)
    return
  window.clearTimeout(hideTimer)
  hideTimer = undefined
}

function canAutoHideChrome() {
  return hasMedia.value && isPlaying.value && !controlsInteracting.value && isWindowFocused.value
}

function scheduleChromeHide() {
  clearHideTimer()
  if (!canAutoHideChrome())
    return

  hideTimer = window.setTimeout(() => {
    if (canAutoHideChrome())
      chromeVisible.value = false
  }, AUTO_HIDE_DELAY)
}

function revealChrome() {
  chromeVisible.value = true
  scheduleChromeHide()
}

function handleControlsInteraction(next: boolean) {
  controlsInteracting.value = next
  chromeVisible.value = true
  if (next)
    clearHideTimer()
  else
    scheduleChromeHide()
}

function handleWindowBlur() {
  isWindowFocused.value = false
  revealChrome()
}

function handleWindowFocus() {
  isWindowFocused.value = true
  revealChrome()
}

watch(
  () => route.query.path,
  async (path) => {
    const nextPath = typeof path === 'string' ? path : ''
    mediaPath.value = nextPath
    mediaTitle.value = typeof route.query.title === 'string' ? route.query.title : '未命名影片'
    revealChrome()

    if (nextPath)
      await load(nextPath)
  },
  { immediate: true },
)

watch(isPlaying, () => {
  revealChrome()
})

async function handleFileDrop(path: string) {
  mediaPath.value = path
  mediaTitle.value = path.split(/[\\/]/).pop() || '本地视频'
  revealChrome()
  await load(path)
}

onMounted(() => {
  window.addEventListener('blur', handleWindowBlur)
  window.addEventListener('focus', handleWindowFocus)
  void initializeRender()
  scheduleChromeHide()
})

onBeforeUnmount(() => {
  clearHideTimer()
  window.removeEventListener('blur', handleWindowBlur)
  window.removeEventListener('focus', handleWindowFocus)
})
</script>

<template>
  <div
    class="player-view relative h-screen w-full overflow-hidden bg-black text-white"
    :class="{ 'is-chrome-hidden': !shouldShowChrome }"
    @mousemove="revealChrome"
    @mouseleave="scheduleChromeHide"
    @touchstart.passive="revealChrome"
  >
    <VideoPlayer
      :is-playing="isPlaying"
      :has-media="hasMedia"
      :render-status="renderStatus"
      :render-error="renderError"
      @file-drop="handleFileDrop"
    />

    <Transition name="player-chrome-top">
      <div
        v-show="shouldShowChrome"
        class="pointer-events-none absolute inset-x-0 top-0 z-10 bg-gradient-to-b from-black/72 via-black/30 to-transparent px-6 pb-24 pt-20"
      >
        <div class="max-w-4xl">
          <p class="text-xs uppercase tracking-[0.24em] text-white/38">
            Now Playing
          </p>
          <h1 class="mt-2 truncate text-2xl font-bold text-white drop-shadow-lg">
            {{ mediaTitle }}
          </h1>
          <p v-if="mediaPath" class="mt-2 truncate text-xs text-white/35">
            {{ displayMediaPath }}
          </p>
        </div>
      </div>
    </Transition>

    <Transition name="player-chrome-bottom">
      <div
        v-show="shouldShowChrome"
        class="pointer-events-none absolute inset-x-0 bottom-0 z-20 bg-gradient-to-t from-black/88 via-black/44 to-transparent px-6 pb-6 pt-28"
      >
        <PlayerControls
          :is-playing="isPlaying"
          :current-time="currentTime"
          :duration="duration"
          :volume="volume"
          @toggle-pause="togglePause"
          @seek="seek"
          @seek-relative="seekRelative"
          @set-volume="setVolume"
          @interaction-change="handleControlsInteraction"
        />
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.player-view {
  cursor: default;
}

.player-view.is-chrome-hidden {
  cursor: none;
}

.player-chrome-top-enter-active,
.player-chrome-top-leave-active,
.player-chrome-bottom-enter-active,
.player-chrome-bottom-leave-active {
  transition: opacity 260ms var(--ease-out), transform 260ms var(--ease-out);
}

.player-chrome-top-enter-from,
.player-chrome-top-leave-to {
  opacity: 0;
  transform: translateY(-18px);
}

.player-chrome-bottom-enter-from,
.player-chrome-bottom-leave-to {
  opacity: 0;
  transform: translateY(24px);
}
</style>
