<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { buildDiscoveryPath, buildRefreshPayload, discoveryDetailRoute, discoveryRefreshPath, providerLabel, type DiscoveryCategory, type DiscoveryOverview, type DiscoveryProviderCode, type DiscoverySection, type DiscoveryWork } from '@/discovery'
import { notify } from '@/toast'

const router = useRouter()
const provider = ref<DiscoveryProviderCode>('tmdb')
const category = ref<DiscoveryCategory>('movie')
const loading = ref(true)
const error = ref('')
const overview = ref<DiscoveryOverview>({ providers: [], sections: [], updated_at: '' })
const refreshing = ref('')

const visibleSections = computed(() => overview.value.sections.filter(section => section.provider === provider.value && section.category === category.value))
const hero = computed(() => visibleSections.value.flatMap(section => section.items).find(item => item.backdrop_url) ?? visibleSections.value.flatMap(section => section.items)[0])

async function load() {
  loading.value = true
  error.value = ''
  try { overview.value = await api<DiscoveryOverview>(buildDiscoveryPath(provider.value)) }
  catch (reason) { error.value = message(reason) }
  finally { loading.value = false }
}

async function selectProvider(value: DiscoveryProviderCode) {
  if (provider.value === value) return
  provider.value = value
  await load()
}

async function refreshSection(section: DiscoverySection) {
  refreshing.value = `${section.provider}:${section.code}`
  try {
    const updated = await api<DiscoverySection>(discoveryRefreshPath, { method: 'POST', body: JSON.stringify(buildRefreshPayload(section)) })
    overview.value = { ...overview.value, sections: overview.value.sections.map(item => item.provider === updated.provider && item.code === updated.code ? updated : item) }
    notify(`${section.title} 已刷新`, 'success')
  } catch (reason) { notify(message(reason), 'error') }
  finally { refreshing.value = '' }
}

function openWork(work: DiscoveryWork) { void router.push(discoveryDetailRoute(work)) }
function message(reason: unknown) { return reason instanceof Error ? reason.message : '推荐内容加载失败' }
onMounted(load)
</script>

<template>
  <section class="space-y-5">
    <header class="flex flex-wrap items-end justify-between gap-3">
      <div><p class="text-xs font-700 uppercase tracking-widest text-[var(--text-subtle)]">Discovery</p><h1 class="mt-1 text-2xl font-800">影视推荐</h1><p class="page-description mt-1">TMDB 与豆瓣各自提供真实栏目；打开作品后才搜索 PT 资源。</p></div>
      <div class="flex gap-2" role="group" aria-label="推荐来源">
        <button v-for="item in ([['tmdb', 'TMDB'], ['douban', '豆瓣']] as const)" :key="item[0]" class="btn-secondary" :class="{ '!border-[var(--accent)] !bg-[var(--accent-soft)] !text-[var(--accent)]': provider === item[0] }" @click="selectProvider(item[0])">{{ item[1] }}</button>
      </div>
    </header>

    <nav class="panel flex flex-wrap gap-2 p-2" aria-label="推荐内容分类">
      <button v-for="item in ([['movie', '电影'], ['tv', '电视剧'], ['anime', '动漫']] as const)" :key="item[0]" class="btn-secondary min-w-24" :class="{ '!border-[var(--accent)] !bg-[var(--accent-soft)] !text-[var(--accent)]': category === item[0] }" @click="category = item[0]">{{ item[1] }}</button>
    </nav>

    <div v-if="loading" class="panel py-12 text-center text-muted">正在载入真实推荐栏目…</div>
    <div v-else-if="error" class="semantic-error p-4"><strong>推荐暂时不可用</strong><p class="mt-1 text-sm">{{ error }}</p><button class="btn-secondary mt-3" @click="load">重试</button></div>
    <template v-else>
      <article v-if="hero" class="discovery-hero panel" :style="hero.backdrop_url ? { backgroundImage: `linear-gradient(90deg, var(--surface) 20%, color-mix(in srgb, var(--surface) 38%, transparent)), url(${hero.backdrop_url})` } : undefined">
        <div class="max-w-xl"><span class="status-chip">{{ providerLabel(hero.provider) }}</span><h2 class="mt-3 text-3xl font-850">{{ hero.title }}</h2><p class="mt-2 line-clamp-3 text-sm text-muted">{{ hero.overview || '进入详情后可选择搜索资源或订阅。' }}</p><button class="btn-primary mt-4" @click="openWork(hero)">查看详情</button></div>
      </article>

      <article v-for="section in visibleSections" :key="`${section.provider}:${section.code}`" class="panel overflow-hidden p-0">
        <header class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] px-5 py-4">
          <div class="flex items-center gap-2"><h2 class="text-lg font-750">{{ section.title }}</h2><span class="status-chip">{{ providerLabel(section.provider) }}</span><span v-if="section.stale" class="status-chip status-chip--warning">旧缓存</span></div>
          <button class="btn-secondary" :disabled="refreshing === `${section.provider}:${section.code}`" @click="refreshSection(section)">{{ refreshing === `${section.provider}:${section.code}` ? '刷新中…' : '刷新栏目' }}</button>
        </header>
        <div v-if="section.error_code && !section.items.length" class="p-5 text-sm text-muted">该来源暂时不可用（{{ section.error_code }}），可以单独重试，不影响其它栏目。</div>
        <div v-else class="discovery-row p-5">
          <button v-for="work in section.items" :key="`${work.provider}:${work.provider_id}`" class="discovery-poster" @click="openWork(work)">
            <div class="discovery-poster__image"><img v-if="work.poster_url" :src="work.poster_url" :alt="`${work.title} 海报`" loading="lazy" referrerpolicy="no-referrer" /><span v-else>暂无海报</span></div>
            <strong :title="work.title">{{ work.title }}</strong><small>{{ work.year || '年份未知' }}<template v-if="work.rating != null"> · {{ work.rating.toFixed(1) }}</template></small>
          </button>
        </div>
        <footer class="px-5 pb-4 text-xs text-subtle">更新于 {{ new Date(section.fetched_at).toLocaleString() }}</footer>
      </article>
      <div v-if="!visibleSections.length" class="panel py-12 text-center text-muted">当前来源还没有可展示的真实栏目。</div>
    </template>
  </section>
</template>
