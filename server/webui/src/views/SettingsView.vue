<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import DirectoryPickerDialog from '@/components/DirectoryPickerDialog.vue'
import SecretInput from '@/components/SecretInput.vue'
import { credentialLoader } from '@/credentials'
import { useAuthStore } from '@/stores/auth'
import { notify } from '@/toast'
import { credentialKindLabel, credentialSourceLabel, defaultTMDBAPIBaseURL, defaultTMDBImageBaseURL } from '@/metadata-settings'
import { aiProviderLabel, aiRuntimeNotice, defaultOpenAIBaseURL, effectiveAIBaseURL, googleAIStudioBaseURL } from '@/ai-recognition-settings'
import { displayedStagingPath, DOWNLOAD_STAGING_DIRECTORY_ENDPOINT } from '@/directory-navigation'
import type { AIProviderModel, AIRecognitionSettings, DownloadSettings, MetadataSettings, SeedingSettings } from '@/types/api'

const auth = useAuthStore()
const settings = ref<DownloadSettings | null>(null)
const metadata = ref<MetadataSettings | null>(null)
const seeding = ref<SeedingSettings | null>(null)
const aiSettings = ref<AIRecognitionSettings | null>(null)
const aiProvider = ref<AIRecognitionSettings['provider_type']>('openai_compatible')
const aiBaseURL = ref(defaultOpenAIBaseURL)
const aiAPIKey = ref('')
const clearAIAPIKey = ref(false)
const aiModel = ref('')
const aiModels = ref<AIProviderModel[]>([])
const aiProbing = ref(false)
const tmdbToken = ref('')
const tmdbCredentialKind = ref<'read_access_token' | 'api_key'>('read_access_token')
const clearTMDB = ref(false)
const apiBaseURL = ref('')
const imageBaseURL = ref('')
const selectedPath = ref('')
const directoryToken = ref('')
const pickerOpen = ref(false)
const loading = ref(true)
const saving = ref(false)
const canUpdate = computed(() => auth.can(Permissions.SettingsUpdate))
const canBrowse = computed(() => auth.can(Permissions.StoragesBrowse))

watch(clearTMDB, value => { if (value) tmdbToken.value = '' })
watch(clearAIAPIKey, value => { if (value) aiAPIKey.value = '' })
watch(aiProvider, value => {
  aiBaseURL.value = effectiveAIBaseURL(value, value === 'openai_compatible' ? aiBaseURL.value : '')
  aiModels.value = []
})

async function load() {
  loading.value = true
  try {
    const [current, metadataCurrent, seedingCurrent, aiCurrent] = await Promise.all([
      api<DownloadSettings>('/api/v1/settings/downloads'),
      api<MetadataSettings>('/api/v1/settings/metadata'),
      api<SeedingSettings>('/api/v1/settings/seeding'),
      api<AIRecognitionSettings>('/api/v1/settings/ai-recognition'),
    ])
    settings.value = current
    metadata.value = metadataCurrent
    seeding.value = seedingCurrent
    aiSettings.value = aiCurrent
    aiProvider.value = aiCurrent.provider_type
    aiBaseURL.value = effectiveAIBaseURL(aiCurrent.provider_type, aiCurrent.base_url)
    aiModel.value = aiCurrent.model
    tmdbCredentialKind.value = metadataCurrent.credential_kind || 'read_access_token'
    apiBaseURL.value = metadataCurrent.api_base_url
    imageBaseURL.value = metadataCurrent.image_base_url
  } catch (reason) { notify(message(reason), 'error') } finally { loading.value = false }
}

