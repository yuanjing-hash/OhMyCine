<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import FollowEditorDialog from '@/components/FollowEditorDialog.vue'
import { deleteFollow, followAction, followPath, followRunStatusLabel, followStatusLabel, type FollowPage, type FollowRunSummary, type FollowSummary } from '@/follows'
import { useAuthStore } from '@/stores/auth'
import { notify } from '@/toast'

const loading = ref(true); const error = ref(''); const items = ref<FollowSummary[]>([]); const status = ref('')
const auth = useAuthStore()
const editing = ref<FollowSummary | null>(null); const expanded = ref(''); const runs = ref<Record<string, FollowRunSummary[]>>({}); const busy = ref('')
async function load() { loading.value = true; error.value = ''; try { const query = new URLSearchParams({ page: '1', page_size: '100' }); if (status.value) query.set('status', status.value); const page = await api<FollowPage>(`/api/v1/follows?${query}`); items.value = page.list } catch (reason) { error.value = reason instanceof Error ? reason.message : '订阅加载失败' } finally { loading.value = false } }
async function toggleRuns(item: FollowSummary) { if (expanded.value === item.id) { expanded.value = ''; return } expanded.value = item.id; if (!runs.value[item.id]) { const result = await api<{ list: FollowRunSummary[] }>(`${followPath(item.id)}/runs`); runs.value[item.id] = result.list } }
async function action(item: FollowSummary, name: 'pause' | 'resume' | 'search') { busy.value = `${item.id}:${name}`; try { await followAction(item.id, name); notify(name === 'search' ? '已排队立即搜索。' : name === 'pause' ? '订阅已暂停；已提交下载不会被取消。' : '订阅已恢复。', 'success'); await load() } catch (reason) { notify(reason instanceof Error ? reason.message : '操作失败', 'error') } finally { busy.value = '' } }
async function remove(item: FollowSummary) { if (!window.confirm(`删除《${item.title}》订阅？已提交下载和媒体文件不会被删除。`)) return; busy.value = `${item.id}:delete`; try { await deleteFollow(item.id); notify('订阅配置已删除，现有下载和媒体保持不变。', 'success'); await load() } catch (reason) { notify(reason instanceof Error ? reason.message : '删除失败', 'error') } finally { busy.value = '' } }
function saved(saved: FollowSummary) { editing.value = null; items.value = items.value.map(item => item.id === saved.id ? saved : item); notify('订阅策略新 revision 已保存，将从下一次运行生效。', 'success') }
function formatTime(value?: string) { return value ? new Date(value).toLocaleString('zh-CN') : '—' }
function owns(item: FollowSummary) { return item.owner_id === auth.user?.id }
function canUpdate(item: FollowSummary) { return auth.can(Permissions.FollowsUpdateAll) || (owns(item) && auth.can(Permissions.FollowsUpdateOwn)) }
function canDelete(item: FollowSummary) { return auth.can(Permissions.FollowsDeleteAll) || (owns(item) && auth.can(Permissions.FollowsDeleteOwn)) }
function canExecute(item: FollowSummary) { return auth.can(Permissions.FollowsExecuteAll) || (owns(item) && auth.can(Permissions.FollowsExecuteOwn)) }
function canReadJob(item: FollowSummary) { return auth.can(Permissions.JobsReadAll) || (owns(item) && auth.can(Permissions.JobsReadOwn)) }
onMounted(load)
</script>

