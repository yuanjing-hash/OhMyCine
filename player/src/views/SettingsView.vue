<script setup lang="ts">
import type { DataSourceConfig, DataSourceType, MediaItem, MediaLibrary } from '@/services/datasource/types'
import { computed, onMounted, reactive, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { AlistDataSource, createAuthenticatedAlistSetupSource, loginAlistAndCreateConfig, normalizeAlistRootPath, readAlistRootPath } from '@/services/datasource/alist'
import { hasPersistentCredentialStorageWarning, readRawCredentialBackup, removeCredential, saveRawCredentialBackup } from '@/services/datasource/credentialStore'
import { loginEmbyAndCreateConfig } from '@/services/datasource/emby'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { useDataSourceStore } from '@/stores/datasource'

type LoginDataSourceType = Extract<DataSourceType, 'emby' | 'alist'>
type LoginDataSourceConfig = DataSourceConfig & { type: LoginDataSourceType }
type SettingsMode = 'overview' | 'manage' | 'add' | 'edit'
type SettingsEntryId = 'datasources' | 'playback' | 'appearance' | 'ai' | 'diagnostics'
type SettingsQueryState = Partial<Record<'section' | 'action' | 'id', string>>

interface DataSourceFormState {
  id: string | null
  type: LoginDataSourceType
  displayName: string
  url: string
  username: string
  password: string
  rootPath: string
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
  type: LoginDataSourceType
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
    urlPlaceholder: 'http://192.168.1.2:8096',
    usernamePlaceholder: 'Emby 登录账号',
  },
  {
    type: 'alist',
    label: 'OpenList/Alist',
    shortLabel: 'A',
    description: 'OpenList/Alist API 账号登录',
    defaultName: 'OpenList/Alist',
    urlPlaceholder: 'http://192.168.1.2:5244',
    usernamePlaceholder: 'OpenList/Alist 登录账号',
  },
]

const store = useDataSourceStore()
const route = useRoute()
const router = useRouter()
const form = reactive<DataSourceFormState>({
  id: null,
  type: 'emby',
  displayName: 'Emby',
  url: '',
  username: '',
  password: '',
  rootPath: '/',
})
const mode = ref<SettingsMode>('overview')
const isSaving = ref(false)
const clearingCacheSourceId = ref<string | null>(null)
const feedback = ref<{ type: 'success' | 'error' | 'info', message: string } | null>(null)
const lastFetchedLibraries = ref<MediaLibrary[]>([])
const persistentCredentialWarning = ref(hasPersistentCredentialStorageWarning())
const alistBrowserSource = shallowRef<AlistDataSource | null>(null)
const alistBrowserPath = ref('/')
const alistBrowserDirectories = ref<MediaItem[]>([])
const alistBrowserLoading = ref(false)
const alistBrowserError = ref<string | null>(null)

const configuredSources = computed(() => store.orderedConfigs)
const isEditing = computed(() => mode.value === 'edit')
const selectedProvider = computed(() => sourceTypeOptions.find(option => option.type === form.type) ?? sourceTypeOptions[0])
const isAlistForm = computed(() => form.type === 'alist')
const selectedRootPathLabel = computed(() => normalizeAlistRootPath(form.rootPath))
const alistParentPath = computed(() => parentDirectoryPath(alistBrowserPath.value))
const canBrowseAlistParent = computed(() => alistBrowserPath.value !== '/')
const activeSourceCount = computed(() => configuredSources.value.filter(source => source.enabled !== false).length)
const dataSourceEntryMeta = computed(() => {
  if (configuredSources.value.length === 0)
    return '尚未配置'
  return `${activeSourceCount.value}/${configuredSources.value.length} 个启用`
})
const pageDescription = computed(() => mode.value === 'overview'
  ? '集中管理 Player 的本机体验、数据源连接和后续增强能力。当前可直接配置数据源，其余入口会按功能完成度逐步开放。'
  : 'Player 可直接连接 Emby 和 OpenList/Alist 浏览播放媒体，不依赖 OhMyCine Server。账号、密码和访问令牌保存到 Tauri app data 下的 SQLite 凭证边界中，DataSource 配置和 localStorage 只保留 credentialRef 等非敏感字段。')
