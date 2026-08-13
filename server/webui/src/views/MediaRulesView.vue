<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, APIError } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import RuleGroupEditor from '@/components/RuleGroupEditor.vue'
import { cloneRules, emptyRules } from '@/media-rules'
import { useAuthStore } from '@/stores/auth'
import type { ClassificationRulesV1, ListResponse, MediaClassificationProfileDetail, MediaClassificationProfileSummary, MediaType } from '@/types/api'

const auth = useAuthStore()
const profiles = ref<MediaClassificationProfileSummary[]>([])
const selectedID = ref<number | null>(null)
const detail = ref<MediaClassificationProfileDetail | null>(null)
const draftName = ref('')
const draftRules = ref<ClassificationRulesV1>(emptyRules())
const activeGroup = ref<MediaType>('movie')
const loading = ref(true)
const saving = ref(false)
const error = ref('')
const notice = ref('')
const creating = ref(false)
const createName = ref('')

const selectedGroup = computed(() => draftRules.value.groups.find(group => group.media_type === activeGroup.value)!)
const readOnly = computed(() => !detail.value || detail.value.protected || !auth.can(Permissions.MediaClassificationProfilesUpdate))

function message(reason: unknown) { return reason instanceof Error ? reason.message : '请求失败' }
async function loadList(preferred?: number) {
  const data = await api<ListResponse<MediaClassificationProfileSummary>>('/api/v1/media-classification-profiles')
  profiles.value = data.list
  selectedID.value = profiles.value.some(item => item.id === (preferred ?? selectedID.value)) ? (preferred ?? selectedID.value) ?? null : profiles.value[0]?.id ?? null
  if (selectedID.value) await select(selectedID.value)
}
async function select(id: number) {
  selectedID.value = id; error.value = ''; notice.value = ''
  try { detail.value = await api<MediaClassificationProfileDetail>(`/api/v1/media-classification-profiles/${id}`); draftName.value = detail.value.name; draftRules.value = cloneRules(detail.value.rules) }
  catch (reason) { error.value = message(reason) }
}
async function startCreate() { creating.value = true; createName.value = ''; error.value = ''; notice.value = '' }
async function create() {
  await run(async () => { const created = await api<MediaClassificationProfileDetail>('/api/v1/media-classification-profiles', { method: 'POST', body: JSON.stringify({ name: createName.value }) }); creating.value = false; await loadList(created.id); notice.value = '已创建空白规则 Profile' })
}
async function copyProfile() {
  if (!detail.value) return
  await run(async () => { const copied = await api<MediaClassificationProfileDetail>(`/api/v1/media-classification-profiles/${detail.value!.id}/copy`, { method: 'POST', body: JSON.stringify({}) }); await loadList(copied.id); notice.value = '已创建独立副本' })
}
async function save() {
  if (!detail.value) return
  await run(async () => { const updated = await api<MediaClassificationProfileDetail>(`/api/v1/media-classification-profiles/${detail.value!.id}`, { method: 'PATCH', body: JSON.stringify({ name: draftName.value, revision: detail.value!.revision, rules: draftRules.value }) }); detail.value = updated; draftName.value = updated.name; draftRules.value = cloneRules(updated.rules); await loadList(updated.id); notice.value = '规则已保存' })
}
async function remove() {
  if (!detail.value || !window.confirm(`确认删除“${detail.value.name}”？只会删除 Profile 配置，不会删除媒体文件。`)) return
  const id = detail.value.id
  await run(async () => { await api(`/api/v1/media-classification-profiles/${id}`, { method: 'DELETE' }); detail.value = null; selectedID.value = null; await loadList(); notice.value = 'Profile 配置已删除' })
}
async function run(action: () => Promise<void>) {
  saving.value = true; error.value = ''; notice.value = ''
  try { await action() } catch (reason) { error.value = reason instanceof APIError && reason.errorCode === 'media_classification_profile_revision_conflict' ? '规则已被其他会话更新。当前草稿仍保留；请复制草稿内容或刷新后重新应用。' : message(reason) } finally { saving.value = false }
}
function updateGroup(value: typeof selectedGroup.value) { draftRules.value = { ...draftRules.value, groups: draftRules.value.groups.map(group => group.media_type === value.media_type ? value : group) } }

