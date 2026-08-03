import type { UpdateCheckResult, UpdateProgressEvent, UpdaterSettings, UpdateStatus } from '@/services/updater'
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { flushAppSettings } from '@/services/appSettings'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import {
  checkPlayerUpdate,
  currentPlayerVersion,
  installPlayerUpdate,
  loadUpdaterSettings,
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
  let activeCheck: Promise<UpdateCheckResult> | null = null
  let startupTimer: number | undefined

  const progressPercent = computed(() => {
    if (!contentLength.value || contentLength.value <= 0)
      return null
    return Math.min(100, Math.max(0, (downloadedBytes.value / contentLength.value) * 100))
  })

  async function initialize() {
    settings.value = loadUpdaterSettings()
    currentVersion.value = await currentPlayerVersion()
  }

  async function persistSettings(next: UpdaterSettings) {
    settings.value = { ...next }
    await saveUpdaterSettings(settings.value)
    await flushAppSettings()
  }

  function scheduleStartupCheck() {
    if (!settings.value.autoCheck || startupTimer)
      return
    startupTimer = window.setTimeout(() => {
      startupTimer = undefined
      void checkForUpdates(true).catch(() => undefined)
    }, STARTUP_CHECK_DELAY_MS)
  }

  function cancelStartupCheck() {
    if (!startupTimer)
      return
    window.clearTimeout(startupTimer)
    startupTimer = undefined
  }

  async function checkForUpdates(silent = false): Promise<UpdateCheckResult> {
    if (activeCheck)
      return activeCheck

    status.value = 'checking'
    error.value = null
    activeCheck = checkPlayerUpdate(settings.value.channel)
    try {
      const result = await activeCheck
      currentVersion.value = result.currentVersion || currentVersion.value
      lastCheckedAt.value = Date.now()
      availableUpdate.value = result.available ? result : null
      status.value = result.available ? 'available' : 'latest'
      if (result.available)
        promptOpen.value = true
      return result
    }
    catch (cause) {
      const message = toSafeErrorMessage(cause, '更新检查失败。')
      error.value = message
      status.value = 'error'
      if (!silent)
        promptOpen.value = false
      throw new Error(message)
    }
    finally {
      activeCheck = null
    }
  }

  async function installAvailableUpdate() {
    if (!availableUpdate.value || status.value === 'downloading' || status.value === 'installing')
      return

    status.value = 'downloading'
    error.value = null
    downloadedBytes.value = 0
    contentLength.value = null
    try {
      await installPlayerUpdate(handleProgress)
      status.value = 'installing'
    }
    catch (cause) {
      const message = toSafeErrorMessage(cause, '更新安装失败。')
      error.value = message
      status.value = message.includes('允许 OhMyCine 安装未知应用') ? 'available' : 'error'
      throw cause
    }
  }

  function handleProgress(event: UpdateProgressEvent) {
    if (event.event === 'Started') {
      status.value = 'downloading'
      contentLength.value = event.data.content_length ?? null
      downloadedBytes.value = 0
    }
    else if (event.event === 'Progress') {
      downloadedBytes.value += event.data.chunk_length
    }
    else {
      status.value = 'installing'
    }
  }

  function dismissPrompt() {
    if (status.value === 'downloading' || status.value === 'installing')
      return
    promptOpen.value = false
  }

  function reopenPrompt() {
    if (availableUpdate.value)
      promptOpen.value = true
  }

  async function retryCheck() {
    await checkForUpdates(false)
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
    lastCheckedAt,
    initialize,
    persistSettings,
    scheduleStartupCheck,
    cancelStartupCheck,
    checkForUpdates,
    installAvailableUpdate,
    dismissPrompt,
    reopenPrompt,
    retryCheck,
  }
})
