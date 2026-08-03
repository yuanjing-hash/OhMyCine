<script setup lang="ts">
import type { MpvPlaybackDiagnostics, MpvRenderDiagnostics, MpvRenderStatus, MpvZOrderStrategy, RenderSurfaceBounds } from '@/composables/useMpv'
import type { ProviderPlaybackSyncDiagnostic } from '@/services/datasource/types'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { redactSensitiveText } from '@/services/datasource/errors'

const props = defineProps<{
  isPlaying: boolean
  hasMedia: boolean
  videoReady: boolean
  backdropUrl: string
  renderStatus: MpvRenderStatus
  renderError: string | null
  renderDiagnostics: MpvRenderDiagnostics | null
  playbackDiagnostics: MpvPlaybackDiagnostics | null
  renderStrategy: MpvZOrderStrategy
  topOcclusion: number
  bottomOcclusion: number
  diagnosticsOpen: boolean
  providerSyncDiagnostics: ProviderPlaybackSyncDiagnostic[]
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
let disposed = false
const windowEventUnlisteners: Array<() => void> = []

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
  if (!props.hasMedia || props.renderStatus !== 'ready' || !props.videoReady)
    return 'bg-black'
  return 'player-surface-root--transparent'
})

const playbackFailure = computed(() => props.playbackDiagnostics?.state === 'error')
const playbackLoading = computed(() => props.hasMedia && (!props.videoReady || props.playbackDiagnostics?.state === 'loading'))

const renderStatusLabel = computed(() => {
  if (playbackFailure.value)
    return '播放错误'
  if (playbackLoading.value)
    return '正在载入'
  switch (props.renderStatus) {
    case 'initializing':
      return '准备中'
    case 'ready':
      return '已就绪'
    case 'unsupported':
      return '暂不可用'
    case 'error':
      return '需要重试'
    case 'idle':
    default:
      return '待播放'
  }
})

const renderTitle = computed(() => {
  if (playbackFailure.value)
    return '视频加载失败'
  if (playbackLoading.value)
    return '正在连接媒体并准备解码'
  switch (props.renderStatus) {
    case 'initializing':
      return '正在准备视频画面'
    case 'ready':
      return '视频画面已就绪'
    case 'unsupported':
      return '当前平台暂未启用内嵌画面'
    case 'error':
      return '视频画面初始化失败'
    case 'idle':
    default:
      return '播放器已准备'
  }
})

