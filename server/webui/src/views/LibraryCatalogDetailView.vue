<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import MediaReorganizationDialog from '@/components/MediaReorganizationDialog.vue'
import { mediaCatalogActionEndpoint, mediaCatalogDetailEndpoint, mediaCatalogOverrideEndpoint, normalizeMediaCatalogDetail } from '@/media-catalog'
import { useAuthStore } from '@/stores/auth'
import { notify } from '@/toast'
import type { ListResponse, MediaCatalogDeletionPreview, MediaCatalogDeletionResult, MediaCatalogDetail, MediaCatalogManagedTransfer, TMDBCandidate } from '@/types/api'

const route = useRoute(); const router = useRouter(); const auth = useAuthStore()
const loading = ref(true); const error = ref(''); const detail = ref<MediaCatalogDetail | null>(null)
const expanded = ref<number[]>([]); const actionBusy = ref(false); const metadataOpen = ref(false)
const candidates = ref<TMDBCandidate[]>([]); const candidatesLoading = ref(false); const candidateError = ref('')
const deletion = ref<MediaCatalogDeletionPreview | null>(null); const deletionPhrase = ref('')
const reorganizing = ref<MediaCatalogManagedTransfer | null>(null)
const libraryID = computed(() => Number(route.params.libraryID)); const workID = computed(() => String(route.params.workID))

async function load() {
  loading.value = true; error.value = ''; expanded.value = []
  try { detail.value = normalizeMediaCatalogDetail(await api<unknown>(mediaCatalogDetailEndpoint(libraryID.value, workID.value))) }
  catch (reason) { error.value = message(reason) }
  finally { loading.value = false }
}
async function loadCandidates() {
  if (!detail.value) return; metadataOpen.value = true; candidatesLoading.value = true; candidateError.value = ''
  const query = new URLSearchParams({ title: detail.value.work.title, media_type: detail.value.work.kind === 'series' ? 'tv' : 'movie' }); if (detail.value.work.release_year) query.set('year', String(detail.value.work.release_year))
  try { candidates.value = (await api<ListResponse<TMDBCandidate>>(`${mediaCatalogActionEndpoint(libraryID.value, workID.value, 'tmdb-candidates')}?${query}`)).list }
  catch (reason) { candidateError.value = message(reason) }
  finally { candidatesLoading.value = false }
}
async function override(candidate: TMDBCandidate) {
  actionBusy.value = true
  try { await api(mediaCatalogOverrideEndpoint(libraryID.value, workID.value), { method: 'PUT', body: JSON.stringify({ tmdb_id: candidate.id, media_type: candidate.media_type }) }); notify('元数据匹配已更新', 'success'); metadataOpen.value = false; await load() }
  catch (reason) { notify(message(reason), 'error') } finally { actionBusy.value = false }
}
async function clearOverride() {
  actionBusy.value = true
  try { await api(mediaCatalogOverrideEndpoint(libraryID.value, workID.value), { method: 'DELETE' }); notify('人工匹配已清除并重新识别', 'success'); await load() }
  catch (reason) { notify(message(reason), 'error') } finally { actionBusy.value = false }
}
async function rescrape() {
  actionBusy.value = true
  try { await api(mediaCatalogActionEndpoint(libraryID.value, workID.value, 'rescrape'), { method: 'POST' }); notify('重新刮削完成，源文件未移动', 'success'); await load() }
  catch (reason) { notify(message(reason), 'error') } finally { actionBusy.value = false }
}
async function previewDeletion() {
  actionBusy.value = true; deletionPhrase.value = ''
  try { deletion.value = await api<MediaCatalogDeletionPreview>(mediaCatalogActionEndpoint(libraryID.value, workID.value, 'deletion-preview'), { method: 'POST' }) }
  catch (reason) { notify(message(reason), 'error') } finally { actionBusy.value = false }
}
async function confirmDeletion() {
  if (!deletion.value || deletionPhrase.value !== deletion.value.title) return
  actionBusy.value = true
  try { const result = await api<MediaCatalogDeletionResult>(mediaCatalogActionEndpoint(libraryID.value, workID.value, 'deletion-confirm'), { method: 'POST', body: JSON.stringify({ token: deletion.value.confirmation_token }) }); notify(`已从数据源移除 ${result.removed_files} 个文件`, 'success'); deletion.value = null; await router.push('/discovery/library') }
  catch (reason) { notify(message(reason), 'error'); deletion.value = null } finally { actionBusy.value = false }
}
function toggleSeason(value: number) { expanded.value = expanded.value.includes(value) ? expanded.value.filter(item => item !== value) : [...expanded.value, value] }
async function reorganizationQueued() { reorganizing.value = null; await load() }
function message(reason: unknown) { return reason instanceof Error ? reason.message : '操作失败' }
function formatBytes(value: number) { if (value < 1024) return `${value} B`; const units = ['KB','MB','GB','TB']; let size = value / 1024; let index = 0; while (size >= 1024 && index < units.length-1) { size /= 1024; index++ }; return `${size.toFixed(1)} ${units[index]}` }
onMounted(load); watch(() => route.fullPath, load)
</script>

