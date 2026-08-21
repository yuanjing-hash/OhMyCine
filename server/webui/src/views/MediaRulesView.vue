<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, APIError } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import RuleGroupEditor from '@/components/RuleGroupEditor.vue'
import { cloneRecognitionRules, cloneRules, emptyRules, moveItem, newRecognitionRule } from '@/media-rules'
import { useAuthStore } from '@/stores/auth'
import type { ClassificationRulesV1, ListResponse, MediaClassificationProfileDetail, MediaClassificationProfileSummary, MediaType, RecognitionRule } from '@/types/api'

type RuleSection = 'classification' | 'recognition' | 'naming'

const auth = useAuthStore()
const profiles = ref<MediaClassificationProfileSummary[]>([])
const selectedID = ref<number | null>(null)
const detail = ref<MediaClassificationProfileDetail | null>(null)
const draftName = ref('')
const draftRules = ref<ClassificationRulesV1>(emptyRules())
const draftRecognitionRules = ref<RecognitionRule[]>([])
const draftBuiltinPacks = ref<Array<'tv-v1' | 'anime-v1'>>([])
const movieDirectoryTemplate = ref('{category}/{title} ({year})')
const movieFilenameTemplate = ref('{title} ({year})')
const tvDirectoryTemplate = ref('{category}/{title} ({year})/Season {season:02}')
const tvFilenameTemplate = ref('{title} - S{season:02}E{episode:02}')
const activeSection = ref<RuleSection>('classification')
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
  try {
    detail.value = await api<MediaClassificationProfileDetail>(`/api/v1/media-classification-profiles/${id}`)
    draftName.value = detail.value.name
    draftRules.value = cloneRules(detail.value.rules)
    draftRecognitionRules.value = cloneRecognitionRules(detail.value.recognition_rules)
    draftBuiltinPacks.value = [...detail.value.builtin_recognition_packs]
    movieDirectoryTemplate.value = detail.value.movie_directory_template
    movieFilenameTemplate.value = detail.value.movie_filename_template
    tvDirectoryTemplate.value = detail.value.tv_directory_template
    tvFilenameTemplate.value = detail.value.tv_filename_template
  }
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
  await run(async () => {
    const updated = await api<MediaClassificationProfileDetail>(`/api/v1/media-classification-profiles/${detail.value!.id}`, { method: 'PATCH', body: JSON.stringify({
      name: draftName.value, revision: detail.value!.revision, rules: draftRules.value,
      recognition_rules: draftRecognitionRules.value,
      builtin_recognition_packs: draftBuiltinPacks.value,
      movie_directory_template: movieDirectoryTemplate.value, movie_filename_template: movieFilenameTemplate.value,
      tv_directory_template: tvDirectoryTemplate.value, tv_filename_template: tvFilenameTemplate.value,
    }) })
    detail.value = updated; draftName.value = updated.name; draftRules.value = cloneRules(updated.rules)
    draftRecognitionRules.value = cloneRecognitionRules(updated.recognition_rules)
    draftBuiltinPacks.value = [...updated.builtin_recognition_packs]
    movieDirectoryTemplate.value = updated.movie_directory_template; movieFilenameTemplate.value = updated.movie_filename_template
    tvDirectoryTemplate.value = updated.tv_directory_template; tvFilenameTemplate.value = updated.tv_filename_template
    await loadList(updated.id); notice.value = '规则已保存'
  })
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
function addRecognitionRule() { draftRecognitionRules.value = [...draftRecognitionRules.value, newRecognitionRule()] }
function updateRecognitionRule(index: number, patch: Partial<RecognitionRule>) { draftRecognitionRules.value = draftRecognitionRules.value.map((rule, ruleIndex) => ruleIndex === index ? { ...rule, ...patch } : rule) }
function removeRecognitionRule(index: number) { draftRecognitionRules.value = draftRecognitionRules.value.filter((_, ruleIndex) => ruleIndex !== index) }
function moveRecognitionRule(index: number, direction: -1 | 1) { draftRecognitionRules.value = moveItem(draftRecognitionRules.value, index, direction) }
function toggleBuiltinPack(code: 'tv-v1' | 'anime-v1', enabled: boolean) {
  const selected = new Set(draftBuiltinPacks.value)
  if (enabled) selected.add(code); else selected.delete(code)
  draftBuiltinPacks.value = (['tv-v1', 'anime-v1'] as const).filter(item => selected.has(item))
}

onMounted(async () => { try { await loadList() } catch (reason) { error.value = message(reason) } finally { loading.value = false } })
</script>