const renderDescription = computed(() => {
  if (playbackFailure.value)
    return redactDiagnosticText(props.playbackDiagnostics?.lastError || '媒体文件未能完成加载，请展开诊断信息查看具体原因。')
  if (playbackLoading.value)
    return '播放器已经接管播放请求，正在等待媒体信息和首帧。'
  if (props.renderError)
    return redactDiagnosticText(props.renderError)

  switch (props.renderStatus) {
    case 'initializing':
      return '正在准备内嵌视频画面和播放控制层。'
    case 'ready':
      return '视频画面已就绪，控制条会保持在画面上方；实际显示效果仍需在 Windows 宿主中验证。'
    case 'unsupported':
      return '当前平台的内嵌视频画面将在后续版本启用；播放器会避免打开额外外部窗口。'
    case 'error':
      return '视频画面初始化失败，请稍后重试或查看运行日志。'
    case 'idle':
    default:
      return '打开媒体后会初始化内嵌视频画面。'
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

const providerSyncDiagnosticRows = computed(() => props.providerSyncDiagnostics.map(item => [
  item.timestamp,
  formatProviderSyncDiagnostic(item),
] as const))

const nativePlaybackDiagnosticRows = computed(() => {
  const diagnostics = props.playbackDiagnostics
  if (!diagnostics)
    return []
  return [
    ['playbackState', diagnostics.state],
    ['lastEvent', diagnostics.lastEvent],
    ['fileLoaded', diagnostics.fileLoaded ? 'yes' : 'no'],
    ['voConfigured', diagnostics.voConfigured ? 'yes' : 'no'],
    ['videoFormat', diagnostics.videoFormat ?? 'none'],
    ['audioCodec', diagnostics.audioCodec ?? 'none'],
    ['hardwareDecoder', diagnostics.hardwareDecoder ?? 'none'],
    ['videoOutput', diagnostics.videoOutput],
    ['videoOutputFallback', diagnostics.videoOutputFallbackUsed ? 'yes' : 'no'],
    ['playbackTransport', diagnostics.playbackTransport],
  ] as const
})

const diagnosticText = computed(() => {
  const lines = [
    'Render diagnostics',
    `renderStatus=${props.renderStatus}`,
    `renderStrategy=${props.renderStrategy}`,
  ]

  for (const [key, value] of diagnosticRows.value)
    lines.push(`${key}=${redactDiagnosticText(value)}`)

  if (props.renderError)
    lines.push(`renderError=${redactDiagnosticText(props.renderError)}`)

  if (nativePlaybackDiagnosticRows.value.length > 0) {
    lines.push('', 'Android playback')
    for (const [key, value] of nativePlaybackDiagnosticRows.value)
      lines.push(`${key}=${redactDiagnosticText(value)}`)
    if (props.playbackDiagnostics?.lastError)
      lines.push(`lastError=${redactDiagnosticText(props.playbackDiagnostics.lastError)}`)
    for (const line of props.playbackDiagnostics?.logs ?? [])
      lines.push(`log=${redactDiagnosticText(line)}`)
  }

  if (providerSyncDiagnosticRows.value.length > 0) {
    lines.push('', 'Provider sync')
    for (const [timestamp, value] of providerSyncDiagnosticRows.value)
      lines.push(`${timestamp} ${redactDiagnosticText(value)}`)
  }
  else {
    lines.push('', 'Provider sync', 'none')
  }

  return lines.join('\n')
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

function trackWindowListener(listener: Promise<() => void>) {
  void listener.then((unlisten) => {
    if (disposed)
      unlisten()
    else
      windowEventUnlisteners.push(unlisten)
  }).catch(() => undefined)
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

function redactDiagnosticText(value: unknown): string {
  return redactSensitiveText(value)
    .replace(/\b0x[0-9a-f]{6,}\b/gi, '[native-handle]')
    .replace(/\b((?:owner|mpv)?_?hwnd|hglrc|hdc|handle|pointer|ptr)\s*[:=]\s*-?\d+\b/gi, '$1=[native-handle]')
}

function formatProviderSyncDiagnostic(item: ProviderPlaybackSyncDiagnostic): string {
  return `${item.ok ? 'ok' : 'fail'} · ${item.event} · ${item.stage} · ${item.endpoint} · item=${item.itemIdPresent ? 'yes' : 'no'} mediaSource=${item.mediaSourceIdPresent ? 'yes' : 'no'} playSession=${item.playSessionIdPresent ? 'yes' : 'no'} pos=${Math.round(item.position)}${item.message ? ` · ${redactDiagnosticText(item.message)}` : ''}`
}

async function copyDiagnostics() {
  try {
    await navigator.clipboard.writeText(diagnosticText.value)
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
  if (isTauriRuntime()) {
    const appWindow = getCurrentWindow()
    trackWindowListener(appWindow.onResized(reportBounds))
    trackWindowListener(appWindow.onMoved(reportBounds))
    trackWindowListener(appWindow.onScaleChanged(reportBounds))
  }
  reportBounds()
})

onBeforeUnmount(() => {
  disposed = true
  resizeObserver?.disconnect()
  window.removeEventListener('resize', reportBounds)
  for (const unlisten of windowEventUnlisteners)
    unlisten()
  windowEventUnlisteners.length = 0
  if (pendingFrame)
    window.cancelAnimationFrame(pendingFrame)
})

function isTauriRuntime(): boolean {
  const root = globalThis as {
    readonly __TAURI_INTERNALS__?: unknown
    readonly window?: { readonly __TAURI_INTERNALS__?: unknown }
  }
  return root.__TAURI_INTERNALS__ != null || root.window?.__TAURI_INTERNALS__ != null
}
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
    <Transition name="playback-backdrop">
      <div v-if="hasMedia && !videoReady" class="playback-backdrop absolute inset-0 overflow-hidden">
        <img v-if="backdropUrl" :src="backdropUrl" alt="" class="absolute -inset-6 h-[calc(100%+3rem)] w-[calc(100%+3rem)] scale-105 object-cover" aria-hidden="true">
        <div class="absolute inset-0 bg-black/48" />
      </div>
    </Transition>
    <div
      v-if="!hasMedia || renderStatus === 'error' || renderStatus === 'unsupported' || playbackFailure"
      class="absolute inset-0 bg-[radial-gradient(circle_at_center,rgba(74,158,255,0.14),transparent_34%),linear-gradient(135deg,#050509,#090911_52%,#030305)]"
    />
    <div class="pointer-events-none absolute inset-0 flex items-center justify-center px-8">
      <div v-if="!hasMedia" class="pointer-events-none text-center">
        <p class="text-sm font-semibold tracking-[0.12em] text-white/48">
          等待播放中
        </p>
      </div>
      <div v-else-if="renderStatus !== 'ready' || playbackFailure || playbackLoading" class="playback-status-panel pointer-events-auto max-w-xl p-6 text-center">
        <p class="text-xs uppercase tracking-[0.24em] text-white/35">
          {{ renderStatusLabel }}
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
        <button
          v-if="playbackFailure"
          type="button"
          class="mt-5 min-h-11 border border-white/16 bg-white/8 px-4 text-sm font-semibold text-white/86"
          @click="toggleDiagnosticsClick"
        >
          查看播放诊断
        </button>
      </div>
    </div>
  </div>

  <Teleport to="body">
    <div
      v-if="diagnosticsOpen"
      id="render-diagnostics-panel"
      class="pointer-events-auto fixed bottom-6 left-5 top-20 z-[1100] flex min-h-0 w-[min(44rem,calc(100vw-2.5rem))] flex-col overflow-hidden rounded-2xl border border-white/10 bg-black/72 px-5 py-4 text-[12px] leading-5 text-white/80 shadow-2xl backdrop-blur-xl"
      role="region"
      aria-label="Render diagnostics"
      tabindex="0"
      @wheel.stop
      @pointerdown.stop
      @mousedown.stop
      @touchstart.stop
      @touchmove.stop
      @mousemove.stop
    >
      <div class="flex shrink-0 items-center justify-between gap-3">
        <p class="font-semibold uppercase tracking-[0.22em] text-white/58">
          Render diagnostics
        </p>
        <div class="flex items-center gap-2">
          <button
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

      <div
        class="cinema-scrollbar mt-3 min-h-0 flex-1 overflow-y-auto overscroll-contain pr-2"
        @wheel.stop
        @pointerdown.stop
        @mousedown.stop
        @touchstart.stop
        @touchmove.stop
        @mousemove.stop
      >
        <div class="flex flex-wrap items-center gap-2">
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
              {{ redactDiagnosticText(value) }}
            </dd>
          </template>
        </dl>
        <div v-if="nativePlaybackDiagnosticRows.length" class="mt-4 border-t border-white/10 pt-3">
          <p class="text-[10px] uppercase tracking-[0.2em] text-white/44">
            Android playback
          </p>
          <dl class="mt-2 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-[11px]">
            <template v-for="[key, value] in nativePlaybackDiagnosticRows" :key="key">
              <dt class="font-mono text-white/44">
                {{ key }}
              </dt>
              <dd class="break-all font-mono text-white/76">
                {{ redactDiagnosticText(value) }}
              </dd>
            </template>
          </dl>
          <p v-if="playbackDiagnostics?.lastError" class="mt-3 break-words text-red-100/82">
            {{ redactDiagnosticText(playbackDiagnostics.lastError) }}
          </p>
          <div v-if="playbackDiagnostics?.logs.length" class="mt-3 space-y-1 border-t border-white/8 pt-3 font-mono text-[10px] text-white/54">
            <p v-for="(line, index) in playbackDiagnostics.logs" :key="`${index}:${line}`" class="break-all">
              {{ redactDiagnosticText(line) }}
            </p>
          </div>
        </div>
        <div v-if="providerSyncDiagnosticRows.length" class="mt-4 rounded-2xl border border-white/10 bg-white/5 p-3">
          <p class="text-[10px] uppercase tracking-[0.2em] text-white/44">
            Provider sync
          </p>
          <dl class="mt-2 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-[11px]">
            <template v-for="[key, value] in providerSyncDiagnosticRows" :key="key">
              <dt class="font-mono text-white/44">
                {{ key }}
              </dt>
              <dd class="break-all font-mono text-white/76">
                {{ redactDiagnosticText(value) }}
              </dd>
            </template>
          </dl>
        </div>
        <p class="mt-3 whitespace-pre-wrap break-words text-white/82">
          {{ renderError ? redactDiagnosticText(renderError) : '当前无额外诊断信息，渲染通路保持默认状态。' }}
        </p>
        <p v-if="logFilePath" class="mt-3 break-all text-[11px] text-white/56">
          诊断日志已写入：<span class="font-mono">{{ logFilePath }}</span>
        </p>
        <p class="mt-3 text-[11px] text-white/48">
          快捷键 Ctrl+Shift+D 可以随时唤起本面板。诊断信息不会泄露媒体路径、凭据或原生窗口指针；如需反馈请复制以上摘要。
        </p>
      </div>
    </div>
  </Teleport>
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

.playback-backdrop {
  background: linear-gradient(135deg, #050509, #10131b 52%, #030305);
}

.playback-backdrop img {
  filter: blur(28px) saturate(0.78);
}

.playback-backdrop-enter-active,
.playback-backdrop-leave-active {
  transition: opacity 260ms ease;
}

.playback-backdrop-enter-from,
.playback-backdrop-leave-to {
  opacity: 0;
}

.playback-status-panel {
  border: 1px solid rgba(255, 255, 255, 0.14);
  border-radius: 8px;
  background: rgba(8, 10, 15, 0.82);
  box-shadow: 0 18px 54px rgba(0, 0, 0, 0.48);
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
}
</style>
