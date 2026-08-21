<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { APIError } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import { useAuthStore } from '@/stores/auth'
import { notify } from '@/toast'
import { executeSTRMCleanup, listSTRMArtifacts, listSTRMLibraries, listSTRMRuns, previewSTRMCleanup, reconcileSTRM, retrySTRMRun, type CleanupPreview, type STRMArtifact, type STRMLibraryOverview, type STRMRun } from '@/strm'

const auth = useAuthStore()
const libraries = ref<STRMLibraryOverview[]>([])
const selectedID = ref(0)
const runs = ref<STRMRun[]>([])
const artifacts = ref<STRMArtifact[]>([])
const runTotal = ref(0)
const artifactTotal = ref(0)
const runPage = ref(1)
const artifactPage = ref(1)
const activeTab = ref<'runs' | 'artifacts'>('runs')
const loading = ref(true)
const saving = ref(false)
const error = ref('')
const cleanup = ref<CleanupPreview | null>(null)
const selected = computed(() => libraries.value.find(item => item.id === selectedID.value))
let timer: number | undefined

function message(reason: unknown) { return reason instanceof APIError ? reason.message : '请求失败，请稍后重试' }
function dateTime(value: string | null) { return value ? new Date(value).toLocaleString('zh-CN') : '—' }
function statusClass(value: string) { return value === 'completed' ? 'status-chip status-chip--ready' : value === 'failed' ? 'status-chip status-chip--error' : ['running','queued'].includes(value) ? 'status-chip status-chip--warning' : 'status-chip' }
function cleanupStatus(value: string) { return ({pending:'待执行',running:'清理中',completed:'已完成',failed:'失败',skipped:'已跳过'} as Record<string,string>)[value] ?? value }

async function load(quiet = false) {
	if (!quiet) loading.value = true
	try {
		libraries.value = await listSTRMLibraries()
		if (!libraries.value.some(item => item.id === selectedID.value)) selectedID.value = libraries.value[0]?.id ?? 0
		await loadDetails()
		error.value = ''
	} catch (reason) { if (!quiet) error.value = message(reason) } finally { if (!quiet) loading.value = false }
}
async function loadDetails() {
	if (!selectedID.value) { runs.value=[]; artifacts.value=[]; return }
	const [runResult, artifactResult] = await Promise.all([listSTRMRuns(selectedID.value,runPage.value), listSTRMArtifacts(selectedID.value,artifactPage.value)])
	runs.value=runResult.list; runTotal.value=runResult.total; artifacts.value=artifactResult.list; artifactTotal.value=artifactResult.total
}
async function reconcile(mode: 'incremental'|'full') { saving.value=true; try { await reconcileSTRM(selectedID.value,mode); notify(mode==='full'?'全量重建已入队':'增量刷新已入队','success'); await load(true) } catch(reason){notify(message(reason),'error')} finally{saving.value=false} }
async function retry(run: STRMRun) { saving.value=true; try{await retrySTRMRun(run.id);notify('失败任务已重新入队','success');await load(true)}catch(reason){notify(message(reason),'error')}finally{saving.value=false} }
async function previewCleanup(){saving.value=true;try{cleanup.value=await previewSTRMCleanup(selectedID.value)}catch(reason){notify(message(reason),'error')}finally{saving.value=false}}
async function confirmCleanup(){if(!cleanup.value)return;saving.value=true;try{const result=await executeSTRMCleanup(selectedID.value,cleanup.value.confirmation_token);cleanup.value=null;notify(`已清理 ${result.removed} 个失效托管产物`,'success');await load(true)}catch(reason){notify(message(reason),'error')}finally{saving.value=false}}
async function changeRunPage(next:number){if(next<1||(next-1)*30>=runTotal.value)return;runPage.value=next;await loadDetails()}
async function changeArtifactPage(next:number){if(next<1||(next-1)*30>=artifactTotal.value)return;artifactPage.value=next;await loadDetails()}
watch(selectedID, async()=>{runPage.value=1;artifactPage.value=1;cleanup.value=null;await loadDetails()})
onMounted(()=>{void load();timer=window.setInterval(()=>void load(true),5000)})
onBeforeUnmount(()=>{if(timer)window.clearInterval(timer)})
</script>

