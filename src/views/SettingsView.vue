<script setup lang="ts">
import type { PlayerStorageInfo } from '@/services/appSettings'
import type { OpenSubtitlesAuthMode, OpenSubtitlesCredentialValue } from '@/services/datasource/credentialStore'
import type { DataSourceConfig, MediaLibrary } from '@/services/datasource/types'
import type { ImageCacheStats } from '@/services/imageCache'
import type { NavigationShortcutBindings, NavigationShortcutTarget } from '@/services/navigationShortcuts'
import type { MobileEpisodeLayout, PlayerCacheMode, PlayerDemuxerCacheSize, PlayerHardwareDecoder, PlayerVideoOutput, PlayerVideoSync } from '@/services/playerInteractionSettings'
import type { PlayerShortcutBindings, PlayerShortcutTarget } from '@/services/playerShortcuts'
import type { EditableDataSourceConfig, EditableDataSourceType, LoginDataSourceType } from '@/services/settingsSourceOptions'
import type { SubtitleLanguage } from '@/services/subtitle'
import type { UpdateChannel } from '@/services/updater'
import { confirm as confirmDialog } from '@tauri-apps/plugin-dialog'
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import DanmakuProviderSettings from '@/components/player/DanmakuProviderSettings.vue'
import SecretInput from '@/components/SecretInput.vue'
import { flushAppSettings, getPlayerStorageInfo } from '@/services/appSettings'
import { readEmbyCredential, readOpenSubtitlesCredential, readRawCredentialBackup, removeCredential, saveRawCredentialBackup } from '@/services/datasource/credentialStore'
import { loginEmbyAndCreateConfig } from '@/services/datasource/emby'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { loginServerAndCreateConfig, logoutServerBestEffort } from '@/services/datasource/server'
import { getImageCacheStats, loadImageCacheSettings, saveImageCacheSettings } from '@/services/imageCache'
import { loadNavigationShortcutBindings, resetNavigationShortcutBindings, saveNavigationShortcutBindings, shortcutDisplayLabel, shortcutFromKeyboardEvent, validateUniqueNavigationShortcuts } from '@/services/navigationShortcuts'
import { loadPlayerInteractionSettings, normalizeLongPressPlaybackSpeed, savePlayerInteractionSettings } from '@/services/playerInteractionSettings'
import { loadPlayerShortcutBindings, resetPlayerShortcutBindings, savePlayerShortcutBindings, validateUniquePlayerShortcuts } from '@/services/playerShortcuts'
import { defaultDisplayName, isEditableDataSourceConfig, isEditableDataSourceType, sourceTypeLabel, SOURCE_TYPE_OPTIONS as sourceTypeOptions, SUBTITLE_LANGUAGE_OPTIONS as subtitleLanguageOptions } from '@/services/settingsSourceOptions'
import { clearOpenSubtitlesCredentials, loadSubtitleSearchSettings, OPENSUBTITLES_CREDENTIAL_REF, readOpenSubtitlesCredentials, saveOpenSubtitlesCredentials, saveSubtitleSearchSettings, testOpenSubtitlesLogin } from '@/services/subtitle'
import { useDataSourceStore } from '@/stores/datasource'
import { useUpdaterStore } from '@/stores/updater'

type SettingsMode = 'overview' | 'manage' | 'add' | 'edit' | 'playback' | 'shortcuts' | 'updates' | 'diagnostics'
type SettingsEntryId = 'datasources' | 'playback' | 'shortcuts' | 'appearance' | 'updates' | 'diagnostics'
type SettingsQueryState = Partial<Record<'section' | 'action' | 'id', string>>

interface DataSourceFormState {
  id: string | null
  type: EditableDataSourceType
  displayName: string
  url: string
  username: string
  password: string
}

interface SubtitleSettingsFormState {
  defaultLanguage: SubtitleLanguage
  openSubtitlesEnabled: boolean
  openSubtitlesAuthMode: OpenSubtitlesAuthMode
  shooterEnabled: boolean
  xunleiEnabled: boolean
  apiKey: string
  username: string
  password: string
  longPressPlaybackSpeed: number
  mobileEpisodeLayout: MobileEpisodeLayout
  androidBackgroundPlaybackEnabled: boolean
  videoOutput: PlayerVideoOutput
  hardwareDecoder: PlayerHardwareDecoder
  cacheMode: PlayerCacheMode
  demuxerMaxBytesMb: PlayerDemuxerCacheSize
  videoSync: PlayerVideoSync
}

interface UpdaterSettingsFormState {
  autoCheck: boolean
  channel: UpdateChannel
}

interface SettingsEntry {
  id: SettingsEntryId
  label: string
  title: string
  description: string
  meta: string
  actionLabel: string
  disabled: boolean
}

const store = useDataSourceStore()
const updaterStore = useUpdaterStore()
const route = useRoute()
const router = useRouter()
const form = reactive<DataSourceFormState>({
  id: null,
  type: 'emby',
  displayName: 'Emby',
  url: '',
  username: '',
  password: '',
})
const mode = ref<SettingsMode>('overview')
const isSaving = ref(false)
const clearingCacheSourceId = ref<string | null>(null)
const feedback = ref<{ type: 'success' | 'error' | 'info', message: string } | null>(null)

const subtitleSettings = loadSubtitleSearchSettings()
const playerInteractionSettings = loadPlayerInteractionSettings()
const subtitleForm = reactive<SubtitleSettingsFormState>({
  defaultLanguage: subtitleSettings.defaultLanguage,
  openSubtitlesEnabled: subtitleSettings.openSubtitlesEnabled,
  openSubtitlesAuthMode: 'apiKey',
  shooterEnabled: subtitleSettings.shooterEnabled,
  xunleiEnabled: subtitleSettings.xunleiEnabled,
  apiKey: '',
  username: '',
  password: '',
  longPressPlaybackSpeed: playerInteractionSettings.longPressPlaybackSpeed,
  mobileEpisodeLayout: playerInteractionSettings.mobileEpisodeLayout,
  androidBackgroundPlaybackEnabled: playerInteractionSettings.androidBackgroundPlaybackEnabled,
  videoOutput: playerInteractionSettings.videoOutput,
  hardwareDecoder: playerInteractionSettings.hardwareDecoder,
  cacheMode: playerInteractionSettings.cacheMode,
  demuxerMaxBytesMb: playerInteractionSettings.demuxerMaxBytesMb,
  videoSync: playerInteractionSettings.videoSync,
})
const openSubtitlesConfigured = ref(false)
const openSubtitlesConfiguredAuthMode = ref<OpenSubtitlesAuthMode | null>(null)
const isSavingSubtitleSettings = ref(false)
const subtitleFeedback = ref<{ type: 'success' | 'error' | 'info', message: string } | null>(null)
const navigationShortcutForm = reactive<NavigationShortcutBindings>(loadNavigationShortcutBindings())
const playerShortcutForm = reactive<PlayerShortcutBindings>(loadPlayerShortcutBindings())
const shortcutFeedback = ref<{ type: 'success' | 'error' | 'info', message: string } | null>(null)
const isSavingShortcuts = ref(false)
const isClearingPlayerCache = ref(false)
const updateForm = reactive<UpdaterSettingsFormState>({
  autoCheck: updaterStore.settings.autoCheck,
  channel: updaterStore.settings.channel,
})
const isSavingUpdaterSettings = ref(false)
const updateFeedback = ref<{ type: 'success' | 'error' | 'info', message: string } | null>(null)
const storageInfo = ref<PlayerStorageInfo | null>(null)
const imageCacheForm = reactive(loadImageCacheSettings())
const imageCacheStats = ref<ImageCacheStats | null>(null)
const imageCacheFeedback = ref<{ type: 'success' | 'error', message: string } | null>(null)
const isSavingImageCache = ref(false)
const configuredSources = computed(() => store.orderedConfigs)
const isEditing = computed(() => mode.value === 'edit')
const sourceCredentialConfigured = computed(() => {
  if (!isEditing.value || !form.id)
    return false
  const source = store.configs.find(config => config.id === form.id)
  return source != null && credentialRefFromConfig(source) != null
})
const editedSource = computed(() => form.id ? store.configs.find(config => config.id === form.id) ?? null : null)
const selectedProvider = computed(() => sourceTypeOptions.find(option => option.type === form.type) ?? sourceTypeOptions[0])
const activeSourceCount = computed(() => configuredSources.value.filter(source => source.enabled !== false).length)
const dataSourceEntryMeta = computed(() => {
  if (configuredSources.value.length === 0)
    return '尚未配置'
  return `${activeSourceCount.value}/${configuredSources.value.length} 个启用`
})
async function loadOpenSubtitlesCredentialField(field: 'apiKey' | 'password'): Promise<string> {
  const credential = await readOpenSubtitlesCredential(OPENSUBTITLES_CREDENTIAL_REF)
  const value = field === 'apiKey' && credential?.authMode === 'apiKey'
    ? credential.apiKey
    : field === 'password' && credential?.authMode === 'account'
      ? credential.password
      : undefined
  if (!value)
    throw new Error('当前登录方式没有已保存的 OpenSubtitles 凭据。')
  return value
}