<template>
  <section class="space-y-5">
    <header class="flex flex-wrap items-end justify-between gap-4"><div><h1 class="m-0 text-3xl font-800">订阅管理</h1><p class="page-description mb-0 mt-2">自动对账明确已播的缺集，按每条订阅自己的策略搜索并进入正常下载、整理和入库流水线。</p></div><label><span class="label">状态</span><select v-model="status" class="input min-w-40" @change="load"><option value="">全部</option><option value="active">追更中</option><option value="paused">已暂停</option><option value="completed">当前已补齐</option><option value="blocked">需要处理</option></select></label></header>
    <div v-if="loading" class="panel py-12 text-center text-muted">正在读取订阅进度…</div>
    <div v-else-if="error" class="semantic-error p-4"><strong>订阅暂时不可用</strong><p class="mb-0 mt-1 text-sm">{{ error }}</p><button class="btn-secondary mt-3" @click="load">重试</button></div>
    <div v-else-if="!items.length" class="panel py-12 text-center"><h2 class="m-0 text-lg">还没有订阅</h2><p class="page-description mb-0 mt-2">从推荐或搜索进入电视剧详情，选择一个或多个季并配置执行策略。</p></div>
    <article v-for="item in items" v-else :key="item.id" class="panel overflow-hidden p-0">
      <div class="grid gap-4 p-5 md:grid-cols-[5rem_minmax(0,1fr)_auto]"><div class="detail-poster-mini"><img v-if="item.poster_ref" :src="item.poster_ref" :alt="`${item.title} 海报`"><span v-else>无海报</span></div><div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><h2 class="m-0 text-lg">{{ item.title }}</h2><span :class="item.status === 'blocked' ? 'status-chip status-chip--error' : item.status === 'completed' ? 'status-chip status-chip--ready' : item.status === 'paused' ? 'status-chip' : 'status-chip status-chip--warning'">{{ followStatusLabel(item.status) }}</span><span class="status-chip">revision {{ item.revision }}</span></div><p class="mb-0 mt-2 text-sm text-muted">季：{{ item.snapshot.seasons.map(value => value === 0 ? '特别篇' : `S${String(value).padStart(2, '0')}`).join('、') }} · 站点 {{ item.snapshot.site_ids.length }} 个 · 每 {{ item.snapshot.schedule.minutes }} 分钟</p><div class="mt-3 grid max-w-xl grid-cols-3 gap-2"><div class="semantic-inset p-2"><small class="text-subtle">已播目标</small><strong class="block">{{ item.progress_target }}</strong></div><div class="semantic-inset p-2"><small class="text-subtle">已入库</small><strong class="block">{{ item.progress_present }}</strong></div><div class="semantic-inset p-2"><small class="text-subtle">仍缺失</small><strong class="block">{{ item.progress_missing }}</strong></div></div><p v-if="item.last_error_message" class="semantic-danger-text mb-0 mt-3 text-sm">{{ item.last_error_message }} <span class="font-mono text-xs">{{ item.last_error_code }}</span></p><p class="mb-0 mt-3 text-xs text-muted">最近运行 {{ formatTime(item.last_run_at) }} · 下次检查 {{ formatTime(item.next_run_at) }}</p></div><div class="flex flex-wrap content-start gap-2 md:max-w-55 md:justify-end"><button v-if="canUpdate(item)" class="btn-secondary" @click="editing = item">编辑策略</button><button v-if="canUpdate(item)" class="btn-secondary" :disabled="busy !== ''" @click="action(item, item.status === 'paused' ? 'resume' : 'pause')">{{ item.status === 'paused' ? '恢复' : '暂停' }}</button><button v-if="canExecute(item)" class="btn-primary" :disabled="busy !== '' || item.status === 'paused'" @click="action(item, 'search')">立即搜索</button><button class="btn-secondary" @click="toggleRuns(item)">运行记录</button><button v-if="canDelete(item)" class="btn-danger" :disabled="busy !== ''" @click="remove(item)">删除</button></div></div>
      <div v-if="expanded === item.id" class="semantic-divider border-t bg-[var(--surface-muted)] p-5"><h3 class="m-0 text-base">最近运行</h3><div v-if="!runs[item.id]?.length" class="mt-3 text-sm text-muted">暂无运行记录。</div><div v-else class="mt-3 space-y-2"><div v-for="run in runs[item.id]" :key="run.id" class="semantic-inset flex flex-wrap items-center justify-between gap-3 p-3"><div><strong>{{ followRunStatusLabel(run.status) }}</strong><p class="mb-0 mt-1 text-xs text-muted">revision {{ run.subscription_revision }} · 查询名 {{ run.searched_names_count }} · 候选 {{ run.candidates }} · 提交 {{ run.selected }} · {{ formatTime(run.created_at) }}</p><p v-if="run.error_message" class="semantic-danger-text mb-0 mt-1 text-xs">{{ run.error_message }}</p></div><RouterLink v-if="canReadJob(item)" class="semantic-link text-sm" :to="`/automation/tasks?job_type=follow-search&job_id=${encodeURIComponent(run.job_id)}`">查看任务</RouterLink></div></div></div>
    </article>
    <FollowEditorDialog v-if="editing" :tmdb-id="editing.tmdb_id" :title="editing.title" :year="editing.year" :poster-ref="editing.poster_ref" :follow="editing" @close="editing = null" @saved="saved" />
  </section>
</template>

<style scoped>
.detail-poster-mini { display: grid; aspect-ratio: 2 / 3; place-items: center; overflow: hidden; border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--surface-muted); color: var(--text-subtle); font-size: .7rem; }
.detail-poster-mini img { width: 100%; height: 100%; object-fit: cover; }
</style>
