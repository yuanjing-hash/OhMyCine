<script setup lang="ts">
import type { PlayerStorageInfo } from '@/services/appSettings'
import type { OpenSubtitlesAuthMode, OpenSubtitlesCredentialValue } from '@/services/datasource/credentialStore'
import type { DataSourceConfig, DataSourceType, MediaItem, MediaLibrary } from '@/services/datasource/types'
import type { NavigationShortcutBindings, NavigationShortcutTarget } from '@/services/navigationShortcuts'
import type { PlayerCacheMode, PlayerDemuxerCacheSize, PlayerHardwareDecoder, PlayerVideoOutput, PlayerVideoSync } from '@/services/playerInteractionSettings'
import type { PlayerShortcutBindings, PlayerShortcutTarget } from '@/services/playerShortcuts'
import type { ScrapeCategoryRule, ScrapeMediaType, ScrapeNamedOption, ScrapeRuleGroup, ScrapeValueCondition, TmdbGenreOption } from '@/services/scraper/classificationRules'
import type { RawSourceScanKind } from '@/services/scraper/rawSourceScanSchedule'
import type { TmdbAuthType } from '@/services/scraper/tmdb'
import type { SubtitleLanguage } from '@/services/subtitle'
import type { UpdateChannel } from '@/services/updater'
import { confirm as confirmDialog, open } from '@tauri-apps/plugin-dialog'
import { computed, onMounted, reactive, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { flushAppSettings, getPlayerStorageInfo } from '@/services/appSettings'
import { AlistDataSource, createAuthenticatedAlistSetupSource, loginAlistAndCreateConfig, normalizeAlistRootPath, readAlistRootPath } from '@/services/datasource/alist'
import { CloudDrive2DataSource, createAuthenticatedCloudDrive2SetupSource, normalizeCloudDrive2RootPath, readCloudDrive2RootPath, saveCloudDrive2TokenAndCreateConfig } from '@/services/datasource/clouddrive2'
import { readRawCredentialBackup, removeCredential, saveRawCredentialBackup } from '@/services/datasource/credentialStore'
import { loginEmbyAndCreateConfig } from '@/services/datasource/emby'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { createLocalFileDataSourceConfig, normalizeLocalRootPath, readLocalRootPath, validateLocalFileDataSourceConfig } from '@/services/datasource/local'
import { createAuthenticatedWebDavSetupSource, loginWebDavAndCreateConfig, normalizeWebDavRootPath, readWebDavRootPath, WebDavDataSource } from '@/services/datasource/webdav'
import {
  loadNavigationShortcutBindings,
  resetNavigationShortcutBindings,
  saveNavigationShortcutBindings,
  shortcutDisplayLabel,
  shortcutFromKeyboardEvent,
  validateUniqueNavigationShortcuts,
} from '@/services/navigationShortcuts'
import { loadPlayerInteractionSettings, normalizeLongPressPlaybackSpeed, savePlayerInteractionSettings } from '@/services/playerInteractionSettings'
import {
  loadPlayerShortcutBindings,
  resetPlayerShortcutBindings,
  savePlayerShortcutBindings,
  validateUniquePlayerShortcuts,
} from '@/services/playerShortcuts'
import {
  createEmptyScrapeCategoryRule,
  loadScrapeClassificationRules,
  normalizeScrapeFallbackCategoryName,
  resetScrapeClassificationRules,
  saveScrapeClassificationRules,
  SCRAPE_COUNTRY_OPTIONS,
  SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME,
  SCRAPE_LANGUAGE_OPTIONS,
  TMDB_MOVIE_GENRES,
  TMDB_TV_GENRES,
} from '@/services/scraper/classificationRules'
import { intervalMinutesToMs, intervalMsToMinutes, readRawSourceScanScheduleConfig, updateRawSourceScanScheduleExtra } from '@/services/scraper/rawSourceScanSchedule'
import {
  clearConfiguredTmdbCredential,
  loadTmdbLocalSettings,
  readStoredTmdbCredential,
  saveConfiguredTmdbCredential,
  saveTmdbLocalSettings,
} from '@/services/scraper/tmdb'
import {
  clearOpenSubtitlesCredentials,
  loadSubtitleSearchSettings,
  readOpenSubtitlesCredentials,
  saveOpenSubtitlesCredentials,
  saveSubtitleSearchSettings,
  testOpenSubtitlesLogin,
} from '@/services/subtitle'
import { useDataSourceStore } from '@/stores/datasource'
import { useUpdaterStore } from '@/stores/updater'

type LoginDataSourceType = Extract<DataSourceType, 'emby' | 'alist' | 'clouddrive2' | 'webdav'>
type EditableDataSourceType = LoginDataSourceType | 'local'
type EditableDataSourceConfig = DataSourceConfig & { type: EditableDataSourceType }
type SettingsMode = 'overview' | 'manage' | 'add' | 'edit' | 'scraping' | 'playback' | 'shortcuts' | 'updates' | 'diagnostics'
type SettingsEntryId = 'datasources' | 'scraping' | 'playback' | 'shortcuts' | 'appearance' | 'ai' | 'updates' | 'diagnostics'
type SettingsQueryState = Partial<Record<'section' | 'action' | 'id', string>>
type ConditionValueState = 'none' | 'include' | 'exclude'

interface DataSourceFormState {
  id: string | null
  type: EditableDataSourceType
  displayName: string
  url: string
  username: string
  password: string
  apiToken: string
  rootPath: string
}

interface TmdbFormState {
  authType: TmdbAuthType
  credential: string
  language: string
  region: string
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

const sourceTypeOptions: Array<{
  type: EditableDataSourceType
  label: string
  shortLabel: string
  description: string
  defaultName: string
  urlPlaceholder: string
  usernamePlaceholder: string
}> = [
  {
    type: 'emby',
    label: 'Emby',
    shortLabel: 'E',
    description: '媒体服务器账号登录',
    defaultName: 'Emby',
    urlPlaceholder: 'http://emby.example.test:8096',
    usernamePlaceholder: 'Emby 登录账号',
  },
  {
    type: 'alist',
    label: 'OpenList/Alist',
    shortLabel: 'A',
    description: 'OpenList/Alist API 账号登录',
    defaultName: 'OpenList/Alist',
    urlPlaceholder: 'http://openlist.example.test:5244',
    usernamePlaceholder: 'OpenList/Alist 登录账号',
  },
  {
    type: 'clouddrive2',
    label: 'CloudDrive2',
    shortLabel: 'C',
    description: 'CloudDrive2 原生 gRPC API Token',
    defaultName: 'CloudDrive2',
    urlPlaceholder: 'http://clouddrive2.example.test:19798',
    usernamePlaceholder: '',
  },
  {
    type: 'webdav',
    label: 'WebDAV',
    shortLabel: 'W',
    description: '通用 WebDAV 只读数据源',
    defaultName: 'WebDAV',
    urlPlaceholder: 'https://dav.example.test/media',
    usernamePlaceholder: 'WebDAV 用户名',
  },
  {
    type: 'local',
    label: '本地文件夹',
    shortLabel: 'L',
    description: '只读扫描本机媒体目录',
    defaultName: '本地媒体库',
    urlPlaceholder: '',
    usernamePlaceholder: '',
  },
]

const tmdbAuthTypeOptions: Array<{ value: TmdbAuthType, label: string, description: string }> = [
  {
    value: 'readAccessToken',
    label: 'API 读访问令牌 / Read Access Token',
    description: '推荐填写。粘贴 TMDB 设置页生成的 v4 只读访问令牌。',
  },
  {
    value: 'apiKey',
    label: 'API Key',
    description: '兼容旧版 v3 或短 key；已有 API Key 时可继续使用。',
  },
]

const tmdbLanguageOptions = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'zh-TW', label: '繁体中文' },
  { value: 'en-US', label: 'English' },
  { value: 'ja-JP', label: '日本語' },
  { value: 'ko-KR', label: '한국어' },
]

const tmdbRegionOptions = [
  { value: 'CN', label: '中国内地' },
  { value: 'TW', label: '中国台湾' },
  { value: 'HK', label: '中国香港' },
  { value: 'US', label: '美国' },
  { value: 'JP', label: '日本' },
  { value: 'KR', label: '韩国' },
]

const subtitleLanguageOptions: Array<{ value: SubtitleLanguage, label: string }> = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'zh-TW', label: '繁体中文' },
  { value: 'en', label: 'English' },
  { value: 'ja', label: '日本語' },
  { value: 'ko', label: '한국어' },
]

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
  apiToken: '',
  rootPath: '/',
})
const mode = ref<SettingsMode>('overview')
const isSaving = ref(false)
const clearingCacheSourceId = ref<string | null>(null)
const feedback = ref<{ type: 'success' | 'error' | 'info', message: string } | null>(null)
const lastFetchedLibraries = ref<MediaLibrary[]>([])
const alistBrowserSource = shallowRef<AlistDataSource | CloudDrive2DataSource | WebDavDataSource | null>(null)
const alistBrowserPath = ref('/')
const alistBrowserDirectories = ref<MediaItem[]>([])
const alistBrowserLoading = ref(false)
const alistBrowserError = ref<string | null>(null)
const scrapeRules = ref(loadScrapeClassificationRules())
const scrapeRulesDirty = ref(false)
const scrapeFeedback = ref<{ type: 'success' | 'error' | 'info', message: string } | null>(null)
const tmdbSettings = loadTmdbLocalSettings()
const tmdbForm = reactive<TmdbFormState>({
  authType: tmdbSettings.authType,
  credential: '',
  language: tmdbSettings.language,
  region: tmdbSettings.region,
})
const tmdbCredentialConfigured = ref(false)
const tmdbStoredAuthType = ref<TmdbAuthType | null>(null)
const isSavingTmdbSettings = ref(false)
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

