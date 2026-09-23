<script setup lang="ts">
import { open } from '@tauri-apps/plugin-dialog'
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useTheme } from '@/composables/useTheme'
import { createLocalPlaylistContext, listLocalFolderVideos, VIDEO_FILE_EXTENSIONS } from '@/services/localPlaylist'
import { createPlaybackRouteQuery } from '@/services/playbackRoute'
import { useDownloadStore } from '@/stores/downloads'

const router = useRouter()
const route = useRoute()
const { theme, toggle: toggleTheme } = useTheme()
const downloads = useDownloadStore()
const isTouchUi = window.matchMedia('(hover: none) and (pointer: coarse)').matches
const isHovered = ref(isTouchUi)
const isOpeningFile = ref(false)
const openFileError = ref<string | null>(null)
const isPlayerRoute = computed(() => route.name === 'player')

async function openLocalVideo(directory = false) {
  if (isOpeningFile.value)
    return

  isOpeningFile.value = true
  openFileError.value = null
  try {
    const selected = await open({
      multiple: !directory,
      directory,
      title: directory ? '打开视频文件夹' : '打开本地视频',
      filters: directory ? undefined : [{ name: 'Video files', extensions: [...VIDEO_FILE_EXTENSIONS] }],
    })

    if (!selected)
      return

    const files = directory
      ? await listLocalFolderVideos(selected as string)
      : (Array.isArray(selected) ? selected : [selected]).map(path => ({ path }))
    const { contextId, itemId } = await createLocalPlaylistContext(files)

    await router.push({
      name: 'player',
      query: createPlaybackRouteQuery({ sourceId: 'local-file', itemId, contextId }),
    })
  }
  catch (error) {
    openFileError.value = error instanceof Error ? error.message : '打开本地视频失败。'
  }
  finally {
    isOpeningFile.value = false
  }
}
</script>

