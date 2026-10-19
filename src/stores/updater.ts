import type { UpdateCheckResult, UpdateDownloadState, UpdaterSettings, UpdateStatus } from '@/services/updater'
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { flushAppSettings } from '@/services/appSettings'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { isTauriRuntime } from '@/services/runtimePlatform'
import {
  checkPlayerUpdate,
  currentPlayerVersion,
  downloadPlayerUpdate,
  installPlayerUpdate,
  listenPlayerUpdateState,
  loadUpdaterSettings,
  readPlayerUpdateState,
  saveUpdaterSettings,
} from '@/services/updater'

const STARTUP_CHECK_DELAY_MS = 2500

export const useUpdaterStore = defineStore('updater', () => {
  const settings = ref<UpdaterSettings>(loadUpdaterSettings())
  const status = ref<UpdateStatus>('idle')
  const availableUpdate = ref<UpdateCheckResult | null>(null)
  const currentVersion = ref('')
  const error = ref<string | null>(null)
  const promptOpen = ref(false)
  const downloadedBytes = ref(0)
  const contentLength = ref<number | null>(null)
  const lastCheckedAt = ref<number | null>(null)
  const startingDownload = ref(false)
  const startingInstall = ref(false)
  let activeCheck: Promise<UpdateCheckResult> | null = null
  let activeDownload: Promise<void> | null = null
  let activeInstall: Promise<void> | null = null
  let activeRefresh: Promise<void> | null = null
  let initialization: Promise<void> | null = null
  let unlistenNativeState: (() => void) | undefined
  let startupTimer: number | undefined
  let lifecycle = 0
  let operation = 0
  let eventSequence = 0

  const progressPercent = computed(() => {
    if (!contentLength.value || contentLength.value <= 0)
      return null
    return Math.min(100, Math.max(0, (downloadedBytes.value / contentLength.value) * 100))
  })
  const isBusy = computed(() => startingDownload.value || startingInstall.value
    || status.value === 'downloading' || status.value === 'verifying' || status.value === 'installing')
  const canCheckForUpdates = computed(() => !isBusy.value && status.value !== 'checking' && status.value !== 'ready')
  const canDownload = computed(() => availableUpdate.value != null && !isBusy.value
    && (status.value === 'available' || status.value === 'paused' || status.value === 'error'))
  const canInstall = computed(() => availableUpdate.value != null && status.value === 'ready' && !isBusy.value)
  const canDismiss = computed(() => !startingInstall.value && status.value !== 'installing')
  const progressText = computed(() => contentLength.value
    ? `${formatBytes(downloadedBytes.value)} / ${formatBytes(contentLength.value)}`
    : downloadedBytes.value > 0 ? formatBytes(downloadedBytes.value) : '')
  const statusLabel = computed(() => {
    if (startingInstall.value || status.value === 'installing')
      return '正在启动安装…'
    if (startingDownload.value)
      return '正在继续下载…'
    switch (status.value) {
      case 'downloading': return '正在下载更新'
      case 'verifying': return '正在校验更新包…'
      case 'ready': return '更新已下载，可以安装'
      case 'paused': return '下载已中断，可以继续'
      case 'error': return '更新未完成'
      default: return '发现新版本'
    }
  })
  const primaryActionLabel = computed(() => {
    if (startingInstall.value || status.value === 'installing')
      return '正在安装…'
    if (startingDownload.value || status.value === 'downloading')
      return '正在下载…'
    if (status.value === 'verifying')
      return '正在校验…'
    if (status.value === 'ready')
      return '立即安装'
    return status.value === 'paused' || status.value === 'error' ? '继续下载' : '下载更新'
  })

  function applyNativeState(state: UpdateDownloadState) {
    availableUpdate.value = state.update
    status.value = state.status
    downloadedBytes.value = state.downloadedBytes
    contentLength.value = state.contentLength
    error.value = state.error
    if (state.update?.currentVersion)
      currentVersion.value = state.update.currentVersion
    if (!state.update)
      promptOpen.value = false
  }

  function refreshState(): Promise<void> {
    if (!initialization || !isTauriRuntime())
      return Promise.resolve()
    if (activeRefresh)
      return activeRefresh
    const owner = lifecycle
    const action = operation
    const sequence = eventSequence
    const refresh = (async () => {
      const state = await readPlayerUpdateState()
      // Events are emitted in native state order. An IPC reply must not undo
      // a later event, a newly started action, or a disposed app lifecycle.
      if (owner === lifecycle && action === operation && sequence === eventSequence)
        applyNativeState(state)
    })()
    activeRefresh = refresh
    void refresh.finally(() => {
      if (activeRefresh === refresh)
        activeRefresh = null
    }).catch(() => undefined)
    return refresh
  }

  function initialize(): Promise<void> {
    if (initialization)
      return initialization
    const owner = lifecycle
    initialization = (async () => {
      settings.value = loadUpdaterSettings()
      const version = await currentPlayerVersion()
      if (owner !== lifecycle)
        return
      currentVersion.value = version
      if (!isTauriRuntime())
        return
      // Listen first, then reconcile events missed while the WebView was away.
      try {
        const unlisten = await listenPlayerUpdateState((state) => {
          if (owner !== lifecycle)
            return
          eventSequence += 1
          applyNativeState(state)
        })
        if (owner !== lifecycle) {
          unlisten()
          return
        }
        unlistenNativeState = unlisten
      }
      catch {
        // The state query still lets Settings reattach if event setup failed.
      }
      await refreshState().catch(() => undefined)
      if (owner === lifecycle && status.value === 'paused' && availableUpdate.value)
        await startDownload().catch(() => undefined)
    })()
    return initialization
  }

  async function persistSettings(next: UpdaterSettings) {
    if (next.channel !== settings.value.channel && !canCheckForUpdates.value)
      throw new Error('当前更新完成后再切换更新渠道。')
    settings.value = { ...next }
    await saveUpdaterSettings(settings.value)
    await flushAppSettings()
  }

  function scheduleStartupCheck() {
    if (!initialization || !settings.value.autoCheck || startupTimer != null || availableUpdate.value || isBusy.value)
      return
    startupTimer = window.setTimeout(() => {
      startupTimer = undefined
      if (settings.value.autoCheck && !availableUpdate.value && !isBusy.value)
        void checkForUpdates(true).catch(() => undefined)
    }, STARTUP_CHECK_DELAY_MS)
  }

  function cancelStartupCheck() {
    if (startupTimer == null)
      return
    window.clearTimeout(startupTimer)
    startupTimer = undefined
  }

  function checkForUpdates(silent = false): Promise<UpdateCheckResult> {
    if (activeCheck)
      return activeCheck
    const owner = lifecycle
    const check = (async () => {
      await initialize()
      if (owner !== lifecycle)
        throw new Error('更新检查已结束。')
      if (!canCheckForUpdates.value && availableUpdate.value)
        return availableUpdate.value
      cancelStartupCheck()
      const action = ++operation
      const sequence = eventSequence
      status.value = 'checking'
      error.value = null
      try {
        const result = await checkPlayerUpdate(settings.value.channel)
        if (owner !== lifecycle || action !== operation)
          return result
        currentVersion.value = result.currentVersion || currentVersion.value
        lastCheckedAt.value = Date.now()
        if (sequence === eventSequence) {
          availableUpdate.value = result.available ? result : null
          status.value = result.available ? 'available' : 'latest'
          downloadedBytes.value = 0
          contentLength.value = null
        }
        else if (!result.available && !availableUpdate.value && !isBusy.value) {
          status.value = 'latest'
        }
        if (result.available && availableUpdate.value?.version === result.version && !isBusy.value)
          promptOpen.value = true
        return result
      }
      catch (cause) {
        const message = toSafeErrorMessage(cause, '更新检查失败。')
        if (owner === lifecycle && action === operation && sequence === eventSequence) {
          error.value = message
          status.value = 'error'
          if (!silent)
            promptOpen.value = false
        }
        throw new Error(message)
      }
    })()
    activeCheck = check
    void check.finally(() => {
      if (activeCheck === check)
        activeCheck = null
    }).catch(() => undefined)
    return check
  }

  async function recoverActionError(cause: unknown, fallback: string, owner: number, action: number) {
    const message = toSafeErrorMessage(cause, fallback)
    await refreshState().catch(() => undefined)
    if (owner === lifecycle && action === operation && !error.value)
      error.value = message
    return new Error(message)
  }

  function startDownload(): Promise<void> {
    if (activeDownload)
      return activeDownload
    if (!canDownload.value)
      return Promise.resolve()
    const owner = lifecycle
    const action = ++operation
    const sequence = eventSequence
    startingDownload.value = true
    error.value = null
    const download = (async () => {
      try {
        const state = await downloadPlayerUpdate()
        if (owner === lifecycle && action === operation && sequence === eventSequence)
          applyNativeState(state)
      }
      catch (cause) {
        throw await recoverActionError(cause, '更新下载失败，请继续下载。', owner, action)
      }
      finally {
        if (owner === lifecycle && action === operation)
          startingDownload.value = false
      }
    })()
    activeDownload = download
    void download.finally(() => {
      if (activeDownload === download)
        activeDownload = null
    }).catch(() => undefined)
    return download
  }

  async function downloadAvailableUpdate() {
    await initialize()
    await startDownload()
  }

  function installAvailableUpdate(): Promise<void> {
    if (activeInstall)
      return activeInstall
    if (!canInstall.value)
      return Promise.resolve()
    const owner = lifecycle
    const action = ++operation
    startingInstall.value = true
    error.value = null
    const install = (async () => {
      try {
        await installPlayerUpdate()
        await refreshState().catch(() => undefined)
      }
      catch (cause) {
        throw await recoverActionError(cause, '更新安装失败，请重试安装。', owner, action)
      }
      finally {
        if (owner === lifecycle && action === operation)
          startingInstall.value = false
      }
    })()
    activeInstall = install
    void install.finally(() => {
      if (activeInstall === install)
        activeInstall = null
    }).catch(() => undefined)
    return install
  }

  function dismissPrompt() {
    if (canDismiss.value)
      promptOpen.value = false
  }

  function reopenPrompt() {
    if (availableUpdate.value)
      promptOpen.value = true
    void refreshState().catch(() => undefined)
  }

  function dispose() {
    lifecycle += 1
    operation += 1
    cancelStartupCheck()
    unlistenNativeState?.()
    unlistenNativeState = undefined
    initialization = null
    activeRefresh = null
    activeCheck = null
    activeDownload = null
    activeInstall = null
    startingDownload.value = false
    startingInstall.value = false
  }

  return {
    settings,
    status,
    availableUpdate,
    currentVersion,
    error,
    promptOpen,
    downloadedBytes,
    contentLength,
    progressPercent,
    progressText,
    statusLabel,
    primaryActionLabel,
    isBusy,
    canCheckForUpdates,
    canDownload,
    canInstall,
    canDismiss,
    lastCheckedAt,
    initialize,
    refreshState,
    persistSettings,
    scheduleStartupCheck,
    cancelStartupCheck,
    checkForUpdates,
    downloadAvailableUpdate,
    installAvailableUpdate,
    dismissPrompt,
    reopenPrompt,
    dispose,
  }
})

function formatBytes(bytes: number): string {
  if (bytes < 1024 * 1024)
    return `${(bytes / 1024).toFixed(0)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}
