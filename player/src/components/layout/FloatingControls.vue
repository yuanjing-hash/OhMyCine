<script setup lang="ts">
import { open } from '@tauri-apps/plugin-dialog'
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useTheme } from '@/composables/useTheme'

const VIDEO_EXTENSIONS = [
  'mp4',
  'mkv',
  'avi',
  'mov',
  'webm',
  'm4v',
  'flv',
  'wmv',
  'ts',
  'm2ts',
  'rmvb',
  'mpg',
  'mpeg',
  '3gp',
  'ogv',
  'divx',
  'vob',
  'iso',
]

const router = useRouter()
const route = useRoute()
const { theme, toggle: toggleTheme } = useTheme()
const isHovered = ref(false)
const isOpeningFile = ref(false)
const isPlayerRoute = computed(() => route.name === 'player')

function getFileName(path: string) {
  return path.split(/[\\/]/).pop() || '本地视频'
}

async function openLocalVideo() {
  if (isOpeningFile.value)
    return

  isOpeningFile.value = true
  try {
    const selected = await open({
      multiple: false,
      directory: false,
      filters: [
        {
          name: 'Video files',
          extensions: VIDEO_EXTENSIONS,
        },
      ],
    })

    if (typeof selected !== 'string')
      return

    await router.push({
      path: '/player',
      query: {
        path: selected,
        title: getFileName(selected),
      },
    })
  }
  finally {
    isOpeningFile.value = false
  }
}
</script>

<template>
  <div
    class="fixed bottom-4 right-4 z-50"
    @mouseenter="isHovered = true"
    @mouseleave="isHovered = false"
  >
    <div class="absolute -inset-6" />

    <Transition name="fade-up">
      <div
        v-show="isHovered"
        class="glass-panel relative flex items-center gap-1 rounded-2xl p-1.5"
      >
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

          <div class="gp-divider h-5 w-px" />
        </template>

        <!-- Player -->
        <button
          class="gp-btn flex h-10 w-10 items-center justify-center rounded-xl transition-all duration-200 disabled:cursor-wait disabled:opacity-60"
          :disabled="isOpeningFile"
          title="打开本地视频"
          aria-label="打开本地视频"
          @click="openLocalVideo"
        >
          <svg width="18" height="18" viewBox="0 0 20 20" fill="none">
            <path d="M5 3l12 7-12 7V3z" fill="currentColor" />
          </svg>
        </button>

        <div class="gp-divider h-5 w-px" />

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
.gp-divider {
  background: var(--gp-divider);
}

.fade-up-enter-active,
.fade-up-leave-active {
  transition: opacity 0.3s cubic-bezier(0.16, 1, 0.3, 1), transform 0.3s cubic-bezier(0.16, 1, 0.3, 1);
}

.fade-up-enter-from,
.fade-up-leave-to {
  opacity: 0;
  transform: translateY(8px);
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
</style>
