<script setup lang="ts">
import type { MpvRenderDiagnostics, MpvRenderStatus, MpvZOrderStrategy, RenderSurfaceBounds } from '@/composables/useMpv'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{
  isPlaying: boolean
  hasMedia: boolean
  renderStatus: MpvRenderStatus
  renderError: string | null
  renderDiagnostics: MpvRenderDiagnostics | null
  renderStrategy: MpvZOrderStrategy
  topOcclusion: number
  bottomOcclusion: number
  diagnosticsOpen: boolean
}>()

const emit = defineEmits<{
  fileDrop: [path: string]
  renderBounds: [bounds: RenderSurfaceBounds]
  toggleDiagnostics: []
  setStrategy: [strategy: MpvZOrderStrategy]
}>()

const surfaceHost = ref<HTMLElement | null>(null)
let resizeObserver: ResizeObserver | undefined
let pendingFrame = 0

// Transparent-overlay model: mpv renders into a full-bleed native underlay window and the
// Tauri/WebView window above it stays transparent where the video should show through. The previous
// top/bottom occlusion model is intentionally neutralized; Vue controls remain clickable because
// they are in the overlay window, not because the video HWND is shrunk away from them.
const surfaceStyle = computed(() => {
  if (!props.hasMedia) {
    return {
      top: '0px',
      bottom: 'auto',
      left: '0px',
      right: 'auto',
      width: '1px',
      height: '1px',
    }
  }

  return {
    top: '0px',
    bottom: '0px',
    left: '0px',
    right: '0px',
    width: 'auto',
    height: 'auto',
  }
})

const rootBackgroundClass = computed(() => {
  if (!props.hasMedia || props.renderStatus !== 'ready')
    return 'bg-black'
  return 'player-surface-root--transparent'
})

const renderTitle = computed(() => {
  switch (props.renderStatus) {
    case 'initializing':
      return '正在初始化内嵌渲染'
    case 'ready':
      return '透明叠层视频后端已就绪'
    case 'unsupported':
      return '当前平台暂未启用内嵌渲染'
    case 'error':
      return '内嵌渲染初始化失败'
    case 'idle':
    default:
      return '内嵌渲染基础已接入'
  }
})

const renderDescription = computed(() => {
  if (props.renderError)
    return props.renderError

  switch (props.renderStatus) {
    case 'initializing':
      return '正在创建 mpv 视频底层窗口，并准备透明 Tauri/WebView 叠层。'
    case 'ready':
      return 'Windows 后端已创建 mpv 视频底层窗口，Tauri/WebView 透明叠层位于其上方，Vue 控制条应保持可见并可点击；实际视频画面与窗口同步表现需要在 Windows 宿主运行验证。'
    case 'unsupported':
      return 'OhMyCine 保留跨平台渲染后端架构；此平台的实际 native surface/render loop 会在后续里程碑接入，当前仍安全禁止外部 mpv 窗口。'
    case 'error':
      return '播放器已保留 no-external-window 安全保护，请稍后重试或查看运行日志。'
    case 'idle':
    default:
      return 'Windows 透明叠层渲染通路已经接入；打开媒体后会初始化 mpv 视频底层窗口。'
  }
})

const diagnosticsBadgeLabel = computed(() => {
  switch (props.renderStatus) {
    case 'ready':
      return '渲染已就绪 · 查看诊断'
    case 'initializing':
      return '渲染初始化中 · 查看诊断'
    case 'error':
      return '渲染错误 · 查看诊断'
    case 'unsupported':
      return '平台未支持 · 查看诊断'
    case 'idle':
    default:
      return '渲染待机 · 查看诊断'
  }
})

const strategyLabel = computed(() => {
  if (props.renderStrategy === 'transparentOverlay')
    return '透明 Tauri/WebView 叠层 + mpv 视频底层窗口'
  return `Legacy ${props.renderStrategy} · 已中和为透明叠层模式`
})

