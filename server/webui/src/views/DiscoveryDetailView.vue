<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { Permissions } from '@/auth/generated-permissions'
import { coverageStatusLabel, discoveryCoveragePath, discoveryDetailPath, discoveryDetailRoute, discoveryResourceRoute, providerLabel, type DiscoveryDetail, type DiscoveryMediaType, type DiscoveryProviderCode, type DiscoveryWork, type MediaCoverage } from '@/discovery'
import { notify } from '@/toast'
import FollowEditorDialog from '@/components/FollowEditorDialog.vue'
import type { FollowSummary } from '@/follows'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const loading = ref(true)
const error = ref('')
const detail = ref<DiscoveryDetail | null>(null)
const coverage = ref<MediaCoverage | null>(null)
const coverageLoading = ref(false)
const coverageError = ref('')
const expandedSeasons = ref<number[]>([])
const followEditorOpen = ref(false)

const identity = computed(() => ({
  provider: String(route.params.provider) as DiscoveryProviderCode,
  media_type: String(route.params.mediaType) as DiscoveryMediaType,
  provider_id: String(route.params.providerID),
}))

async function load() {
  loading.value = true
  error.value = ''
  coverage.value = null
  coverageError.value = ''
  expandedSeasons.value = []
  try {
    detail.value = await api<DiscoveryDetail>(discoveryDetailPath(identity.value))
    void loadCoverage()
  }
  catch (reason) { error.value = message(reason) }
  finally { loading.value = false }
}

async function loadCoverage() {
  const work = detail.value?.work
  if (!work?.tmdb_id) return
  coverageLoading.value = true
  coverageError.value = ''
  try { coverage.value = await api<MediaCoverage>(discoveryCoveragePath(work.media_type, work.tmdb_id)) }
  catch (reason) { coverageError.value = reason instanceof Error ? reason.message : '媒体库覆盖率加载失败' }
  finally { coverageLoading.value = false }
}

function searchResources() { if (detail.value) void router.push(discoveryResourceRoute(detail.value.work)) }
function toggleSeason(season: number) { expandedSeasons.value = expandedSeasons.value.includes(season) ? expandedSeasons.value.filter(item => item !== season) : [...expandedSeasons.value, season] }
function libraryNames(ids: number[]) { return ids.map(id => coverage.value?.libraries.find(item => item.id === id)?.name).filter(Boolean).join('、') }

function subscribe() {
	if (!auth.can(Permissions.FollowsCreate)) { notify('当前账户没有创建订阅的权限。', 'warning'); return }
  if (!detail.value?.work.tmdb_id || detail.value.work.media_type !== 'tv') { notify('只有已确认 TMDB 身份的电视剧可以订阅。', 'warning'); return }
  followEditorOpen.value = true
}
function followSaved(follow: FollowSummary) { followEditorOpen.value = false; notify(`已创建《${follow.title}》自动追更，正在排队检查缺集。`, 'success') }
function openWork(work: DiscoveryWork) { void router.push(discoveryDetailRoute(work)) }
function message(reason: unknown) { return reason instanceof Error ? reason.message : '作品详情加载失败' }
onMounted(load)
watch(() => route.fullPath, load)
</script>

