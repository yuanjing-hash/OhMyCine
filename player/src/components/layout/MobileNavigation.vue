<script setup lang="ts">
import { open } from '@tauri-apps/plugin-dialog'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useTheme } from '@/composables/useTheme'
import { pickAndroidLocalVideo } from '@/services/androidLocalMedia'
import { savePlaybackMediaContext } from '@/services/playbackContext'
import { isNativeAndroidRuntime } from '@/services/runtimePlatform'
import { useDataSourceStore } from '@/stores/datasource'

type MobileSheet = 'libraries' | 'quick'

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

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()
const { theme, toggle: toggleTheme } = useTheme()
const activeSheet = ref<MobileSheet | null>(null)
const isOpeningFile = ref(false)
const openFileError = ref<string | null>(null)
const isNativeAndroid = isNativeAndroidRuntime()

const isHomeActive = computed(() => route.name === 'home')
const isLibraryActive = computed(() => route.name === 'source' || route.name === 'media-detail' || activeSheet.value === 'libraries')
const isSettingsActive = computed(() => route.name === 'settings')
const enabledSources = computed(() => store.orderedConfigs.filter(source => source.enabled !== false))

const sourceIcons: Record<string, string> = {
  emby: 'E',
  jellyfin: 'J',
  alist: 'A',
  clouddrive2: 'C',
  webdav: 'W',
  local: 'L',
  server: 'S',
  115: '1',
  123: '2',
  quark: 'Q',
}

function sourceIcon(type: string): string {
  return sourceIcons[type] ?? '?'
}

function toggleSheet(sheet: MobileSheet) {
  activeSheet.value = activeSheet.value === sheet ? null : sheet
}

function closeSheet() {
  activeSheet.value = null
}

async function navigateHome() {
  closeSheet()
  await router.push({ name: 'home' })
}

async function navigateSettings() {
  closeSheet()
  await router.push({ name: 'settings' })
}

async function navigateSource(sourceId: string) {
  closeSheet()
  await router.push({ name: 'source', params: { sourceId } })
}

async function navigateDataSources(action?: 'add') {
  closeSheet()
  await router.push({
    name: 'settings',
    query: {
      section: 'datasources',
      ...(action ? { action } : {}),
    },
  })
}

function fileName(path: string): string {
  return path.split(/[\\/]/).pop() || '本地视频'
}

async function openLocalVideo() {
  if (isOpeningFile.value)
    return

  isOpeningFile.value = true
  openFileError.value = null
  try {
    if (isNativeAndroid) {
      const selected = await pickAndroidLocalVideo()
      if (selected.cancelled)
        return
      if (!selected.uri)
        throw new Error('Android 文件选择未返回可播放媒体。')
      if (selected.name && !isSupportedVideoName(selected.name))
        throw new Error('请选择受支持的视频文件。')

      const title = selected.name?.trim() || '本地视频'
      const itemId = `android-local-${Date.now()}`
      const contextId = savePlaybackMediaContext({
        sourceId: 'local-file',
        itemId,
        title,
        locator: {
          kind: 'localPath',
          path: selected.uri,
        },
      })
      closeSheet()
      await router.push({
        name: 'player',
        query: {
          contextId,
          sourceId: 'local-file',
          itemId,
          title,
        },
      })
      return
    }

    const selected = await open({
      multiple: false,
      directory: false,
      title: '打开本地视频',
      filters: [{ name: '视频文件', extensions: VIDEO_EXTENSIONS }],
    })
    if (typeof selected !== 'string')
      return

    closeSheet()
    await router.push({
      name: 'player',
      query: {
        path: selected,
        title: fileName(selected),
      },
    })
  }
  catch (error) {
    openFileError.value = error instanceof Error ? error.message : '选择本地视频失败。'
  }
  finally {
    isOpeningFile.value = false
  }
}

function isSupportedVideoName(name: string): boolean {
  const extension = name.trim().split('.').at(-1)?.toLowerCase()
  return Boolean(extension && VIDEO_EXTENSIONS.includes(extension))
}

function handleThemeToggle() {
  toggleTheme()
  closeSheet()
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === 'Escape' && activeSheet.value) {
    event.preventDefault()
    closeSheet()
  }
}

watch(() => route.fullPath, closeSheet)

onMounted(() => window.addEventListener('keydown', handleEscape))
onBeforeUnmount(() => window.removeEventListener('keydown', handleEscape))
</script>