function showSubtitleRevealError(message: string) {
  subtitleFeedback.value = { type: 'error', message }
}
const storageModeLabel = computed(() => storageInfo.value?.mode === 'portable' ? '便携模式' : '标准模式')
const playbackEntryMeta = computed(() => `${subtitleForm.videoOutput} · ${subtitleForm.hardwareDecoder}`)
const shortcutEntries = computed(() => [
  { target: 'home' as const, label: '首页', description: '返回海报墙首页。' },
  { target: 'settings' as const, label: '设置', description: '打开设置总览。' },
  { target: 'datasources' as const, label: '管理数据源', description: '打开数据源管理页面。' },
  ...configuredSources.value.map(source => ({
    target: `source:${source.id}` as NavigationShortcutTarget,
    label: source.displayName ?? source.name,
    description: source.enabled === false ? '当前数据源已停用。' : `打开 ${sourceTypeLabel(source.type)} 媒体库。`,
  })),
])
const playerShortcutEntries: Array<{ target: PlayerShortcutTarget, label: string, description: string }> = [
  { target: 'hideControls', label: '隐藏控制 UI', description: '立即隐藏控制界面，移动鼠标恢复。' },
  { target: 'playPrevious', label: '上一集', description: '对应控制栏第一个按钮。' },
  { target: 'seekBackward', label: '后退 10 秒', description: '对应控制栏后退按钮。' },
  { target: 'togglePause', label: '播放 / 暂停', description: '对应控制栏播放按钮。' },
  { target: 'seekForward', label: '前进 10 秒', description: '对应控制栏前进按钮。' },
  { target: 'playNext', label: '下一集', description: '对应控制栏下一集按钮。' },
  { target: 'toggleMute', label: '静音 / 恢复音量', description: '对应控制栏音量按钮。' },
  { target: 'toggleSpeedMenu', label: '切换倍速', description: '循环切换可用倍速。' },
  { target: 'toggleSubtitleMenu', label: '切换字幕', description: '循环切换关闭和可用字幕。' },
  { target: 'toggleDanmaku', label: '开启 / 关闭弹幕', description: '切换当前播放器的弹幕显示。' },
  { target: 'toggleDanmakuSettings', label: '弹幕设置', description: '打开播放器弹幕设置面板。' },
  { target: 'toggleAudioMenu', label: '切换音轨', description: '循环切换可用音轨。' },
  { target: 'toggleQueueMenu', label: '播放队列状态', description: '显示当前队列位置和媒体标题。' },
  { target: 'toggleSettings', label: '画面设置状态', description: '显示当前画面比例和适配模式。' },
  { target: 'toggleFullscreen', label: '全屏', description: '进入或退出播放器全屏。' },
]
const configuredNavigationShortcutCount = computed(() => shortcutEntries.value.filter(entry => navigationShortcutForm[entry.target]).length)
const configuredPlayerShortcutCount = computed(() => playerShortcutEntries.filter(entry => playerShortcutForm[entry.target]).length)
const configuredShortcutCount = computed(() => configuredNavigationShortcutCount.value + configuredPlayerShortcutCount.value)
const shortcutEntryCount = computed(() => shortcutEntries.value.length + playerShortcutEntries.length)
const openSubtitlesStatusLabel = computed(() => {
  if (!openSubtitlesConfigured.value)
    return 'OpenSubtitles 未配置'
  const modeLabel = openSubtitlesConfiguredAuthMode.value === 'account' ? '账号密码模式' : 'API Key 模式'
  return subtitleForm.openSubtitlesEnabled
    ? `OpenSubtitles ${modeLabel} · 已启用`
    : `OpenSubtitles ${modeLabel} · 已关闭`
})
const updateEntryMeta = computed(() => `${updaterStore.settings.channel === 'beta' ? 'Beta' : '正式版'} · ${updaterStore.settings.autoCheck ? '自动检测' : '手动检测'}`)
const portableStorageIsNetworkLike = computed(() =>
  storageInfo.value?.mode === 'portable' && storageInfo.value.storagePerformance === 'networkLike',
)
const credentialProtectionLabel = computed(() => {
  switch (storageInfo.value?.credentialProtection) {
    case 'windowsDpapi':
      return 'Windows DPAPI'
    case 'androidKeystore':
      return 'Android Keystore'
    case 'appleKeychain':
      return 'Apple Keychain'
    case 'linuxSecretService':
      return 'Linux Secret Service'
    case 'portableFileKey':
      return '便携文件密钥'
    case 'localFileKey':
      return '本机文件密钥'
    default:
      return '当前会话内存'
  }
})
const storageEntryMeta = computed(() => storageInfo.value ? `${storageModeLabel.value} · ${credentialProtectionLabel.value}` : '浏览器模式')
const storageModeDescription = computed(() => {
  if (!storageInfo.value)
    return '当前是浏览器开发模式，桌面版存储信息不可用。'
  if (storageInfo.value.mode === 'portable')
    return '配置、播放记录、日志和缓存跟随当前程序目录移动；凭据主密钥也保存在该目录中。'
  if (storageInfo.value.credentialProtection === 'localFileKey')
    return '应用数据库使用当前系统的用户数据目录，但系统安全存储当前不可用，凭据主密钥已降级为本机文件保护。'
  return `应用数据库使用当前系统的用户数据目录，凭据主密钥由 ${credentialProtectionLabel.value} 保护。`
})
const credentialProtectionWarning = computed(() => {
  if (storageInfo.value?.mode === 'portable')
    return '便携模式为了整目录迁移而使用文件主密钥，保护等级低于系统安全存储。请保护整个便携目录，不要将其放入共享盘、同步盘或公开备份。'
  if (storageInfo.value?.credentialProtection === 'localFileKey')
    return '当前系统安全存储不可用，凭据主密钥只能由用户数据目录权限保护。请先启用 Linux Secret Service/系统钥匙串后重新启动 Player。'
  return null
})
const pageDescription = computed(() => mode.value === 'overview'
  ? '管理媒体服务器、播放、字幕、更新和本地存储。'
  : mode.value === 'playback'
    ? '设置字幕搜索语言和字幕提供器。'
    : mode.value === 'shortcuts'
      ? '配置播放器控制和页面导航快捷键。'
      : mode.value === 'updates'
        ? '选择更新渠道并检查新版本。'
        : mode.value === 'diagnostics'
          ? '查看当前运行模式和数据目录。'
          : '添加、编辑和管理远程媒体库。')
const settingsEntries = computed<SettingsEntry[]>(() => [
  {
    id: 'datasources',
    label: 'DS',
    title: '管理媒体库',
    description: '添加和管理 OhMyCine Server、Emby 与 Jellyfin。',
    meta: dataSourceEntryMeta.value,
    actionLabel: '打开',
    disabled: false,
  },
  {
    id: 'playback',
    label: 'Play',
    title: '播放与字幕',
    description: '配置播放引擎、OpenSubtitles、射手网和迅雷字幕搜索。',
    meta: playbackEntryMeta.value,
    actionLabel: '打开',
    disabled: false,
  },
  {
    id: 'shortcuts',
    label: 'Key',
    title: '快捷键',
    description: '自定义播放器控制、控制栏按钮和页面导航按键。',
    meta: `${configuredShortcutCount.value}/${shortcutEntryCount.value} 个已设置`,
    actionLabel: '打开',
    disabled: false,
  },
  {
    id: 'appearance',
    label: 'UI',
    title: '外观',
    description: '主题、玻璃强度、海报墙密度和动画偏好会随 Cinema OS 设计系统开放。',
    meta: '规划中',
    actionLabel: '待开放',
    disabled: true,
  },
  {
    id: 'updates',
    label: 'Up',
    title: '软件更新',
    description: '选择 Beta 或正式版渠道，检查并安装新版本。',
    meta: updateEntryMeta.value,
    actionLabel: '打开',
    disabled: false,
  },
  {
    id: 'diagnostics',
    label: 'Disk',
    title: '存储 / 诊断',
    description: '查看标准或便携模式，以及数据、缓存和日志目录。',
    meta: storageEntryMeta.value,
    actionLabel: '查看',
    disabled: false,
  },
])

onMounted(() => {
  store.loadConfigs()
  void refreshOpenSubtitlesCredentialState()
  void refreshStorageInfo()
  void updaterStore.initialize().then(syncUpdaterForm)
  syncModeFromRoute()
})

watch(() => route.query, syncModeFromRoute)
watch(() => form.type, (type) => {
  if (!isEditing.value)
    form.displayName = defaultDisplayName(type)
})

function syncModeFromRoute() {
  const section = routeQueryValue('section')
  if (section === 'diagnostics') {
    replaceSettingsQuery({ section })
    mode.value = 'diagnostics'
    feedback.value = null
    void refreshStorageInfo()
    return
  }
  if (section === 'playback') {
    replaceSettingsQuery({ section })
    mode.value = 'playback'
    feedback.value = null
    subtitleFeedback.value = null
    void refreshOpenSubtitlesCredentialState()
    return
  }
  if (section === 'shortcuts') {
    replaceSettingsQuery({ section })
    mode.value = 'shortcuts'
    feedback.value = null
    shortcutFeedback.value = null
    syncShortcutForms()
    return
  }
  if (section === 'updates') {
    replaceSettingsQuery({ section })
    mode.value = 'updates'
    feedback.value = null
    updateFeedback.value = null
    syncUpdaterForm()
    return
  }
  if (section !== 'datasources') {
    replaceSettingsQuery()
    mode.value = 'overview'
    return
  }

  const action = routeQueryValue('action')
  if (action === 'add') {
    replaceSettingsQuery({ section, action })
    if (mode.value !== 'add')
      resetForm()
    mode.value = 'add'
    return
  }
  if (action === 'edit') {
    const id = routeQueryValue('id')
    const source = id ? store.configs.find(config => config.id === id) : null
    if (source && isEditableDataSourceConfig(source)) {
      replaceSettingsQuery({ section, action, id: source.id })
      if (mode.value !== 'edit' || form.id !== source.id)
        populateEditForm(source)
      return
    }
    replaceSettingsQuery({ section })
    mode.value = 'manage'
    if (id)
      feedback.value = { type: 'error', message: '未找到可编辑的数据源，请从列表中重新选择。' }
    return
  }
  replaceSettingsQuery({ section })
  mode.value = 'manage'
}

function routeQueryValue(key: string): string | null {
  const value = route.query[key]
  if (typeof value === 'string')
    return value
  if (Array.isArray(value) && typeof value[0] === 'string')
    return value[0]
  return null
}

function replaceSettingsQuery(query: SettingsQueryState = {}) {
  if (isCurrentSettingsQuery(query))
    return

  void router.replace({ name: 'settings', query })
}

function isCurrentSettingsQuery(query: SettingsQueryState): boolean {
  const currentKeys = Object.keys(route.query)
  const nextKeys = Object.keys(query)

  return currentKeys.length === nextKeys.length
    && nextKeys.every((key) => {
      const currentValue = route.query[key]
      return typeof currentValue === 'string' && currentValue === query[key as keyof SettingsQueryState]
    })
}

function openSettingsEntry(entry: SettingsEntry) {
  if (entry.disabled)
    return
  if (entry.id === 'datasources')
    goDataSources()
  else if (entry.id === 'playback')
    goPlaybackSettings()
  else if (entry.id === 'shortcuts')
    goShortcutSettings()
  else if (entry.id === 'updates')
    goUpdaterSettings()
  else if (entry.id === 'diagnostics')
    goStorageDiagnostics()
}

function goStorageDiagnostics() {
  mode.value = 'diagnostics'
  feedback.value = null
  void router.push({ name: 'settings', query: { section: 'diagnostics' } })
  void refreshStorageInfo()
}

async function refreshStorageInfo() {
  storageInfo.value = await getPlayerStorageInfo()
  imageCacheStats.value = await getImageCacheStats()
}

function formatStorageBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0)
    return '0 MB'
  if (bytes >= 1024 * 1024 * 1024)
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

