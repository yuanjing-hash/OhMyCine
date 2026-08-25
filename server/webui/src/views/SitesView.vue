<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api/client'
import SecretInput from '@/components/SecretInput.vue'
import { notify } from '@/toast'
import {
  cookieCloudErrorLabel,
  cookieCloudSettingsPath,
  cookieCloudSyncPath,
  siteCatalogPath,
  sitePath,
  sitesPath,
  siteTestPath,
  type CookieCloudMode,
  type CookieCloudSettings,
  type CookieCloudSyncResult,
  type SiteCatalogItem,
  type SiteSummary,
} from '@/sites'
import type { ListResponse } from '@/types/api'

interface SiteForm {
  kind: string
  name: string
  baseURL: string
  cookie: string
  passkey: string
  apiKey: string
  userAgent: string
  enabled: boolean
  priority: number
  timeoutSeconds: number
  rateLimitPerMinute: number
  browserEmulation: boolean
  browserServiceURL: string
  clearPasskey: boolean
  clearAPIKey: boolean
}

interface CookieCloudForm {
  mode: CookieCloudMode
  baseURL: string
  uuid: string
  password: string
  authHeader: string
  autoSyncMinutes: number
  revision: number
}

const sites = ref<SiteSummary[]>([])
const siteCatalog = ref<SiteCatalogItem[]>([])
const loading = ref(true)
const loadError = ref('')
const saving = ref(false)
const busyID = ref<number | null>(null)
const dialogOpen = ref(false)
const dialogStep = ref<'type' | 'form'>('type')
const selectedType = ref<'pt' | 'bt'>('pt')
const editing = ref<SiteSummary | null>(null)
const form = ref<SiteForm>(emptyForm())
const cookieCloudOpen = ref(false)
const cookieCloudLoading = ref(false)
const cookieCloudSaving = ref(false)
const cookieCloudSyncing = ref(false)
const cookieCloudSettings = ref<CookieCloudSettings | null>(null)
const cookieCloudForm = ref<CookieCloudForm>(emptyCookieCloudForm())

const title = computed(() => editing.value ? `编辑 ${editing.value.name}` : `添加 ${selectedType.value.toUpperCase()} 站点`)
const filteredCatalog = computed(() => siteCatalog.value.filter(item => item.site_type === selectedType.value))
const selectedCatalog = computed(() => siteCatalog.value.find(item => item.key === form.value.kind))
const credentialKind = computed(() => selectedCatalog.value?.credential_kind || editing.value?.credential_kind || 'cookie')
const cookieCloudEndpoint = computed(() => {
  const path = cookieCloudSettings.value?.local_upload_path || '/cookiecloud'
  return `${window.location.origin}${path}`
})

function emptyForm(): SiteForm {
  return {
    kind: 'pttime', name: 'PTTime', baseURL: '', cookie: '', passkey: '', apiKey: '', userAgent: '', enabled: true,
    priority: 100, timeoutSeconds: 12, rateLimitPerMinute: 12,
    browserEmulation: false, browserServiceURL: '', clearPasskey: false, clearAPIKey: false,
  }
}

function emptyCookieCloudForm(): CookieCloudForm {
  return { mode: 'disabled', baseURL: '', uuid: '', password: '', authHeader: '', autoSyncMinutes: 0, revision: 0 }
}

async function loadSites() {
  loading.value = true
  loadError.value = ''
  try {
    const [response, catalog] = await Promise.all([
      api<ListResponse<SiteSummary>>(sitesPath),
      api<ListResponse<SiteCatalogItem>>(siteCatalogPath),
    ])
    sites.value = response.list
    siteCatalog.value = catalog.list
  } catch (reason) { loadError.value = message(reason) }
  finally { loading.value = false }
}

function openCreate() {
  editing.value = null
  form.value = emptyForm()
  applyCatalogSelection()
  dialogStep.value = 'type'
  dialogOpen.value = true
}

function selectSiteType(type: 'pt' | 'bt') {
  selectedType.value = type
  const first = siteCatalog.value.find(item => item.site_type === type)
  if (first) form.value.kind = first.key
  applyCatalogSelection()
  dialogStep.value = 'form'
}