<template>
  <Teleport to="body">
    <Transition name="mobile-sheet-fade">
      <div v-if="activeSheet" class="mobile-sheet-layer" @pointerdown.self="closeSheet">
        <section class="mobile-sheet" role="dialog" aria-modal="true" :aria-label="activeSheet === 'libraries' ? '选择媒体库' : '快捷操作'">
          <div class="mobile-sheet-handle" aria-hidden="true" />

          <template v-if="activeSheet === 'libraries'">
            <header class="mobile-sheet-header">
              <div>
                <p>LIBRARIES</p>
                <h2>选择媒体库</h2>
              </div>
              <button type="button" class="mobile-sheet-close" aria-label="关闭媒体库菜单" @click="closeSheet">
                <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
              </button>
            </header>

            <div v-if="enabledSources.length" class="mobile-library-list">
              <button v-for="source in enabledSources" :key="source.id" type="button" class="mobile-library-row" :class="{ 'is-current': route.params.sourceId === source.id }" @click="navigateSource(source.id)">
                <span class="mobile-library-icon">
                  <img v-if="source.iconUrl" :src="source.iconUrl" :alt="source.displayName ?? source.name">
                  <span v-else>{{ sourceIcon(source.type) }}</span>
                </span>
                <span class="mobile-library-copy">
                  <strong>{{ source.displayName ?? source.name }}</strong>
                  <small>{{ source.type }}</small>
                </span>
                <svg class="mobile-row-chevron" viewBox="0 0 24 24" aria-hidden="true"><path d="m9 5 7 7-7 7" /></svg>
              </button>
            </div>
            <div v-else class="mobile-sheet-empty">
              还没有可用媒体库
            </div>

            <button type="button" class="mobile-sheet-primary" @click="navigateDataSources('add')">
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
              添加数据源
            </button>
          </template>

          <template v-else>
            <header class="mobile-sheet-header">
              <div>
                <p>QUICK ACTIONS</p>
                <h2>快捷操作</h2>
              </div>
              <button type="button" class="mobile-sheet-close" aria-label="关闭快捷操作" @click="closeSheet">
                <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
              </button>
            </header>

            <div class="mobile-quick-grid">
              <button type="button" class="mobile-quick-action" :disabled="isOpeningFile" @click="openLocalVideo">
                <span class="mobile-quick-icon">
                  <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 4h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Zm5 4.5 6 3.5-6 3.5v-7Z" /></svg>
                </span>
                <strong>{{ isOpeningFile ? '正在打开' : '本地视频' }}</strong>
                <small>选择设备上的媒体文件</small>
              </button>

              <button type="button" class="mobile-quick-action" @click="navigateDataSources('add')">
                <span class="mobile-quick-icon">
                  <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
                </span>
                <strong>添加媒体库</strong>
                <small>连接新的媒体来源</small>
              </button>

              <button type="button" class="mobile-quick-action" @click="navigateDataSources()">
                <span class="mobile-quick-icon">
                  <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M4 12h16M4 17h10" /></svg>
                </span>
                <strong>管理数据源</strong>
                <small>编辑、扫描与排序</small>
              </button>

              <button type="button" class="mobile-quick-action" @click="handleThemeToggle">
                <span class="mobile-quick-icon">
                  <svg v-if="theme === 'dark'" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3v2m0 14v2M3 12h2m14 0h2M5.64 5.64l1.42 1.42m9.88 9.88 1.42 1.42m0-12.72-1.42 1.42m-9.88 9.88-1.42 1.42M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z" /></svg>
                  <svg v-else viewBox="0 0 24 24" aria-hidden="true"><path d="M20.2 15.5A8.5 8.5 0 0 1 8.5 3.8 8.5 8.5 0 1 0 20.2 15.5Z" /></svg>
                </span>
                <strong>{{ theme === 'dark' ? '浅色模式' : '深色模式' }}</strong>
                <small>切换界面显示主题</small>
              </button>
            </div>
            <p v-if="openFileError" class="mobile-sheet-error">
              {{ openFileError }}
            </p>
          </template>
        </section>
      </div>
    </Transition>
  </Teleport>

  <nav class="mobile-bottom-nav" aria-label="手机主导航">
    <button type="button" class="mobile-nav-item" :class="{ 'is-active': isHomeActive }" aria-label="首页" @click="navigateHome">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m4 10 8-7 8 7v10h-5v-6H9v6H4V10Z" /></svg>
      <span>首页</span>
    </button>

    <button type="button" class="mobile-nav-item" :class="{ 'is-active': isLibraryActive }" aria-label="媒体库" :aria-expanded="activeSheet === 'libraries'" @click="toggleSheet('libraries')">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5.5h16v13H4v-13Zm4-2h8M8 9h8M8 13h5" /></svg>
      <span>媒体库</span>
    </button>

    <button type="button" class="mobile-nav-item" :class="{ 'is-active': activeSheet === 'quick' }" aria-label="快捷操作" :aria-expanded="activeSheet === 'quick'" @click="toggleSheet('quick')">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
      <span>快捷</span>
    </button>

    <button type="button" class="mobile-nav-item" :class="{ 'is-active': isSettingsActive }" aria-label="设置" @click="navigateSettings">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 15.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7Zm7-3.5 2-1.2-2-3.5-2.3.7a8 8 0 0 0-1.2-.7L15 5h-4l-.5 2.3a8 8 0 0 0-1.2.7L7 7.3l-2 3.5L7 12a8 8 0 0 0 0 1.4l-2 1.2 2 3.5 2.3-.7a8 8 0 0 0 1.2.7L11 20h4l.5-1.9a8 8 0 0 0 1.2-.7l2.3.7 2-3.5-2-1.2a8 8 0 0 0 0-1.4Z" /></svg>
      <span>设置</span>
    </button>
  </nav>