onMounted(async () => { try { await loadList() } catch (reason) { error.value = message(reason) } finally { loading.value = false } })
</script>

<template>
  <div>
    <div class="flex flex-wrap items-end justify-between gap-4"><div><h2 class="m-0 text-2xl font-800">规则管理</h2><p class="page-description mt-2">Profile 只对已识别媒体做逻辑分类，不选择下载目标，也不写入文件。</p></div><button v-if="auth.can(Permissions.MediaClassificationProfilesCreate)" class="btn-primary" @click="startCreate">创建空白规则</button></div>
    <p v-if="error" class="semantic-error mt-5 p-3 text-sm" role="alert">{{ error }}</p><p v-if="notice" class="semantic-success mt-5 p-3 text-sm">{{ notice }}</p>
    <form v-if="creating" class="panel mt-6 flex flex-wrap items-end gap-3" @submit.prevent="create"><div class="min-w-56 flex-1"><label class="label" for="new-profile-name">新 Profile 名称</label><input id="new-profile-name" v-model="createName" class="input" maxlength="128" required autofocus /></div><button class="btn-primary" :disabled="saving">创建</button><button type="button" class="btn-secondary" @click="creating = false">取消</button></form>
    <div v-if="loading" class="panel mt-6">正在加载规则…</div>
    <div v-else class="rules-workspace mt-6">
      <aside class="panel rules-list"><button v-for="profile in profiles" :key="profile.id" type="button" class="semantic-list-item rules-list__item" :class="profile.id === selectedID ? 'semantic-list-item--selected' : ''" @click="select(profile.id)"><span class="flex items-center justify-between gap-2"><strong>{{ profile.name }}</strong><span class="status-chip" :class="profile.kind === 'system' ? 'status-chip--ready' : ''">{{ profile.kind === 'system' ? '内置' : '自定义' }}</span></span><small class="text-subtle mt-2 block">电影 {{ profile.movie_category_count }} · 剧集 {{ profile.tv_category_count }} · r{{ profile.revision }}</small></button></aside>
      <main v-if="detail" class="panel min-w-0">
        <div class="flex flex-wrap items-start justify-between gap-3"><div class="min-w-56 flex-1"><label class="label" for="profile-name">Profile 名称</label><input id="profile-name" v-model="draftName" class="input" maxlength="128" :disabled="readOnly" /></div><div class="flex flex-wrap gap-2 pt-5"><button v-if="auth.can(Permissions.MediaClassificationProfilesCreate)" type="button" class="btn-secondary" :disabled="saving" @click="copyProfile">复制</button><button v-if="!readOnly" type="button" class="btn-primary" :disabled="saving" @click="save">保存</button><button v-if="detail.kind === 'custom' && auth.can(Permissions.MediaClassificationProfilesDelete)" type="button" class="btn-danger" :disabled="saving" @click="remove">删除</button></div></div>
        <p v-if="detail.protected" class="semantic-warning mt-4 p-3 text-sm">内置 Profile 只读。你可以复制它，再编辑独立副本。</p><p v-else-if="readOnly" class="text-subtle mt-4 text-sm">当前账户为只读视图；复制、保存和删除操作按权限隐藏。</p>
        <div class="management-tabs mt-6" role="tablist" aria-label="媒体类型"><button v-for="type in (['movie', 'tv'] as const)" :id="`media-rule-tab-${type}`" :key="type" type="button" class="management-tab" :class="activeGroup === type ? 'management-tab--active' : ''" role="tab" :aria-controls="`media-rule-panel-${type}`" :aria-selected="activeGroup === type" :tabindex="activeGroup === type ? 0 : -1" @click="activeGroup = type">{{ type === 'movie' ? '电影' : '剧集' }}</button></div>
        <RuleGroupEditor :id="`media-rule-panel-${activeGroup}`" class="mt-5" role="tabpanel" :aria-labelledby="`media-rule-tab-${activeGroup}`" :model-value="selectedGroup" :disabled="readOnly" @update:model-value="updateGroup" />
      </main>
      <main v-else class="panel">尚无可查看的 Profile。</main>
    </div>
  </div>
</template>