async function testAndSetRoute(kind: 'api' | 'image', restoreDefault = false) {
  if (!metadata.value) return
  saving.value = true
  const baseURL = restoreDefault ? (kind === 'api' ? defaultTMDBAPIBaseURL : defaultTMDBImageBaseURL) : (kind === 'api' ? apiBaseURL.value : imageBaseURL.value)
  try {
    metadata.value = await api<MetadataSettings>(`/api/v1/settings/metadata/test-${kind}`, { method: 'POST', body: JSON.stringify({ base_url: baseURL, revision: metadata.value.revision }) })
    apiBaseURL.value = metadata.value.api_base_url; imageBaseURL.value = metadata.value.image_base_url
    notify(`${kind === 'api' ? 'TMDB API' : 'TMDB 图片'}地址测试成功并已启用`, 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false }
}


async function saveMetadata(testAfter = false) {
  if (!metadata.value) return
  saving.value = true
  const testingCandidate = testAfter && Boolean(tmdbToken.value.trim())
  try {
    if (testingCandidate) {
      metadata.value = await api<MetadataSettings>('/api/v1/settings/metadata/test-token', { method: 'POST', body: JSON.stringify({ tmdb_token: tmdbToken.value, credential_kind: tmdbCredentialKind.value, revision: metadata.value.revision }) })
    } else if (testAfter) {
      await api('/api/v1/settings/metadata/test', { method: 'POST', body: '{}' })
    } else {
      metadata.value = await api<MetadataSettings>('/api/v1/settings/metadata', { method: 'PATCH', body: JSON.stringify({ tmdb_token: tmdbToken.value, credential_kind: tmdbCredentialKind.value, clear_tmdb: clearTMDB.value, revision: metadata.value.revision }) })
    }
    tmdbToken.value = ''; clearTMDB.value = false
    notify(testAfter ? (testingCandidate ? 'TMDB 凭据测试成功并已加密保存' : 'TMDB 当前凭据连接成功') : 'TMDB 元数据设置已保存', 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false }
}

function aiProbePayload() {
  if (!aiSettings.value) return null
  return {
    provider_type: aiProvider.value,
    base_url: effectiveAIBaseURL(aiProvider.value, aiBaseURL.value),
    api_key: aiAPIKey.value,
    model: aiModel.value,
    revision: aiSettings.value.revision,
  }
}

async function loadAIModels() {
  const payload = aiProbePayload()
  if (!payload) return
  aiProbing.value = true
  try {
    const result = await api<{ list: AIProviderModel[]; total: number }>('/api/v1/settings/ai-recognition/models', { method: 'POST', body: JSON.stringify(payload) })
    aiModels.value = result.list
    if (!aiModel.value && result.list.length) aiModel.value = result.list[0].id
    notify(result.list.length ? `已读取 ${result.list.length} 个可用模型` : '连接成功，但 Provider 没有返回可用模型；可手动填写', result.list.length ? 'success' : 'warning')
  } catch (reason) { notify(message(reason), 'error') } finally { aiProbing.value = false }
}

async function testAIConnection() {
  const payload = aiProbePayload()
  if (!payload) return
  aiProbing.value = true
  try {
    await api('/api/v1/settings/ai-recognition/test', { method: 'POST', body: JSON.stringify(payload) })
    notify('AI Provider 连接成功', 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { aiProbing.value = false }
}

async function saveAISettings() {
  if (!aiSettings.value) return
  saving.value = true
  try {
    aiSettings.value = await api<AIRecognitionSettings>('/api/v1/settings/ai-recognition', { method: 'PATCH', body: JSON.stringify({
      enabled: aiSettings.value.enabled,
      provider_type: aiProvider.value,
      base_url: effectiveAIBaseURL(aiProvider.value, aiBaseURL.value),
      api_key: aiAPIKey.value,
      clear_api_key: clearAIAPIKey.value,
      model: aiModel.value,
      send_relative_basenames: aiSettings.value.send_relative_basenames,
      revision: aiSettings.value.revision,
    }) })
    aiAPIKey.value = ''
    clearAIAPIKey.value = false
    aiProvider.value = aiSettings.value.provider_type
    aiBaseURL.value = effectiveAIBaseURL(aiSettings.value.provider_type, aiSettings.value.base_url)
    aiModel.value = aiSettings.value.model
    notify(aiSettings.value.enabled ? 'AI 媒体识别辅助已开启' : 'AI 设置已保存，运行时辅助保持关闭', 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false }
}

function chooseDirectory(value: { path: string; token: string }) {
  selectedPath.value = value.path
  directoryToken.value = value.token
}

async function save() {
  if (!settings.value || !directoryToken.value) {
    notify('请先从 Server 目录选择器选择下载暂存目录', 'warning')
    return
  }
  saving.value = true
  try {
    settings.value = await api<DownloadSettings>('/api/v1/settings/downloads', { method: 'PATCH', body: JSON.stringify({ directory_token: directoryToken.value, revision: settings.value.revision }) })
    selectedPath.value = ''; directoryToken.value = ''
    notify('统一下载暂存目录已保存，之后新建的本地下载任务会使用该目录', 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false }
}

async function saveSeeding() {
  if (!seeding.value) return
  saving.value = true
  try {
    seeding.value = await api<SeedingSettings>('/api/v1/settings/seeding', { method: 'PATCH', body: JSON.stringify({ enabled: seeding.value.enabled, minimum_seed_minutes: seeding.value.minimum_seed_minutes, minimum_ratio: seeding.value.minimum_ratio, completion_mode: seeding.value.completion_mode, revision: seeding.value.revision }) })
    notify(seeding.value.enabled ? '自动做种清理设置已保存；只影响之后新建的下载任务' : '自动做种清理已关闭；之后的新任务会保留在做种管理中', 'success')
  } catch (reason) { notify(message(reason), 'error') } finally { saving.value = false }
}

function message(reason: unknown) { return reason instanceof Error ? reason.message : '操作失败' }
onMounted(load)
</script>

<template>
  <section class="mx-auto max-w-5xl">
    <div><h1 class="m-0 text-3xl font-800">系统设置</h1><p class="page-description mt-2 max-w-3xl">集中管理下载、调度与运行参数。下载器连接和最终媒体库在各自页面维护。</p></div>
    <div v-if="loading" class="text-subtle mt-6">正在读取系统设置…</div>
    <form v-else-if="settings" class="panel mt-6" @submit.prevent="save">
      <div class="flex flex-wrap items-start justify-between gap-4"><div><h2 class="m-0 text-lg">下载暂存目录</h2><p class="text-subtle mb-0 mt-1 text-sm">qBittorrent 先下载到这里；下载完成后，后续刮削与转移流水线再决定最终媒体库。</p></div><span :class="settings.configured ? 'status-chip status-chip--ready' : 'status-chip status-chip--warning'">{{ settings.configured ? '已配置' : '待配置' }}</span></div>
      <div class="mt-5"><span class="label">当前下载暂存目录</span><div class="flex gap-2"><input class="input font-mono text-xs" :value="displayedStagingPath(settings.absolute_path, selectedPath)" readonly placeholder="尚未配置，请从 Server 目录选择器选择" /><button class="btn-secondary" type="button" :disabled="!canBrowse" @click="pickerOpen = true">浏览</button></div><p v-if="selectedPath && selectedPath !== settings.absolute_path" class="semantic-warning-text mb-0 mt-2 text-xs">{{ canUpdate ? '已选择新目录，保存后才会用于之后创建的下载任务。' : '当前账号只有查看权限，所选目录不会保存。' }}</p></div>
      <p class="text-subtle mb-0 mt-3 text-xs">可从 Server 可见的 Windows 盘符、UNC 位置或 Linux 挂载点选择，不需要先创建 Storage。页面只提交短期目录令牌，Server 会在保存和每次任务执行时重新校验完整路径。</p>
      <button v-if="canUpdate" class="btn-primary mt-5" :disabled="saving || !directoryToken">{{ saving ? '正在保存…' : '保存下载设置' }}</button>
    </form>
    <form v-if="seeding" class="panel mt-6" @submit.prevent="saveSeeding">
      <div class="flex flex-wrap items-start justify-between gap-4"><div><h2 class="m-0 text-lg">做种管理默认策略</h2><p class="text-subtle mb-0 mt-1 text-sm">复制或软链接入库后继续由 qBittorrent 做种；策略在下载任务创建时快照，之后修改不会改变已入队任务。</p></div><span :class="seeding.enabled ? 'status-chip status-chip--ready' : 'status-chip'">{{ seeding.enabled ? '自动清理开启' : '仅保留并展示' }}</span></div>
      <label class="text-muted mt-5 flex items-start gap-3 text-sm"><input v-model="seeding.enabled" type="checkbox" :disabled="!canUpdate" /><span><strong class="text-normal block">条件满足后自动删种</strong><span class="text-subtle text-xs">默认关闭。复制入库会删除 qBittorrent 任务和暂存源文件；软链接入库只删任务，绝不删除链接依赖的源文件。</span></span></label>
      <div class="mt-5 grid gap-4 md:grid-cols-3"><div><label class="label">最低做种时长（分钟）</label><input v-model.number="seeding.minimum_seed_minutes" class="input" type="number" min="0" max="525600" :disabled="!canUpdate" /></div><div><label class="label">最低分享率</label><input v-model.number="seeding.minimum_ratio" class="input" type="number" min="0" max="1000" step="0.05" :disabled="!canUpdate" /></div><div><label class="label">条件组合</label><select v-model="seeding.completion_mode" class="input" :disabled="!canUpdate"><option value="all">全部达到</option><option value="any">任一达到</option></select></div></div>
      <p class="semantic-warning-text mb-0 mt-3 text-xs">数值 0 表示不启用该条件；开启自动清理时至少要保留一个非 0 条件。</p>
      <button v-if="canUpdate" class="btn-primary mt-5" :disabled="saving">保存做种策略</button>
    </form>
    <form v-if="metadata" class="panel mt-6" @submit.prevent="saveMetadata(false)">
      <div class="flex flex-wrap items-start justify-between gap-4"><div><h2 class="m-0 text-lg">TMDB 元数据</h2><p class="text-subtle mb-0 mt-1 text-sm">用于磁力 metadata 到达后的轻量识别与完成后复核。自定义凭据加密保存，并优先于部署或内置通道。</p></div><div class="flex flex-wrap gap-2"><span :class="metadata.tmdb_configured ? 'status-chip status-chip--ready' : 'status-chip status-chip--warning'">{{ credentialSourceLabel(metadata.credential_source) }}</span><span v-if="metadata.tmdb_configured" class="status-chip">{{ credentialKindLabel(metadata.credential_kind) }}</span></div></div>
      <div class="mt-5 grid gap-4 md:grid-cols-[minmax(12rem,.45fr)_minmax(20rem,1.55fr)]"><div><label class="label" for="tmdb-credential-kind">凭据类型</label><select id="tmdb-credential-kind" v-model="tmdbCredentialKind" class="input" :disabled="!canUpdate || clearTMDB"><option value="read_access_token">API 读访问令牌</option><option value="api_key">API 密钥</option></select></div><div><label class="label" for="tmdb-credential-value">{{ tmdbCredentialKind === 'api_key' ? 'TMDB API 密钥' : 'TMDB API 读访问令牌' }}</label><SecretInput id="tmdb-credential-value" v-model="tmdbToken" class="input" :configured="metadata.custom_configured" :load-secret="auth.can(Permissions.ConnectionsSecretsExport) && metadata.custom_configured ? credentialLoader({ resourceType: 'metadata', resourceID: 1, field: 'tmdb_credential' }) : undefined" :reset-key="metadata.revision" autocomplete="new-password" :placeholder="`留空保留现有${tmdbCredentialKind === 'api_key' ? '密钥' : '令牌'}`" :disabled="!canUpdate || clearTMDB" /></div></div>
      <p class="text-subtle mb-0 mt-2 text-xs">API 密钥使用 v3 <code>api_key</code> 查询参数；API 读访问令牌使用 Bearer。类型由你明确选择，不会按内容猜测。</p>
      <label class="text-muted mt-3 flex items-center gap-2 text-xs"><input v-model="clearTMDB" type="checkbox" :disabled="!canUpdate || !metadata.custom_configured" />清除自定义凭据，并恢复使用下一级凭据</label>
      <div v-if="canUpdate" class="mt-5 flex flex-wrap gap-3"><button class="btn-primary" :disabled="saving">保存</button><button class="btn-secondary" type="button" :disabled="saving || clearTMDB || (!metadata.tmdb_configured && !tmdbToken)" @click="saveMetadata(true)">保存并测试</button></div>
      <div class="semantic-inset mt-6 grid gap-4 p-4 md:grid-cols-[1fr_auto]"><div><label class="label">TMDB API 地址</label><input v-model="apiBaseURL" class="input font-mono text-xs" :disabled="!canUpdate" /><p class="text-subtle mb-0 mt-2 text-xs">默认短域名只在网络故障时回退旧域名；自定义地址不会跨域回退。</p></div><div class="flex flex-wrap items-end gap-2"><button class="btn-secondary" type="button" :disabled="saving || !metadata.tmdb_configured" @click="testAndSetRoute('api')">测试并启用</button><button class="btn-secondary" type="button" :disabled="saving || apiBaseURL === defaultTMDBAPIBaseURL || !metadata.tmdb_configured" @click="testAndSetRoute('api', true)">恢复默认</button></div></div>
      <div class="semantic-inset mt-4 grid gap-4 p-4 md:grid-cols-[1fr_auto]"><div><label class="label">TMDB 图片地址</label><input v-model="imageBaseURL" class="input font-mono text-xs" :disabled="!canUpdate" /><p class="text-subtle mb-0 mt-2 text-xs">图片通道独立测试；失败不会改变 API 地址或当前图片地址。</p></div><div class="flex flex-wrap items-end gap-2"><button class="btn-secondary" type="button" :disabled="saving" @click="testAndSetRoute('image')">测试并启用</button><button class="btn-secondary" type="button" :disabled="saving || imageBaseURL === defaultTMDBImageBaseURL" @click="testAndSetRoute('image', true)">恢复默认</button></div></div>
    </form>
    <form v-if="aiSettings" class="panel mt-6" @submit.prevent="saveAISettings">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div><h2 class="m-0 text-lg">AI 媒体识别辅助</h2><p class="text-subtle mb-0 mt-1 text-sm">仅在普通识别出现低置信度、候选冲突或标题极度混乱时辅助判断；不会直接执行下载或文件操作。</p></div>
        <div class="flex flex-wrap gap-2"><span :class="aiSettings.enabled ? 'status-chip status-chip--ready' : 'status-chip'">{{ aiSettings.enabled ? '已开启' : '默认关闭' }}</span><span class="status-chip">{{ aiProviderLabel(aiProvider) }}</span></div>
      </div>
      <div class="semantic-inset mt-5 p-4">
        <label class="text-muted flex items-start gap-3 text-sm"><input v-model="aiSettings.enabled" type="checkbox" :disabled="!canUpdate" /><span><strong class="text-normal block">允许运行时调用 AI</strong><span class="text-subtle text-xs">{{ aiRuntimeNotice(aiSettings.enabled) }}</span></span></label>
      </div>
      <div class="mt-5 grid gap-4 md:grid-cols-2">
        <div><label class="label" for="ai-provider">Provider 协议</label><select id="ai-provider" v-model="aiProvider" class="input" :disabled="!canUpdate"><option value="openai_compatible">OpenAI-compatible</option><option value="google_ai_studio">Google AI Studio（Gemini native）</option></select></div>
        <div><label class="label" for="ai-base-url">Base URL</label><input id="ai-base-url" v-model="aiBaseURL" class="input font-mono text-xs" :readonly="aiProvider === 'google_ai_studio'" :disabled="!canUpdate" /><p class="text-subtle mb-0 mt-2 text-xs">Google 固定使用 {{ googleAIStudioBaseURL }}；OpenAI-compatible 只接受安全的 HTTPS 根地址或 <code>/v1</code>。</p></div>
      </div>
      <div class="mt-4 grid gap-4 md:grid-cols-2">
        <div><label class="label" for="ai-api-key">API Key</label><SecretInput id="ai-api-key" v-model="aiAPIKey" class="input" :configured="aiSettings.api_key_configured" :load-secret="auth.can(Permissions.ConnectionsSecretsExport) && aiSettings.api_key_configured ? credentialLoader({ resourceType: 'ai_recognition', resourceID: 1, field: 'api_key' }) : undefined" :reset-key="aiSettings.revision" autocomplete="new-password" placeholder="留空保留已加密保存的 API Key" :disabled="!canUpdate || clearAIAPIKey" /><label class="text-muted mt-2 flex items-center gap-2 text-xs"><input v-model="clearAIAPIKey" type="checkbox" :disabled="!canUpdate || !aiSettings.api_key_configured" />清除已保存 API Key</label></div>
        <div><label class="label" for="ai-model">模型</label><input id="ai-model" v-model="aiModel" class="input font-mono text-xs" list="ai-model-options" placeholder="可读取列表或手动填写模型名" :disabled="!canUpdate" /><datalist id="ai-model-options"><option v-for="item in aiModels" :key="item.id" :value="item.id">{{ item.display_name }}</option></datalist><p class="text-subtle mb-0 mt-2 text-xs">模型列表获取失败不影响手动填写。</p></div>
      </div>
      <label class="text-muted mt-4 flex items-start gap-3 text-sm"><input v-model="aiSettings.send_relative_basenames" type="checkbox" :disabled="!canUpdate" /><span><strong class="text-normal block">允许发送清理后的相对文件名 basename</strong><span class="text-subtle text-xs">默认关闭。绝对路径、Cookie、Token、磁力链接、下载器和云盘内部 ID 永远不会发送。</span></span></label>
      <div v-if="canUpdate" class="mt-5 flex flex-wrap gap-3"><button class="btn-primary" :disabled="saving">保存 AI 设置</button><button class="btn-secondary" type="button" :disabled="aiProbing || clearAIAPIKey || (!aiAPIKey && !aiSettings.api_key_configured)" @click="testAIConnection">{{ aiProbing ? '正在连接…' : '测试连接' }}</button><button class="btn-secondary" type="button" :disabled="aiProbing || clearAIAPIKey || (!aiAPIKey && !aiSettings.api_key_configured)" @click="loadAIModels">获取模型列表</button></div>
    </form>
    <DirectoryPickerDialog :open="pickerOpen" :restrict-to-storage="false" :initial-endpoint="DOWNLOAD_STAGING_DIRECTORY_ENDPOINT" @close="pickerOpen = false" @select="chooseDirectory" />
  </section>
</template>