function applyCatalogSelection() {
  if (editing.value) return
  const selected = siteCatalog.value.find(item => item.key === form.value.kind)
  if (!selected) return
  form.value.name = selected.name
  form.value.baseURL = selected.base_urls[0] || ''
}

function openEdit(site: SiteSummary) {
  editing.value = site
  form.value = {
    kind: site.kind, name: site.name, baseURL: site.base_url, cookie: '', passkey: '', apiKey: '', userAgent: site.user_agent,
    enabled: site.enabled, priority: site.priority, timeoutSeconds: site.timeout_seconds,
    rateLimitPerMinute: site.rate_limit_per_minute, browserEmulation: site.browser_emulation,
    browserServiceURL: site.browser_service_url, clearPasskey: false, clearAPIKey: false,
  }
  selectedType.value = site.site_type
  dialogStep.value = 'form'
  dialogOpen.value = true
}

function closeDialog() {
  if (saving.value) return
  dialogOpen.value = false
  editing.value = null
  form.value = emptyForm()
}

async function save() {
  saving.value = true
  try {
    const current = editing.value
    const common = {
      name: form.value.name,
      base_url: form.value.baseURL,
      user_agent: form.value.userAgent,
      enabled: form.value.enabled,
      priority: form.value.priority,
      timeout_seconds: form.value.timeoutSeconds,
      rate_limit_per_minute: form.value.rateLimitPerMinute,
      browser_emulation: form.value.browserEmulation,
      browser_service_url: form.value.browserEmulation ? form.value.browserServiceURL : '',
    }
    if (current) {
      await api(sitePath(current.id), { method: 'PATCH', body: JSON.stringify({
        ...common, cookie: form.value.cookie.trim() || undefined,
        passkey: form.value.passkey.trim() || undefined, clear_passkey: form.value.clearPasskey,
        api_key: form.value.apiKey.trim() || undefined, clear_api_key: form.value.clearAPIKey,
        revision: current.revision,
      }) })
      notify('候选配置测试通过，站点已更新', 'success')
    } else {
      await api(sitesPath, { method: 'POST', body: JSON.stringify({
        ...common, kind: form.value.kind, cookie: form.value.cookie, passkey: form.value.passkey, api_key: form.value.apiKey,
      }) })
      notify('站点测试通过并已安全保存', 'success')
    }
    dialogOpen.value = false
    editing.value = null
    form.value = emptyForm()
    await loadSites()
  } catch (reason) { notify(message(reason), 'error') }
  finally { saving.value = false }
}

async function openCookieCloud() {
  cookieCloudOpen.value = true
  cookieCloudLoading.value = true
  try {
    const settings = await api<CookieCloudSettings>(cookieCloudSettingsPath)
    cookieCloudSettings.value = settings
    cookieCloudForm.value = {
      mode: settings.mode, baseURL: settings.base_url, uuid: '', password: '', authHeader: '',
      autoSyncMinutes: settings.auto_sync_minutes, revision: settings.revision,
    }
  } catch (reason) {
    notify(message(reason), 'error')
    cookieCloudOpen.value = false
  } finally { cookieCloudLoading.value = false }
}

function closeCookieCloud() {
  if (cookieCloudSaving.value || cookieCloudSyncing.value) return
  cookieCloudOpen.value = false
}

async function saveCookieCloud() {
  cookieCloudSaving.value = true
  try {
    const updated = await api<CookieCloudSettings>(cookieCloudSettingsPath, {
      method: 'PATCH',
      body: JSON.stringify({
        mode: cookieCloudForm.value.mode, base_url: cookieCloudForm.value.baseURL,
        uuid: cookieCloudForm.value.uuid, password: cookieCloudForm.value.password,
        auth_header: cookieCloudForm.value.authHeader,
        auto_sync_minutes: cookieCloudForm.value.autoSyncMinutes, revision: cookieCloudForm.value.revision,
      }),
    })
    cookieCloudSettings.value = updated
    cookieCloudForm.value.revision = updated.revision
    cookieCloudForm.value.uuid = ''
    cookieCloudForm.value.password = ''
    cookieCloudForm.value.authHeader = ''
    notify(updated.mode === 'disabled' ? 'CookieCloud 已关闭' : updated.mode === 'remote' ? 'CookieCloud 设置已验证并保存' : '本地 CookieCloud 接收设置已保存', 'success')
  } catch (reason) { notify(message(reason), 'error') }
  finally { cookieCloudSaving.value = false }
}