const settingsEntries = computed<SettingsEntry[]>(() => [
  {
    id: 'datasources',
    label: 'DS',
    title: '管理数据源',
    description: '连接、编辑、停用或清理 Emby 与 OpenList/Alist 数据源，控制左侧媒体入口。',
    meta: dataSourceEntryMeta.value,
    actionLabel: '打开',
    disabled: false,
  },
  {
    id: 'playback',
    label: 'Play',
    title: '播放',
    description: '默认音轨、字幕偏好、自动续播和快捷键微调将集中放在这里。',
    meta: '规划中',
    actionLabel: '待开放',
    disabled: true,
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
    id: 'diagnostics',
    label: 'Info',
    title: '关于 / 诊断',
    description: '版本信息、日志导出、运行环境诊断和依赖状态会在桌面封装稳定后接入。',
    meta: '规划中',
    actionLabel: '待开放',
    disabled: true,
  },
])

onMounted(() => {
  store.loadConfigs()
  refreshPersistentCredentialWarning()
  syncModeFromRoute()
})

watch(() => route.query, () => {
  syncModeFromRoute()
})

watch(() => form.type, (type) => {
  if (!isEditing.value)
    form.displayName = defaultDisplayName(type)
  resetAlistBrowser()
})

watch(() => [form.url, form.username, form.password] as const, () => {
  if (form.type === 'alist')
    resetAlistBrowser()
})