async function saveImageCacheLimit() {
  isSavingImageCache.value = true
  imageCacheFeedback.value = null
  try {
    const saved = await saveImageCacheSettings(imageCacheForm)
    imageCacheForm.maxSizeMb = saved.maxSizeMb
    await flushAppSettings()
    imageCacheStats.value = await getImageCacheStats()
    imageCacheFeedback.value = {
      type: 'success',
      message: `图片缓存上限已保存为 ${saved.maxSizeMb} MB，超出部分已按最近最少使用顺序清理。`,
    }
  }
  catch (error) {
    imageCacheFeedback.value = { type: 'error', message: toSafeErrorMessage(error, '图片缓存上限保存失败。') }
  }
  finally {
    isSavingImageCache.value = false
  }
}

function goOverview() {
  mode.value = 'overview'
  feedback.value = null
  void router.replace({ name: 'settings' })
}

function goDataSources() {
  mode.value = 'manage'
  feedback.value = null
  void router.push({ name: 'settings', query: { section: 'datasources' } })
}

function goPlaybackSettings() {
  mode.value = 'playback'
  feedback.value = null
  subtitleFeedback.value = null
  void router.push({ name: 'settings', query: { section: 'playback' } })
  void refreshOpenSubtitlesCredentialState()
}

function goShortcutSettings() {
  mode.value = 'shortcuts'
  feedback.value = null
  shortcutFeedback.value = null
  syncShortcutForms()
  void router.push({ name: 'settings', query: { section: 'shortcuts' } })
}

function syncShortcutForms() {
  replaceReactiveRecord(navigationShortcutForm, loadNavigationShortcutBindings())
  replaceReactiveRecord(playerShortcutForm, loadPlayerShortcutBindings())
}

function captureNavigationShortcut(event: KeyboardEvent, target: NavigationShortcutTarget) {
  event.preventDefault()
  event.stopPropagation()
  shortcutFeedback.value = null
  if (event.code === 'Backspace' || event.code === 'Delete') {
    delete navigationShortcutForm[target]
    return
  }
  const shortcut = shortcutFromKeyboardEvent(event)
  if (!shortcut) {
    shortcutFeedback.value = {
      type: 'info',
      message: '该按键已保留给播放器或不能单独绑定，请换一个组合键。',
    }
    return
  }
  navigationShortcutForm[target] = shortcut
}

function clearNavigationShortcut(target: NavigationShortcutTarget) {
  delete navigationShortcutForm[target]
  shortcutFeedback.value = null
}

function capturePlayerShortcut(event: KeyboardEvent, target: PlayerShortcutTarget) {
  event.preventDefault()
  event.stopPropagation()
  shortcutFeedback.value = null
  if (event.code === 'Backspace' || event.code === 'Delete') {
    delete playerShortcutForm[target]
    return
  }
  const shortcut = shortcutFromKeyboardEvent(event)
  if (!shortcut) {
    shortcutFeedback.value = {
      type: 'info',
      message: '空格、方向键和 Esc 使用固定播放行为，不能覆盖。',
    }
    return
  }
  playerShortcutForm[target] = shortcut
}

function clearPlayerShortcut(target: PlayerShortcutTarget) {
  delete playerShortcutForm[target]
  shortcutFeedback.value = null
}

function resetShortcuts() {
  replaceReactiveRecord(navigationShortcutForm, resetNavigationShortcutBindings())
  replaceReactiveRecord(playerShortcutForm, resetPlayerShortcutBindings())
  shortcutFeedback.value = { type: 'info', message: '已恢复默认映射，点击保存后生效。' }
}

async function saveNavigationShortcuts() {
  isSavingShortcuts.value = true
  shortcutFeedback.value = null
  try {
    validateUniqueNavigationShortcuts(navigationShortcutForm)
    validateUniquePlayerShortcuts(playerShortcutForm)
    await Promise.all([
      saveNavigationShortcutBindings(navigationShortcutForm),
      savePlayerShortcutBindings(playerShortcutForm),
    ])
    await flushAppSettings()
    syncShortcutForms()
    shortcutFeedback.value = { type: 'success', message: '快捷键已保存并立即生效。' }
  }
  catch (error) {
    shortcutFeedback.value = { type: 'error', message: toSafeErrorMessage(error, '快捷键保存失败。') }
  }
  finally {
    isSavingShortcuts.value = false
  }
}

function replaceReactiveRecord(target: Record<string, string | undefined>, source: Record<string, string | undefined>) {
  for (const key of Object.keys(target))
    delete target[key]
  Object.assign(target, source)
}

function goUpdaterSettings() {
  mode.value = 'updates'
  feedback.value = null
  updateFeedback.value = null
  syncUpdaterForm()
  void router.push({ name: 'settings', query: { section: 'updates' } })
}

function syncUpdaterForm() {
  updateForm.autoCheck = updaterStore.settings.autoCheck
  updateForm.channel = updaterStore.settings.channel
}

async function saveUpdaterPreferences(showFeedback = true) {
  isSavingUpdaterSettings.value = true
  if (showFeedback)
    updateFeedback.value = null
  try {
    await updaterStore.persistSettings({
      autoCheck: updateForm.autoCheck,
      channel: updateForm.channel,
    })
    updaterStore.cancelStartupCheck()
    if (updateForm.autoCheck)
      updaterStore.scheduleStartupCheck()
    if (showFeedback) {
      updateFeedback.value = {
        type: 'success',
        message: `更新设置已保存。当前使用${updateForm.channel === 'beta' ? ' Beta' : '正式版'}渠道${updateForm.autoCheck ? '，启动后会自动检测。' : '，仅在手动点击时检测。'}`,
      }
    }
  }
  catch (error) {
    updateFeedback.value = { type: 'error', message: toSafeErrorMessage(error, '更新设置保存失败。') }
    throw error
  }
  finally {
    isSavingUpdaterSettings.value = false
  }
}

async function checkForUpdatesNow() {
  updateFeedback.value = null
  try {
    await saveUpdaterPreferences(false)
    const result = await updaterStore.checkForUpdates(false)
    updateFeedback.value = result.available
      ? { type: 'success', message: `发现新版本 ${result.version}，已打开签名更新确认窗口。` }
      : { type: 'success', message: `当前 ${result.currentVersion} 已是${updateForm.channel === 'beta' ? ' Beta' : '正式版'}渠道的最新版本。` }
  }
  catch (error) {
    updateFeedback.value = { type: 'error', message: toSafeErrorMessage(error, '更新检查失败。') }
  }
}

async function refreshOpenSubtitlesCredentialState() {
  const credential = await readOpenSubtitlesCredentials()
  openSubtitlesConfigured.value = credential != null
  openSubtitlesConfiguredAuthMode.value = credential?.authMode ?? null
  if (credential)
    subtitleForm.openSubtitlesAuthMode = credential.authMode
}

function subtitleSettingsSavedMessage(accountAuthenticated: boolean | null): string {
  if (accountAuthenticated === false) {
    return '设置已保存。当前账号不兼容 OpenSubtitles.org 旧账号接口，Player 已自动使用免 API Key 兼容搜索；自定义关键词和字幕下载仍可正常使用。'
  }
  if (openSubtitlesConfigured.value && subtitleForm.openSubtitlesEnabled) {
    const modeLabel = openSubtitlesConfiguredAuthMode.value === 'account' ? '账号密码' : 'API Key'
    return `播放与字幕设置已保存。OpenSubtitles ${modeLabel}模式、射手网和迅雷开关已生效。`
  }
  if (openSubtitlesConfigured.value) {
    return '播放与字幕设置已保存。OpenSubtitles 登录已保留但提供器处于关闭状态；迅雷仍会按关键词搜索，射手网按视频文件哈希匹配。'
  }
  return '播放与字幕设置已保存。射手网和迅雷字幕可直接用于本地文件。'
}

async function savePlaybackSubtitleSettings() {
  isSavingSubtitleSettings.value = true
  subtitleFeedback.value = null
  let accountAuthenticated: boolean | null = null
  try {
    const existing = await readOpenSubtitlesCredentials()
    const enteredApiKey = subtitleForm.apiKey.trim()
    const enteredUsername = subtitleForm.username.trim()
    const enteredPassword = subtitleForm.password
    const modeChanged = Boolean(existing && existing.authMode !== subtitleForm.openSubtitlesAuthMode)
    const credentialEdited = modeChanged || (subtitleForm.openSubtitlesAuthMode === 'apiKey'
      ? Boolean(enteredApiKey)
      : Boolean(enteredUsername || enteredPassword))
    if (credentialEdited) {
      const nextCredential: OpenSubtitlesCredentialValue = subtitleForm.openSubtitlesAuthMode === 'apiKey'
        ? {
            authMode: 'apiKey',
            apiKey: enteredApiKey || (existing?.authMode === 'apiKey' ? existing.apiKey : undefined),
          }
        : {
            authMode: 'account',
            username: enteredUsername || (existing?.authMode === 'account' ? existing.username : undefined),
            password: enteredPassword || (existing?.authMode === 'account' ? existing.password : undefined),
          }
      if (nextCredential.authMode === 'apiKey' && !nextCredential.apiKey)
        throw new Error('请输入 OpenSubtitles API Key。')
      if (nextCredential.authMode === 'account' && (!nextCredential.username || !nextCredential.password))
        throw new Error('请输入完整的 OpenSubtitles 账号和密码。')
      if (nextCredential.authMode === 'account') {
        const loginStatus = await testOpenSubtitlesLogin(nextCredential)
        accountAuthenticated = loginStatus.authenticated
      }
      await saveOpenSubtitlesCredentials(nextCredential)
      subtitleForm.openSubtitlesEnabled = true
    }

    if (credentialEdited) {
      subtitleForm.apiKey = ''
      subtitleForm.username = ''
      subtitleForm.password = ''
    }
    await saveSubtitleSearchSettings({
      defaultLanguage: subtitleForm.defaultLanguage,
      openSubtitlesEnabled: subtitleForm.openSubtitlesEnabled,
      shooterEnabled: subtitleForm.shooterEnabled,
      xunleiEnabled: subtitleForm.xunleiEnabled,
    })
    subtitleForm.longPressPlaybackSpeed = normalizeLongPressPlaybackSpeed(subtitleForm.longPressPlaybackSpeed)
    await savePlayerInteractionSettings({
      ...loadPlayerInteractionSettings(),
      longPressPlaybackSpeed: subtitleForm.longPressPlaybackSpeed,
      mobileEpisodeLayout: subtitleForm.mobileEpisodeLayout,
      androidBackgroundPlaybackEnabled: subtitleForm.androidBackgroundPlaybackEnabled,
      videoOutput: subtitleForm.videoOutput,
      hardwareDecoder: subtitleForm.hardwareDecoder,
      cacheMode: subtitleForm.cacheMode,
      demuxerMaxBytesMb: subtitleForm.demuxerMaxBytesMb,
      videoSync: subtitleForm.videoSync,
    })
    await flushAppSettings()
    await refreshOpenSubtitlesCredentialState()
    subtitleFeedback.value = {
      type: accountAuthenticated === false ? 'info' : 'success',
      message: `${subtitleSettingsSavedMessage(accountAuthenticated)} 播放器引擎参数将在下一次播放时生效。`,
    }
  }
  catch (error) {
    subtitleFeedback.value = { type: 'error', message: toSafeErrorMessage(error, '播放与字幕设置保存失败。') }
  }
  finally {
    isSavingSubtitleSettings.value = false
  }
}

