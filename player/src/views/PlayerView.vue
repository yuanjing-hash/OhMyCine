<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import PlayerControls from '@/components/player/PlayerControls.vue'
import VideoPlayer from '@/components/player/VideoPlayer.vue'
import { useMpv } from '@/composables/useMpv'

const route = useRoute()
const mediaTitle = ref('未命名影片')
const mediaPath = ref('')

const {
  isPlaying,
  currentTime,
  duration,
  volume,
  load,
  togglePause,
  seek,
  seekRelative,
  setVolume,
} = useMpv()

const hasMedia = computed(() => mediaPath.value.length > 0)

watch(
  () => route.query.path,
  async (path) => {
    const nextPath = typeof path === 'string' ? path : ''
    mediaPath.value = nextPath
    mediaTitle.value = typeof route.query.title === 'string' ? route.query.title : '未命名影片'

    if (nextPath)
      await load(nextPath)
  },
  { immediate: true },
)

async function handleFileDrop(path: string) {
  mediaPath.value = path
  mediaTitle.value = path.split(/[\\/]/).pop() || '本地视频'
  await load(path)
}
</script>

<template>
  <div class="relative h-screen w-full overflow-hidden bg-black text-white">
    <VideoPlayer
      :is-playing="isPlaying"
      :has-media="hasMedia"
      @file-drop="handleFileDrop"
    />

    <div class="pointer-events-none absolute inset-x-0 top-0 z-10 bg-gradient-to-b from-black/70 to-transparent p-6 pt-20">
      <div class="max-w-4xl">
        <p class="text-xs uppercase tracking-[0.24em] text-white/35">
          Now Playing
        </p>
        <h1 class="mt-2 truncate text-2xl font-bold text-white drop-shadow-lg">
          {{ mediaTitle }}
        </h1>
        <p v-if="mediaPath" class="mt-2 truncate text-xs text-white/35">
          {{ mediaPath }}
        </p>
      </div>
    </div>

    <div class="absolute inset-x-0 bottom-0 z-20 bg-gradient-to-t from-black/90 via-black/55 to-transparent p-6 pt-24">
      <PlayerControls
        :is-playing="isPlaying"
        :current-time="currentTime"
        :duration="duration"
        :volume="volume"
        @toggle-pause="togglePause"
        @seek="seek"
        @seek-relative="seekRelative"
        @set-volume="setVolume"
      />
    </div>
  </div>
</template>