const configuredSources = computed(() => store.orderedConfigs)
const isEditing = computed(() => mode.value === 'edit')
const selectedProvider = computed(() => sourceTypeOptions.find(option => option.type === form.type) ?? sourceTypeOptions[0])
const isAlistForm = computed(() => form.type === 'alist')
const isCloudDrive2Form = computed(() => form.type === 'clouddrive2')
const isWebDavForm = computed(() => form.type === 'webdav')
const isLocalForm = computed(() => form.type === 'local')
const isRemoteRootBrowserForm = computed(() => isAlistForm.value || isCloudDrive2Form.value || isWebDavForm.value)
const isAccountPasswordForm = computed(() => !isLocalForm.value && !isCloudDrive2Form.value)
const selectedRootPathLabel = computed(() => isLocalForm.value
  ? localRootPathLabel(form.rootPath)
  : normalizeRemoteRootPath(form.rootPath))
const alistParentPath = computed(() => parentDirectoryPath(alistBrowserPath.value))
const canBrowseAlistParent = computed(() => alistBrowserPath.value !== '/')
const activeSourceCount = computed(() => configuredSources.value.filter(source => source.enabled !== false).length)
const dataSourceEntryMeta = computed(() => {
  if (configuredSources.value.length === 0)
    return '尚未配置'
  return `${activeSourceCount.value}/${configuredSources.value.length} 个启用`
})
const scrapingEntryMeta = computed(() => {
  if (scrapeRulesDirty.value)
    return '规则未保存'
  if (tmdbCredentialConfigured.value)
    return 'TMDB 已配置'
  return tmdbStoredAuthType.value ? '类型待确认' : 'TMDB 可选'
})
const tmdbCredentialInputLabel = computed(() =>
  tmdbForm.authType === 'readAccessToken' ? 'API 读访问令牌 / Read Access Token' : 'API Key',
)
const tmdbCredentialPlaceholder = computed(() =>
  tmdbCredentialConfigured.value
    ? `留空表示保留当前 ${tmdbCredentialInputLabel.value}`
    : `可选：粘贴 TMDB ${tmdbCredentialInputLabel.value}`,
)
const tmdbCredentialStatusLabel = computed(() => {
  if (tmdbCredentialConfigured.value)
    return '已配置'
  if (tmdbStoredAuthType.value)
    return `已保存 ${tmdbAuthTypeLabel(tmdbStoredAuthType.value)}，当前类型未配置`
  return '未配置，可继续扫描'
})
const storageModeLabel = computed(() => storageInfo.value?.mode === 'portable' ? '便携模式' : '标准模式')
const storageEntryMeta = computed(() => storageInfo.value ? storageModeLabel.value : '浏览器模式')
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
    case 'portableFileKey':
      return '便携文件密钥'
    case 'localFileKey':
      return '本机文件密钥'
    default:
      return '当前会话内存'
  }
})
const storageModeDescription = computed(() => {
  if (!storageInfo.value)
    return '当前是浏览器开发模式，桌面版存储信息不可用。'
  if (storageInfo.value.mode === 'portable')
    return '配置、播放记录、日志和缓存跟随当前程序目录移动。'
  if (storageInfo.value.credentialProtection === 'windowsDpapi')
    return '配置和播放记录保存在 Windows 用户目录，升级程序不会清除数据。'
  return '应用数据库使用当前系统的用户数据目录，升级或替换程序文件不会影响配置和播放历史。'
})
const pageDescription = computed(() => mode.value === 'overview'
  ? '管理数据源、播放、字幕、刮削、更新和本地存储。'
  : mode.value === 'scraping'
    ? '设置原始文件媒体库的元数据匹配与分类规则。'
    : mode.value === 'playback'
      ? '设置字幕搜索语言和字幕提供器。'
      : mode.value === 'shortcuts'
        ? '配置播放器控制和页面导航快捷键。'
        : mode.value === 'updates'
          ? '选择更新渠道并检查新版本。'
          : mode.value === 'diagnostics'
            ? '查看当前运行模式和数据目录。'
            : '添加、编辑和管理媒体数据源。')