const diagnosticRows = computed(() => {
  const diagnostics = props.renderDiagnostics
  if (!diagnostics)
    return []

  return [
    ['ownerHwndAttached', diagnostics.ownerHwndAttached ? 'yes' : 'no'],
    ['mpvHwndCreated', diagnostics.mpvHwndCreated ? 'yes' : 'no'],
    ['mpvHwndShown', diagnostics.mpvHwndShown ? 'yes' : 'no'],
    ['overlayWindowTransparent', diagnostics.overlayWindowTransparent ? 'yes' : 'no'],
    ['webviewBackgroundTransparentApplied', diagnostics.webviewBackgroundTransparentApplied ? 'yes' : 'no'],
    ['zOrderUnderlayApplied', diagnostics.zOrderUnderlayApplied ? 'yes' : 'no'],
    ['geometryFollowing', diagnostics.geometryFollowing ? 'yes' : 'no'],
    ['taskbarIgnored', diagnostics.taskbarIgnored ? 'yes' : 'no'],
    ['fullscreenState', diagnostics.fullscreenState],
    ['lastSyncResult', diagnostics.lastSyncResult],
    ['mpvWidAccepted', diagnostics.mpvWidAccepted ? 'yes' : 'no'],
    ['mpvInitialized', diagnostics.mpvInitialized ? 'yes' : 'no'],
    ['lastBounds', diagnostics.lastBounds ?? 'none'],
    ['scale', diagnostics.scale.toFixed(2)],
    ['syncs', diagnostics.syncs.toString()],
  ]
})

const logFilePath = computed(() => {
  if (props.renderDiagnostics?.logFile)
    return props.renderDiagnostics.logFile

  const message = props.renderError
  if (!message)
    return null

  const match = message.match(/logFile=(\S+)/)
  return match ? match[1] : null
})

function reportBounds() {
  if (pendingFrame)
    window.cancelAnimationFrame(pendingFrame)

  pendingFrame = window.requestAnimationFrame(() => {
    pendingFrame = 0
    const host = surfaceHost.value
    if (!host)
      return

    const rect = host.getBoundingClientRect()
    emit('renderBounds', {
      x: rect.left,
      y: rect.top,
      width: rect.width,
      height: rect.height,
      scaleFactor: window.devicePixelRatio || 1,
      topOcclusion: 0,
      bottomOcclusion: 0,
    })
  })
}

function handleDrop(event: DragEvent) {
  const file = event.dataTransfer?.files.item(0) as (File & { path?: string }) | null
  if (!file?.path)
    return
  emit('fileDrop', file.path)
}

function toggleDiagnosticsClick() {
  emit('toggleDiagnostics')
}

async function copyDiagnostics() {
  if (!props.renderError)
    return
  try {
    await navigator.clipboard.writeText(props.renderError)
  }
  catch {
    // Clipboard may be unavailable; ignore without exposing UI noise.
  }
}

watch(
  () => [props.hasMedia, props.renderStatus, props.renderStrategy, props.topOcclusion, props.bottomOcclusion] as const,
  async () => {
    await nextTick()
    reportBounds()
  },
)

onMounted(() => {
  if (surfaceHost.value) {
    resizeObserver = new ResizeObserver(reportBounds)
    resizeObserver.observe(surfaceHost.value)
  }
  window.addEventListener('resize', reportBounds)
  reportBounds()
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  window.removeEventListener('resize', reportBounds)
  if (pendingFrame)
    window.cancelAnimationFrame(pendingFrame)
})
</script>