<template>
  <section class="space-y-5">
    <button class="btn-secondary" type="button" @click="router.back()">返回推荐</button>
    <div v-if="loading" class="panel py-14 text-center text-muted">正在获取作品详情与演职人员…</div>
    <div v-else-if="error" class="semantic-error p-4"><strong>详情暂时不可用</strong><p class="mt-1 text-sm">{{ error }}</p><button class="btn-secondary mt-3" @click="load">重试</button></div>
    <template v-else-if="detail">
      <article class="detail-hero panel overflow-hidden p-0" :style="detail.work.backdrop_url ? { backgroundImage: `linear-gradient(90deg, var(--surface) 12%, color-mix(in srgb, var(--surface) 82%, transparent) 58%, color-mix(in srgb, var(--surface) 32%, transparent)), url(${detail.work.backdrop_url})` } : undefined">
        <div class="grid gap-6 p-6 md:grid-cols-[11rem_minmax(0,1fr)]">
          <div class="detail-poster"><img v-if="detail.work.poster_url" :src="detail.work.poster_url" :alt="`${detail.work.title} 海报`" /><span v-else>暂无海报</span></div>
          <div class="min-w-0 self-end">
            <div class="flex flex-wrap gap-2"><span class="status-chip">{{ providerLabel(detail.work.provider) }}</span><span class="status-chip">{{ detail.work.media_type === 'tv' ? '剧集' : '电影' }}</span><span v-if="detail.work.rating != null" class="status-chip status-chip--ready">{{ detail.work.rating.toFixed(1) }}</span></div>
            <h1 class="mb-0 mt-3 text-3xl font-850">{{ detail.work.title }}</h1>
            <p v-if="detail.work.original_title" class="mb-0 mt-1 text-sm text-muted">{{ detail.work.original_title }}</p>
            <p class="mb-0 mt-3 text-sm text-muted">{{ [detail.work.year, detail.runtime_minutes ? `${detail.runtime_minutes} 分钟` : '', ...detail.genres].filter(Boolean).join(' · ') }}</p>
            <p v-if="detail.tagline" class="mb-0 mt-3 font-650">{{ detail.tagline }}</p>
            <p class="mb-0 mt-3 max-w-3xl text-sm leading-6 text-muted">{{ detail.work.overview || '暂无简介。' }}</p>
            <div class="mt-5 flex flex-wrap gap-3"><button class="btn-primary" :disabled="!detail.work.tmdb_id" @click="searchResources">搜索</button><button v-if="detail.work.media_type === 'tv' && auth.can(Permissions.FollowsCreate)" class="btn-secondary" :disabled="!detail.work.tmdb_id || coverageLoading" @click="subscribe">订阅</button></div>
          </div>
        </div>
      </article>

      <article class="panel" aria-labelledby="media-coverage-title">
        <header class="flex flex-wrap items-center justify-between gap-3"><div><h2 id="media-coverage-title" class="m-0 text-lg">媒体库覆盖率</h2><p class="page-description mt-1 text-sm">按当前可读媒体库中的可信 TMDB 身份对账；未播或日期未知不会计为缺失。</p></div><button v-if="coverageError" class="btn-secondary" @click="loadCoverage">重试</button></header>
        <div v-if="coverageLoading" class="py-8 text-center text-muted">正在对账媒体库与 TMDB 季集信息…</div>
        <div v-else-if="coverageError" class="semantic-warning mt-4 p-4"><strong>覆盖率暂时不可用</strong><p class="mb-0 mt-1 text-sm">{{ coverageError }}</p></div>
        <template v-else-if="coverage">
          <div class="mt-4 flex flex-wrap items-center gap-2"><span :class="coverage.status === 'present' ? 'status-chip status-chip--ready' : coverage.status === 'missing' ? 'status-chip status-chip--warning' : 'status-chip'">{{ coverageStatusLabel(coverage.status) }}</span><span class="text-subtle text-xs">{{ coverage.libraries.length }} 个可读媒体库 · {{ coverage.freshness.library_scan_state === 'complete' ? '扫描事实完整' : coverage.freshness.library_scan_state === 'partial' ? '扫描事实不完整' : '尚未完成扫描' }}</span></div>
          <div v-if="coverage.movie" class="semantic-inset mt-4 p-4"><strong>{{ coverage.movie.present ? '这部电影已经入库' : coverage.status === 'missing' ? '这部电影尚未入库' : '暂时无法确认是否入库' }}</strong><p v-if="coverage.movie.library_ids.length" class="mb-0 mt-1 text-sm text-muted">存在于：{{ libraryNames(coverage.movie.library_ids) }}</p></div>
          <div v-else-if="coverage.tv" class="mt-4 space-y-3">
            <div class="grid gap-2 sm:grid-cols-5"><div v-for="item in [['总集数', coverage.tv.counts.total], ['已入库', coverage.tv.counts.present], ['已播缺失', coverage.tv.counts.missing], ['未播', coverage.tv.counts.future], ['未知', coverage.tv.counts.unknown]]" :key="String(item[0])" class="semantic-inset p-3"><span class="text-subtle block text-xs">{{ item[0] }}</span><strong class="mt-1 block text-lg">{{ item[1] }}</strong></div></div>
            <section v-for="season in coverage.tv.seasons" :key="season.season_number" class="semantic-list-item overflow-hidden">
              <button class="flex w-full items-center gap-4 p-4 text-left" :aria-expanded="expandedSeasons.includes(season.season_number)" @click="toggleSeason(season.season_number)"><img v-if="season.poster_url" :src="season.poster_url" :alt="`${season.name} 海报`" class="h-20 w-14 shrink-0 rounded object-cover" loading="lazy" /><div class="min-w-0 flex-1"><div class="flex flex-wrap items-center gap-2"><strong>{{ season.name || `第 ${season.season_number} 季` }}</strong><span v-if="season.special" class="status-chip">特别篇 · 不计普通缺集</span><span :class="season.status === 'present' ? 'status-chip status-chip--ready' : season.status === 'missing' || season.status === 'partial' ? 'status-chip status-chip--warning' : 'status-chip'">{{ coverageStatusLabel(season.status) }}</span></div><p class="mb-0 mt-2 text-xs text-muted">共 {{ season.counts.total }} · 已入库 {{ season.counts.present }} · 已播缺失 {{ season.counts.missing }} · 未播 {{ season.counts.future }} · 未知 {{ season.counts.unknown }}</p></div><span aria-hidden="true">{{ expandedSeasons.includes(season.season_number) ? '收起' : '展开' }}</span></button>
              <div v-if="expandedSeasons.includes(season.season_number)" class="border-t border-[var(--border)] p-4"><div v-if="season.episodes.length" class="grid gap-2 md:grid-cols-2"><div v-for="episode in season.episodes" :key="episode.episode_number" class="semantic-inset flex items-start justify-between gap-3 p-3"><div><strong>E{{ String(episode.episode_number).padStart(2, '0') }} · {{ episode.name || '名称未知' }}</strong><p class="mb-0 mt-1 text-xs text-muted">{{ episode.air_date || '播出日期未知' }}<template v-if="episode.library_ids.length"> · {{ libraryNames(episode.library_ids) }}</template></p></div><span :class="episode.status === 'present' ? 'status-chip status-chip--ready' : episode.status === 'missing' ? 'status-chip status-chip--warning' : 'status-chip'">{{ coverageStatusLabel(episode.status) }}</span></div></div><p v-else class="mb-0 text-sm text-muted">该季 TMDB 集信息暂时不可用，因此不会推断缺集。</p></div>
            </section>
          </div>
        </template>
      </article>

      <div class="grid gap-5 xl:grid-cols-[minmax(0,2fr)_minmax(18rem,1fr)]">
        <article class="panel"><h2 class="m-0 text-lg">演职人员</h2><div v-if="detail.cast.length" class="people-row mt-4"><div v-for="person in detail.cast" :key="`${person.tmdb_id || person.name}:${person.character}`" class="person-card"><div class="person-card__avatar"><img v-if="person.profile_url" :src="person.profile_url" :alt="`${person.name} 照片`" loading="lazy" /><span v-else>{{ person.name.slice(0, 1) }}</span></div><strong :title="person.name">{{ person.name }}</strong><small :title="person.character || person.role || '演员'">{{ person.character || person.role || '演员' }}</small></div></div><p v-else class="mb-0 mt-3 text-sm text-muted">暂未获取到演职人员。</p></article>
        <article class="panel"><h2 class="m-0 text-lg">作品信息</h2><dl class="mt-4 grid gap-3 text-sm"><div><dt class="text-subtle text-xs">导演</dt><dd class="m-0 mt-1">{{ detail.directors.map(item => item.name).join('、') || '—' }}</dd></div><div><dt class="text-subtle text-xs">编剧</dt><dd class="m-0 mt-1">{{ detail.writers.map(item => item.name).join('、') || '—' }}</dd></div><div><dt class="text-subtle text-xs">制片</dt><dd class="m-0 mt-1">{{ detail.studios.join('、') || '—' }}</dd></div><div><dt class="text-subtle text-xs">地区 / 语言</dt><dd class="m-0 mt-1">{{ [...detail.countries, ...detail.spoken_languages].join('、') || '—' }}</dd></div><div v-if="detail.work.tmdb_id"><dt class="text-subtle text-xs">TMDB ID</dt><dd class="m-0 mt-1 font-mono">{{ detail.work.tmdb_id }}</dd></div></dl></article>
      </div>

      <article v-if="detail.recommendations.length" class="panel overflow-hidden p-0"><header class="border-b border-[var(--border)] px-5 py-4"><h2 class="m-0 text-lg">推荐</h2></header><div class="discovery-row p-5"><button v-for="work in detail.recommendations" :key="`recommendation:${work.provider_id}`" class="discovery-poster" @click="openWork(work)"><div class="discovery-poster__image"><img v-if="work.poster_url" :src="work.poster_url" :alt="`${work.title} 海报`" loading="lazy" /><span v-else>暂无海报</span></div><strong :title="work.title">{{ work.title }}</strong><small>{{ work.year || '年份未知' }}<template v-if="work.rating != null"> · {{ work.rating.toFixed(1) }}</template></small></button></div></article>
      <article v-if="detail.similar.length" class="panel overflow-hidden p-0"><header class="border-b border-[var(--border)] px-5 py-4"><h2 class="m-0 text-lg">类似作品</h2></header><div class="discovery-row p-5"><button v-for="work in detail.similar" :key="`similar:${work.provider_id}`" class="discovery-poster" @click="openWork(work)"><div class="discovery-poster__image"><img v-if="work.poster_url" :src="work.poster_url" :alt="`${work.title} 海报`" loading="lazy" /><span v-else>暂无海报</span></div><strong :title="work.title">{{ work.title }}</strong><small>{{ work.year || '年份未知' }}<template v-if="work.rating != null"> · {{ work.rating.toFixed(1) }}</template></small></button></div></article>
      <FollowEditorDialog v-if="followEditorOpen && detail.work.tmdb_id" :tmdb-id="detail.work.tmdb_id" :title="detail.work.title" :year="detail.work.year" :poster-ref="detail.work.poster_url" :initial-seasons="coverage?.tv?.seasons.filter(item => item.counts.missing > 0).map(item => item.season_number)" @close="followEditorOpen = false" @saved="followSaved" />
    </template>
  </section>
</template>

<style scoped>
.detail-hero { background-position: center; background-size: cover; }
.detail-poster { aspect-ratio: 2 / 3; overflow: hidden; border: 1px solid var(--border); border-radius: 0.8rem; background: var(--surface-muted); display: grid; place-items: center; color: var(--text-subtle); }
.detail-poster img { width: 100%; height: 100%; object-fit: cover; }
.people-row { display: grid; grid-auto-flow: column; grid-auto-columns: minmax(7.5rem, 8.5rem); gap: 0.85rem; overflow-x: auto; padding-bottom: 0.5rem; }
.person-card { min-width: 0; text-align: center; }
.person-card__avatar { width: 5.5rem; height: 5.5rem; margin: 0 auto 0.65rem; overflow: hidden; border: 1px solid var(--border); border-radius: 999px; background: var(--surface-muted); display: grid; place-items: center; color: var(--text-subtle); font-size: 1.25rem; }
.person-card__avatar img { width: 100%; height: 100%; object-fit: cover; }
.person-card strong, .person-card small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.person-card small { margin-top: 0.2rem; color: var(--text-muted); }
</style>