async function syncCookieCloud() {
  cookieCloudSyncing.value = true
  try {
    const result = await api<CookieCloudSyncResult>(cookieCloudSyncPath, { method: 'POST', body: '{}' })
    const issueLabels = [...new Set((result.issues || []).map(issue => cookieCloudErrorLabel(issue.error_code)))]
    const reason = issueLabels.length ? `；原因：${issueLabels.join('、')}` : ''
    const skipDetails = result.skipped
      ? `（CookieCloud 中其他域名、当前不是受支持站点：${result.skipped_unsupported_domains || 0}；受支持候选缺少有效登录 Cookie：${result.skipped_missing_login_cookies || 0}）`
      : ''
    notify(`同步完成：新增 ${result.created}，更新 ${result.updated}，跳过 ${result.skipped}${skipDetails}，失败 ${result.failed}${reason}`, result.failed || result.skipped ? 'warning' : 'success')
    const settings = await api<CookieCloudSettings>(cookieCloudSettingsPath)
    cookieCloudSettings.value = settings
    cookieCloudForm.value.revision = settings.revision
    await loadSites()
  } catch (reason) { notify(message(reason), 'error') }
  finally { cookieCloudSyncing.value = false }
}

async function testSite(site: SiteSummary) {
  busyID.value = site.id
  try {
    await api(siteTestPath(site.id), { method: 'POST', body: '{}' })
    notify(`${site.name} 连接正常`, 'success')
    await loadSites()
  } catch (reason) {
    notify(message(reason), 'error')
    await loadSites()
  } finally { busyID.value = null }
}

async function toggleSite(site: SiteSummary) {
  busyID.value = site.id
  try {
    await api(sitePath(site.id), { method: 'PATCH', body: JSON.stringify({ enabled: !site.enabled, revision: site.revision }) })
    notify(site.enabled ? '站点已停用' : '站点已启用', 'success')
    await loadSites()
  } catch (reason) { notify(message(reason), 'error') }
  finally { busyID.value = null }
}

async function deleteSite(site: SiteSummary) {
  if (!window.confirm(`确认删除站点“${site.name}”？这会使该站点尚未使用的搜索结果令牌立即失效，但不会删除已经创建的下载任务。`)) return
  busyID.value = site.id
  try {
    await api(sitePath(site.id), { method: 'DELETE' })
    notify('站点已删除', 'success')
    await loadSites()
  } catch (reason) { notify(message(reason), 'error') }
  finally { busyID.value = null }
}

function healthLabel(site: SiteSummary) {
  if (site.health.status === 'online') return site.health.username ? `在线 · ${site.health.username}` : '在线'
  if (site.health.status === 'offline') return '连接异常'
  return '尚未检测'
}

function cookieCloudStatus() {
  const settings = cookieCloudSettings.value
  if (!settings?.last_sync_at) return '尚未同步'
  const time = new Date(settings.last_sync_at).toLocaleString()
  if (settings.last_sync_status === 'success') return `最近同步成功 · ${time}`
  const status = settings.last_sync_status === 'partial' ? '最近同步部分失败' : '最近同步失败'
  return `${status} · ${cookieCloudErrorLabel(settings.last_sync_error_code)} · ${time}`
}

function message(reason: unknown) { return reason instanceof Error ? reason.message : '站点操作失败' }
onMounted(loadSites)
</script>