<template>
  <section class="page-shell">
    <header class="page-header"><div><p class="eyebrow">MEDIA AUTOMATION</p><h1>STRM 管理</h1><p class="page-description">按媒体库管理 signed STRM 投影。扫描、生成与清理都走真实持久任务和 manifest 所有权边界。</p></div></header>
    <p v-if="error" role="alert" class="semantic-error p-3 text-sm">{{ error }}</p>
    <div v-if="loading" class="panel">正在读取 STRM 状态…</div>
    <div v-else-if="libraries.length===0" class="panel"><h2 class="m-0 text-lg">没有启用 STRM 的媒体库</h2><p class="text-subtle mb-0 mt-2 text-sm">请先在媒体库配置中为云盘媒体库启用 signed 302 / STRM，并选择本地投影目录。</p></div>
    <div v-else class="grid gap-5 xl:grid-cols-[20rem_minmax(0,1fr)]">
      <aside class="panel p-2"><button v-for="library in libraries" :key="library.id" type="button" class="semantic-list-item mb-1 w-full p-3 text-left" :class="{'semantic-list-item--selected':selectedID===library.id}" @click="selectedID=library.id"><div class="flex items-center justify-between gap-2"><strong>{{ library.name }}</strong><span :class="statusClass(library.artifact_status)">{{ library.artifact_status }}</span></div><small class="text-subtle mt-2 block">Generation {{ library.artifact_applied_generation }} / {{ library.artifact_generation }}</small></button></aside>
      <main v-if="selected" class="min-w-0">
        <section class="panel"><div class="flex flex-wrap items-start justify-between gap-4"><div><h2 class="m-0">{{ selected.name }}</h2><p class="text-subtle mb-0 mt-2 text-sm">已应用 / 当前 generation：{{ selected.artifact_applied_generation }} / {{ selected.artifact_generation }} · 最近更新 {{ dateTime(selected.artifact_updated_at) }}</p></div><div class="flex flex-wrap gap-2"><button v-if="auth.can(Permissions.StrmRunsCreate)" class="btn-primary" :disabled="saving" @click="reconcile('incremental')">立即增量</button><button v-if="auth.can(Permissions.StrmRunsCreate)" class="btn-secondary" :disabled="saving" @click="reconcile('full')">全量重建</button><button v-if="auth.can(Permissions.StrmCleanup)" class="btn-danger" :disabled="saving" @click="previewCleanup">清理预览</button></div></div>
          <div v-if="selected.latest_run" class="mt-4 grid gap-3 sm:grid-cols-3 lg:grid-cols-6"><div v-for="item in [['写入',selected.latest_run.written_count],['更新',selected.latest_run.updated_count],['跳过',selected.latest_run.skipped_count],['自动清理',selected.latest_run.removed_count],['失败',selected.latest_run.failed_count],['总计',selected.latest_run.expected_count]]" :key="String(item[0])" class="semantic-inset p-3"><span class="text-subtle text-xs">{{ item[0] }}</span><strong class="mt-1 block">{{ item[1] }}</strong></div></div>
          <p v-if="selected.artifact_cleanup_at" class="text-subtle mb-0 mt-3 text-sm">最近自动清理：{{ selected.artifact_cleanup_removed }} 个 · {{ dateTime(selected.artifact_cleanup_at) }}</p>
          <p v-if="selected.artifact_error" class="semantic-error mb-0 mt-4 p-3 text-sm">{{ selected.artifact_error }}</p>
          <p v-if="selected.artifact_cleanup_error" class="semantic-error mb-0 mt-4 p-3 text-sm">自动清理：{{ selected.artifact_cleanup_error }}</p>
        </section>
        <section v-if="cleanup" class="semantic-warning mt-4 p-4"><h3 class="m-0 text-base">确认清理 {{ cleanup.count }} 个失效托管产物</h3><p class="mb-2 mt-2 text-sm">Generation {{ cleanup.generation }} · 确认将在 {{ dateTime(cleanup.expires_at) }} 失效。只删除 manifest 中 inactive、managed 且位于当前 STRM 投影根内的文件。</p><ul class="max-h-40 overflow-auto font-mono text-xs"><li v-for="path in cleanup.paths" :key="path">{{ path }}</li></ul><div class="mt-3 flex gap-2"><button class="btn-danger" :disabled="saving||cleanup.count===0" @click="confirmCleanup">确认删除</button><button class="btn-secondary" :disabled="saving" @click="cleanup=null">取消</button></div></section>
        <div class="management-tabs mt-4" role="tablist"><button class="management-tab" :class="activeTab==='runs'?'management-tab--active':''" @click="activeTab='runs'">运行历史</button><button class="management-tab" :class="activeTab==='artifacts'?'management-tab--active':''" @click="activeTab='artifacts'">托管产物</button></div>
        <section v-if="activeTab==='runs'" class="panel mt-4 overflow-x-auto"><table class="semantic-table min-w-190 w-full text-left text-sm"><thead><tr><th>创建时间</th><th>Generation</th><th>状态</th><th>写入 / 更新 / 跳过 / 失败</th><th>自动清理</th><th>错误</th><th>操作</th></tr></thead><tbody><tr v-for="run in runs" :key="run.id"><td>{{ dateTime(run.created_at) }}</td><td>{{ run.generation }}</td><td><span :class="statusClass(run.status)">{{ run.status }}</span></td><td>{{ run.written_count }} / {{ run.updated_count }} / {{ run.skipped_count }} / {{ run.failed_count }}</td><td>{{ cleanupStatus(run.cleanup_status) }} · {{ run.removed_count }}<small v-if="run.cleanup_at" class="text-subtle ml-2">{{ dateTime(run.cleanup_at) }}</small><small v-if="run.cleanup_error_code" class="semantic-danger-text ml-2">{{ run.cleanup_error_code }}</small></td><td class="semantic-danger-text">{{ run.error_code||'—' }}</td><td><button v-if="run.status==='failed'&&auth.can(Permissions.StrmRunsCreate)" class="btn-secondary" :disabled="saving" @click="retry(run)">重试</button></td></tr><tr v-if="runs.length===0"><td colspan="7" class="text-subtle py-8 text-center">暂无运行记录</td></tr></tbody></table><footer class="mt-4 flex justify-end gap-2"><button class="btn-secondary" :disabled="runPage<=1" @click="changeRunPage(runPage-1)">上一页</button><button class="btn-secondary" :disabled="runPage*30>=runTotal" @click="changeRunPage(runPage+1)">下一页</button></footer></section>
        <section v-else class="panel mt-4 overflow-x-auto"><table class="semantic-table min-w-180 w-full text-left text-sm"><thead><tr><th>相对路径</th><th>类型</th><th>状态</th><th>有效</th><th>更新时间</th></tr></thead><tbody><tr v-for="artifact in artifacts" :key="artifact.id"><td class="font-mono text-xs">{{ artifact.relative_path }}</td><td>{{ artifact.kind }}</td><td>{{ artifact.status }}</td><td>{{ artifact.active?'是':'失效' }}</td><td>{{ dateTime(artifact.updated_at) }}</td></tr><tr v-if="artifacts.length===0"><td colspan="5" class="text-subtle py-8 text-center">暂无托管产物</td></tr></tbody></table><footer class="mt-4 flex justify-end gap-2"><button class="btn-secondary" :disabled="artifactPage<=1" @click="changeArtifactPage(artifactPage-1)">上一页</button><button class="btn-secondary" :disabled="artifactPage*30>=artifactTotal" @click="changeArtifactPage(artifactPage+1)">下一页</button></footer></section>
      </main>
    </div>
  </section>
</template>
