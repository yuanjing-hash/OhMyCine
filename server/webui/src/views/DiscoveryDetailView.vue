<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { discoveryDetailPath, discoveryDetailRoute, providerLabel, type DiscoveryDetail, type DiscoveryMediaType, type DiscoveryProviderCode, type DiscoveryWork } from '@/discovery'
import { notify } from '@/toast'

const route = useRoute()
const router = useRouter()
const loading = ref(true)
const error = ref('')
const detail = ref<DiscoveryDetail | null>(null)

const identity = computed(() => ({
  provider: String(route.params.provider) as DiscoveryProviderCode,
  media_type: String(route.params.mediaType) as DiscoveryMediaType,
  provider_id: String(route.params.providerID),
}))

async function load() {
  loading.value = true
  error.value = ''
  try { detail.value = await api<DiscoveryDetail>(discoveryDetailPath(identity.value)) }
  catch (reason) { error.value = message(reason) }
  finally { loading.value = false }
}

function searchBy(mode: 'title' | 'tmdb_id') {
  const work = detail.value?.work
  if (!work) return
  const query: Record<string, string> = { title: work.title, media_type: work.media_type, search_by: mode }
  if (work.year) query.year = String(work.year)
  if (work.tmdb_id) query.tmdb_id = String(work.tmdb_id)
  void router.push({ path: '/discovery/explore', query })
}

function subscribe() { notify('订阅能力将在下一阶段接入；当前不会创建任何订阅任务。', 'info') }
function openWork(work: DiscoveryWork) { void router.push(discoveryDetailRoute(work)) }
function message(reason: unknown) { return reason instanceof Error ? reason.message : '作品详情加载失败' }
onMounted(load)
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
            <div class="mt-5 flex flex-wrap gap-3"><button class="btn-primary" @click="searchBy('title')">按标题搜索资源</button><button class="btn-secondary" :disabled="!detail.work.tmdb_id" @click="searchBy('tmdb_id')">按 TMDB ID 搜索</button><button class="btn-secondary" @click="subscribe">订阅（即将支持）</button></div>
          </div>
        </div>
      </article>

      <div class="grid gap-5 xl:grid-cols-[minmax(0,2fr)_minmax(18rem,1fr)]">
        <article class="panel"><h2 class="m-0 text-lg">演职人员</h2><div v-if="detail.cast.length" class="people-row mt-4"><div v-for="person in detail.cast" :key="`${person.tmdb_id || person.name}:${person.character}`" class="person-card"><div class="person-card__avatar"><img v-if="person.profile_url" :src="person.profile_url" :alt="`${person.name} 照片`" loading="lazy" /><span v-else>{{ person.name.slice(0, 1) }}</span></div><strong :title="person.name">{{ person.name }}</strong><small :title="person.character || person.role || '演员'">{{ person.character || person.role || '演员' }}</small></div></div><p v-else class="mb-0 mt-3 text-sm text-muted">暂未获取到演职人员。</p></article>
        <article class="panel"><h2 class="m-0 text-lg">作品信息</h2><dl class="mt-4 grid gap-3 text-sm"><div><dt class="text-subtle text-xs">导演</dt><dd class="m-0 mt-1">{{ detail.directors.map(item => item.name).join('、') || '—' }}</dd></div><div><dt class="text-subtle text-xs">编剧</dt><dd class="m-0 mt-1">{{ detail.writers.map(item => item.name).join('、') || '—' }}</dd></div><div><dt class="text-subtle text-xs">制片</dt><dd class="m-0 mt-1">{{ detail.studios.join('、') || '—' }}</dd></div><div><dt class="text-subtle text-xs">地区 / 语言</dt><dd class="m-0 mt-1">{{ [...detail.countries, ...detail.spoken_languages].join('、') || '—' }}</dd></div><div v-if="detail.work.tmdb_id"><dt class="text-subtle text-xs">TMDB ID</dt><dd class="m-0 mt-1 font-mono">{{ detail.work.tmdb_id }}</dd></div></dl></article>
      </div>

      <article v-if="detail.recommendations.length" class="panel overflow-hidden p-0"><header class="border-b border-[var(--border)] px-5 py-4"><h2 class="m-0 text-lg">推荐</h2></header><div class="discovery-row p-5"><button v-for="work in detail.recommendations" :key="`recommendation:${work.provider_id}`" class="discovery-poster" @click="openWork(work)"><div class="discovery-poster__image"><img v-if="work.poster_url" :src="work.poster_url" :alt="`${work.title} 海报`" loading="lazy" /><span v-else>暂无海报</span></div><strong :title="work.title">{{ work.title }}</strong><small>{{ work.year || '年份未知' }}<template v-if="work.rating != null"> · {{ work.rating.toFixed(1) }}</template></small></button></div></article>
      <article v-if="detail.similar.length" class="panel overflow-hidden p-0"><header class="border-b border-[var(--border)] px-5 py-4"><h2 class="m-0 text-lg">类似作品</h2></header><div class="discovery-row p-5"><button v-for="work in detail.similar" :key="`similar:${work.provider_id}`" class="discovery-poster" @click="openWork(work)"><div class="discovery-poster__image"><img v-if="work.poster_url" :src="work.poster_url" :alt="`${work.title} 海报`" loading="lazy" /><span v-else>暂无海报</span></div><strong :title="work.title">{{ work.title }}</strong><small>{{ work.year || '年份未知' }}<template v-if="work.rating != null"> · {{ work.rating.toFixed(1) }}</template></small></button></div></article>
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