</template>

<style scoped>
.mobile-bottom-nav,
.mobile-sheet-layer {
  display: none;
}

.mobile-sheet-error {
  margin: 12px 2px 0;
  color: var(--color-error, #ff8f8f);
  font-size: 13px;
  line-height: 1.5;
}

@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .mobile-bottom-nav {
    position: fixed;
    z-index: 1050;
    right: 0.75rem;
    bottom: max(0.5rem, env(safe-area-inset-bottom));
    left: 0.75rem;
    display: grid;
    min-height: 4.5rem;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    align-items: end;
    border: 1px solid rgba(255, 255, 255, 0.14);
    border-radius: 8px;
    padding: 0.42rem 0.35rem;
    background: rgba(9, 11, 17, 0.9);
    box-shadow: 0 20px 48px rgba(0, 0, 0, 0.44), inset 0 1px rgba(255, 255, 255, 0.1);
    backdrop-filter: blur(28px) saturate(1.6);
    -webkit-backdrop-filter: blur(28px) saturate(1.6);
  }

  .mobile-nav-item {
    position: relative;
    display: flex;
    min-width: 0;
    min-height: 3.5rem;
    align-items: center;
    justify-content: center;
    gap: 0.2rem;
    color: rgba(255, 255, 255, 0.46);
  }

  .mobile-nav-item {
    flex-direction: column;
    border-radius: 6px;
  }

  .mobile-nav-item::before {
    position: absolute;
    top: 0;
    width: 1.25rem;
    height: 2px;
    border-radius: 999px;
    background: transparent;
    content: '';
  }

  .mobile-nav-item.is-active {
    color: rgba(255, 255, 255, 0.96);
    background: rgba(255, 255, 255, 0.075);
  }

  .mobile-nav-item.is-active::before {
    background: var(--color-primary);
  }

  .mobile-nav-item svg {
    width: 1.25rem;
    height: 1.25rem;
    fill: none;
    stroke: currentColor;
    stroke-width: 1.65;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  .mobile-nav-item span {
    font-size: 0.62rem;
    font-weight: 700;
  }

  .mobile-sheet-layer {
    position: fixed;
    z-index: 1200;
    inset: 0;
    display: flex;
    align-items: flex-end;
    justify-content: center;
    padding: 0.75rem;
    padding-bottom: max(0.75rem, env(safe-area-inset-bottom));
    background: rgba(0, 0, 0, 0.58);
    backdrop-filter: blur(7px);
    -webkit-backdrop-filter: blur(7px);
  }

  .mobile-sheet {
    width: min(100%, 32rem);
    max-height: min(80svh, 42rem);
    overflow-y: auto;
    border: 1px solid rgba(255, 255, 255, 0.14);
    border-radius: 8px;
    padding: 0.55rem 0.85rem 1rem;
    color: white;
    background: rgba(14, 17, 24, 0.97);
    box-shadow: 0 28px 80px rgba(0, 0, 0, 0.58);
  }

  .mobile-sheet-handle {
    width: 2.5rem;
    height: 0.22rem;
    margin: 0 auto 0.55rem;
    border-radius: 999px;
    background: rgba(255, 255, 255, 0.2);
  }

  .mobile-sheet-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.35rem 0.15rem 0.85rem;
  }

  .mobile-sheet-header p {
    color: rgba(255, 255, 255, 0.36);
    font-size: 0.6rem;
    font-weight: 800;
  }

  .mobile-sheet-header h2 {
    margin-top: 0.18rem;
    font-size: 1.2rem;
    font-weight: 800;
  }

  .mobile-sheet-close {
    display: flex;
    width: 2.5rem;
    height: 2.5rem;
    align-items: center;
    justify-content: center;
    border-radius: 50%;
    color: rgba(255, 255, 255, 0.72);
    background: rgba(255, 255, 255, 0.08);
  }

  .mobile-sheet-close svg,
  .mobile-row-chevron,
  .mobile-sheet-primary svg,
  .mobile-quick-icon svg {
    fill: none;
    stroke: currentColor;
    stroke-width: 1.8;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  .mobile-sheet-close svg {
    width: 1.1rem;
    height: 1.1rem;
  }

  .mobile-library-list {
    display: grid;
    gap: 0.45rem;
  }

  .mobile-library-row {
    display: grid;
    width: 100%;
    min-height: 4rem;
    grid-template-columns: 2.75rem minmax(0, 1fr) 1.25rem;
    align-items: center;
    gap: 0.75rem;
    border: 1px solid rgba(255, 255, 255, 0.08);
    border-radius: 8px;
    padding: 0.5rem 0.65rem;
    color: rgba(255, 255, 255, 0.78);
    background: rgba(255, 255, 255, 0.045);
    text-align: left;
  }

  .mobile-library-row.is-current {
    border-color: color-mix(in srgb, var(--color-primary) 52%, transparent);
    background: color-mix(in srgb, var(--color-primary) 14%, rgba(255, 255, 255, 0.045));
  }

  .mobile-library-icon {
    display: flex;
    width: 2.75rem;
    height: 2.75rem;
    align-items: center;
    justify-content: center;
    overflow: hidden;
    border-radius: 8px;
    color: white;
    background: rgba(255, 255, 255, 0.1);
    font-size: 0.9rem;
    font-weight: 800;
  }

  .mobile-library-icon img {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }

  .mobile-library-copy {
    min-width: 0;
  }

  .mobile-library-copy strong,
  .mobile-library-copy small {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .mobile-library-copy strong {
    font-size: 0.88rem;
  }

  .mobile-library-copy small {
    margin-top: 0.18rem;
    color: rgba(255, 255, 255, 0.38);
    font-size: 0.66rem;
  }

  .mobile-row-chevron {
    width: 1rem;
    height: 1rem;
    color: rgba(255, 255, 255, 0.32);
  }

  .mobile-sheet-empty {
    padding: 2rem 1rem;
    color: rgba(255, 255, 255, 0.42);
    text-align: center;
  }

  .mobile-sheet-primary {
    display: flex;
    width: 100%;
    min-height: 3.2rem;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
    margin-top: 0.7rem;
    border-radius: 8px;
    color: #0a0c11;
    background: rgba(255, 255, 255, 0.94);
    font-size: 0.85rem;
    font-weight: 800;
  }

  .mobile-sheet-primary svg {
    width: 1.05rem;
    height: 1.05rem;
  }

  .mobile-quick-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.55rem;
  }

  .mobile-quick-action {
    display: flex;
    min-height: 8.2rem;
    flex-direction: column;
    align-items: flex-start;
    border: 1px solid rgba(255, 255, 255, 0.08);
    border-radius: 8px;
    padding: 0.85rem;
    color: rgba(255, 255, 255, 0.82);
    background: rgba(255, 255, 255, 0.05);
    text-align: left;
  }

  .mobile-quick-action:disabled {
    opacity: 0.5;
  }

  .mobile-quick-icon {
    display: flex;
    width: 2.4rem;
    height: 2.4rem;
    align-items: center;
    justify-content: center;
    border-radius: 8px;
    color: var(--color-primary);
    background: color-mix(in srgb, var(--color-primary) 15%, transparent);
  }

  .mobile-quick-icon svg {
    width: 1.25rem;
    height: 1.25rem;
  }

  .mobile-quick-action strong {
    margin-top: 0.75rem;
    font-size: 0.84rem;
  }

  .mobile-quick-action small {
    margin-top: 0.25rem;
    color: rgba(255, 255, 255, 0.38);
    font-size: 0.66rem;
    line-height: 1.4;
  }

  .mobile-sheet-fade-enter-active,
  .mobile-sheet-fade-leave-active {
    transition: opacity 180ms ease;
  }

  .mobile-sheet-fade-enter-active .mobile-sheet,
  .mobile-sheet-fade-leave-active .mobile-sheet {
    transition: transform 220ms cubic-bezier(0.16, 1, 0.3, 1);
  }

  .mobile-sheet-fade-enter-from,
  .mobile-sheet-fade-leave-to {
    opacity: 0;
  }

  .mobile-sheet-fade-enter-from .mobile-sheet,
  .mobile-sheet-fade-leave-to .mobile-sheet {
    transform: translateY(1.5rem);
  }
}
</style>