function syncModeFromRoute() {
  if (routeQueryValue('section') !== 'datasources') {
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
    if (source && isLoginDataSourceConfig(source)) {
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
  form.rootPath = '/'
  feedback.value = null
  lastFetchedLibraries.value = []
  resetAlistBrowser()
}

function editSource(config: DataSourceConfig) {
  if (!isLoginDataSourceConfig(config)) {
    feedback.value = {
      type: 'error',
      message: `${sourceTypeLabel(config.type)} 暂不支持在当前设置页编辑。`,
    }
    return
  }

  populateEditForm(config)
  void router.replace({ name: 'settings', query: { section: 'datasources', action: 'edit', id: config.id } })
}

function populateEditForm(config: LoginDataSourceConfig) {
  form.id = config.id
  form.type = config.type
  form.displayName = config.displayName ?? config.name
  form.url = config.url
  form.username = ''
  form.password = ''
  form.rootPath = config.type === 'alist' ? readAlistRootPath(config) : '/'
  feedback.value = {
    type: 'info',
    message: `可修改显示名称与启用状态；如 ${sourceTypeLabel(config.type)} URL 或账号变化，请输入账号密码重新登录。`,
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
    const result = await loginAndCreateConfig(form.type, {
      id,
      url: form.url,
      displayName: form.displayName,
      username: form.username,
      password: form.password,
      rootPath: form.type === 'alist' ? selectedRootPathLabel.value : undefined,
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
    feedback.value = { type: 'success', message: `${label} 登录测试成功，已验证 ${libraryCount} 个入口。新数据源已添加到左侧侧边栏。` }
    goManage({ preserveFeedback: true })
  }
  catch (error) {
    feedback.value = {
      type: 'error',
      message: toSafeErrorMessage(error, `添加数据源失败，请检查 ${sourceTypeLabel(form.type)} URL、账号和密码。`),
    }
  }
  finally {
    refreshPersistentCredentialWarning()
    isSaving.value = false
  }
}

async function saveEditedSource(id: string) {
  const existing = store.configs.find(config => config.id === id)
  if (!existing)
    throw new Error('数据源不存在。')
  if (!isLoginDataSourceType(existing.type))
    throw new Error(`${sourceTypeLabel(existing.type)} 暂不支持在当前设置页编辑。`)

  const username = form.username.trim()
  const nextUrl = form.url.trim()
  const nextDisplayName = form.displayName.trim() || existing.displayName || existing.name
  const nextRootPath = existing.type === 'alist' ? selectedRootPathLabel.value : undefined
  const label = sourceTypeLabel(existing.type)
  const shouldRelogin = shouldReloginSource(existing, nextUrl, username, form.password)
  if (shouldRelogin && (!username || !form.password))
    throw new Error(`更新 ${label} URL 或重新登录时必须同时填写账号和密码。`)

  if (shouldRelogin) {
    const previousCredential = await readCredentialBackupForConfig(existing)
    const result = await loginAndCreateConfig(existing.type, {
      id,
      url: nextUrl,
      displayName: nextDisplayName,
      username,
      password: form.password,
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
    feedback.value = { type: 'success', message: `${label} 已重新登录，并验证 ${result.libraries.length} 个入口。` }
    form.password = ''
    goManage({ preserveFeedback: true })
    return
  }

  const nextExtra = { ...(existing.extra ?? {}) }
  if (existing.type === 'alist') {
    const rootPathChanged = nextRootPath !== readAlistRootPath(existing)
    const libraries = rootPathChanged
      ? await validateExistingAlistRoot(existing, nextUrl, nextDisplayName, nextRootPath ?? '/')
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
  feedback.value = { type: 'success', message: '数据源已更新。若会话凭证已过期，请再次编辑并输入账号密码登录。' }
  goManage({ preserveFeedback: true })
}

function loginAndCreateConfig(type: LoginDataSourceType, input: {
  id: string
  url: string
  displayName: string
  username: string
  password: string
  rootPath?: string
  order: number
}): Promise<{ config: DataSourceConfig, libraries: MediaLibrary[] }> {
  if (type === 'alist')
    return loginAlistAndCreateConfig(input)
  return loginEmbyAndCreateConfig(input)
}

function isLoginDataSourceType(type: DataSourceType): type is LoginDataSourceType {
  return type === 'emby' || type === 'alist'
}

function isLoginDataSourceConfig(config: DataSourceConfig): config is LoginDataSourceConfig {
  return isLoginDataSourceType(config.type)
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
    case 'local':
      return '本地文件'
    case 'server':
      return 'OhMyCine Server'
    default:
      return type
  }
}

function defaultDisplayName(type: LoginDataSourceType): string {
  return sourceTypeOptions.find(option => option.type === type)?.defaultName ?? '数据源'
}

function sourceStatusLine(source: DataSourceConfig): string {
  const credentialState = typeof source.extra?.credentialRef === 'string' ? '凭据已绑定' : '需要重新登录'
  const rootState = source.type === 'alist' ? ` · 根目录：${readAlistRootPath(source)}` : ''
  return `状态：${source.enabled === false ? '已停用' : '已启用'} · 类型：${sourceTypeLabel(source.type)} · ${credentialState}${rootState}`
}

function normalizeComparableUrl(value: string): string {
  return value.trim().replace(/\/+$/, '')
}

function refreshPersistentCredentialWarning() {
  persistentCredentialWarning.value = hasPersistentCredentialStorageWarning()
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

function selectSourceType(type: LoginDataSourceType) {
  if (isEditing.value)
    return

  form.type = type
  form.displayName = defaultDisplayName(type)
  feedback.value = null
  lastFetchedLibraries.value = []
}

async function loadAlistRootBrowser() {
  await loadAlistDirectory('/')
}

async function loadAlistDirectory(path: string) {
  if (form.type !== 'alist')
    return

  alistBrowserLoading.value = true
  alistBrowserError.value = null
  try {
    const source = await ensureAlistBrowserSource()
    const nextPath = normalizeAlistRootPath(path)
    const items = await source.list(nextPath)
    alistBrowserPath.value = nextPath
    alistBrowserDirectories.value = items
      .filter(item => item.type === 'folder')
      .sort((left, right) => left.name.localeCompare(right.name, 'zh-Hans-CN'))
  }
  catch (error) {
    alistBrowserDirectories.value = []
    alistBrowserError.value = toSafeErrorMessage(error, 'OpenList/Alist 目录加载失败。')
  }
  finally {
    alistBrowserLoading.value = false
  }
}

async function ensureAlistBrowserSource(): Promise<AlistDataSource> {
  if (alistBrowserSource.value)
    return alistBrowserSource.value

  const sourceId = form.id ?? `alist-setup-${Date.now()}`
  const displayName = form.displayName.trim() || 'OpenList/Alist'
  const existing = form.id ? store.configs.find(config => config.id === form.id) : null
  const username = form.username.trim()
  const shouldUseExistingCredential = existing?.type === 'alist'
    && !shouldReloginSource(existing, form.url, username, form.password)

  if (shouldUseExistingCredential) {
    const source = new AlistDataSource()
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

  const source = await createAuthenticatedAlistSetupSource({
    id: sourceId,
    url: form.url,
    displayName,
    username,
    password: form.password,
    order: existing?.order ?? store.configs.length,
  })
  alistBrowserSource.value = source
  return source
}

function selectAlistRoot(path: string) {
  form.rootPath = normalizeAlistRootPath(path)
  feedback.value = {
    type: 'info',
    message: `已选择 OpenList/Alist 根目录：${form.rootPath}`,
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

function shouldReloginSource(config: DataSourceConfig, nextUrl: string, username: string, password: string): boolean {
  return normalizeComparableUrl(nextUrl) !== normalizeComparableUrl(config.url) || Boolean(username || password)
}

async function validateExistingAlistRoot(config: DataSourceConfig, url: string, displayName: string, rootPath: string): Promise<MediaLibrary[]> {
  const source = new AlistDataSource()
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
  const normalized = normalizeAlistRootPath(path)
  if (normalized === '/')
    return '/'
  const index = normalized.lastIndexOf('/')
  return index <= 0 ? '/' : normalized.slice(0, index)
}
</script>

<template>
  <div class="settings-view min-h-full p-6 pl-20 pt-16">
    <div
      v-if="feedback && mode === 'manage'"
      class="fixed right-6 top-20 z-50 max-w-md rounded-2xl border px-4 py-3 text-sm shadow-2xl backdrop-blur-xl"
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

      <div
        v-if="persistentCredentialWarning && mode !== 'overview'"
        class="rounded-2xl border border-amber-300/20 bg-amber-300/10 px-5 py-4 text-sm leading-6 text-amber-100"
      >
        当前运行环境不可用 Tauri SQLite 凭证命令，数据源账号、密码和 token 仅保存在内存中。请使用 Tauri 桌面应用运行以跨重启保留登录状态。
      </div>

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

      <section v-if="mode === 'overview'" class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        <button
          v-for="entry in settingsEntries"
          :key="entry.id"
          type="button"
          class="glass-panel flex min-h-56 flex-col rounded-[1.5rem] p-5 text-left transition-all duration-200 disabled:cursor-not-allowed"
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
          </article>
        </div>

        <div v-else class="rounded-2xl border border-dashed border-white/12 p-10 text-center">
          <p class="text-base font-semibold text-white">
            还没有数据源
          </p>
          <p class="mt-2 text-sm leading-6 text-white/42">
            添加 Emby 或 OpenList/Alist 数据源后，它会出现在左侧侧边栏，并可进入详细媒体库浏览页。
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
              先选择数据源类型，再填写对应登录信息。当前 OpenList/Alist 仅支持账号登录，不提供手填 token、公开目录或 WebDAV 模式；点击添加或保存时会先登录测试，成功后只持久化安全配置和 credentialRef。
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
            <input
              v-model="form.password"
              class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
              :placeholder="isEditing ? '留空则不重新登录' : '将保存到 SQLite 凭证边界'"
              type="password"
              autocomplete="current-password"
            >
          </label>

          <div v-if="isAlistForm" class="rounded-2xl border border-white/10 bg-white/5 p-4">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div>
                <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">OpenList/Alist 根目录</span>
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
                {{ alistBrowserSource ? '刷新目录' : '登录并浏览目录' }}
              </button>
            </div>

            <p class="mt-3 text-xs leading-5 text-white/42">
              不选择时默认使用 `/`。目录浏览使用当前表单的登录会话，只有点击添加或保存成功后才持久化凭据和根目录配置。
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
              {{ isSaving ? '登录测试中…' : (isEditing ? '保存' : '添加') }}
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
</style>