<template>
  <section class="space-y-5">
    <button class="btn-secondary" @click="router.push('/discovery/library')">返回媒体库</button>
    <div v-if="loading" class="panel py-14 text-center text-muted">正在读取作品与真实季集覆盖…</div>
    <div v-else-if="error" class="semantic-error p-4"><strong>作品详情暂时不可用</strong><p class="mt-1 text-sm">{{ error }}</p><button class="btn-secondary mt-3" @click="load">重试</button></div>
    <template v-else-if="detail">
      <article class="detail-hero panel overflow-hidden p-0" :style="detail.work.backdrop_url ? { backgroundImage: `linear-gradient(90deg, var(--surface) 12%, color-mix(in srgb, var(--surface) 84%, transparent) 60%), url(${detail.work.backdrop_url})` } : undefined">
        <div class="grid gap-6 p-6 md:grid-cols-[11rem_minmax(0,1fr)]"><div class="detail-poster"><img v-if="detail.work.poster_url" :src="detail.work.poster_url" :alt="`${detail.work.title} 海报`"><span v-else>暂无海报</span></div><div class="min-w-0 self-end"><div class="flex flex-wrap gap-2"><span class="status-chip">{{ detail.work.library_works[0]?.library_name || `媒体库 ${libraryID}` }}</span><span class="status-chip">{{ detail.work.kind === 'series' ? '电视剧' : '电影' }}</span><span :class="detail.work.match_status === 'matched' ? 'status-chip status-chip--ready' : 'status-chip status-chip--warning'">{{ detail.work.match_status === 'matched' ? '已匹配' : '待识别' }}</span></div><h1 class="mb-0 mt-3 text-3xl font-850">{{ detail.work.title }}</h1><p v-if="detail.work.original_title" class="mb-0 mt-1 text-sm text-muted">{{ detail.work.original_title }}</p><p class="mb-0 mt-3 max-w-3xl text-sm leading-6 text-muted">{{ detail.work.overview || '暂无简介。' }}</p><p class="mb-0 mt-3 text-sm text-muted">{{ detail.work.file_count }} 个文件 · {{ formatBytes(detail.work.size) }}<template v-if="detail.work.tmdb_id"> · TMDB {{ detail.work.tmdb_id }}</template></p><div class="mt-5 flex flex-wrap gap-2"><button v-if="auth.can(Permissions.MediaLibrariesScan)" class="btn-secondary" :disabled="actionBusy" @click="loadCandidates">修改元数据</button><button v-if="auth.can(Permissions.MediaLibrariesScan)" class="btn-secondary" :disabled="actionBusy || detail.work.manual_override" @click="rescrape">重新刮削</button><button v-if="detail.work.manual_override && auth.can(Permissions.MediaLibrariesScan)" class="btn-secondary" :disabled="actionBusy" @click="clearOverride">清除人工匹配</button><button v-if="auth.can(Permissions.MediaLibrariesMediaDelete)" class="btn-danger" :disabled="actionBusy" @click="previewDeletion">从数据源删除</button></div></div></div>
      </article>

      <article class="panel"><header><h2 class="m-0 text-lg">真实入库覆盖</h2><p class="page-description mt-1 text-sm">以下季集和文件来自当前媒体库扫描事实，不会用 TMDB 总集数冒充已入库内容。</p></header>
        <div v-if="detail.reorganizable_transfers.length" class="mt-4 flex flex-wrap items-center gap-2"><span class="text-xs text-muted">OhMyCine 托管入库记录：</span><button v-for="transfer in detail.reorganizable_transfers" :key="transfer.transfer_task_id" type="button" class="btn-secondary" @click="reorganizing = transfer">修正识别并重新整理 {{ transfer.file_count }} 个文件</button></div>
        <div v-if="detail.work.kind === 'series'" class="mt-4 space-y-3"><section v-for="season in detail.seasons" :key="season.number" class="semantic-list-item overflow-hidden"><button class="flex w-full items-center justify-between gap-3 p-4 text-left" :aria-expanded="expanded.includes(season.number)" @click="toggleSeason(season.number)"><div><strong>{{ season.number === 0 ? '特别篇 / 未知季' : `第 ${season.number} 季` }}</strong><p class="mb-0 mt-1 text-xs text-muted">已入库 {{ season.episodes.length }} 个文件</p></div><span>{{ expanded.includes(season.number) ? '收起' : '展开' }}</span></button><div v-if="expanded.includes(season.number)" class="border-t border-[var(--border)] p-4"><div v-if="season.episodes.length" class="grid gap-2 md:grid-cols-2"><div v-for="episode in season.episodes" :key="episode.id" class="semantic-inset p-3"><strong>{{ episode.episode == null ? '集数未知' : `E${String(episode.episode).padStart(2, '0')}` }} · {{ episode.title }}</strong><p class="mb-0 mt-1 break-all text-xs text-muted">{{ episode.relative_path }} · {{ formatBytes(episode.size) }}</p></div></div><p v-else class="m-0 text-sm text-muted">该季暂无可显示文件。</p></div></section><p v-if="!detail.seasons.length" class="semantic-warning p-4">季集投影暂时为空，但页面仍可进行元数据维护。</p></div>
        <div v-else class="mt-4 space-y-2"><div v-for="file in detail.files" :key="file.id" class="semantic-inset p-3"><strong>{{ file.title }}</strong><p class="mb-0 mt-1 break-all text-xs text-muted">{{ file.relative_path }} · {{ formatBytes(file.size) }}</p></div><p v-if="!detail.files.length" class="text-sm text-muted">暂无可显示文件。</p></div>
      </article>

      <div v-if="metadataOpen" class="modal-backdrop" @click.self="metadataOpen = false"><section class="panel modal-panel max-h-[85vh] overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="metadata-dialog-title"><header class="flex items-center justify-between gap-3"><div><h2 id="metadata-dialog-title" class="m-0 text-lg">修改元数据匹配</h2><p class="page-description mt-1 text-sm">选择经 TMDB 验证的作品身份；不会任意写入不可信字段。</p></div><button class="btn-secondary" aria-label="关闭元数据匹配" @click="metadataOpen = false">关闭</button></header><div v-if="candidatesLoading" class="py-10 text-center text-muted">正在搜索 TMDB 候选…</div><div v-else-if="candidateError" class="semantic-error mt-4 p-4">{{ candidateError }}</div><div v-else class="mt-4 space-y-2"><button v-for="candidate in candidates" :key="`${candidate.media_type}:${candidate.id}`" class="semantic-list-item flex w-full items-center justify-between gap-3 p-4 text-left" :disabled="actionBusy" @click="override(candidate)"><span><strong>{{ candidate.title }}</strong><small class="mt-1 block text-muted">{{ candidate.original_title || '—' }} · {{ candidate.release_year || '年份未知' }}</small></span><span class="status-chip">TMDB {{ candidate.id }}</span></button><p v-if="!candidates.length" class="text-sm text-muted">没有找到候选，可先在媒体库管理页检查识别输入。</p></div></section></div>

      <div v-if="deletion" class="modal-backdrop" @click.self="deletion = null"><section class="panel modal-panel" role="dialog" aria-modal="true" aria-labelledby="deletion-dialog-title"><h2 id="deletion-dialog-title" class="m-0 text-lg text-[var(--danger)]">确认从数据源删除</h2><p class="mt-3 text-sm">将从“{{ deletion.library_name }}”删除《{{ deletion.title }}》的 {{ deletion.file_count }} 个文件（{{ formatBytes(deletion.total_bytes) }}）。该操作不会影响其它媒体库。</p><div class="semantic-warning mt-3 p-3"><p v-for="warning in deletion.warnings" :key="warning" class="mb-1 text-sm">{{ warning }}</p></div><ul class="mt-3 max-h-36 overflow-y-auto text-xs text-muted"><li v-for="path in deletion.relative_paths" :key="path" class="break-all">{{ path }}</li></ul><label class="mt-4 block text-sm">输入作品标题 <strong>{{ deletion.title }}</strong> 以确认<input v-model="deletionPhrase" class="input mt-2 w-full" autocomplete="off"></label><div class="mt-4 flex justify-end gap-2"><button class="btn-secondary" @click="deletion = null">取消</button><button class="btn-danger" :disabled="actionBusy || deletionPhrase !== deletion.title" @click="confirmDeletion">确认删除源文件</button></div></section></div>
      <MediaReorganizationDialog v-if="reorganizing" :open="true" :transfer-task-id="reorganizing.transfer_task_id" :download-task-id="reorganizing.download_task_id" :display-name="detail.work.title" :current-title="detail.work.title" :current-media-type="detail.work.kind === 'movie' ? 'movie' : 'tv'" @close="reorganizing = null" @queued="reorganizationQueued" />
    </template>
  </section>
</template>

<style scoped>
.detail-hero { background-position: center; background-size: cover; }
.detail-poster { aspect-ratio: 2/3; overflow: hidden; border: 1px solid var(--border); border-radius: .8rem; background: var(--surface-muted); display: grid; place-items: center; color: var(--text-subtle); }
.detail-poster img { width: 100%; height: 100%; object-fit: cover; }
.modal-backdrop { position: fixed; inset: 0; z-index: 80; display: grid; place-items: center; padding: 1rem; background: color-mix(in srgb, #000 64%, transparent); }
.modal-panel { width: min(42rem, 100%); }
</style>