<template>
  <div
    class="player-surface-root relative h-full w-full overflow-hidden"
    :class="rootBackgroundClass"
    @dragover.prevent
    @drop.prevent="handleDrop"
  >
    <!-- Surface bounds reporter: its rect is what Rust uses for the native HWND region. -->
    <div
      ref="surfaceHost"
      class="pointer-events-none absolute overflow-hidden bg-transparent"
      :style="surfaceStyle"
      aria-hidden="true"
    />
    <div
      v-if="renderStatus !== 'ready'"
      class="absolute inset-0 bg-[radial-gradient(circle_at_center,rgba(74,158,255,0.14),transparent_34%),linear-gradient(135deg,#050509,#090911_52%,#030305)]"
    />
    <!-- Always-available diagnostics affordance. The button lives in the transparent WebView
         overlay above the mpv video underlay, so it should remain visible and clickable. -->
    <button
      type="button"
      class="pointer-events-auto absolute left-5 top-5 z-30 rounded-full border border-white/10 bg-black/50 px-3 py-1.5 text-[11px] font-medium uppercase tracking-[0.18em] text-white/72 shadow-lg backdrop-blur-xl transition hover:bg-black/70 hover:text-white"
      :aria-expanded="diagnosticsOpen"
      aria-controls="render-diagnostics-panel"
      @click="toggleDiagnosticsClick"
    >
      {{ diagnosticsBadgeLabel }}
    </button>
    <div
      v-if="diagnosticsOpen"
      id="render-diagnostics-panel"
      class="pointer-events-auto absolute left-5 top-16 z-30 max-w-[min(44rem,calc(100%-2.5rem))] rounded-2xl border border-white/10 bg-black/72 px-5 py-4 text-[12px] leading-5 text-white/80 shadow-2xl backdrop-blur-xl"
      role="region"
      aria-label="Render diagnostics"
    >
      <div class="flex items-center justify-between gap-3">
        <p class="font-semibold uppercase tracking-[0.22em] text-white/58">
          Render diagnostics
        </p>
        <div class="flex items-center gap-2">
          <button
            v-if="renderError"
            type="button"
            class="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-[10px] uppercase tracking-[0.2em] text-white/70 hover:bg-white/10 hover:text-white"
            @click="copyDiagnostics"
          >
            复制
          </button>
          <button
            type="button"
            class="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-[10px] uppercase tracking-[0.2em] text-white/70 hover:bg-white/10 hover:text-white"
            @click="toggleDiagnosticsClick"
          >
            关闭
          </button>
        </div>
      </div>
      <div class="mt-3 flex flex-wrap items-center gap-2">
        <span class="text-[11px] uppercase tracking-[0.2em] text-white/48">
          当前策略: {{ strategyLabel }}
        </span>
      </div>
      <dl v-if="diagnosticRows.length" class="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-[11px]">
        <template v-for="[key, value] in diagnosticRows" :key="key">
          <dt class="font-mono text-white/44">
            {{ key }}
          </dt>
          <dd class="break-all font-mono text-white/76">
            {{ value }}
          </dd>
        </template>
      </dl>
      <p class="mt-3 whitespace-pre-wrap break-words text-white/82">
        {{ renderError || '当前无额外诊断信息，渲染通路保持默认状态。' }}
      </p>
      <p v-if="logFilePath" class="mt-3 break-all text-[11px] text-white/56">
        诊断日志已写入：<span class="font-mono">{{ logFilePath }}</span>
      </p>
      <p class="mt-3 text-[11px] text-white/48">
        快捷键 Ctrl+Shift+D 可以随时唤起本面板。诊断信息不会泄露媒体路径、凭据或原生窗口指针；如需反馈请复制以上摘要。
      </p>
    </div>
    <div class="pointer-events-none absolute inset-0 flex items-center justify-center px-8">
      <div v-if="!hasMedia" class="glass-panel pointer-events-auto max-w-md rounded-3xl p-8 text-center">
        <p class="text-lg font-semibold text-white">
          拖拽文件到此处播放
        </p>
        <p class="mt-3 text-sm leading-6 text-white/50">
          支持 libmpv 可播放的本地视频文件。加载后可使用底部控制条播放、暂停、拖动进度和调节音量。
        </p>
      </div>
      <div v-else-if="renderStatus !== 'ready'" class="glass-panel pointer-events-auto max-w-xl rounded-3xl p-8 text-center">
        <p class="text-xs uppercase tracking-[0.24em] text-white/35">
          {{ renderStatus }}
        </p>
        <p class="mt-3 text-base font-semibold text-white">
          {{ renderTitle }}
        </p>
        <p class="mt-3 text-sm leading-6 text-white/54">
          {{ renderDescription }}
        </p>
        <p v-if="!isPlaying" class="mt-4 text-xs uppercase tracking-[0.22em] text-white/34">
          Paused
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.player-surface-root {
  /* Opaque black by default so desktop cannot leak through the WebView before media loads. */
  background: #000;
}
.player-surface-root--transparent {
  /* Active/ready playback: keep the WebView region fully transparent so the mpv video underlay is
     visible through the transparent Tauri/WebView overlay. Idle/error/unsupported states keep an
     intentional dark placeholder surface. */
  background: transparent;
  background-color: transparent;
}
</style>