<template>
  <div
    class="floating-controls fixed bottom-24 right-0 top-24 z-50 w-2"
    @mouseenter="isHovered = true"
    @mouseleave="isHovered = false"
  >
    <div class="absolute -left-4 inset-y-0 w-6" />

    <Transition name="edge-reveal">
      <div
        v-show="isHovered"
        class="floating-controls-panel glass-panel fixed right-6 top-1/2 flex -translate-y-1/2 flex-col items-center gap-1 rounded-2xl p-1.5"
        @mouseenter="isHovered = true"
        @mouseleave="isHovered = false"
      >
        <button
          class="gp-btn relative flex h-10 w-10 items-center justify-center rounded-xl transition-all duration-200"
          title="下载管理"
          aria-label="下载管理"
          @click="router.push('/downloads')"
        >
          <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
            <path d="M10 2v10m0 0 4-4m-4 4L6 8M3 16h14" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
          <b v-if="downloads.activeCount" class="download-count">{{ downloads.activeCount > 99 ? '99+' : downloads.activeCount }}</b>
        </button>

        <div class="gp-divider my-1 h-px w-6" />

        <!-- Navigation buttons — only on player page (top bar/sidebar hidden there) -->
        <template v-if="isPlayerRoute">
          <button
            class="gp-btn flex h-10 w-10 items-center justify-center rounded-xl transition-all duration-200"
            title="返回主页"
            aria-label="返回主页"
            @click="router.push('/')"
          >
            <svg width="18" height="18" viewBox="0 0 20 20" fill="none">
              <path d="M3 10l7-7 7 7M5 8v8h3v-4h4v4h3V8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>

          <button
            class="gp-btn flex h-10 w-10 items-center justify-center rounded-xl transition-all duration-200"
            title="设置"
            aria-label="设置"
            @click="router.push('/settings')"
          >
            <svg width="18" height="18" viewBox="0 0 20 20" fill="none">
              <circle cx="10" cy="10" r="3" stroke="currentColor" stroke-width="1.5" />
              <path d="M10 1v3M10 16v3M1 10h3M16 10h3M3.5 3.5l2 2M14.5 14.5l2 2M3.5 16.5l2-2M14.5 5.5l2-2" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
            </svg>
          </button>

          <div class="gp-divider my-1 h-px w-6" />
        </template>

        <!-- Player -->
        <button
          class="gp-btn flex h-10 w-10 items-center justify-center rounded-xl transition-all duration-200 disabled:cursor-wait disabled:opacity-60"
          :disabled="isOpeningFile"
          title="打开本地视频"
          aria-label="打开本地视频"
          @click="openLocalVideo(false)"
        >
          <svg width="18" height="18" viewBox="0 0 20 20" fill="none">
            <path d="M5 3l12 7-12 7V3z" fill="currentColor" />
          </svg>
        </button>

        <div class="gp-divider my-1 h-px w-6" />

        <button
          class="gp-btn flex h-10 w-10 items-center justify-center rounded-xl transition-all duration-200 disabled:cursor-wait disabled:opacity-60"
          :disabled="isOpeningFile"
          title="打开视频文件夹"
          aria-label="打开视频文件夹"
          @click="openLocalVideo(true)"
        >
          <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
            <path d="M2.5 5.5A2 2 0 0 1 4.5 3.5h4l2 2h5A2 2 0 0 1 17.5 7.5v8a2 2 0 0 1-2 2h-11a2 2 0 0 1-2-2v-10Z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
            <path d="m8 9 5 3-5 3V9Z" fill="currentColor" />
          </svg>
        </button>

        <p v-if="openFileError" class="w-44 max-w-[40vw] rounded-lg bg-red-950/90 px-2 py-1 text-xs text-red-100" role="alert">
          {{ openFileError }}
        </p>

        <!-- Theme toggle -->
        <button
          class="gp-btn theme-toggle relative flex h-10 w-10 items-center justify-center overflow-hidden rounded-xl transition-all duration-200"
          :class="{ 'is-light': theme === 'light' }"
          title="Toggle theme"
          @click="toggleTheme"
        >
          <svg
            class="icon-sun absolute"
            width="18"
            height="18"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <circle cx="12" cy="12" r="5" />
            <path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
          </svg>
          <svg
            class="icon-moon absolute"
            width="18"
            height="18"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M21 12.79A9 9 0 1111.21 3 7 7 0 0021 12.79z" />
          </svg>
        </button>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.gp-btn {
  color: var(--gp-text);
}
.gp-btn:hover {
  color: var(--gp-text-full);
  background: var(--gp-hover);
}
.gp-btn.is-active {
  color: var(--gp-text-full);
  background: var(--gp-hover);
}
.gp-divider {
  background: var(--gp-divider);
}
.download-count { position:absolute; right:-.3rem; top:-.3rem; min-width:1.05rem; border-radius:999px; background:var(--color-primary); padding:.08rem .25rem; color:white; font-size:.58rem; line-height:1rem; }

.edge-reveal-enter-active,
.edge-reveal-leave-active {
  transition: opacity 0.3s cubic-bezier(0.16, 1, 0.3, 1), transform 0.3s cubic-bezier(0.16, 1, 0.3, 1);
}

.edge-reveal-enter-from,
.edge-reveal-leave-to {
  opacity: 0;
  transform: translateX(12px) translateY(-50%) scale(0.96);
}

.edge-reveal-enter-to,
.edge-reveal-leave-from {
  opacity: 1;
  transform: translateX(0) translateY(-50%) scale(1);
}

.icon-sun,
.icon-moon {
  transition: transform 0.5s cubic-bezier(0.16, 1, 0.3, 1), opacity 0.3s ease;
}

.icon-sun {
  opacity: 0;
  transform: rotate(90deg) scale(0);
}

.icon-moon {
  opacity: 1;
  transform: rotate(0) scale(1);
}

.is-light .icon-sun {
  opacity: 1;
  transform: rotate(0) scale(1);
}

.is-light .icon-moon {
  opacity: 0;
  transform: rotate(-90deg) scale(0);
}

@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .floating-controls {
    display: none;
  }
}
</style>