<template>
  <div>
    <div class="flex flex-wrap items-end justify-between gap-4"><div><h2 class="m-0 text-2xl font-800">规则管理</h2><p class="page-description mt-2">每个 Profile 统一管理识别预处理、媒体分类和电影/剧集命名；媒体库只负责目标目录与转移策略。</p></div><button v-if="auth.can(Permissions.MediaClassificationProfilesCreate)" class="btn-primary" @click="startCreate">创建空白规则</button></div>
    <p v-if="error" class="semantic-error mt-5 p-3 text-sm" role="alert">{{ error }}</p><p v-if="notice" class="semantic-success mt-5 p-3 text-sm">{{ notice }}</p>
    <form v-if="creating" class="panel mt-6 flex flex-wrap items-end gap-3" @submit.prevent="create"><div class="min-w-56 flex-1"><label class="label" for="new-profile-name">新 Profile 名称</label><input id="new-profile-name" v-model="createName" class="input" maxlength="128" required autofocus /></div><button class="btn-primary" :disabled="saving">创建</button><button type="button" class="btn-secondary" @click="creating = false">取消</button></form>
    <div v-if="loading" class="panel mt-6">正在加载规则…</div>
    <div v-else class="rules-workspace mt-6">
      <aside class="panel rules-list"><button v-for="profile in profiles" :key="profile.id" type="button" class="semantic-list-item rules-list__item" :class="profile.id === selectedID ? 'semantic-list-item--selected' : ''" @click="select(profile.id)"><span class="flex items-center justify-between gap-2"><strong>{{ profile.name }}</strong><span class="status-chip" :class="profile.kind === 'system' ? 'status-chip--ready' : ''">{{ profile.kind === 'system' ? '内置' : '自定义' }}</span></span><small class="text-subtle mt-2 block">电影 {{ profile.movie_category_count }} · 剧集 {{ profile.tv_category_count }} · 内置词包 {{ profile.builtin_recognition_pack_count }} · 自定义识别词 {{ profile.recognition_rule_count }} · r{{ profile.revision }}</small></button></aside>
      <main v-if="detail" class="panel min-w-0">
        <div class="flex flex-wrap items-start justify-between gap-3"><div class="min-w-56 flex-1"><label class="label" for="profile-name">Profile 名称</label><input id="profile-name" v-model="draftName" class="input" maxlength="128" :disabled="readOnly" /></div><div class="flex flex-wrap gap-2 pt-5"><button v-if="auth.can(Permissions.MediaClassificationProfilesCreate)" type="button" class="btn-secondary" :disabled="saving" @click="copyProfile">复制</button><button v-if="!readOnly" type="button" class="btn-primary" :disabled="saving" @click="save">保存</button><button v-if="detail.kind === 'custom' && auth.can(Permissions.MediaClassificationProfilesDelete)" type="button" class="btn-danger" :disabled="saving" @click="remove">删除</button></div></div>
        <p v-if="detail.protected" class="semantic-warning mt-4 p-3 text-sm">内置 Profile 只读。你可以复制它，再编辑独立副本。</p><p v-else-if="readOnly" class="text-subtle mt-4 text-sm">当前账户为只读视图；复制、保存和删除操作按权限隐藏。</p>
        <div class="management-tabs mt-6" role="tablist" aria-label="规则配置"><button v-for="section in ([['classification','分类规则'],['recognition','识别预处理'],['naming','命名格式']] as const)" :key="section[0]" type="button" class="management-tab" :class="activeSection === section[0] ? 'management-tab--active' : ''" role="tab" :aria-selected="activeSection === section[0]" @click="activeSection = section[0]">{{ section[1] }}</button></div>

        <section v-if="activeSection === 'classification'" class="mt-5">
          <div class="management-tabs" role="tablist" aria-label="媒体类型"><button v-for="type in (['movie', 'tv'] as const)" :id="`media-rule-tab-${type}`" :key="type" type="button" class="management-tab" :class="activeGroup === type ? 'management-tab--active' : ''" role="tab" :aria-controls="`media-rule-panel-${type}`" :aria-selected="activeGroup === type" :tabindex="activeGroup === type ? 0 : -1" @click="activeGroup = type">{{ type === 'movie' ? '电影' : '剧集' }}</button></div>
          <RuleGroupEditor :id="`media-rule-panel-${activeGroup}`" class="mt-5" role="tabpanel" :aria-labelledby="`media-rule-tab-${activeGroup}`" :model-value="selectedGroup" :disabled="readOnly" @update:model-value="updateGroup" />
        </section>

        <section v-else-if="activeSection === 'recognition'" class="mt-5">
          <div class="semantic-inset mb-5 p-4">
            <h3 class="m-0 text-base">内置预识别词包</h3>
            <p class="text-subtle mb-3 mt-1 text-sm">固定离线快照按“电视剧 → 动画”顺序执行，再执行下面的自定义规则；词包正文只读，不占 64 条自定义规则上限。</p>
            <div class="grid gap-3 md:grid-cols-2">
              <label v-for="pack in ([['tv-v1','电视剧词表','Putarku / MoviePilot-Help TV 固定快照'],['anime-v1','动画词表','Putarku / MoviePilot-Help anime 固定快照']] as const)" :key="pack[0]" class="semantic-list-item flex cursor-pointer items-start gap-3 p-3">
                <input class="mt-1" type="checkbox" :checked="draftBuiltinPacks.includes(pack[0])" :disabled="readOnly" @change="toggleBuiltinPack(pack[0], ($event.target as HTMLInputElement).checked)" />
                <span><strong class="block">{{ pack[1] }}</strong><small class="text-subtle">{{ pack[2] }}</small></span>
              </label>
            </div>
          </div>
          <div class="flex flex-wrap items-start justify-between gap-3"><div><h3 class="m-0 text-base">有序识别预处理</h3><p class="text-subtle mb-0 mt-1 text-sm">在内置文件名解析和 TMDB 查询之前按顺序执行。使用 Go RE2 正则；替换内容支持 <code>$1</code> 捕获组，留空即删除匹配内容。</p></div><button v-if="!readOnly" type="button" class="btn-secondary" @click="addRecognitionRule">添加规则</button></div>
          <div v-if="draftRecognitionRules.length" class="mt-4 grid gap-3">
            <article v-for="(rule, index) in draftRecognitionRules" :key="index" class="semantic-inset p-4">
              <div class="grid gap-3 lg:grid-cols-[auto_9rem_minmax(12rem,1fr)_minmax(10rem,1fr)_auto] lg:items-end">
                <label class="text-muted flex items-center gap-2 pb-2 text-sm"><input :checked="rule.enabled" type="checkbox" :disabled="readOnly" @change="updateRecognitionRule(index, { enabled: ($event.target as HTMLInputElement).checked })" />启用</label>
                <div><label class="label" :for="`recognition-type-${index}`">适用类型</label><select :id="`recognition-type-${index}`" :value="rule.media_type" class="input" :disabled="readOnly" @change="updateRecognitionRule(index, { media_type: ($event.target as HTMLSelectElement).value as RecognitionRule['media_type'] })"><option value="all">全部</option><option value="movie">电影</option><option value="tv">剧集</option></select></div>
                <div><label class="label" :for="`recognition-pattern-${index}`">匹配正则</label><input :id="`recognition-pattern-${index}`" :value="rule.pattern" class="input font-mono text-xs" maxlength="512" :disabled="readOnly" placeholder="例如：^【[^】]*发布[^】]*】" @input="updateRecognitionRule(index, { pattern: ($event.target as HTMLInputElement).value })" /></div>
                <div><label class="label" :for="`recognition-replace-${index}`">替换为</label><input :id="`recognition-replace-${index}`" :value="rule.replacement" class="input font-mono text-xs" maxlength="512" :disabled="readOnly" placeholder="留空表示删除" @input="updateRecognitionRule(index, { replacement: ($event.target as HTMLInputElement).value })" /></div>
                <div v-if="!readOnly" class="flex gap-1"><button type="button" class="btn-quiet px-2" :disabled="index === 0" aria-label="上移识别规则" @click="moveRecognitionRule(index, -1)">↑</button><button type="button" class="btn-quiet px-2" :disabled="index === draftRecognitionRules.length - 1" aria-label="下移识别规则" @click="moveRecognitionRule(index, 1)">↓</button><button type="button" class="btn-danger px-2" aria-label="删除识别规则" @click="removeRecognitionRule(index)">删除</button></div>
              </div>
            </article>
          </div>
          <p v-else class="semantic-inset mt-4 p-4 text-subtle text-sm">当前没有自定义识别预处理规则，系统会直接使用内置发行名解析器。</p>
        </section>

        <section v-else class="mt-5">
          <h3 class="m-0 text-base">电影与剧集命名</h3><p class="text-subtle mb-0 mt-1 text-sm">目录始终限制在所选媒体库根内；文件扩展名由源文件保留，不写在模板中。</p>
          <div class="mt-4 grid gap-4 md:grid-cols-2">
            <div><label class="label" for="profile-movie-directory">电影目录模板</label><input id="profile-movie-directory" v-model="movieDirectoryTemplate" class="input font-mono text-xs" maxlength="512" :disabled="readOnly" /><p class="text-subtle mb-0 mt-1 text-xs">可用：{category} {title} {year}</p></div>
            <div><label class="label" for="profile-movie-filename">电影文件名模板</label><input id="profile-movie-filename" v-model="movieFilenameTemplate" class="input font-mono text-xs" maxlength="512" :disabled="readOnly" /><p class="text-subtle mb-0 mt-1 text-xs">可用：{category} {title} {year} {version}；旧模板会自动在片名后追加识别出的版本规格。</p></div>
            <div><label class="label" for="profile-tv-directory">剧集目录模板</label><input id="profile-tv-directory" v-model="tvDirectoryTemplate" class="input font-mono text-xs" maxlength="512" :disabled="readOnly" /><p class="text-subtle mb-0 mt-1 text-xs">另可用：{season} {season:02} {episode} {episode:02}</p></div>
            <div><label class="label" for="profile-tv-filename">剧集文件名模板</label><input id="profile-tv-filename" v-model="tvFilenameTemplate" class="input font-mono text-xs" maxlength="512" :disabled="readOnly" /></div>
          </div>
        </section>
      </main>
      <main v-else class="panel">尚无可查看的 Profile。</main>
    </div>
  </div>
</template>