async function clearPlaybackCache() {
  const confirmed = await confirmDialog(
    '将清除所有媒体缓存和单独视频的字幕、音轨、字幕偏移、倍速及画面设置。数据源、登录凭据和全局软件设置会保留。',
    { title: '清除播放缓存', kind: 'warning' },
  )
  if (!confirmed)
    return

  isClearingPlayerCache.value = true
  subtitleFeedback.value = null
  try {
    const result = await store.clearAllMediaCaches()
    subtitleFeedback.value = {
      type: 'success',
      message: `播放缓存已清除：移除 ${result.playbackPreferencesDeleted} 条单视频设置。数据源、登录凭据、播放记录和全局设置均已保留。`,
    }
  }
  catch (error) {
    subtitleFeedback.value = { type: 'error', message: toSafeErrorMessage(error, '播放缓存清除失败。') }
  }
  finally {
    isClearingPlayerCache.value = false
  }
}

async function clearOpenSubtitlesCredential() {
  isSavingSubtitleSettings.value = true
  subtitleFeedback.value = null
  try {
    await clearOpenSubtitlesCredentials()
    subtitleForm.apiKey = ''
    subtitleForm.username = ''
    subtitleForm.password = ''
    await refreshOpenSubtitlesCredentialState()
    subtitleFeedback.value = { type: 'success', message: 'OpenSubtitles 登录凭据已清除。' }
  }
  catch (error) {
    subtitleFeedback.value = { type: 'error', message: toSafeErrorMessage(error, 'OpenSubtitles 凭据清除失败。') }
  }
  finally {
    isSavingSubtitleSettings.value = false
  }
}

function goManage(options: { preserveFeedback?: boolean } = {}) {
  mode.value = 'manage'
  if (!options.preserveFeedback)
    feedback.value = null
  void router.replace({ name: 'settings', query: { section: 'datasources' } })
}

function goAdd() {
  resetForm()
  mode.value = 'add'
  void router.replace({ name: 'settings', query: { section: 'datasources', action: 'add' } })
}

function resetForm() {
  form.id = null
  form.type = 'emby'
  form.displayName = defaultDisplayName(form.type)
  form.url = ''
  form.username = ''
  form.password = ''
  feedback.value = null
}

function editSource(config: DataSourceConfig) {
  if (!isEditableDataSourceConfig(config)) {
    feedback.value = { type: 'error', message: '此数据源不再支持配置。' }
    return
  }
  populateEditForm(config)
  void router.replace({ name: 'settings', query: { section: 'datasources', action: 'edit', id: config.id } })
}

function populateEditForm(config: EditableDataSourceConfig) {
  form.id = config.id
  form.type = config.type
  form.displayName = config.displayName ?? config.name
  form.url = config.url
  form.username = ''
  form.password = ''
  feedback.value = {
    type: 'info',
    message: '显示名称可直接修改；服务器地址或账号变化时，请同时输入账号和密码以重新登录。',
  }
  mode.value = 'edit'
}

async function toggleSource(config: DataSourceConfig) {
  await store.updateConfig(config.id, { enabled: config.enabled === false })
}

async function removeSource(id: string) {
  await store.removeConfig(id)
  if (form.id === id)
    goManage()
}

async function clearSourceCache(source: DataSourceConfig) {
  clearingCacheSourceId.value = source.id
  feedback.value = null
  try {
    await store.clearSourceCache(source.id)
    feedback.value = { type: 'success', message: `已清除「${source.displayName ?? source.name}」的媒体库、列表与详情缓存，凭证和配置未受影响。` }
  }
  catch (error) {
    feedback.value = { type: 'error', message: toSafeErrorMessage(error, '清除缓存失败，请稍后重试。') }
  }
  finally {
    clearingCacheSourceId.value = null
  }
}

async function saveSource() {
  isSaving.value = true
  feedback.value = null
  try {
    if (mode.value === 'edit' && form.id) {
      await saveEditedSource(form.id)
      return
    }

    const id = `${form.type}-${Date.now()}`
    const result = await loginAndCreateConfig(form.type, {
      id,
      url: form.url,
      displayName: form.displayName,
      username: form.username,
      password: form.password,
      order: store.configs.length,
    })
    try {
      await store.replaceConfig(result.config)
    }
    catch (error) {
      if (result.config.type === 'server')
        await logoutServerBestEffort(result.config)
      await restoreCredentialForConfig(result.config, null).catch(() => undefined)
      throw error
    }
    const libraryCount = result.libraries.length
    const label = sourceTypeLabel(form.type)
    resetForm()
    feedback.value = { type: 'success', message: `${label} 连接测试成功，已验证 ${libraryCount} 个入口。` }
    goManage({ preserveFeedback: true })
  }
  catch (error) {
    feedback.value = {
      type: 'error',
      message: toSafeErrorMessage(error, '添加数据源失败，请检查服务器 URL、账号和密码。'),
    }
  }
  finally {
    isSaving.value = false
  }
}

async function saveEditedSource(id: string) {
  const existing = store.configs.find(config => config.id === id)
  if (!existing)
    throw new Error('数据源不存在。')
  if (!isEditableDataSourceType(existing.type))
    throw new Error('此数据源不再支持编辑。')

  const username = form.username.trim()
  const nextUrl = form.url.trim()
  const nextDisplayName = form.displayName.trim() || existing.displayName || existing.name
  const label = sourceTypeLabel(existing.type)
  const shouldRelogin = normalizeComparableUrl(nextUrl) !== normalizeComparableUrl(existing.url)
    || Boolean(username || form.password)
  if (shouldRelogin && (!username || !form.password))
    throw new Error(`更新 ${label} 地址或重新登录时必须同时填写账号和密码。`)

  if (shouldRelogin) {
    const previousCredential = await readCredentialBackupForConfig(existing)
    const sameServerOrigin = existing.type === 'server'
      && normalizeComparableUrl(nextUrl) === normalizeComparableUrl(existing.url)
    let result: { config: DataSourceConfig, libraries: MediaLibrary[] }
    try {
      result = await loginAndCreateConfig(existing.type, {
        id,
        url: nextUrl,
        displayName: nextDisplayName,
        username,
        password: form.password,
        order: existing.order,
        deviceId: existing.type === 'server' && typeof existing.extra?.deviceId === 'string' ? existing.extra.deviceId : undefined,
        retainTokenOnValidationFailure: sameServerOrigin,
      })
    }
    catch (error) {
      if (sameServerOrigin)
        await store.reloadSource(id).catch(() => undefined)
      throw error
    }
    try {
      await store.replaceConfig({ ...result.config, enabled: existing.enabled !== false })
    }
    catch (error) {
      const changedServerOrigin = result.config.type === 'server'
        && normalizeComparableUrl(result.config.url) !== normalizeComparableUrl(existing.url)
      if (result.config.type === 'server' && !changedServerOrigin) {
        await store.reloadSource(id).catch(() => undefined)
      }
      else {
        if (result.config.type === 'server')
          await logoutServerBestEffort(result.config)
        await restoreCredentialForConfig(result.config, previousCredential).catch(() => undefined)
      }
      throw error
    }
    feedback.value = { type: 'success', message: `${label} 已重新连接，并验证 ${result.libraries.length} 个入口。` }
    form.username = ''
    form.password = ''
    goManage({ preserveFeedback: true })
    return
  }

  await store.updateConfig(id, { name: nextDisplayName, displayName: nextDisplayName })
  feedback.value = { type: 'success', message: '数据源已更新。若凭据已过期，请编辑后输入账号密码重新登录。' }
  goManage({ preserveFeedback: true })
}

function loginAndCreateConfig(type: LoginDataSourceType, input: {
  id: string
  url: string
  displayName: string
  username: string
  password: string
  order: number
  deviceId?: string
  retainTokenOnValidationFailure?: boolean
}): Promise<{ config: DataSourceConfig, libraries: MediaLibrary[] }> {
  if (type === 'server')
    return loginServerAndCreateConfig(input)
  return loginEmbyAndCreateConfig({ ...input, sourceType: type })
}

function sourceStatusLine(source: DataSourceConfig): string {
  const status = source.enabled === false ? '已停用' : '已启用'
  const credentialState = credentialRefFromConfig(source) ? '登录信息已保存' : '需要重新登录'
  return `状态：${status} · 类型：${sourceTypeLabel(source.type)} · ${credentialState}`
}

function normalizeComparableUrl(value: string): string {
  return value.trim().replace(/\/+$/, '')
}

async function readCredentialBackupForConfig(config: DataSourceConfig): Promise<string | null> {
  const credentialRef = credentialRefFromConfig(config)
  return credentialRef ? readRawCredentialBackup(credentialRef) : null
}

async function restoreCredentialForConfig(config: DataSourceConfig, previousCredential: string | null): Promise<void> {
  const credentialRef = credentialRefFromConfig(config)
  if (!credentialRef)
    return

  if (previousCredential)
    await saveRawCredentialBackup(credentialRef, previousCredential)
  else
    await removeCredential(credentialRef)
}

function credentialRefFromConfig(config: DataSourceConfig): string | null {
  return typeof config.extra?.credentialRef === 'string' ? config.extra.credentialRef : null
}

function sourceCredentialLoader(): (() => Promise<string>) | undefined {
  const source = editedSource.value
  if (!source || (source.type !== 'emby' && source.type !== 'jellyfin'))
    return undefined
  return async () => {
    const credentialRef = credentialRefFromConfig(source)
    if (!credentialRef)
      throw new Error('该数据源没有已保存的登录凭据。')
    const credential = await readEmbyCredential(credentialRef)
    if (!credential?.password)
      throw new Error('当前登录方式没有保存密码。')
    return credential.password
  }
}

function showSourceRevealError(message: string) {
  feedback.value = { type: 'error', message }
}

function selectSourceType(type: EditableDataSourceType) {
  if (isEditing.value)
    return
  form.type = type
  form.displayName = defaultDisplayName(type)
  form.url = ''
  form.username = ''
  form.password = ''
  feedback.value = null
}
</script>