<template>
  <section class="space-y-5">
    <header class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <p class="text-xs font-700 uppercase tracking-widest text-[var(--text-subtle)]">Sites</p>
        <h1 class="mt-1 text-2xl font-800">站点管理</h1>
        <p class="page-description mt-1">统一管理 PT、公开 BT 与 Torznab 连接。Cookie、passkey 与 API Key 只加密保存在 Server。</p>
      </div>
      <div class="flex flex-wrap gap-2">
        <button class="btn-secondary" type="button" @click="openCookieCloud">CookieCloud</button>
        <button class="btn-primary" type="button" @click="openCreate">添加</button>
      </div>
    </header>

    <div v-if="loading" class="panel py-12 text-center text-muted">正在读取站点…</div>
    <div v-else-if="loadError" class="semantic-error p-4">
      <strong>站点列表不可用</strong><p class="mt-1 text-sm">{{ loadError }}</p><button class="btn-secondary mt-3" @click="loadSites">重试</button>
    </div>
    <div v-else-if="!sites.length" class="panel py-12 text-center">
      <h2 class="m-0 text-lg">尚未添加站点</h2>
      <p class="page-description mt-2">可添加内建 PT、公开 BT，或连接 Jackett/Prowlarr 的 Torznab API。</p>
      <button class="btn-primary mt-4" @click="openCreate">添加第一个站点</button>
    </div>
    <div v-else class="site-grid">
      <article v-for="site in sites" :key="site.id" class="panel flex min-h-72 flex-col">
        <header class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2"><h2 class="m-0 truncate text-lg">{{ site.name }}</h2><span class="status-chip">{{ site.site_type.toUpperCase() }}</span></div>
            <p class="text-subtle mt-1 truncate font-mono text-xs" :title="site.base_url">{{ site.base_url }}</p>
          </div>
          <span :class="site.enabled ? 'status-chip status-chip--ready' : 'status-chip'">{{ site.enabled ? '已启用' : '已停用' }}</span>
        </header>
        <dl class="mt-5 grid grid-cols-2 gap-3 text-sm">
          <div><dt class="text-subtle text-xs">连接状态</dt><dd class="m-0 mt-1">{{ healthLabel(site) }}</dd></div>
          <div><dt class="text-subtle text-xs">凭据</dt><dd class="m-0 mt-1">{{ site.credential_kind === 'none' ? '无需凭据' : site.credential_configured ? '已安全配置' : '未配置' }}</dd></div>
          <div><dt class="text-subtle text-xs">请求策略</dt><dd class="m-0 mt-1">{{ site.rate_limit_per_minute }} 次/分钟</dd></div>
          <div><dt class="text-subtle text-xs">连接方式</dt><dd class="m-0 mt-1">{{ site.kind === 'torznab' ? 'Torznab API' : site.browser_emulation ? '浏览器仿真' : '原生适配' }}</dd></div>
        </dl>
        <p v-if="site.health.error_code" class="semantic-warning mt-4 p-3 text-xs">最近检测：<span class="font-mono">{{ site.health.error_code }}</span>。更新候选凭据失败时原配置会保留。</p>
        <div class="mt-auto flex flex-wrap gap-2 pt-5">
          <button class="btn-primary" :disabled="busyID !== null" @click="testSite(site)">{{ busyID === site.id ? '检测中…' : '测试连接' }}</button>
          <button class="btn-secondary" :disabled="busyID !== null" @click="openEdit(site)">编辑</button>
          <button class="btn-secondary" :disabled="busyID !== null" @click="toggleSite(site)">{{ site.enabled ? '停用' : '启用' }}</button>
          <button class="btn-danger" :disabled="busyID !== null" @click="deleteSite(site)">删除</button>
        </div>
      </article>
    </div>

    <div v-if="dialogOpen" class="modal-backdrop fixed inset-0 z-50 flex items-center justify-center p-4" @click.self="closeDialog">
      <section v-if="dialogStep === 'type'" class="panel w-full max-w-2xl" role="dialog" aria-modal="true" aria-labelledby="site-type-title">
        <div class="flex items-start justify-between gap-3">
          <div><h2 id="site-type-title" class="m-0 text-xl">添加站点</h2><p class="page-description mt-1 text-sm">先选择要连接的站点类型。连接能力会持续扩展。</p></div>
          <button class="btn-secondary" type="button" @click="closeDialog">关闭</button>
        </div>
        <button class="type-card mt-5 w-full text-left" type="button" @click="selectSiteType('pt')">
          <span class="type-card__icon">PT</span>
          <span><strong class="block">PT 站点</strong><span class="text-subtle mt-1 block text-sm">从内建站点目录选择，标准 NexusPHP 站点复用通用解析引擎。</span></span>
          <span class="ml-auto text-subtle">下一步 →</span>
        </button>
        <button class="type-card mt-3 w-full text-left" type="button" @click="selectSiteType('bt')">
          <span class="type-card__icon">BT</span>
          <span><strong class="block">公开 BT / Torznab</strong><span class="text-subtle mt-1 block text-sm">选择 Nyaa 等内建公开索引，或连接 Jackett/Prowlarr。</span></span>
          <span class="ml-auto text-subtle">下一步 →</span>
        </button>
      </section>

      <form v-else class="panel max-h-[92vh] w-full max-w-2xl overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="site-dialog-title" @submit.prevent="save">
        <div class="flex items-start justify-between gap-3">
          <div><h2 id="site-dialog-title" class="m-0 text-xl">{{ title }}</h2><p class="page-description mt-1 text-sm">{{ editing ? '敏感字段留空会继续使用原凭据；保存前会测试完整候选配置。' : credentialKind === 'cookie' ? '请配置已登录账号的 Cookie，或先在 CookieCloud 中统一同步。' : credentialKind === 'api_key' ? '请填写 Torznab API 地址与 API Key。' : '公开 BT 索引无需登录凭据，Server 会使用受控内建地址。' }}</p></div>
          <button class="btn-secondary" type="button" :disabled="saving" @click="closeDialog">关闭</button>
        </div>
        <button v-if="!editing" class="link-button mt-4" type="button" @click="dialogStep = 'type'">← 返回选择类型</button>
        <div class="mt-5 grid gap-4 sm:grid-cols-2">
          <div class="sm:col-span-2"><label class="label" for="site-catalog">站点适配</label><select id="site-catalog" v-model="form.kind" class="input" :disabled="Boolean(editing)" @change="applyCatalogSelection"><option v-for="item in filteredCatalog" :key="item.key" :value="item.key">{{ item.name }} · {{ item.engine === 'nexusphp' ? 'NexusPHP' : item.engine.toUpperCase() }}</option></select><p class="text-subtle mb-0 mt-1 text-xs">公开 RSS 索引固定使用内建受控地址；自定义聚合器请使用 Torznab。</p></div>
          <div><label class="label" for="site-name">显示名称</label><input id="site-name" v-model="form.name" class="input" maxlength="128" required /></div>
          <div><label class="label" for="site-url">HTTPS 根地址</label><input id="site-url" v-model="form.baseURL" class="input font-mono" type="url" placeholder="https://example.test" required autocomplete="off" :readonly="selectedCatalog?.engine === 'rss'" /></div>
          <div v-if="credentialKind === 'cookie'" class="sm:col-span-2"><label class="label" for="site-cookie">Cookie{{ editing ? '（留空不修改）' : '' }}</label><SecretInput id="site-cookie" v-model="form.cookie" class="input min-h-24 font-mono text-xs" multiline :configured="Boolean(editing?.credential_configured)" :required="!editing" autocomplete="off" spellcheck="false" /><p class="text-subtle mb-0 mt-1 text-xs">CookieCloud 同步成功后也可在此继续手动更新，不会写入日志或普通任务字段。</p></div>
          <div v-if="credentialKind === 'cookie'" class="sm:col-span-2"><label class="label" for="site-passkey">Passkey（可选，{{ editing ? '留空不修改' : '仅在站点下载接口需要时使用' }}）</label><SecretInput id="site-passkey" v-model="form.passkey" class="input font-mono" :configured="Boolean(editing?.credential_configured)" autocomplete="new-password" /></div>
          <label v-if="editing && credentialKind === 'cookie'" class="text-muted flex items-center gap-2 text-sm sm:col-span-2"><input v-model="form.clearPasskey" type="checkbox" />清除已保存的 passkey</label>
          <div v-if="credentialKind === 'api_key'" class="sm:col-span-2"><label class="label" for="site-api-key">Torznab API Key{{ editing ? '（留空不修改）' : '' }}</label><SecretInput id="site-api-key" v-model="form.apiKey" class="input font-mono" :configured="Boolean(editing?.credential_configured)" :required="!editing" autocomplete="new-password" /></div>
          <label v-if="editing && credentialKind === 'api_key'" class="text-muted flex items-center gap-2 text-sm sm:col-span-2"><input v-model="form.clearAPIKey" type="checkbox" />清除已保存的 API Key</label>
          <div><label class="label" for="site-priority">优先级</label><input id="site-priority" v-model.number="form.priority" class="input" type="number" min="1" max="999" required /></div>
          <div><label class="label" for="site-rate">每分钟请求上限</label><input id="site-rate" v-model.number="form.rateLimitPerMinute" class="input" type="number" min="1" max="120" required /></div>
          <div><label class="label" for="site-timeout">请求超时（秒）</label><input id="site-timeout" v-model.number="form.timeoutSeconds" class="input" type="number" min="3" max="30" required /></div>
          <div><label class="label" for="site-ua">自定义 User-Agent（可选）</label><input id="site-ua" v-model="form.userAgent" class="input font-mono" maxlength="256" autocomplete="off" /></div>
          <label v-if="credentialKind === 'cookie'" class="text-muted flex items-center gap-2 text-sm sm:col-span-2"><input v-model="form.browserEmulation" type="checkbox" />启用浏览器模拟登录 / 反爬兼容</label>
          <div v-if="form.browserEmulation" class="semantic-warning p-4 sm:col-span-2">
            <label class="label" for="site-browser-service">FlareSolverr 服务地址</label>
            <input id="site-browser-service" v-model="form.browserServiceURL" class="input font-mono" type="url" placeholder="http://127.0.0.1:8191" required autocomplete="off" />
            <p class="mb-0 mt-2 text-xs">Server 会把目标站点 URL 和该站 Cookie 发送给此服务完成浏览器渲染。建议只使用自己在局域网部署并信任的 FlareSolverr；种子下载仍由 Server 直接完成。</p>
          </div>
          <label class="text-muted flex items-center gap-2 text-sm sm:col-span-2"><input v-model="form.enabled" type="checkbox" />启用此站点参与聚合搜索</label>
        </div>
        <div class="mt-5 flex justify-end gap-3"><button class="btn-secondary" type="button" :disabled="saving" @click="closeDialog">取消</button><button class="btn-primary" :disabled="saving">{{ saving ? '正在测试候选配置…' : editing ? '测试并保存' : '校验并添加' }}</button></div>
      </form>
    </div>

    <div v-if="cookieCloudOpen" class="modal-backdrop fixed inset-0 z-50 flex items-center justify-center p-4" @click.self="closeCookieCloud">
      <form class="panel max-h-[92vh] w-full max-w-2xl overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="cookiecloud-title" @submit.prevent="saveCookieCloud">
        <div class="flex items-start justify-between gap-3">
          <div><h2 id="cookiecloud-title" class="m-0 text-xl">CookieCloud</h2><p class="page-description mt-1 text-sm">从公共/自建服务拉取，或让浏览器扩展直接上传到当前 Server。Cookie 只在 Server 内解密、匹配和验证。</p></div>
          <button class="btn-secondary" type="button" :disabled="cookieCloudSaving || cookieCloudSyncing" @click="closeCookieCloud">关闭</button>
        </div>
        <div v-if="cookieCloudLoading" class="py-12 text-center text-muted">正在读取设置…</div>
        <template v-else>
          <div class="mt-5 grid gap-4 sm:grid-cols-2">
            <div class="sm:col-span-2"><label class="label" for="cookiecloud-mode">同步模式</label><select id="cookiecloud-mode" v-model="cookieCloudForm.mode" class="input"><option value="disabled">关闭</option><option value="remote">远程 CookieCloud 服务</option><option value="local">当前 Server 本地接收</option></select></div>
            <div v-if="cookieCloudForm.mode === 'remote'" class="sm:col-span-2"><label class="label" for="cookiecloud-url">CookieCloud 服务地址</label><input id="cookiecloud-url" v-model="cookieCloudForm.baseURL" class="input font-mono" type="url" placeholder="https://cookie.example.com" required autocomplete="off" /><p class="text-subtle mb-0 mt-1 text-xs">兼容 CookieCloud 的 <span class="font-mono">/get/{用户 KEY}</span> 接口，可填写公共服务或自己的服务器。</p></div>
            <template v-if="cookieCloudForm.mode !== 'disabled'">
              <div><label class="label" for="cookiecloud-uuid">用户 KEY / UUID</label><SecretInput id="cookiecloud-uuid" v-model="cookieCloudForm.uuid" class="input font-mono" :configured="Boolean(cookieCloudSettings?.credential_configured)" :required="!cookieCloudSettings?.credential_configured" autocomplete="off" /></div>
              <div><label class="label" for="cookiecloud-password">端到端加密密码</label><SecretInput id="cookiecloud-password" v-model="cookieCloudForm.password" class="input" :configured="Boolean(cookieCloudSettings?.credential_configured)" :required="!cookieCloudSettings?.credential_configured" autocomplete="new-password" /></div>
              <div v-if="cookieCloudForm.mode === 'local'" class="sm:col-span-2"><label class="label" for="cookiecloud-auth">上传共享认证</label><SecretInput id="cookiecloud-auth" v-model="cookieCloudForm.authHeader" class="input font-mono" :configured="Boolean(cookieCloudSettings?.credential_configured)" minlength="12" :required="!cookieCloudSettings?.credential_configured" autocomplete="new-password" placeholder="至少 12 个字符" /><p class="text-subtle mb-0 mt-1 text-xs">浏览器扩展上传时必须携带相同的 <span class="font-mono">X-CookieCloud-Auth</span> 请求头。</p></div>
              <div><label class="label" for="cookiecloud-interval">自动同步</label><select id="cookiecloud-interval" v-model.number="cookieCloudForm.autoSyncMinutes" class="input"><option :value="0">仅手动</option><option :value="60">每小时</option><option :value="360">每 6 小时</option><option :value="720">每 12 小时</option><option :value="1440">每天</option><option :value="10080">每周</option><option :value="43200">每 30 天</option></select></div>
            </template>
          </div>
          <div v-if="cookieCloudForm.mode === 'local'" class="semantic-inset mt-4 p-4">
            <strong class="text-sm">浏览器扩展 Server 地址</strong>
            <p class="mb-0 mt-2 break-all font-mono text-sm">{{ cookieCloudEndpoint }}</p>
            <p class="text-subtle mb-0 mt-2 text-xs">扩展会向该地址下的 <span class="font-mono">/update</span> 上传端到端密文，Server 不需要访问第三方 CookieCloud。</p>
          </div>
          <div v-if="cookieCloudSettings" class="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border)] pt-4">
            <p class="text-subtle m-0 text-xs">{{ cookieCloudStatus() }}</p>
            <button class="btn-secondary" type="button" :disabled="cookieCloudForm.mode === 'disabled' || !cookieCloudSettings.credential_configured || cookieCloudSyncing || cookieCloudSaving" @click="syncCookieCloud">{{ cookieCloudSyncing ? '正在同步…' : '立即同步' }}</button>
          </div>
          <div class="mt-5 flex justify-end gap-3"><button class="btn-secondary" type="button" :disabled="cookieCloudSaving || cookieCloudSyncing" @click="closeCookieCloud">取消</button><button class="btn-primary" :disabled="cookieCloudSaving || cookieCloudSyncing">{{ cookieCloudSaving ? (cookieCloudForm.mode === 'remote' ? '正在验证…' : '正在保存…') : (cookieCloudForm.mode === 'remote' ? '保存并验证' : '保存设置') }}</button></div>
        </template>
      </form>
    </div>
  </section>
</template>

<style scoped>
.site-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 22rem), 1fr)); gap: 1rem; }
.type-card { display: flex; align-items: center; gap: 1rem; border: 1px solid var(--border); border-radius: .8rem; background: var(--surface); padding: 1rem; color: var(--text); cursor: pointer; }
.type-card:hover { border-color: var(--accent); background: var(--surface-hover); }
.type-card__icon { display: grid; width: 3rem; height: 3rem; place-items: center; border-radius: .7rem; background: var(--accent-soft); color: var(--accent); font-weight: 800; }
.link-button { border: 0; background: transparent; padding: 0; color: var(--accent); cursor: pointer; }
</style>