const movieRuleGroup = computed(() => getScrapeRuleGroup('movie'))
const tvRuleGroup = computed(() => getScrapeRuleGroup('tv'))
const scrapeRuleGroups = computed(() => [movieRuleGroup.value, tvRuleGroup.value])
const settingsEntries = computed<SettingsEntry[]>(() => [
  {
    id: 'datasources',
    label: 'DS',
    title: '管理数据源',
    description: '添加和管理 Emby、OpenList/Alist、CloudDrive2、WebDAV 与本地文件夹。',
    meta: dataSourceEntryMeta.value,
    actionLabel: '打开',
    disabled: false,
  },
  {
    id: 'scraping',
    label: 'Meta',
    title: '刮削与分类',
    description: '设置海报、简介等元数据匹配和媒体分类规则。',
    meta: scrapingEntryMeta.value,
    actionLabel: '打开',
    disabled: false,
  },
  {
    id: 'playback',
    label: 'Play',
    title: '播放与字幕',
    description: '配置 OpenSubtitles、射手网和迅雷字幕搜索。',
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
    id: 'ai',
    label: 'AI',
    title: 'AI 推荐',
    description: '本地库索引、模型提供商和隐私边界设置将在推荐功能稳定后接入。',
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
  void refreshTmdbCredentialState()
  void refreshOpenSubtitlesCredentialState()
  void refreshStorageInfo()
  void updaterStore.initialize().then(syncUpdaterForm)
  syncModeFromRoute()
})

watch(() => route.query, () => {
  syncModeFromRoute()
})

watch(() => form.type, (type) => {
  if (!isEditing.value)
    form.displayName = defaultDisplayName(type)
  if (type === 'local') {
    form.url = ''
    form.username = ''
    form.password = ''
    form.apiToken = ''
    form.rootPath = ''
  }
  else if (type === 'emby') {
    form.rootPath = '/'
  }
  else if (isRootSelectableRemoteSourceType(type) && !form.rootPath) {
    form.rootPath = '/'
  }
  if (type === 'clouddrive2') {
    form.username = ''
    form.password = ''
  }
  else {
    form.apiToken = ''
  }
  resetAlistBrowser()
})

watch(() => [form.url, form.username, form.password, form.apiToken] as const, () => {
  if (isRootSelectableRemoteSourceType(form.type))
    resetAlistBrowser()
})

watch(() => tmdbForm.authType, () => {
  void refreshTmdbCredentialState()
})

function syncModeFromRoute() {
  const section = routeQueryValue('section')
  if (section === 'diagnostics') {
    replaceSettingsQuery({ section: 'diagnostics' })
    mode.value = 'diagnostics'
    feedback.value = null
    void refreshStorageInfo()
    return
  }

  if (section === 'scraping') {
    replaceSettingsQuery({ section: 'scraping' })
    if (mode.value !== 'scraping') {
      lastFetchedLibraries.value = []
      resetAlistBrowser()
    }
    mode.value = 'scraping'
    feedback.value = null
    void refreshTmdbCredentialState()
    return
  }

  if (section === 'playback') {
    replaceSettingsQuery({ section: 'playback' })
    mode.value = 'playback'
    feedback.value = null
    subtitleFeedback.value = null
    void refreshOpenSubtitlesCredentialState()
    return
  }

  if (section === 'shortcuts') {
    replaceSettingsQuery({ section: 'shortcuts' })
    mode.value = 'shortcuts'
    feedback.value = null
    shortcutFeedback.value = null
    syncShortcutForms()
    return
  }

  if (section === 'updates') {
    replaceSettingsQuery({ section: 'updates' })
    mode.value = 'updates'
    feedback.value = null
    updateFeedback.value = null
    syncUpdaterForm()
    return
  }

  if (section !== 'datasources') {
    replaceSettingsQuery()
    if (mode.value !== 'overview') {
      lastFetchedLibraries.value = []
      resetAlistBrowser()
    }
    mode.value = 'overview'
    return
  }

  const action = routeQueryValue('action')
  if (action === 'add') {
    replaceSettingsQuery({ section: 'datasources', action: 'add' })
    if (mode.value !== 'add')
      resetForm()
    mode.value = 'add'
    return
  }

  if (action === 'edit') {
    const id = routeQueryValue('id')
    const source = id ? store.configs.find(config => config.id === id) : null
    if (source && isEditableDataSourceConfig(source)) {
      replaceSettingsQuery({ section: 'datasources', action: 'edit', id: source.id })
      if (mode.value !== 'edit' || form.id !== source.id)
        populateEditForm(source)
      return
    }
    replaceSettingsQuery({ section: 'datasources' })
    mode.value = 'manage'
    if (id) {
      feedback.value = {
        type: 'error',
        message: '未找到可编辑的数据源，请从列表中重新选择。',
      }
    }
    return
  }

  replaceSettingsQuery({ section: 'datasources' })
  if (mode.value !== 'manage') {
    lastFetchedLibraries.value = []
    resetAlistBrowser()
  }
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
  else if (entry.id === 'scraping')
    goScrapingSettings()
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
}

function goOverview() {
  mode.value = 'overview'
  feedback.value = null
  lastFetchedLibraries.value = []
  resetAlistBrowser()
  void router.replace({ name: 'settings' })
}

function goDataSources() {
  mode.value = 'manage'
  feedback.value = null
  lastFetchedLibraries.value = []
  resetAlistBrowser()
  void router.push({ name: 'settings', query: { section: 'datasources' } })
}

function goScrapingSettings() {
  mode.value = 'scraping'
  feedback.value = null
  scrapeFeedback.value = null
  lastFetchedLibraries.value = []
  resetAlistBrowser()
  void router.push({ name: 'settings', query: { section: 'scraping' } })
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
      longPressPlaybackSpeed: subtitleForm.longPressPlaybackSpeed,
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
      message: `播放缓存已清除：移除 ${result.playbackPreferencesDeleted} 条单视频设置和 ${result.rawScanCacheEntriesDeleted} 条媒体扫描缓存。数据源、登录凭据、播放记录和全局设置均已保留。`,
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
  lastFetchedLibraries.value = []
  resetAlistBrowser()
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
  form.apiToken = ''
  form.rootPath = '/'
  feedback.value = null
  lastFetchedLibraries.value = []
  resetAlistBrowser()
}

function editSource(config: DataSourceConfig) {
  if (!isEditableDataSourceConfig(config)) {
    feedback.value = {
      type: 'error',
      message: `${sourceTypeLabel(config.type)} 暂不支持在当前设置页编辑。`,
    }
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
  form.apiToken = ''
  if (config.type === 'alist')
    form.rootPath = readAlistRootPath(config)
  else if (config.type === 'clouddrive2')
    form.rootPath = readCloudDrive2RootPath(config)
  else if (config.type === 'webdav')
    form.rootPath = readWebDavRootPath(config)
  else if (config.type === 'local')
    form.rootPath = readLocalRootPath(config)
  else
    form.rootPath = '/'
  feedback.value = {
    type: 'info',
    message: config.type === 'local'
      ? '可修改显示名称或重新选择本地根目录；本地文件源不会保存账号、密码或 token。'
      : config.type === 'clouddrive2'
        ? '可修改显示名称、根目录与启用状态；API Token 留空表示保留，修改服务地址时必须重新输入 Token。'
        : `可修改显示名称、根目录与启用状态；如 ${sourceTypeLabel(config.type)} URL 或账号变化，请输入账号密码重新登录。`,
  }
  lastFetchedLibraries.value = []
  resetAlistBrowser()
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
    feedback.value = {
      type: 'error',
      message: toSafeErrorMessage(error, '清除缓存失败，请稍后重试。'),
    }
  }
  finally {
    clearingCacheSourceId.value = null
  }
}

async function saveSource() {
  isSaving.value = true
  feedback.value = null
  lastFetchedLibraries.value = []
  try {
    if (mode.value === 'edit' && form.id) {
      await saveEditedSource(form.id)
      return
    }

    const id = `${form.type}-${Date.now()}`
    if (form.type === 'local') {
      const result = await createAndValidateLocalConfig({
        id,
        displayName: form.displayName,
        rootPath: form.rootPath,
        order: store.configs.length,
      })
      await store.replaceConfig(result.config)
      lastFetchedLibraries.value = result.libraries
      resetForm()
      feedback.value = { type: 'success', message: `本地文件夹已验证，已添加到左侧侧边栏。` }
      goManage({ preserveFeedback: true })
      return
    }

    const result = await loginAndCreateConfig(form.type, {
      id,
      url: form.url,
      displayName: form.displayName,
      username: form.username,
      password: form.password,
      apiToken: form.apiToken,
      rootPath: isRootSelectableRemoteSourceType(form.type) ? selectedRootPathLabel.value : undefined,
      order: store.configs.length,
    })
    try {
      await store.replaceConfig(result.config)
    }
    catch (error) {
      await restoreCredentialForConfig(result.config, null).catch(() => undefined)
      throw error
    }
    const libraryCount = result.libraries.length
    const label = sourceTypeLabel(form.type)
    resetForm()
    feedback.value = { type: 'success', message: `${label} 连接测试成功，已验证 ${libraryCount} 个入口。新数据源已添加到左侧侧边栏。` }
    goManage({ preserveFeedback: true })
  }
  catch (error) {
    feedback.value = {
      type: 'error',
      message: toSafeErrorMessage(error, form.type === 'local'
        ? '添加本地文件夹失败，请确认目录存在且有读取权限。'
        : form.type === 'clouddrive2'
          ? '添加 CloudDrive2 失败，请检查 gRPC 服务地址、API Token 权限和服务状态。'
          : `添加数据源失败，请检查 ${sourceTypeLabel(form.type)} URL、账号和密码。`),
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
    throw new Error(`${sourceTypeLabel(existing.type)} 暂不支持在当前设置页编辑。`)

  if (existing.type === 'local') {
    const result = await createAndValidateLocalConfig({
      id,
      displayName: form.displayName,
      rootPath: form.rootPath,
      order: existing.order,
    })
    await store.replaceConfig({ ...result.config, enabled: existing.enabled !== false })
    lastFetchedLibraries.value = result.libraries
    feedback.value = { type: 'success', message: '本地文件夹数据源已更新。' }
    goManage({ preserveFeedback: true })
    return
  }

  const username = form.username.trim()
  const nextUrl = form.url.trim()
  const nextDisplayName = form.displayName.trim() || existing.displayName || existing.name
  const nextRootPath = isRootSelectableRemoteSourceType(existing.type) ? selectedRootPathLabel.value : undefined
  const label = sourceTypeLabel(existing.type)
  const shouldRelogin = shouldReloginSource(existing, nextUrl, username, form.password, form.apiToken)
  if (shouldRelogin && existing.type === 'clouddrive2' && !form.apiToken.trim())
    throw new Error('更新 CloudDrive2 服务地址或 Token 时必须填写 API Token。')
  if (shouldRelogin && existing.type !== 'clouddrive2' && (!username || !form.password))
    throw new Error(`更新 ${label} URL 或重新登录时必须同时填写账号和密码。`)

  if (shouldRelogin) {
    const previousCredential = await readCredentialBackupForConfig(existing)
    const result = await loginAndCreateConfig(existing.type, {
      id,
      url: nextUrl,
      displayName: nextDisplayName,
      username,
      password: form.password,
      apiToken: form.apiToken,
      rootPath: nextRootPath,
      order: existing.order,
    })
    try {
      await store.replaceConfig({ ...result.config, enabled: existing.enabled !== false })
    }
    catch (error) {
      await restoreCredentialForConfig(result.config, previousCredential).catch(() => undefined)
      throw error
    }
    feedback.value = { type: 'success', message: `${label} 已重新连接，并验证 ${result.libraries.length} 个入口。` }
    form.password = ''
    form.apiToken = ''
    goManage({ preserveFeedback: true })
    return
  }

  const nextExtra = { ...(existing.extra ?? {}) }
  if (isRootSelectableRemoteSourceType(existing.type)) {
    const rootPathChanged = nextRootPath !== readRemoteRootPath(existing)
    const libraries = rootPathChanged
      ? await validateExistingRemoteRoot(existing, nextUrl, nextDisplayName, nextRootPath ?? '/')
      : null
    nextExtra.rootPath = nextRootPath ?? '/'
    if (libraries) {
      nextExtra.libraries = libraries.map(library => ({
        id: library.id,
        name: library.name,
        type: library.type,
      }))
    }
  }

  await store.updateConfig(id, {
    name: nextDisplayName,
    displayName: nextDisplayName,
    url: nextUrl,
    extra: nextExtra,
  })
  form.password = ''
  form.apiToken = ''
  feedback.value = { type: 'success', message: existing.type === 'clouddrive2'
    ? '数据源已更新。若 API Token 已撤销或权限变化，请再次编辑并输入新的 Token。'
    : '数据源已更新。若会话凭证已过期，请再次编辑并输入账号密码登录。' }
  goManage({ preserveFeedback: true })
}

function loginAndCreateConfig(type: LoginDataSourceType, input: {
  id: string
  url: string
  displayName: string
  username: string
  password: string
  apiToken: string
  rootPath?: string
  order: number
}): Promise<{ config: DataSourceConfig, libraries: MediaLibrary[] }> {
  if (type === 'alist')
    return loginAlistAndCreateConfig(input)
  if (type === 'clouddrive2')
    return saveCloudDrive2TokenAndCreateConfig(input)
  if (type === 'webdav')
    return loginWebDavAndCreateConfig(input)
  return loginEmbyAndCreateConfig(input)
}

async function createAndValidateLocalConfig(input: {
  id: string
  displayName: string
  rootPath: string
  order: number
}): Promise<{ config: DataSourceConfig, libraries: MediaLibrary[] }> {
  const config = createLocalFileDataSourceConfig(input)
  const libraries = await validateLocalFileDataSourceConfig(config)
  return {
    config: {
      ...config,
      extra: {
        ...(config.extra ?? {}),
        libraries: libraries.map(library => ({
          id: library.id,
          name: library.name,
          type: library.type,
        })),
      },
    },
    libraries,
  }
}

function isLoginDataSourceType(type: DataSourceType): type is LoginDataSourceType {
  return type === 'emby' || type === 'alist' || type === 'clouddrive2' || type === 'webdav'
}

function isEditableDataSourceType(type: DataSourceType): type is EditableDataSourceType {
  return isLoginDataSourceType(type) || type === 'local'
}

function isEditableDataSourceConfig(config: DataSourceConfig): config is EditableDataSourceConfig {
  return isEditableDataSourceType(config.type)
}

function isRootSelectableRemoteSourceType(type: DataSourceType): type is Extract<LoginDataSourceType, 'alist' | 'clouddrive2' | 'webdav'> {
  return type === 'alist' || type === 'clouddrive2' || type === 'webdav'
}

function sourceTypeLabel(type: DataSourceType): string {
  switch (type) {
    case 'emby':
      return 'Emby'
    case 'alist':
      return 'OpenList/Alist'
    case 'jellyfin':
      return 'Jellyfin'
    case 'clouddrive2':
      return 'CloudDrive2'
    case 'webdav':
      return 'WebDAV'
    case 'local':
      return '本地文件'
    case 'server':
      return 'OhMyCine Server'
    default:
      return type
  }
}

function defaultDisplayName(type: EditableDataSourceType): string {
  return sourceTypeOptions.find(option => option.type === type)?.defaultName ?? '数据源'
}

function sourceStatusLine(source: DataSourceConfig): string {
  const credentialState = source.type === 'local'
    ? '无需登录'
    : typeof source.extra?.credentialRef === 'string' ? '登录信息已保存' : '需要重新登录'
  const rootState = isRootSelectableRemoteSourceType(source.type)
    ? ` · 根目录：${readRemoteRootPath(source)}`
    : source.type === 'local'
      ? ` · 根目录：${readLocalRootPath(source)}`
      : ''
  return `状态：${source.enabled === false ? '已停用' : '已启用'} · 类型：${sourceTypeLabel(source.type)} · ${credentialState}${rootState}`
}

function isRawScanScheduleSource(source: DataSourceConfig): boolean {
  return source.type === 'alist' || source.type === 'clouddrive2' || source.type === 'webdav' || source.type === 'local'
}

function readRemoteRootPath(config: DataSourceConfig): string {
  if (config.type === 'clouddrive2')
    return readCloudDrive2RootPath(config)
  if (config.type === 'webdav')
    return readWebDavRootPath(config)
  return readAlistRootPath(config)
}

function normalizeRemoteRootPath(path: string | undefined): string {
  if (form.type === 'clouddrive2')
    return normalizeCloudDrive2RootPath(path)
  if (form.type === 'webdav')
    return normalizeWebDavRootPath(path)
  return normalizeAlistRootPath(path)
}

function rawScanScheduleEnabled(source: DataSourceConfig, scanKind: RawSourceScanKind): boolean {
  return readRawSourceScanScheduleConfig(source)[scanKind].enabled
}

function rawScanScheduleIntervalMinutes(source: DataSourceConfig, scanKind: RawSourceScanKind): number {
  return intervalMsToMinutes(readRawSourceScanScheduleConfig(source)[scanKind].intervalMs)
}

async function updateRawScanScheduleEnabled(source: DataSourceConfig, scanKind: RawSourceScanKind, enabled: boolean) {
  await updateRawScanSchedule(
    source,
    scanKind,
    { enabled },
    `已保存「${sourceDisplayName(source)}」${rawScanKindLabel(scanKind)}：${enabled ? '已启用' : '已停用'}。`,
  )
}

async function updateRawScanScheduleInterval(source: DataSourceConfig, scanKind: RawSourceScanKind, value: string) {
  const minutes = Number(value)
  if (!Number.isFinite(minutes) || minutes <= 0) {
    feedback.value = {
      type: 'error',
      message: `${rawScanKindLabel(scanKind)}间隔必须是大于 0 的分钟数。`,
    }
    return
  }
  const intervalMs = intervalMinutesToMs(minutes)
  await updateRawScanSchedule(
    source,
    scanKind,
    { intervalMs },
    `已保存「${sourceDisplayName(source)}」${rawScanKindLabel(scanKind)}间隔：${intervalMsToMinutes(intervalMs)} 分钟。`,
  )
}

async function updateRawScanSchedule(
  source: DataSourceConfig,
  scanKind: RawSourceScanKind,
  patch: Parameters<typeof updateRawSourceScanScheduleExtra>[2],
  successMessage: string,
) {
  feedback.value = null
  try {
    await store.updateConfig(source.id, {
      extra: updateRawSourceScanScheduleExtra(source.extra, scanKind, patch),
    })
    feedback.value = {
      type: 'success',
      message: successMessage,
    }
  }
  catch (error) {
    feedback.value = {
      type: 'error',
      message: toSafeErrorMessage(error, '扫描计划保存失败。'),
    }
  }
}

function sourceDisplayName(source: DataSourceConfig): string {
  return source.displayName ?? source.name
}

function rawScanKindLabel(scanKind: RawSourceScanKind): string {
  return scanKind === 'full' ? '全量扫描' : '增量扫描'
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

function selectSourceType(type: EditableDataSourceType) {
  if (isEditing.value)
    return

  form.type = type
  form.displayName = defaultDisplayName(type)
  feedback.value = null
  lastFetchedLibraries.value = []
}

async function chooseLocalRootPath() {
  if (form.type !== 'local')
    return

  feedback.value = null
  try {
    const selected = await open({
      multiple: false,
      directory: true,
    })

    if (typeof selected !== 'string')
      return

    form.rootPath = normalizeLocalRootPath(selected)
    if (!form.displayName.trim())
      form.displayName = localRootDisplayName(form.rootPath)
    feedback.value = {
      type: 'info',
      message: `已选择本地根目录：${form.rootPath}`,
    }
  }
  catch (error) {
    feedback.value = {
      type: 'error',
      message: toSafeErrorMessage(error, '选择本地文件夹失败。'),
    }
  }
}

async function loadAlistRootBrowser() {
  await loadAlistDirectory('/')
}

async function loadAlistDirectory(path: string) {
  if (!isRootSelectableRemoteSourceType(form.type))
    return

  alistBrowserLoading.value = true
  alistBrowserError.value = null
  try {
    const source = await ensureAlistBrowserSource()
    const nextPath = normalizeRemoteRootPath(path)
    const items = await source.list(nextPath)
    alistBrowserPath.value = nextPath
    alistBrowserDirectories.value = items
      .filter(item => item.type === 'folder')
      .sort((left, right) => left.name.localeCompare(right.name, 'zh-Hans-CN'))
  }
  catch (error) {
    alistBrowserDirectories.value = []
    alistBrowserError.value = toSafeErrorMessage(error, `${sourceTypeLabel(form.type)} 目录加载失败。`)
  }
  finally {
    alistBrowserLoading.value = false
  }
}

async function ensureAlistBrowserSource(): Promise<AlistDataSource | CloudDrive2DataSource | WebDavDataSource> {
  if (alistBrowserSource.value)
    return alistBrowserSource.value

  const sourceId = form.id ?? `${form.type}-setup-${Date.now()}`
  const displayName = form.displayName.trim() || defaultDisplayName(form.type)
  const existing = form.id ? store.configs.find(config => config.id === form.id) : null
  const username = form.username.trim()
  const shouldUseExistingCredential = existing?.type === form.type
    && isRootSelectableRemoteSourceType(existing.type)
    && !shouldReloginSource(existing, form.url, username, form.password, form.apiToken)

  if (shouldUseExistingCredential) {
    const source = existing.type === 'clouddrive2'
      ? new CloudDrive2DataSource()
      : existing.type === 'webdav'
        ? new WebDavDataSource()
        : new AlistDataSource()
    await source.init({
      ...existing,
      name: displayName,
      displayName,
      url: form.url.trim(),
      extra: {
        ...(existing.extra ?? {}),
        rootPath: '/',
      },
    })
    await source.test()
    alistBrowserSource.value = source
    return source
  }

  const setupInput = {
    id: sourceId,
    url: form.url,
    displayName,
    username,
    password: form.password,
    apiToken: form.apiToken,
    order: existing?.order ?? store.configs.length,
  }
  const source = form.type === 'clouddrive2'
    ? await createAuthenticatedCloudDrive2SetupSource(setupInput)
    : form.type === 'webdav'
      ? await createAuthenticatedWebDavSetupSource(setupInput)
      : await createAuthenticatedAlistSetupSource(setupInput)
  alistBrowserSource.value = source
  return source
}

function selectAlistRoot(path: string) {
  form.rootPath = normalizeRemoteRootPath(path)
  feedback.value = {
    type: 'info',
    message: `已选择 ${sourceTypeLabel(form.type)} 根目录：${form.rootPath}`,
  }
}

function resetAlistBrowser() {
  alistBrowserSource.value?.destroy()
  alistBrowserSource.value = null
  alistBrowserPath.value = '/'
  alistBrowserDirectories.value = []
  alistBrowserLoading.value = false
  alistBrowserError.value = null
}

function shouldReloginSource(config: DataSourceConfig, nextUrl: string, username: string, password: string, apiToken: string): boolean {
  const credentialChanged = config.type === 'clouddrive2' ? Boolean(apiToken.trim()) : Boolean(username || password)
  return normalizeComparableUrl(nextUrl) !== normalizeComparableUrl(config.url) || credentialChanged
}

async function validateExistingRemoteRoot(config: DataSourceConfig, url: string, displayName: string, rootPath: string): Promise<MediaLibrary[]> {
  if (!isRootSelectableRemoteSourceType(config.type))
    return []

  const source = config.type === 'clouddrive2'
    ? new CloudDrive2DataSource()
    : config.type === 'webdav'
      ? new WebDavDataSource()
      : new AlistDataSource()
  try {
    await source.init({
      ...config,
      name: displayName,
      displayName,
      url,
      extra: {
        ...(config.extra ?? {}),
        rootPath,
      },
    })
    await source.test()
    return source.listLibraries()
  }
  finally {
    source.destroy()
  }
}

function parentDirectoryPath(path: string): string {
  const normalized = normalizeRemoteRootPath(path)
  if (normalized === '/')
    return '/'
  const index = normalized.lastIndexOf('/')
  return index <= 0 ? '/' : normalized.slice(0, index)
}

function localRootPathLabel(path: string): string {
  if (!path.trim())
    return '未选择'
  try {
    return normalizeLocalRootPath(path)
  }
  catch {
    return path.trim()
  }
}

function localRootDisplayName(path: string): string {
  return path.trim().replace(/\\/g, '/').split('/').filter(Boolean).at(-1) ?? '本地媒体库'
}

function getScrapeRuleGroup(mediaType: ScrapeMediaType): ScrapeRuleGroup {
  let group = scrapeRules.value.groups.find(item => item.mediaType === mediaType)
  if (!group) {
    group = {
      mediaType,
      categories: [],
      fallbackCategoryName: SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME,
    }
    scrapeRules.value.groups.push(group)
  }
  return group
}

function genreOptionsForMediaType(mediaType: ScrapeMediaType): TmdbGenreOption[] {
  return mediaType === 'movie' ? TMDB_MOVIE_GENRES : TMDB_TV_GENRES
}

function countryConditionForCategory(category: ScrapeCategoryRule, mediaType: ScrapeMediaType): ScrapeValueCondition<string> {
  if (mediaType === 'movie') {
    category.conditions.productionCountries ??= { include: [], exclude: [] }
    return category.conditions.productionCountries
  }
  category.conditions.originCountries ??= { include: [], exclude: [] }
  return category.conditions.originCountries
}

function addScrapeCategory(mediaType: ScrapeMediaType) {
  const group = getScrapeRuleGroup(mediaType)
  group.categories.push(createEmptyScrapeCategoryRule(mediaType === 'movie' ? '新电影分类' : '新剧集分类'))
  markScrapeRulesDirty()
}

function removeScrapeCategory(group: ScrapeRuleGroup, categoryId: string) {
  group.categories = group.categories.filter(category => category.id !== categoryId)
  markScrapeRulesDirty()
}

function moveScrapeCategory(group: ScrapeRuleGroup, index: number, direction: -1 | 1) {
  const nextIndex = index + direction
  if (nextIndex < 0 || nextIndex >= group.categories.length)
    return
  const next = [...group.categories]
  const [category] = next.splice(index, 1)
  if (!category)
    return
  next.splice(nextIndex, 0, category)
  group.categories = next
  markScrapeRulesDirty()
}

function conditionValueState<T extends string | number>(condition: ScrapeValueCondition<T>, value: T): ConditionValueState {
  if (condition.include.includes(value))
    return 'include'
  if (condition.exclude.includes(value))
    return 'exclude'
  return 'none'
}

function cycleConditionValue<T extends string | number>(condition: ScrapeValueCondition<T>, value: T) {
  const current = conditionValueState(condition, value)
  condition.include = condition.include.filter(item => item !== value)
  condition.exclude = condition.exclude.filter(item => item !== value)
  if (current === 'none')
    condition.include.push(value)
  else if (current === 'include')
    condition.exclude.push(value)
  markScrapeRulesDirty()
}

function conditionChipClass<T extends string | number>(condition: ScrapeValueCondition<T>, value: T): string {
  const state = conditionValueState(condition, value)
  if (state === 'include')
    return 'border-primary/45 bg-primary/18 text-primary'
  if (state === 'exclude')
    return 'border-red-400/35 bg-red-400/12 text-red-100'
  return 'border-white/10 bg-white/5 text-white/48 hover:border-white/18 hover:bg-white/8 hover:text-white/72'
}

function conditionChipPrefix<T extends string | number>(condition: ScrapeValueCondition<T>, value: T): string {
  const state = conditionValueState(condition, value)
  if (state === 'include')
    return '包含'
  if (state === 'exclude')
    return '排除'
  return '不限'
}

function setReleaseYear(category: ScrapeCategoryRule, side: 'from' | 'to', rawValue: string) {
  const trimmed = rawValue.trim()
  const nextRange = category.conditions.releaseYear ? { ...category.conditions.releaseYear } : {}
  if (!trimmed) {
    delete nextRange[side]
  }
  else {
    const year = Number(trimmed)
    if (!Number.isInteger(year) || year < 1888 || year > 2200)
      return
    nextRange[side] = year
  }
  category.conditions.releaseYear = nextRange.from == null && nextRange.to == null ? null : nextRange
  markScrapeRulesDirty()
}

function updateFallbackCategoryName(group: ScrapeRuleGroup, value: string) {
  group.fallbackCategoryName = normalizeScrapeFallbackCategoryName(value)
  markScrapeRulesDirty()
}

function markScrapeRulesDirty() {
  scrapeRulesDirty.value = true
  scrapeFeedback.value = null
}

async function saveScrapeRules() {
  try {
    saveScrapeClassificationRules(scrapeRules.value)
    await flushAppSettings()
    scrapeRules.value = loadScrapeClassificationRules()
    scrapeRulesDirty.value = false
    scrapeFeedback.value = { type: 'success', message: '刮削分类规则已保存。后续扫描会按新规则计算本地逻辑分类。' }
  }
  catch (error) {
    scrapeFeedback.value = { type: 'error', message: toSafeErrorMessage(error, '刮削分类规则保存失败。') }
  }
}

async function resetScrapeRules() {
  try {
    scrapeRules.value = resetScrapeClassificationRules()
    await flushAppSettings()
    scrapeRulesDirty.value = false
    scrapeFeedback.value = { type: 'success', message: '已恢复内置默认分类实例。它只是默认模板，仍可继续按你的库调整。' }
  }
  catch (error) {
    scrapeFeedback.value = { type: 'error', message: toSafeErrorMessage(error, '默认分类规则恢复失败。') }
  }
}

async function saveTmdbSettings() {
  isSavingTmdbSettings.value = true
  scrapeFeedback.value = null
  try {
    saveTmdbLocalSettings({
      authType: tmdbForm.authType,
      language: tmdbForm.language,
      region: tmdbForm.region,
    })

    const credential = tmdbForm.credential.trim()
    const savedCredential = Boolean(credential)
    if (credential) {
      await saveConfiguredTmdbCredential(tmdbForm.authType, credential)
      tmdbForm.credential = ''
    }
    await flushAppSettings()

    await refreshTmdbCredentialState()
    scrapeFeedback.value = {
      type: tmdbCredentialConfigured.value ? 'success' : 'info',
      message: tmdbCredentialConfigured.value
        ? `TMDB 设置已保存。后续 OpenList/Alist、CloudDrive2 和本地文件扫描会按 ${tmdbCredentialInputLabel.value} 路由请求并用 TMDB 元数据执行分类规则。`
        : savedCredential
          ? `已保存 TMDB 设置，但当前 ${tmdbCredentialInputLabel.value} 不可用。扫描会保留本地可播放候选并使用兜底分类。`
          : tmdbStoredAuthType.value
            ? `已保存 TMDB 类型、语言和地区。当前 ${tmdbCredentialInputLabel.value} 未配置；已保存的 ${tmdbAuthTypeLabel(tmdbStoredAuthType.value)} 不会用于当前类型。扫描会保留本地可播放候选并使用兜底分类。`
            : `已保存 TMDB 类型、语言和地区。未填写当前类型的 ${tmdbCredentialInputLabel.value} 时，扫描会保留本地可播放候选并使用兜底分类。`,
    }
  }
  catch (error) {
    scrapeFeedback.value = {
      type: 'error',
      message: toSafeErrorMessage(error, 'TMDB 设置保存失败。'),
    }
  }
  finally {
    isSavingTmdbSettings.value = false
  }
}

async function clearTmdbSettingsCredential() {
  isSavingTmdbSettings.value = true
  scrapeFeedback.value = null
  try {
    await clearConfiguredTmdbCredential()
    tmdbForm.credential = ''
    await refreshTmdbCredentialState()
    scrapeFeedback.value = { type: 'success', message: '已清除 TMDB 凭据。分类规则仍保留，扫描会回到本地兜底分类。' }
  }
  catch (error) {
    scrapeFeedback.value = {
      type: 'error',
      message: toSafeErrorMessage(error, '清除 TMDB 凭据失败。'),
    }
  }
  finally {
    isSavingTmdbSettings.value = false
  }
}

async function refreshTmdbCredentialState() {
  const credential = await readStoredTmdbCredential()
  tmdbStoredAuthType.value = credential?.authType ?? null
  tmdbCredentialConfigured.value = credential?.authType === tmdbForm.authType
}

function optionDisplayLabel(option: TmdbGenreOption | ScrapeNamedOption): string {
  if ('id' in option)
    return `${option.label} · ${option.name}`
  return option.label
}

function tmdbAuthTypeLabel(authType: TmdbAuthType): string {
  return authType === 'readAccessToken' ? 'API 读访问令牌 / Read Access Token' : 'API Key'
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

          <div v-if="!storageInfo" class="mt-5 rounded-xl bg-white/6 px-4 py-3 text-sm leading-6 text-white/48">
            当前是浏览器开发模式，没有可查询的 Tauri 桌面存储路径。
          </div>
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
                  <input
                    v-model="subtitleForm.apiKey"
                    class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                    type="password"
                    autocomplete="off"
                    :placeholder="openSubtitlesConfiguredAuthMode === 'apiKey' ? '留空表示保留当前 API Key' : '粘贴 OpenSubtitles.com API Key'"
                  >
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
                      <input
                        v-model="subtitleForm.password"
                        class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                        type="password"
                        autocomplete="current-password"
                        :placeholder="openSubtitlesConfiguredAuthMode === 'account' ? '留空表示保留当前密码' : 'OpenSubtitles 密码'"
                      >
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
                清除海报与扫描缓存、已下载字幕缓存，以及每个视频单独保存的字幕、音轨、字幕偏移、倍速和画面设置。不会删除数据源、登录凭据、播放记录或全局软件设置。
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

      <section v-else-if="mode === 'scraping'" class="space-y-5">
        <div class="glass-panel rounded-[1.75rem] p-6">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <p class="text-xs uppercase tracking-[0.24em] text-white/34">
                Scraping Rules
              </p>
              <h2 class="mt-1 text-2xl font-bold text-white">
                刮削与分类
              </h2>
              <p class="mt-2 max-w-3xl text-sm leading-6 text-white/42">
                分类用于整理海报墙和筛选，不会改动媒体文件或远端目录。
              </p>
            </div>
            <div class="flex flex-wrap gap-2">
              <button
                type="button"
                class="rounded-2xl bg-white/8 px-4 py-2 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14"
                @click="resetScrapeRules"
              >
                恢复默认实例
              </button>
              <button
                type="button"
                class="rounded-2xl bg-primary/80 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary disabled:cursor-not-allowed disabled:opacity-45"
                :disabled="!scrapeRulesDirty"
                @click="saveScrapeRules"
              >
                保存规则
              </button>
            </div>
          </div>

          <div
            v-if="scrapeFeedback"
            class="mt-5 rounded-2xl border px-4 py-3 text-sm"
            :class="{
              'border-emerald-400/20 bg-emerald-400/10 text-emerald-100': scrapeFeedback.type === 'success',
              'border-red-400/20 bg-red-400/10 text-red-100': scrapeFeedback.type === 'error',
              'border-white/12 bg-white/6 text-white/58': scrapeFeedback.type === 'info',
            }"
          >
            {{ scrapeFeedback.message }}
          </div>
        </div>

        <section class="glass-panel rounded-[1.75rem] p-6">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <p class="text-xs uppercase tracking-[0.24em] text-white/34">
                TMDB
              </p>
              <h3 class="mt-1 text-xl font-bold text-white">
                元数据匹配（可选增强）
              </h3>
              <p class="mt-2 max-w-3xl text-sm leading-6 text-white/42">
                TMDB 用于匹配海报、简介和分类。未配置时仍可扫描和播放媒体。
              </p>
            </div>
            <span
              class="rounded-full px-3 py-1 text-xs font-semibold"
              :class="tmdbCredentialConfigured ? 'bg-emerald-400/14 text-emerald-100' : 'bg-amber-300/12 text-amber-100'"
            >
              {{ tmdbCredentialStatusLabel }}
            </span>
          </div>

          <div class="mt-5 grid gap-4 lg:grid-cols-[1.1fr_1.1fr_0.8fr_0.8fr]">
            <div class="rounded-2xl bg-black/16 p-4">
              <p class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">
                凭据类型
              </p>
              <div class="mt-3 grid gap-2">
                <button
                  v-for="option in tmdbAuthTypeOptions"
                  :key="option.value"
                  type="button"
                  class="rounded-2xl border px-4 py-3 text-left transition-colors"
                  :class="tmdbForm.authType === option.value ? 'border-primary/45 bg-primary/16 text-white' : 'border-white/10 bg-white/5 text-white/58 hover:bg-white/8'"
                  @click="tmdbForm.authType = option.value"
                >
                  <span class="block text-sm font-semibold">{{ option.label }}</span>
                  <span class="mt-1 block text-xs leading-5 text-white/40">{{ option.description }}</span>
                </button>
              </div>
            </div>

            <label class="rounded-2xl bg-black/16 p-4">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">{{ tmdbCredentialInputLabel }}</span>
              <input
                v-model="tmdbForm.credential"
                class="mt-3 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                type="password"
                autocomplete="off"
                :placeholder="tmdbCredentialPlaceholder"
              >
              <p class="mt-2 text-xs leading-5 text-white/38">
                已保存的内容不会显示，留空表示保留当前值。
              </p>
            </label>

            <label class="rounded-2xl bg-black/16 p-4">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">语言</span>
              <select
                v-model="tmdbForm.language"
                class="mt-3 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors focus:border-primary/60"
              >
                <option v-for="option in tmdbLanguageOptions" :key="option.value" :value="option.value">
                  {{ option.label }}
                </option>
              </select>
            </label>

            <label class="rounded-2xl bg-black/16 p-4">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">地区</span>
              <select
                v-model="tmdbForm.region"
                class="mt-3 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors focus:border-primary/60"
              >
                <option v-for="option in tmdbRegionOptions" :key="option.value" :value="option.value">
                  {{ option.label }}
                </option>
              </select>
            </label>
          </div>

          <div class="mt-5 flex flex-wrap gap-3">
            <button
              type="button"
              class="rounded-2xl bg-primary/80 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary disabled:cursor-wait disabled:opacity-55"
              :disabled="isSavingTmdbSettings"
              @click="saveTmdbSettings"
            >
              {{ isSavingTmdbSettings ? '保存中…' : '保存 TMDB 设置' }}
            </button>
            <button
              type="button"
              class="rounded-2xl bg-white/8 px-4 py-2 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14 disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="isSavingTmdbSettings || !tmdbStoredAuthType"
              @click="clearTmdbSettingsCredential"
            >
              清除 TMDB 凭据
            </button>
          </div>
        </section>

        <section
          v-for="group in scrapeRuleGroups"
          :key="group.mediaType"
          class="glass-panel rounded-[1.75rem] p-6"
        >
          <div class="flex flex-wrap items-center justify-between gap-4">
            <div>
              <p class="text-xs uppercase tracking-[0.24em] text-white/34">
                {{ group.mediaType === 'movie' ? 'Movie Categories' : 'TV Categories' }}
              </p>
              <h3 class="mt-1 text-xl font-bold text-white">
                {{ group.mediaType === 'movie' ? '电影分类' : '剧集分类' }}
              </h3>
              <p class="mt-2 text-sm text-white/42">
                {{ group.mediaType === 'movie' ? '只展示 TMDB 官方电影类型，不混入剧集类型。' : '只展示 TMDB 官方剧集类型，例如动画、纪录片、儿童、真人秀、脱口秀。' }}
              </p>
            </div>
            <button
              type="button"
              class="inline-flex items-center gap-2 rounded-2xl bg-primary/80 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary"
              @click="addScrapeCategory(group.mediaType)"
            >
              <span class="text-lg leading-none">+</span>
              添加{{ group.mediaType === 'movie' ? '电影' : '剧集' }}分类
            </button>
          </div>

          <div class="mt-5 rounded-2xl border border-white/10 bg-white/5 p-4">
            <label class="block">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">兜底分类</span>
              <input
                :value="group.fallbackCategoryName"
                class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                :placeholder="SCRAPE_DEFAULT_FALLBACK_CATEGORY_NAME"
                @input="updateFallbackCategoryName(group, ($event.target as HTMLInputElement).value)"
              >
            </label>
            <p class="mt-2 text-xs leading-5 text-white/38">
              没有命中上方显式分类时会落入这里。兜底分类不能删除，但可以改名。
            </p>
          </div>

          <div v-if="group.categories.length" class="mt-5 space-y-4">
            <article
              v-for="(category, index) in group.categories"
              :key="category.id"
              class="rounded-2xl border border-white/10 bg-white/5 p-4"
            >
              <div class="flex flex-wrap items-start justify-between gap-4">
                <label class="min-w-56 flex-1">
                  <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">分类名称</span>
                  <input
                    v-model="category.name"
                    class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                    placeholder="例如 华语电影 / 综艺"
                    @input="markScrapeRulesDirty"
                  >
                </label>

                <div class="flex flex-wrap gap-2 pt-6">
                  <button
                    type="button"
                    class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/70 transition-colors hover:bg-white/14 disabled:cursor-not-allowed disabled:opacity-35"
                    :disabled="index === 0"
                    @click="moveScrapeCategory(group, index, -1)"
                  >
                    上移
                  </button>
                  <button
                    type="button"
                    class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/70 transition-colors hover:bg-white/14 disabled:cursor-not-allowed disabled:opacity-35"
                    :disabled="index === group.categories.length - 1"
                    @click="moveScrapeCategory(group, index, 1)"
                  >
                    下移
                  </button>
                  <button
                    type="button"
                    class="rounded-xl bg-red-500/14 px-3 py-2 text-xs text-red-100 transition-colors hover:bg-red-500/24"
                    @click="removeScrapeCategory(group, category.id)"
                  >
                    删除
                  </button>
                </div>
              </div>

              <div class="mt-5 grid gap-4 xl:grid-cols-2">
                <div class="rounded-2xl bg-black/16 p-4">
                  <p class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">
                    类型 / 题材
                  </p>
                  <div class="mt-3 flex flex-wrap gap-2">
                    <button
                      v-for="genre in genreOptionsForMediaType(group.mediaType)"
                      :key="genre.id"
                      type="button"
                      class="rounded-xl border px-3 py-2 text-xs transition-colors"
                      :class="conditionChipClass(category.conditions.genreIds, genre.id)"
                      :title="`${conditionChipPrefix(category.conditions.genreIds, genre.id)} ${genre.name}`"
                      @click="cycleConditionValue(category.conditions.genreIds, genre.id)"
                    >
                      {{ conditionChipPrefix(category.conditions.genreIds, genre.id) }} · {{ optionDisplayLabel(genre) }}
                    </button>
                  </div>
                </div>

                <div class="rounded-2xl bg-black/16 p-4">
                  <p class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">
                    原始语种
                  </p>
                  <div class="mt-3 flex flex-wrap gap-2">
                    <button
                      v-for="language in SCRAPE_LANGUAGE_OPTIONS"
                      :key="language.value"
                      type="button"
                      class="rounded-xl border px-3 py-2 text-xs transition-colors"
                      :class="conditionChipClass(category.conditions.originalLanguages, language.value)"
                      @click="cycleConditionValue(category.conditions.originalLanguages, language.value)"
                    >
                      {{ conditionChipPrefix(category.conditions.originalLanguages, language.value) }} · {{ optionDisplayLabel(language) }}
                    </button>
                  </div>
                </div>

                <div class="rounded-2xl bg-black/16 p-4">
                  <p class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">
                    {{ group.mediaType === 'movie' ? '制作国家 / 地区' : '剧集来源国家 / 地区' }}
                  </p>
                  <div class="mt-3 flex flex-wrap gap-2">
                    <button
                      v-for="country in SCRAPE_COUNTRY_OPTIONS"
                      :key="country.value"
                      type="button"
                      class="rounded-xl border px-3 py-2 text-xs transition-colors"
                      :class="conditionChipClass(countryConditionForCategory(category, group.mediaType), country.value)"
                      @click="cycleConditionValue(countryConditionForCategory(category, group.mediaType), country.value)"
                    >
                      {{ conditionChipPrefix(countryConditionForCategory(category, group.mediaType), country.value) }} · {{ optionDisplayLabel(country) }}
                    </button>
                  </div>
                </div>

                <div class="rounded-2xl bg-black/16 p-4">
                  <p class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">
                    年份范围
                  </p>
                  <div class="mt-3 grid gap-3 sm:grid-cols-2">
                    <label>
                      <span class="text-xs text-white/38">起始年份</span>
                      <input
                        :value="category.conditions.releaseYear?.from ?? ''"
                        class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                        inputmode="numeric"
                        placeholder="不限"
                        @input="setReleaseYear(category, 'from', ($event.target as HTMLInputElement).value)"
                      >
                    </label>
                    <label>
                      <span class="text-xs text-white/38">结束年份</span>
                      <input
                        :value="category.conditions.releaseYear?.to ?? ''"
                        class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                        inputmode="numeric"
                        placeholder="不限"
                        @input="setReleaseYear(category, 'to', ($event.target as HTMLInputElement).value)"
                      >
                    </label>
                  </div>
                </div>
              </div>
            </article>
          </div>

          <div v-else class="mt-5 rounded-2xl border border-dashed border-white/12 p-8 text-center">
            <p class="text-sm font-semibold text-white">
              还没有显式分类
            </p>
            <p class="mt-2 text-sm leading-6 text-white/42">
              可以先依赖兜底分类，也可以点击右上角添加一个受控分类规则。
            </p>
          </div>
        </section>
      </section>

      <section v-else-if="mode === 'manage'" class="glass-panel rounded-[1.75rem] p-6">
        <div class="mb-6 flex flex-wrap items-center justify-between gap-4">
          <div>
            <p class="text-xs uppercase tracking-[0.24em] text-white/34">
              Data Sources
            </p>
            <h2 class="mt-1 text-2xl font-bold text-white">
              管理数据源
            </h2>
            <p class="mt-2 text-sm text-white/42">
              已启用的数据源会显示在左侧侧边栏；停用后保留配置但不再初始化或浏览。
            </p>
          </div>
          <button class="rounded-2xl bg-primary/80 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-primary" @click="goAdd">
            添加数据源
          </button>
        </div>

        <div v-if="configuredSources.length" class="space-y-3">
          <article
            v-for="source in configuredSources"
            :key="source.id"
            class="rounded-2xl border border-white/10 bg-white/5 p-4"
          >
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
                  :title="source.enabled === false ? '请先启用该数据源再浏览' : '浏览媒体库'"
                  @click="source.enabled === false ? undefined : router.push(`/source/${source.id}`)"
                >
                  浏览
                </button>
                <button
                  class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/72 transition-colors hover:bg-white/14 disabled:cursor-not-allowed disabled:opacity-35"
                  :disabled="clearingCacheSourceId === source.id"
                  title="仅清除该数据源已加载的媒体库、列表与详情缓存，不删除凭证或配置"
                  @click="clearSourceCache(source)"
                >
                  {{ clearingCacheSourceId === source.id ? '清除中…' : '清除缓存' }}
                </button>
                <button class="rounded-xl bg-red-500/14 px-3 py-2 text-xs text-red-100 transition-colors hover:bg-red-500/24" @click="removeSource(source.id)">
                  删除
                </button>
              </div>
            </div>

            <div
              v-if="isRawScanScheduleSource(source)"
              class="mt-4 grid gap-3 border-t border-white/8 pt-4 lg:grid-cols-2"
            >
              <div class="rounded-2xl border border-white/8 bg-black/14 p-4">
                <div class="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <p class="text-sm font-semibold text-white">
                      全量扫描
                    </p>
                    <p class="mt-1 text-xs leading-5 text-white/42">
                      慢速校准整个媒体库，默认 6 小时一次。
                    </p>
                  </div>
                  <label class="inline-flex items-center gap-2 text-xs font-semibold text-white/62">
                    <input
                      class="h-4 w-4 accent-primary"
                      type="checkbox"
                      :checked="rawScanScheduleEnabled(source, 'full')"
                      @change="updateRawScanScheduleEnabled(source, 'full', ($event.target as HTMLInputElement).checked)"
                    >
                    启用
                  </label>
                </div>
                <label class="mt-3 block">
                  <span class="text-xs text-white/38">间隔（分钟）</span>
                  <input
                    class="mt-2 w-full rounded-xl border border-white/10 bg-white/6 px-3 py-2 text-sm text-white outline-none transition-colors focus:border-primary/60"
                    type="number"
                    min="1"
                    step="1"
                    :value="rawScanScheduleIntervalMinutes(source, 'full')"
                    @change="updateRawScanScheduleInterval(source, 'full', ($event.target as HTMLInputElement).value)"
                  >
                </label>
              </div>

              <div class="rounded-2xl border border-white/8 bg-black/14 p-4">
                <div class="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <p class="text-sm font-semibold text-white">
                      增量扫描
                    </p>
                    <p class="mt-1 text-xs leading-5 text-white/42">
                      本地源配合文件监听；远端源用短间隔快照对比。
                    </p>
                  </div>
                  <label class="inline-flex items-center gap-2 text-xs font-semibold text-white/62">
                    <input
                      class="h-4 w-4 accent-primary"
                      type="checkbox"
                      :checked="rawScanScheduleEnabled(source, 'incremental')"
                      @change="updateRawScanScheduleEnabled(source, 'incremental', ($event.target as HTMLInputElement).checked)"
                    >
                    启用
                  </label>
                </div>
                <label class="mt-3 block">
                  <span class="text-xs text-white/38">间隔（分钟）</span>
                  <input
                    class="mt-2 w-full rounded-xl border border-white/10 bg-white/6 px-3 py-2 text-sm text-white outline-none transition-colors focus:border-primary/60"
                    type="number"
                    min="1"
                    step="1"
                    :value="rawScanScheduleIntervalMinutes(source, 'incremental')"
                    @change="updateRawScanScheduleInterval(source, 'incremental', ($event.target as HTMLInputElement).value)"
                  >
                </label>
              </div>
            </div>

            <div v-else class="mt-4 rounded-2xl border border-white/8 bg-black/12 px-4 py-3 text-xs leading-5 text-white/42">
              {{ sourceTypeLabel(source.type) }} 的媒体库和元数据由服务端维护，进入媒体库时会直接刷新，不使用 Player 本地扫描计划。
            </div>
          </article>
        </div>

        <div v-else class="rounded-2xl border border-dashed border-white/12 p-10 text-center">
          <p class="text-base font-semibold text-white">
            还没有数据源
          </p>
          <p class="mt-2 text-sm leading-6 text-white/42">
            添加 Emby、OpenList/Alist、CloudDrive2 或本地文件夹后，它会出现在左侧侧边栏，并可进入详细媒体库浏览页。
          </p>
          <button class="mt-5 rounded-2xl bg-primary/80 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-primary" @click="goAdd">
            添加数据源
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
              {{ isEditing ? '编辑数据源' : '添加数据源' }}
            </h2>
            <p class="mt-2 text-sm leading-6 text-white/42">
              选择数据源类型并填写连接信息，保存前会先测试连接。
            </p>
          </div>
          <button class="rounded-2xl bg-white/8 px-4 py-2 text-sm text-white/70 transition-colors hover:bg-white/14" @click="() => goManage()">
            返回管理
          </button>
        </div>

        <form class="space-y-5" @submit.prevent="saveSource">
          <div>
            <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">数据源类型</span>
            <div class="mt-3 grid gap-3 md:grid-cols-2">
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
                <span
                  class="flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl text-sm font-bold"
                  :class="form.type === option.type ? 'bg-primary/22 text-primary' : 'bg-white/8 text-white/52'"
                >
                  {{ option.shortLabel }}
                </span>
                <span class="min-w-0">
                  <span class="block text-sm font-semibold">{{ option.label }}</span>
                  <span class="mt-1 block text-xs leading-5 text-white/42">{{ option.description }}</span>
                </span>
                <span v-if="form.type === option.type" class="ml-auto rounded-full bg-primary/20 px-3 py-1 text-xs font-semibold text-primary">
                  已选择
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

          <label v-if="!isLocalForm" class="block">
            <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">服务器 URL</span>
            <input
              v-model="form.url"
              class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
              :placeholder="selectedProvider.urlPlaceholder"
              autocomplete="off"
            >
          </label>

          <label v-if="isAccountPasswordForm" class="block">
            <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">账号 / 用户名</span>
            <input
              v-model="form.username"
              class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
              :placeholder="selectedProvider.usernamePlaceholder"
              autocomplete="username"
            >
          </label>

          <label v-if="isCloudDrive2Form" class="block">
            <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">API Token</span>
            <input
              v-model="form.apiToken"
              class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
              :placeholder="isEditing ? '留空则保留当前 API Token' : '粘贴 CloudDrive2 中创建的只读 API Token'"
              type="password"
              autocomplete="off"
            >
            <span class="mt-2 block text-xs leading-5 text-white/42">
              请先在 CloudDrive2 中创建应用 API Token。
            </span>
          </label>

          <label v-if="isAccountPasswordForm" class="block">
            <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">密码</span>
            <input
              v-model="form.password"
              class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
              :placeholder="isEditing ? '留空则不重新登录' : '输入登录密码'"
              type="password"
              autocomplete="current-password"
            >
          </label>

          <div v-if="isLocalForm" class="rounded-2xl border border-white/10 bg-white/5 p-4">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0">
                <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">本地根目录</span>
                <p class="mt-2 break-all text-sm text-white/70">
                  当前选择：<span class="font-semibold text-white">{{ selectedRootPathLabel }}</span>
                </p>
              </div>
              <button
                type="button"
                class="rounded-2xl bg-white/8 px-4 py-2 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14"
                @click="chooseLocalRootPath"
              >
                选择文件夹
              </button>
            </div>
            <p class="mt-3 text-xs leading-5 text-white/42">
              只扫描所选文件夹，不会移动、删除或重命名媒体文件。
            </p>
          </div>

          <div v-if="isRemoteRootBrowserForm" class="rounded-2xl border border-white/10 bg-white/5 p-4">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div>
                <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">{{ sourceTypeLabel(form.type) }} 根目录</span>
                <p class="mt-2 text-sm text-white/70">
                  当前选择：<span class="font-semibold text-white">{{ selectedRootPathLabel }}</span>
                </p>
              </div>
              <button
                type="button"
                class="rounded-2xl bg-white/8 px-4 py-2 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14 disabled:cursor-not-allowed disabled:opacity-45"
                :disabled="alistBrowserLoading"
                @click="loadAlistRootBrowser"
              >
                {{ alistBrowserSource ? '刷新目录' : '连接并浏览目录' }}
              </button>
            </div>

            <p class="mt-3 text-xs leading-5 text-white/42">
              不选择时默认使用 `/`，也可以先连接并选择目录。
            </p>

            <div
              v-if="alistBrowserError"
              class="mt-4 rounded-2xl border border-red-400/20 bg-red-400/10 px-4 py-3 text-sm text-red-100"
            >
              {{ alistBrowserError }}
            </div>

            <div v-if="alistBrowserSource" class="mt-4 rounded-2xl border border-white/8 bg-black/18 p-3">
              <div class="flex flex-wrap items-center justify-between gap-3">
                <div class="min-w-0">
                  <p class="text-xs uppercase tracking-[0.18em] text-white/34">
                    Browsing
                  </p>
                  <p class="mt-1 truncate text-sm font-semibold text-white">
                    {{ alistBrowserPath }}
                  </p>
                </div>
                <div class="flex flex-wrap gap-2">
                  <button
                    type="button"
                    class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/70 transition-colors hover:bg-white/14 disabled:cursor-not-allowed disabled:opacity-35"
                    :disabled="!canBrowseAlistParent || alistBrowserLoading"
                    @click="loadAlistDirectory(alistParentPath)"
                  >
                    上一级
                  </button>
                  <button
                    type="button"
                    class="rounded-xl bg-primary/18 px-3 py-2 text-xs font-semibold text-primary transition-colors hover:bg-primary/26"
                    @click="selectAlistRoot(alistBrowserPath)"
                  >
                    选择当前目录
                  </button>
                </div>
              </div>

              <div v-if="alistBrowserLoading" class="mt-4 rounded-xl bg-white/6 px-4 py-3 text-sm text-white/48">
                正在加载目录…
              </div>

              <div v-else-if="alistBrowserDirectories.length" class="mt-4 max-h-72 space-y-2 overflow-y-auto pr-1">
                <div
                  v-for="directory in alistBrowserDirectories"
                  :key="directory.id"
                  class="flex items-center justify-between gap-3 rounded-xl bg-white/6 px-3 py-2"
                >
                  <button
                    type="button"
                    class="min-w-0 flex-1 truncate text-left text-sm text-white/74 transition-colors hover:text-white"
                    @click="loadAlistDirectory(directory.id)"
                  >
                    {{ directory.name }}
                  </button>
                  <button
                    type="button"
                    class="rounded-lg bg-white/8 px-3 py-1.5 text-xs text-white/62 transition-colors hover:bg-white/14 hover:text-white"
                    @click="selectAlistRoot(directory.id)"
                  >
                    设为根目录
                  </button>
                </div>
              </div>

              <div v-else class="mt-4 rounded-xl bg-white/6 px-4 py-3 text-sm text-white/48">
                当前目录没有可继续浏览的子目录，可直接选择当前目录作为根目录。
              </div>
            </div>
          </div>

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

          <div v-if="lastFetchedLibraries.length" class="rounded-2xl border border-white/10 bg-white/5 p-4">
            <p class="text-sm font-semibold text-white">
              已获取媒体库
            </p>
            <div class="mt-3 flex flex-wrap gap-2">
              <span v-for="library in lastFetchedLibraries" :key="library.id" class="rounded-full bg-white/8 px-3 py-1 text-xs text-white/60">
                {{ library.name }}
              </span>
            </div>
          </div>

          <div class="flex justify-end gap-3 border-t border-white/8 pt-5">
            <button
              type="button"
              class="rounded-2xl bg-white/8 px-5 py-3 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14"
              @click="() => goManage()"
            >
              取消
            </button>
            <button
              class="rounded-2xl bg-primary/80 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-primary disabled:cursor-not-allowed disabled:opacity-45"
              :disabled="isSaving"
            >
              {{ isSaving ? (isLocalForm ? '验证中…' : '登录测试中…') : (isEditing ? '保存' : '添加') }}
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