<template>
  <div class="settings-view theme-adaptive mobile-nav-safe min-h-full px-4 pb-6 pt-20 sm:p-6 sm:pl-20 sm:pt-20">
    <div
      v-if="feedback && mode === 'manage'"
      class="fixed inset-x-4 top-20 z-50 max-w-md rounded-2xl border px-4 py-3 text-sm shadow-2xl backdrop-blur-xl sm:left-auto sm:right-6"
      :class="{
        'border-emerald-400/20 bg-emerald-400/10 text-emerald-100': feedback.type === 'success',
        'border-red-400/20 bg-red-400/10 text-red-100': feedback.type === 'error',
        'border-white/12 bg-black/50 text-white/72': feedback.type === 'info',
      }"
    >
      {{ feedback.message }}
    </div>
    <div class="mx-auto max-w-6xl space-y-8">
      <header>
        <p class="text-xs uppercase tracking-[0.28em] text-white/38">
          Settings
        </p>
        <h1 class="mt-2 text-3xl font-bold text-white">
          设置
        </h1>
        <p class="mt-3 max-w-2xl text-sm leading-6 text-white/48">
          {{ pageDescription }}
        </p>
      </header>

      <div v-if="mode !== 'overview'" class="flex">
        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-2xl bg-white/8 px-4 py-2 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14 hover:text-white"
          title="返回设置总览"
          aria-label="返回设置总览"
          @click="goOverview"
        >
          <svg width="16" height="16" viewBox="0 0 20 20" fill="none" aria-hidden="true">
            <path d="M12.5 4.5L7 10l5.5 5.5M8 10h8" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
          设置总览
        </button>
      </div>

      <section v-if="mode === 'overview'" class="settings-overview-grid grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        <button
          v-for="entry in settingsEntries"
          :key="entry.id"
          type="button"
          class="settings-entry-card glass-panel flex min-h-0 flex-col rounded-[1.5rem] p-5 text-left transition-all duration-200 disabled:cursor-not-allowed sm:min-h-56"
          :class="entry.disabled ? 'opacity-58' : 'hover:-translate-y-0.5 hover:bg-white/10'"
          :disabled="entry.disabled"
          @click="openSettingsEntry(entry)"
        >
          <span class="mb-5 flex items-center justify-between gap-3">
            <span
              class="flex h-11 min-w-11 items-center justify-center rounded-2xl px-3 text-sm font-bold"
              :class="entry.disabled ? 'bg-white/8 text-white/42' : 'bg-primary/18 text-primary'"
            >
              {{ entry.label }}
            </span>
            <span
              class="rounded-full px-3 py-1 text-xs font-semibold"
              :class="entry.disabled ? 'bg-white/8 text-white/42' : 'bg-primary/16 text-primary'"
            >
              {{ entry.meta }}
            </span>
          </span>

          <span class="block text-lg font-bold text-white">
            {{ entry.title }}
          </span>
          <span class="mt-3 block flex-1 text-sm leading-6 text-white/48">
            {{ entry.description }}
          </span>
          <span
            class="mt-6 inline-flex w-fit items-center rounded-xl px-3 py-2 text-xs font-semibold"
            :class="entry.disabled ? 'bg-white/6 text-white/36' : 'bg-white/8 text-white/70'"
          >
            {{ entry.actionLabel }}
          </span>
        </button>
      </section>

      <section v-else-if="mode === 'updates'" class="space-y-5">
        <div class="glass-panel rounded-[1.5rem] p-6">
          <div class="flex flex-wrap items-start justify-between gap-4 border-b border-white/8 pb-5">
            <div>
              <p class="text-xs uppercase tracking-[0.2em] text-white/36">
                Signed Updates
              </p>
              <h2 class="mt-2 text-xl font-bold text-white">
                软件更新
              </h2>
              <p class="mt-2 max-w-3xl text-sm leading-6 text-white/48">
                选择更新渠道，保存后可立即检测。发现新版本后会先征求确认。
              </p>
            </div>
            <span class="rounded-full bg-primary/16 px-3 py-1.5 text-xs font-semibold text-primary">
              当前版本 {{ updaterStore.currentVersion || '读取中…' }}
            </span>
          </div>

          <div
            v-if="updateFeedback"
            class="mt-5 rounded-2xl border px-4 py-3 text-sm"
            :class="{
              'border-emerald-400/20 bg-emerald-400/10 text-emerald-100': updateFeedback.type === 'success',
              'border-red-400/20 bg-red-400/10 text-red-100': updateFeedback.type === 'error',
              'border-white/12 bg-white/6 text-white/58': updateFeedback.type === 'info',
            }"
          >
            {{ updateFeedback.message }}
          </div>

          <div class="mt-5 grid gap-4 lg:grid-cols-2">
            <div class="rounded-2xl bg-black/16 p-4">
              <p class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">
                更新渠道
              </p>
              <div class="mt-3 grid gap-2 sm:grid-cols-2">
                <button
                  type="button"
                  class="rounded-2xl border px-4 py-3 text-left transition-colors"
                  :class="updateForm.channel === 'beta' ? 'border-primary/45 bg-primary/16 text-white' : 'border-white/10 bg-white/5 text-white/58 hover:bg-white/8'"
                  @click="updateForm.channel = 'beta'"
                >
                  <span class="block text-sm font-semibold">Beta</span>
                  <span class="mt-1 block text-xs leading-5 text-white/40">优先获得新功能。</span>
                </button>
                <button
                  type="button"
                  class="rounded-2xl border px-4 py-3 text-left transition-colors"
                  :class="updateForm.channel === 'stable' ? 'border-primary/45 bg-primary/16 text-white' : 'border-white/10 bg-white/5 text-white/58 hover:bg-white/8'"
                  @click="updateForm.channel = 'stable'"
                >
                  <span class="block text-sm font-semibold">正式版</span>
                  <span class="mt-1 block text-xs leading-5 text-white/40">只接收正式发布。</span>
                </button>
              </div>
            </div>

            <div class="rounded-2xl bg-black/16 p-4">
              <label class="flex items-center justify-between gap-4">
                <span>
                  <span class="block text-sm font-semibold text-white">启动时自动检测</span>
                  <span class="mt-1 block text-xs leading-5 text-white/42">启动后自动检查一次。</span>
                </span>
                <input v-model="updateForm.autoCheck" type="checkbox" class="h-5 w-5 accent-primary">
              </label>
            </div>
          </div>

          <div class="mt-5 flex flex-wrap gap-3">
            <button
              type="button"
              class="rounded-2xl bg-primary/80 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary disabled:cursor-wait disabled:opacity-55"
              :disabled="isSavingUpdaterSettings || updaterStore.status === 'checking'"
              @click="saveUpdaterPreferences()"
            >
              {{ isSavingUpdaterSettings ? '保存中…' : '保存更新设置' }}
            </button>
            <button
              type="button"
              class="rounded-2xl bg-white/10 px-4 py-2 text-sm font-semibold text-white/78 transition-colors hover:bg-white/16 disabled:cursor-wait disabled:opacity-55"
              :disabled="isSavingUpdaterSettings || updaterStore.status === 'checking' || updaterStore.status === 'downloading' || updaterStore.status === 'installing'"
              @click="checkForUpdatesNow"
            >
              {{ updaterStore.status === 'checking' ? '检测中…' : '立即检测更新' }}
            </button>
            <button
              v-if="updaterStore.availableUpdate && !updaterStore.promptOpen"
              type="button"
              class="rounded-2xl bg-white/8 px-4 py-2 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14"
              @click="updaterStore.reopenPrompt()"
            >
              查看 {{ updaterStore.availableUpdate.version }}
            </button>
          </div>
        </div>
      </section>

      <section v-else-if="mode === 'diagnostics'" class="space-y-5">
        <div class="glass-panel rounded-[1.5rem] p-6">
          <div class="flex flex-wrap items-start justify-between gap-4 border-b border-white/8 pb-5">
            <div>
              <p class="text-xs uppercase tracking-[0.2em] text-white/36">
                Storage Mode
              </p>
              <h2 class="mt-2 text-xl font-bold text-white">
                {{ storageModeLabel }}
              </h2>
              <p class="mt-2 max-w-2xl text-sm leading-6 text-white/48">
                {{ storageModeDescription }}
              </p>
            </div>
            <span class="rounded-full bg-primary/16 px-3 py-1.5 text-xs font-semibold text-primary">
              {{ credentialProtectionLabel }}
            </span>
          </div>

          <div v-if="storageInfo" class="divide-y divide-white/8">
            <div class="grid gap-2 py-4 md:grid-cols-[9rem_1fr]">
              <span class="text-sm font-semibold text-white/66">数据目录</span>
              <code class="break-all text-sm text-white/48">{{ storageInfo.dataDir }}</code>
            </div>
            <div class="grid gap-2 py-4 md:grid-cols-[9rem_1fr]">
              <span class="text-sm font-semibold text-white/66">缓存目录</span>
              <code class="break-all text-sm text-white/48">{{ storageInfo.cacheDir }}</code>
            </div>
            <div class="grid gap-2 py-4 md:grid-cols-[9rem_1fr]">
              <span class="text-sm font-semibold text-white/66">日志目录</span>
              <code class="break-all text-sm text-white/48">{{ storageInfo.logDir }}</code>
            </div>
            <div class="grid gap-2 py-4 md:grid-cols-[9rem_1fr]">
              <span class="text-sm font-semibold text-white/66">便携标记</span>
              <code class="break-all text-sm text-white/48">{{ storageInfo.portableMarkerPath }}</code>
            </div>
          </div>

          <div
            v-if="portableStorageIsNetworkLike"
            class="mt-4 border-l-2 border-amber-400/70 bg-amber-400/8 px-4 py-3 text-sm leading-6 text-amber-100/80"
          >
            当前便携目录位于 WSL 或网络映射路径，SQLite、日志和缓存读写会明显变慢。请把完整便携文件夹复制到 Windows 本地磁盘，例如 <code class="text-amber-100">C:\OhMyCine-Portable</code>，再从那里启动。
          </div>

          <div
            v-if="credentialProtectionWarning"
            class="mt-4 border-l-2 border-amber-400/70 bg-amber-400/8 px-4 py-3 text-sm leading-6 text-amber-100/80"
          >
            {{ credentialProtectionWarning }}
          </div>

          <div v-if="!storageInfo" class="mt-5 rounded-xl bg-white/6 px-4 py-3 text-sm leading-6 text-white/48">
            当前是浏览器开发模式，没有可查询的 Tauri 桌面存储路径。
          </div>
        </div>

        <div class="glass-panel rounded-[1.5rem] p-6">
          <div class="grid gap-5 md:grid-cols-[minmax(0,1fr)_minmax(15rem,22rem)] md:items-end">
            <div>
              <p class="text-xs uppercase tracking-[0.2em] text-white/36">
                Artwork Cache
              </p>
              <h3 class="mt-2 text-lg font-bold text-white">
                图片缓存上限
              </h3>
              <p class="mt-2 max-w-2xl text-sm leading-6 text-white/48">
                海报、背景和缩略图使用最近最少使用淘汰策略。默认上限为 500 MB，降低上限后会立即清理最久未使用的图片。
              </p>
              <p class="mt-3 text-sm text-white/58">
                当前占用 {{ imageCacheStats ? formatStorageBytes(imageCacheStats.totalBytes) : '正在统计…' }}<span v-if="imageCacheStats"> · {{ imageCacheStats.fileCount }} 张</span>
              </p>
            </div>

            <div class="flex items-end gap-3">
              <label class="min-w-0 flex-1">
                <span class="text-xs font-semibold text-white/46">最大容量（MB）</span>
                <input
                  v-model.number="imageCacheForm.maxSizeMb"
                  type="number"
                  min="100"
                  max="4096"
                  step="100"
                  class="mt-2 w-full rounded-lg border border-white/10 bg-white/6 px-3 py-2.5 text-sm text-white outline-none focus:border-primary/60"
                >
              </label>
              <button
                type="button"
                class="h-[2.7rem] shrink-0 rounded-lg bg-primary/80 px-4 text-sm font-bold text-white transition-colors hover:bg-primary disabled:cursor-wait disabled:opacity-55"
                :disabled="isSavingImageCache"
                @click="saveImageCacheLimit"
              >
                {{ isSavingImageCache ? '保存中…' : '保存' }}
              </button>
            </div>
          </div>
          <p
            v-if="imageCacheFeedback"
            class="mt-4 border-l-2 px-3 py-2 text-sm leading-6"
            :class="imageCacheFeedback.type === 'success' ? 'border-emerald-400/70 bg-emerald-400/8 text-emerald-100/82' : 'border-red-400/70 bg-red-400/8 text-red-100/82'"
          >
            {{ imageCacheFeedback.message }}
          </p>
        </div>

        <div class="border-l-2 border-primary/36 px-5 py-1 text-sm leading-7 text-white/50">
          正式便携 ZIP 会自带 <code class="text-white/70">portable.flag</code>。直接使用单个 EXE 时，在 EXE 同目录创建同名空文件并重启即可进入便携模式；删除标记并重启则回到标准模式。两种模式使用独立数据目录，不会自动互相覆盖。
        </div>
      </section>

      <section v-else-if="mode === 'shortcuts'" class="space-y-5">
        <div class="glass-panel rounded-[1.5rem] p-6">
          <div class="flex flex-wrap items-start justify-between gap-4 border-b border-white/8 pb-5">
            <div>
              <p class="text-xs uppercase tracking-[0.2em] text-white/36">
                Keyboard Controls
              </p>
              <h2 class="mt-2 text-xl font-bold text-white">
                快捷键
              </h2>
              <p class="mt-2 max-w-3xl text-sm leading-6 text-white/48">
                空格、方向键和 Esc 使用固定播放行为；其他播放控制和页面导航按键均可自定义。
              </p>
            </div>
            <span class="rounded-full bg-primary/16 px-3 py-1.5 text-xs font-semibold text-primary">
              {{ configuredShortcutCount }}/{{ shortcutEntryCount }} 个已设置
            </span>
          </div>

          <div
            v-if="shortcutFeedback"
            class="mt-5 rounded-2xl border px-4 py-3 text-sm"
            :class="{
              'border-emerald-400/20 bg-emerald-400/10 text-emerald-100': shortcutFeedback.type === 'success',
              'border-red-400/20 bg-red-400/10 text-red-100': shortcutFeedback.type === 'error',
              'border-white/12 bg-white/6 text-white/58': shortcutFeedback.type === 'info',
            }"
          >
            {{ shortcutFeedback.message }}
          </div>

          <div class="mt-6 flex items-end justify-between gap-4 border-b border-white/8 pb-3">
            <div>
              <h3 class="text-sm font-bold text-white/82">
                播放控制
              </h3>
              <p class="mt-1 text-xs text-white/40">
                仅在播放器页面生效，操作结果显示在右上角。
              </p>
            </div>
            <span class="text-xs font-semibold text-white/38">
              {{ configuredPlayerShortcutCount }}/{{ playerShortcutEntries.length }}
            </span>
          </div>

          <div class="divide-y divide-white/8">
            <div
              v-for="entry in playerShortcutEntries"
              :key="entry.target"
              class="grid gap-3 py-4 md:grid-cols-[minmax(0,1fr)_13rem_auto] md:items-center"
            >
              <div>
                <p class="text-sm font-semibold text-white/82">
                  {{ entry.label }}
                </p>
                <p class="mt-1 text-xs leading-5 text-white/40">
                  {{ entry.description }}
                </p>
              </div>
              <button
                type="button"
                class="h-11 rounded-xl border border-white/12 bg-black/18 px-3 text-left font-mono text-sm text-white/76 outline-none transition-colors focus:border-primary/70 focus:bg-primary/10"
                :title="`当前快捷键：${shortcutDisplayLabel(playerShortcutForm[entry.target])}。点击后按下新按键。`"
                @keydown="capturePlayerShortcut($event, entry.target)"
              >
                {{ shortcutDisplayLabel(playerShortcutForm[entry.target]) }}
              </button>
              <button
                type="button"
                class="h-10 rounded-xl bg-white/8 px-3 text-xs font-semibold text-white/60 transition-colors hover:bg-white/14 hover:text-white disabled:opacity-35"
                :disabled="!playerShortcutForm[entry.target]"
                @click="clearPlayerShortcut(entry.target)"
              >
                清除
              </button>
            </div>
          </div>

          <div class="mt-7 flex items-end justify-between gap-4 border-b border-white/8 pb-3">
            <div>
              <h3 class="text-sm font-bold text-white/82">
                页面导航
              </h3>
              <p class="mt-1 text-xs text-white/40">
                首页、设置和媒体源入口。
              </p>
            </div>
            <span class="text-xs font-semibold text-white/38">
              {{ configuredNavigationShortcutCount }}/{{ shortcutEntries.length }}
            </span>
          </div>

          <div class="divide-y divide-white/8">
            <div
              v-for="entry in shortcutEntries"
              :key="entry.target"
              class="grid gap-3 py-4 md:grid-cols-[minmax(0,1fr)_13rem_auto] md:items-center"
            >
              <div>
                <p class="text-sm font-semibold text-white/82">
                  {{ entry.label }}
                </p>
                <p class="mt-1 text-xs leading-5 text-white/40">
                  {{ entry.description }}
                </p>
              </div>
              <button
                type="button"
                class="h-11 rounded-xl border border-white/12 bg-black/18 px-3 text-left font-mono text-sm text-white/76 outline-none transition-colors focus:border-primary/70 focus:bg-primary/10"
                :title="`当前快捷键：${shortcutDisplayLabel(navigationShortcutForm[entry.target])}。点击后按下新按键。`"
                @keydown="captureNavigationShortcut($event, entry.target)"
              >
                {{ shortcutDisplayLabel(navigationShortcutForm[entry.target]) }}
              </button>
              <button
                type="button"
                class="h-10 rounded-xl bg-white/8 px-3 text-xs font-semibold text-white/60 transition-colors hover:bg-white/14 hover:text-white disabled:opacity-35"
                :disabled="!navigationShortcutForm[entry.target]"
                @click="clearNavigationShortcut(entry.target)"
              >
                清除
              </button>
            </div>
          </div>

          <div class="mt-5 flex flex-wrap gap-3 border-t border-white/8 pt-5">
            <button
              type="button"
              class="rounded-2xl bg-primary/80 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary disabled:cursor-wait disabled:opacity-55"
              :disabled="isSavingShortcuts"
              @click="saveNavigationShortcuts"
            >
              {{ isSavingShortcuts ? '保存中…' : '保存快捷键' }}
            </button>
            <button
              type="button"
              class="rounded-2xl bg-white/8 px-4 py-2 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14"
              @click="resetShortcuts"
            >
              恢复默认
            </button>
          </div>
        </div>
      </section>

      <section v-else-if="mode === 'playback'" class="space-y-5">
        <div class="glass-panel rounded-[1.5rem] p-6">
          <div class="flex flex-wrap items-start justify-between gap-4 border-b border-white/8 pb-5">
            <div>
              <p class="text-xs uppercase tracking-[0.2em] text-white/36">
                Subtitle Search
              </p>
              <h2 class="mt-2 text-xl font-bold text-white">
                播放与字幕
              </h2>
              <p class="mt-2 max-w-3xl text-sm leading-6 text-white/48">
                Emby 播放可选择服务器搜索或本地搜索，其他媒体源使用本地字幕提供器。
              </p>
            </div>
            <span
              class="rounded-full px-3 py-1.5 text-xs font-semibold"
              :class="openSubtitlesConfigured && subtitleForm.openSubtitlesEnabled ? 'bg-emerald-400/14 text-emerald-100' : 'bg-amber-300/12 text-amber-100'"
            >
              {{ openSubtitlesStatusLabel }}
            </span>
          </div>

          <DanmakuProviderSettings />

          <div
            v-if="subtitleFeedback"
            class="mt-5 rounded-2xl border px-4 py-3 text-sm"
            :class="{
              'border-emerald-400/20 bg-emerald-400/10 text-emerald-100': subtitleFeedback.type === 'success',
              'border-red-400/20 bg-red-400/10 text-red-100': subtitleFeedback.type === 'error',
              'border-white/12 bg-white/6 text-white/58': subtitleFeedback.type === 'info',
            }"
          >
            {{ subtitleFeedback.message }}
          </div>

          <div class="mt-5 border-b border-white/8 pb-5">
            <div>
              <p class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">
                播放器引擎
              </p>
              <p class="mt-2 text-xs leading-5 text-white/38">
                参数在下一次播放媒体时生效。默认组合兼顾画质和兼容性，遇到黑屏、花屏或硬解异常时再切换。
              </p>
            </div>

            <div class="mt-4 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              <label class="rounded-2xl bg-black/16 p-4">
                <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">视频输出</span>
                <select
                  v-model="subtitleForm.videoOutput"
                  class="mt-3 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors focus:border-primary/60"
                >
                  <option value="gpu-next">gpu-next（推荐）</option>
                  <option value="gpu">gpu（兼容模式）</option>
                </select>
                <span class="mt-2 block text-xs leading-5 text-white/38">gpu-next 是默认现代渲染器；旧设备或驱动异常时可尝试 gpu。</span>
              </label>

              <label class="rounded-2xl bg-black/16 p-4">
                <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">解码器</span>
                <select
                  v-model="subtitleForm.hardwareDecoder"
                  class="mt-3 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors focus:border-primary/60"
                >
                  <option value="auto-safe">自动安全（推荐）</option>
                  <option value="auto">硬件优先</option>
                  <option value="software">纯软件解码</option>
                </select>
                <span class="mt-2 block text-xs leading-5 text-white/38">Android 使用 MediaCodec，Windows 使用 mpv 自动硬解策略。</span>
              </label>

              <label class="rounded-2xl bg-black/16 p-4">
                <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">媒体缓存</span>
                <select
                  v-model="subtitleForm.cacheMode"
                  class="mt-3 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors focus:border-primary/60"
                >
                  <option value="auto">自动（推荐）</option>
                  <option value="enabled">始终启用</option>
                  <option value="disabled">关闭</option>
                </select>
                <span class="mt-2 block text-xs leading-5 text-white/38">远程媒体建议保持自动；关闭缓存可能影响网络播放稳定性。</span>
              </label>

              <label class="rounded-2xl bg-black/16 p-4">
                <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">缓存上限</span>
                <select
                  v-model.number="subtitleForm.demuxerMaxBytesMb"
                  class="mt-3 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors focus:border-primary/60"
                >
                  <option v-for="size in [64, 128, 256, 512]" :key="size" :value="size">
                    {{ size }} MB
                  </option>
                </select>
                <span class="mt-2 block text-xs leading-5 text-white/38">更大的缓存可缓冲网络抖动，也会占用更多内存。</span>
              </label>

              <label class="rounded-2xl bg-black/16 p-4 md:col-span-2 xl:col-span-2">
                <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">音画同步</span>
                <select
                  v-model="subtitleForm.videoSync"
                  class="mt-3 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors focus:border-primary/60"
                >
                  <option value="audio">以音频为准（推荐）</option>
                  <option value="display-resample">显示刷新率重采样</option>
                  <option value="display-vdrop">显示刷新率丢帧同步</option>
                </select>
                <span class="mt-2 block text-xs leading-5 text-white/38">显示同步适合固定刷新率屏幕；出现音调或流畅度异常时恢复“以音频为准”。</span>
              </label>
            </div>
          </div>

          <div class="mt-5 grid gap-4 lg:grid-cols-3">
            <label class="rounded-2xl bg-black/16 p-4 lg:col-span-1">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">方向键长按倍速</span>
              <select
                v-model.number="subtitleForm.longPressPlaybackSpeed"
                class="mt-3 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors focus:border-primary/60"
              >
                <option v-for="speed in [1.25, 1.5, 2, 2.5, 3, 4]" :key="speed" :value="speed">
                  {{ speed }}x
                </option>
              </select>
              <span class="mt-2 block text-xs leading-5 text-white/38">长按右方向键临时使用该倍速，松开后恢复当前视频原来的速度；长按左方向键持续后退。</span>
            </label>

            <label class="rounded-2xl bg-black/16 p-4 lg:col-span-1">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">手机选集排布</span>
              <select
                v-model="subtitleForm.mobileEpisodeLayout"
                class="mt-3 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors focus:border-primary/60"
              >
                <option value="horizontal">横向卡片</option>
                <option value="vertical">竖向列表</option>
              </select>
              <span class="mt-2 block text-xs leading-5 text-white/38">控制手机剧集详情页的默认选集方式，不影响电脑端选集轨道。</span>
            </label>

            <div class="rounded-2xl bg-black/16 p-4 lg:col-span-1">
              <label class="flex items-center justify-between gap-4">
                <span>
                  <span class="block text-sm font-semibold text-white">Android 后台播放</span>
                  <span class="mt-1 block text-xs leading-5 text-white/42">开启后切到后台继续播放，并显示系统媒体通知；关闭后进入后台会自动暂停。</span>
                </span>
                <input v-model="subtitleForm.androidBackgroundPlaybackEnabled" type="checkbox" class="h-5 w-5 accent-primary">
              </label>
            </div>

            <label class="rounded-2xl bg-black/16 p-4 lg:col-span-1">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">默认搜索语言</span>
              <select
                v-model="subtitleForm.defaultLanguage"
                class="mt-3 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors focus:border-primary/60"
              >
                <option v-for="option in subtitleLanguageOptions" :key="option.value" :value="option.value">
                  {{ option.label }}
                </option>
              </select>
              <span class="mt-2 block text-xs leading-5 text-white/38">OpenSubtitles 支持全部语言；射手网支持中文和英文；迅雷按返回语言筛选。</span>
            </label>

            <div class="rounded-2xl bg-black/16 p-4 lg:col-span-2">
              <label class="flex items-center justify-between gap-4">
                <span>
                  <span class="block text-sm font-semibold text-white">启用 OpenSubtitles</span>
                  <span class="mt-1 block text-xs leading-5 text-white/42">API Key 与账号密码是两种并列登录方式，选择一种即可。</span>
                </span>
                <input v-model="subtitleForm.openSubtitlesEnabled" type="checkbox" class="h-5 w-5 accent-primary">
              </label>

              <div class="mt-4 space-y-4 border-t border-white/8 pt-4">
                <div class="grid grid-cols-2 gap-2 rounded-xl bg-white/5 p-1" role="group" aria-label="OpenSubtitles 登录方式">
                  <button
                    type="button"
                    class="rounded-lg px-3 py-2 text-xs font-semibold transition-colors"
                    :class="subtitleForm.openSubtitlesAuthMode === 'apiKey' ? 'bg-white/14 text-white' : 'text-white/46 hover:text-white/76'"
                    @click="subtitleForm.openSubtitlesAuthMode = 'apiKey'"
                  >
                    API Key
                  </button>
                  <button
                    type="button"
                    class="rounded-lg px-3 py-2 text-xs font-semibold transition-colors"
                    :class="subtitleForm.openSubtitlesAuthMode === 'account' ? 'bg-white/14 text-white' : 'text-white/46 hover:text-white/76'"
                    @click="subtitleForm.openSubtitlesAuthMode = 'account'"
                  >
                    账号密码
                  </button>
                </div>

                <label v-if="subtitleForm.openSubtitlesAuthMode === 'apiKey'" class="block">
                  <span class="block text-xs font-semibold text-white/72">OpenSubtitles API Key</span>
                  <span class="mt-1 block text-xs leading-5 text-white/38">通过 OpenSubtitles.com REST API 搜索和下载字幕。</span>
                  <SecretInput
                    v-model="subtitleForm.apiKey"
                    class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                    :configured="openSubtitlesConfiguredAuthMode === 'apiKey'"
                    :load-secret="openSubtitlesConfiguredAuthMode === 'apiKey' ? () => loadOpenSubtitlesCredentialField('apiKey') : undefined"
                    :reset-key="`opensubtitles:${openSubtitlesConfiguredAuthMode}:apiKey`"
                    autocomplete="off"
                    :placeholder="openSubtitlesConfiguredAuthMode === 'apiKey' ? '留空表示保留当前 API Key' : '粘贴 OpenSubtitles.com API Key'"
                    @reveal-error="showSubtitleRevealError"
                  />
                </label>
                <div v-else>
                  <span class="block text-xs font-semibold text-white/72">OpenSubtitles 账号登录</span>
                  <span class="mt-1 block text-xs leading-5 text-white/38">优先通过 OpenSubtitles.org 旧账号接口登录；现代邮箱账号不兼容时自动使用免 API Key 兼容搜索。</span>
                  <div class="mt-3 grid gap-3 md:grid-cols-2">
                    <label>
                      <span class="text-xs font-semibold text-white/42">账号</span>
                      <input
                        v-model="subtitleForm.username"
                        class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                        type="text"
                        autocomplete="username"
                        :placeholder="openSubtitlesConfiguredAuthMode === 'account' ? '留空表示保留当前账号' : 'OpenSubtitles 用户名'"
                      >
                    </label>
                    <label>
                      <span class="text-xs font-semibold text-white/42">密码</span>
                      <SecretInput
                        v-model="subtitleForm.password"
                        class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                        :configured="openSubtitlesConfiguredAuthMode === 'account'"
                        :load-secret="openSubtitlesConfiguredAuthMode === 'account' ? () => loadOpenSubtitlesCredentialField('password') : undefined"
                        :reset-key="`opensubtitles:${openSubtitlesConfiguredAuthMode}:password`"
                        autocomplete="current-password"
                        :placeholder="openSubtitlesConfiguredAuthMode === 'account' ? '留空表示保留当前密码' : 'OpenSubtitles 密码'"
                        @reveal-error="showSubtitleRevealError"
                      />
                    </label>
                  </div>
                </div>
              </div>
            </div>

            <label class="flex items-start justify-between gap-4 rounded-2xl bg-black/16 p-4">
              <span>
                <span class="block text-sm font-semibold text-white">射手网</span>
                <span class="mt-1 block text-xs leading-5 text-white/42">HTTPS 内容哈希匹配。本地文件直接读取，远程媒体通过受限 Range 片段在本机计算，无需账号。</span>
              </span>
              <input v-model="subtitleForm.shooterEnabled" type="checkbox" class="mt-1 h-5 w-5 accent-primary">
            </label>

            <label class="flex items-start justify-between gap-4 rounded-2xl border border-amber-300/12 bg-amber-300/6 p-4 lg:col-span-2">
              <span>
                <span class="block text-sm font-semibold text-white">迅雷字幕（实验性）</span>
                <span class="mt-1 block text-xs leading-5 text-amber-100/55">通过固定 HTTPS 接口按媒体名或自定义关键词搜索；本地文件及支持 Range 的远程视频会额外计算 CID，用于标记和优先展示精确匹配结果。</span>
              </span>
              <input v-model="subtitleForm.xunleiEnabled" type="checkbox" class="mt-1 h-5 w-5 accent-primary">
            </label>
          </div>

          <div class="mt-5 flex flex-wrap gap-3">
            <button
              type="button"
              class="rounded-2xl bg-primary/80 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary disabled:cursor-wait disabled:opacity-55"
              :disabled="isSavingSubtitleSettings"
              @click="savePlaybackSubtitleSettings"
            >
              {{ isSavingSubtitleSettings ? '保存中…' : '保存播放与字幕设置' }}
            </button>
            <button
              type="button"
              class="rounded-2xl bg-white/8 px-4 py-2 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14 disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="isSavingSubtitleSettings || !openSubtitlesConfigured"
              @click="clearOpenSubtitlesCredential"
            >
              清除 OpenSubtitles 登录
            </button>
          </div>
        </div>

        <div class="glass-panel rounded-[1.5rem] p-6">
          <div class="flex flex-wrap items-center justify-between gap-5">
            <div>
              <p class="text-xs uppercase tracking-[0.2em] text-white/36">
                Playback Cache
              </p>
              <h3 class="mt-2 text-lg font-bold text-white">
                清除播放缓存
              </h3>
              <p class="mt-2 max-w-3xl text-sm leading-6 text-white/48">
                清除海报图片缓存、已下载字幕缓存，以及每个视频单独保存的字幕、音轨、字幕偏移、倍速和画面设置。不会删除数据源、登录凭据、播放记录或全局软件设置。
              </p>
            </div>
            <button
              type="button"
              class="rounded-2xl border border-red-300/20 bg-red-400/10 px-4 py-2 text-sm font-semibold text-red-100 transition-colors hover:bg-red-400/16 disabled:cursor-wait disabled:opacity-55"
              :disabled="isClearingPlayerCache"
              @click="clearPlaybackCache"
            >
              {{ isClearingPlayerCache ? '清除中…' : '清除播放缓存' }}
            </button>
          </div>
        </div>
      </section>

      <section v-else-if="mode === 'manage'" class="glass-panel rounded-[1.75rem] p-6">
        <div class="mb-6 flex flex-wrap items-center justify-between gap-4">
          <div>
            <p class="text-xs uppercase tracking-[0.24em] text-white/34">
              Data Sources
            </p>
            <h2 class="mt-1 text-2xl font-bold text-white">
              管理媒体库
            </h2>
            <p class="mt-2 text-sm text-white/42">
              连接 OhMyCine Server、Emby 或 Jellyfin。停用后保留配置。
            </p>
          </div>
          <button class="rounded-2xl bg-primary/80 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-primary" @click="goAdd">
            添加媒体库
          </button>
        </div>

        <div v-if="configuredSources.length" class="space-y-3">
          <article v-for="source in configuredSources" :key="source.id" class="rounded-2xl border border-white/10 bg-white/5 p-4">
            <div class="flex flex-wrap items-center justify-between gap-4">
              <div class="min-w-0">
                <div class="flex items-center gap-3">
                  <span class="flex h-10 w-10 items-center justify-center rounded-2xl bg-primary/16 text-sm font-bold text-primary">{{ source.type[0].toUpperCase() }}</span>
                  <div class="min-w-0">
                    <p class="truncate text-sm font-semibold text-white">
                      {{ source.displayName ?? source.name }}
                    </p>
                    <p class="mt-1 truncate text-xs text-white/40">
                      {{ source.url }}
                    </p>
                  </div>
                </div>
                <p class="mt-3 text-xs text-white/34">
                  {{ sourceStatusLine(source) }}
                </p>
              </div>
              <div class="flex flex-wrap gap-2">
                <button class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/72 transition-colors hover:bg-white/14" @click="toggleSource(source)">
                  {{ source.enabled === false ? '启用' : '停用' }}
                </button>
                <button class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/72 transition-colors hover:bg-white/14" @click="editSource(source)">
                  编辑
                </button>
                <button
                  class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/72 transition-colors hover:bg-white/14 disabled:cursor-not-allowed disabled:opacity-35"
                  :disabled="source.enabled === false"
                  @click="source.enabled === false ? undefined : router.push(`/source/${source.id}`)"
                >
                  浏览
                </button>
                <button
                  class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/72 transition-colors hover:bg-white/14 disabled:cursor-not-allowed disabled:opacity-35"
                  :disabled="clearingCacheSourceId === source.id"
                  @click="clearSourceCache(source)"
                >
                  {{ clearingCacheSourceId === source.id ? '清除中…' : '清除缓存' }}
                </button>
                <button class="rounded-xl bg-red-500/14 px-3 py-2 text-xs text-red-100 transition-colors hover:bg-red-500/24" @click="removeSource(source.id)">
                  移除
                </button>
              </div>
            </div>
            <p class="mt-4 border-t border-white/8 pt-4 text-xs leading-5 text-white/42">
              媒体库、元数据和图片由对应服务端提供。
            </p>
          </article>
        </div>
        <div v-else class="rounded-2xl border border-dashed border-white/12 p-10 text-center">
          <p class="text-base font-semibold text-white">
            还没有媒体库
          </p>
          <p class="mt-2 text-sm leading-6 text-white/42">
            添加 OhMyCine Server、Emby 或 Jellyfin 后，即可浏览和播放。
          </p>
          <button class="mt-5 rounded-2xl bg-primary/80 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-primary" @click="goAdd">
            添加媒体库
          </button>
        </div>
      </section>

      <section v-else class="glass-panel rounded-[1.75rem] p-6">
        <div class="mb-6 flex items-start justify-between gap-4">
          <div>
            <p class="text-xs uppercase tracking-[0.24em] text-white/34">
              {{ isEditing ? 'Edit Source' : 'Add Source' }}
            </p>
            <h2 class="mt-1 text-2xl font-bold text-white">
              {{ isEditing ? '编辑媒体库' : '添加媒体库' }}
            </h2>
            <p class="mt-2 text-sm leading-6 text-white/42">
              填写服务器连接信息，保存前会先测试登录。
            </p>
          </div>
          <button class="rounded-2xl bg-white/8 px-4 py-2 text-sm text-white/70 transition-colors hover:bg-white/14" @click="goManage()">
            返回管理
          </button>
        </div>

        <form class="space-y-5" @submit.prevent="saveSource">
          <div>
            <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">媒体库类型</span>
            <div class="mt-3 grid gap-3 md:grid-cols-3">
              <button
                v-for="option in sourceTypeOptions"
                :key="option.type"
                type="button"
                class="flex min-h-24 items-center gap-4 rounded-2xl border p-4 text-left transition-colors disabled:cursor-not-allowed"
                :class="form.type === option.type ? 'border-primary/60 bg-primary/14 text-white shadow-lg shadow-primary/10' : 'border-white/10 bg-white/5 text-white/70 hover:border-white/20 hover:bg-white/8'"
                :disabled="isEditing"
                :aria-pressed="form.type === option.type"
                @click="selectSourceType(option.type)"
              >
                <span class="flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl bg-primary/22 text-sm font-bold text-primary">{{ option.shortLabel }}</span>
                <span class="min-w-0">
                  <span class="block text-sm font-semibold">{{ option.label }}</span>
                  <span class="mt-1 block text-xs leading-5 text-white/42">{{ option.description }}</span>
                </span>
              </button>
            </div>
          </div>

          <label class="block">
            <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">显示名称</span>
            <input
              v-model="form.displayName"
              class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
              :placeholder="selectedProvider.defaultName"
              autocomplete="off"
            >
          </label>
          <label class="block">
            <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">服务器 URL</span>
            <input
              v-model="form.url"
              class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
              :placeholder="selectedProvider.urlPlaceholder"
              autocomplete="off"
            >
          </label>
          <label class="block">
            <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">账号 / 用户名</span>
            <input
              v-model="form.username"
              class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
              :placeholder="selectedProvider.usernamePlaceholder"
              autocomplete="username"
            >
          </label>
          <label class="block">
            <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">密码</span>
            <SecretInput
              v-model="form.password"
              class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
              :configured="sourceCredentialConfigured && form.type !== 'server'"
              :load-secret="sourceCredentialLoader()"
              :reset-key="`${form.id ?? 'new'}:${form.type}:password`"
              :placeholder="isEditing ? '留空则不重新登录' : '输入登录密码'"
              autocomplete="current-password"
              @reveal-error="showSourceRevealError"
            />
          </label>

          <div
            v-if="feedback"
            class="rounded-2xl border px-4 py-3 text-sm"
            :class="{
              'border-emerald-400/20 bg-emerald-400/10 text-emerald-100': feedback.type === 'success',
              'border-red-400/20 bg-red-400/10 text-red-100': feedback.type === 'error',
              'border-white/12 bg-white/6 text-white/58': feedback.type === 'info',
            }"
          >
            {{ feedback.message }}
          </div>
          <div class="flex justify-end gap-3 border-t border-white/8 pt-5">
            <button type="button" class="rounded-2xl bg-white/8 px-5 py-3 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14" @click="goManage()">
              取消
            </button>
            <button class="rounded-2xl bg-primary/80 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-primary disabled:cursor-not-allowed disabled:opacity-45" :disabled="isSaving">
              {{ isSaving ? '登录测试中…' : isEditing ? '保存' : '添加' }}
            </button>
          </div>
        </form>
      </section>
    </div>
  </div>
</template>

<style scoped>
.settings-view {
  background: var(--color-bg);
}

@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .settings-view {
    padding-top: max(4.4rem, calc(env(safe-area-inset-top) + 3.4rem));
  }

  .settings-view > div {
    row-gap: 1.35rem;
  }

  .settings-view header h1 {
    font-size: 1.8rem;
  }

  .settings-view header p:last-child {
    margin-top: 0.55rem;
    line-height: 1.55;
  }

  .settings-overview-grid {
    gap: 0.55rem;
  }

  .settings-entry-card {
    display: grid;
    min-height: 0;
    grid-template-columns: 3rem minmax(0, 1fr) auto;
    grid-template-rows: auto auto;
    gap: 0.2rem 0.75rem;
    align-items: center;
    border-radius: 8px;
    padding: 0.75rem;
  }

  .settings-entry-card > span:first-child {
    display: contents;
  }

  .settings-entry-card > span:first-child > span:first-child {
    grid-row: 1 / 3;
    width: 3rem;
    min-width: 3rem;
    height: 3rem;
    margin: 0;
    border-radius: 8px;
    padding: 0.35rem;
    font-size: 0.66rem;
  }

  .settings-entry-card > span:first-child > span:last-child {
    grid-column: 3;
    grid-row: 1 / 3;
    padding: 0.3rem 0.5rem;
    font-size: 0.6rem;
  }

  .settings-entry-card > span:nth-child(2) {
    grid-column: 2;
    font-size: 0.9rem;
  }

  .settings-entry-card > span:nth-child(3) {
    grid-column: 2;
    margin: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 0.68rem;
    line-height: 1.3;
  }

  .settings-entry-card > span:last-child {
    display: none;
  }

  .settings-view :deep(.glass-panel) {
    border-radius: 8px;
  }

  .settings-view :deep(input),
  .settings-view :deep(select),
  .settings-view :deep(textarea) {
    font-size: 16px;
  }
}
</style>
