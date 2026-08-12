<script setup lang="ts">
import type { DanmakuSearchAnime, DanmakuSearchEpisode } from '@/services/danmaku/types'
import { computed, ref, watch } from 'vue'

const props = defineProps<{
  open: boolean
  mediaTitle: string
  fileName: string
  initialEpisode: string
  results: readonly DanmakuSearchAnime[]
  hasMore: boolean
  loading: boolean
  selectingEpisodeId: number | null
  error: string | null
  mobileLayout?: boolean
}>()

const emit = defineEmits<{
  close: []
  search: [keyword: string, episode: string]
  select: [anime: DanmakuSearchAnime, episode: DanmakuSearchEpisode]
}>()

const keyword = ref('')
const episode = ref('')
const keywordValid = computed(() => [...keyword.value.trim()].length >= 2)
const resultCount = computed(() => props.results.reduce((total, anime) => total + anime.episodes.length, 0))

watch(() => props.open, (open) => {
  if (!open)
    return
  keyword.value = props.mediaTitle.trim() || props.fileName.trim()
  episode.value = props.initialEpisode
})

function submit() {
  if (keywordValid.value && !props.loading)
    emit('search', keyword.value.trim(), episode.value.trim())
}
</script>

<template>
  <div v-if="open" class="danmaku-search-overlay fixed inset-0 z-[1210] flex items-center justify-center bg-black/60 p-5 backdrop-blur-md" :class="{ 'is-mobile': mobileLayout }" role="presentation" @pointerdown.self="emit('close')">
    <section class="danmaku-search-sheet theme-immersive-dark glass-panel flex max-h-[min(46rem,calc(100vh-2.5rem))] w-full max-w-3xl flex-col overflow-hidden rounded-3xl border border-white/14 bg-black/76 shadow-2xl" role="dialog" aria-modal="true" aria-label="搜索弹幕库" @pointerdown.stop>
      <header class="danmaku-search-header flex shrink-0 items-start justify-between gap-4 border-b border-white/10 px-6 py-5">
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.2em] text-white/36">
            Danmaku Search
          </p>
          <h2 class="mt-1 text-xl font-bold text-white">
            搜索弹幕库
          </h2>
          <p class="mt-2 text-sm leading-6 text-white/48">
            自动匹配不准确时，可搜索作品并手动选择剧集。
          </p>
        </div>
        <button type="button" class="flex h-9 w-9 items-center justify-center rounded-full bg-white/8 text-white/58 transition-colors hover:bg-white/14 hover:text-white" aria-label="关闭弹幕搜索" @click="emit('close')">
          <svg width="17" height="17" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="m5 5 10 10M15 5 5 15" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" /></svg>
        </button>
      </header>

      <form class="danmaku-search-controls grid shrink-0 gap-3 border-b border-white/8 px-6 py-4 sm:grid-cols-[minmax(0,1fr)_8rem_auto] sm:items-end" @submit.prevent="submit">
        <label class="min-w-0">
          <span class="text-xs font-semibold uppercase tracking-[0.16em] text-white/38">作品名称</span>
          <input v-model="keyword" class="mt-2 w-full rounded-xl border border-white/10 bg-white/8 px-3 py-2.5 text-sm text-white outline-none placeholder:text-white/28 focus:border-primary/55" type="text" maxlength="160" placeholder="至少输入两个字符" autofocus>
        </label>
        <label>
          <span class="text-xs font-semibold uppercase tracking-[0.16em] text-white/38">集数（可选）</span>
          <input v-model="episode" class="mt-2 w-full rounded-xl border border-white/10 bg-white/8 px-3 py-2.5 text-sm text-white outline-none placeholder:text-white/28 focus:border-primary/55" type="text" maxlength="12" placeholder="1 / S1 / O1">
        </label>
        <button type="submit" class="rounded-xl bg-primary/80 px-5 py-2.5 text-sm font-bold text-white transition-colors hover:bg-primary disabled:cursor-not-allowed disabled:opacity-45" :disabled="loading || selectingEpisodeId !== null || !keywordValid">
          {{ loading ? '搜索中…' : '搜索' }}
        </button>
      </form>

      <div class="danmaku-search-results min-h-48 flex-1 overflow-y-auto p-4">
        <div class="mb-3 flex items-center justify-between gap-3 px-1 text-xs text-white/42">
          <span>{{ loading ? '正在搜索' : resultCount ? `找到 ${results.length} 部作品、${resultCount} 个剧集` : '等待搜索' }}</span>
          <span v-if="hasMore">结果较多，请缩小关键词</span>
        </div>
        <p v-if="error" class="rounded-2xl border border-red-400/20 bg-red-400/10 px-4 py-3 text-sm leading-6 text-red-100">
          {{ error }}
        </p>
        <div v-else-if="loading" class="flex min-h-52 flex-col items-center justify-center gap-4 text-center">
          <span class="danmaku-search-spinner h-7 w-7 rounded-full border-2 border-white/16 border-t-white/78" />
          <p class="text-sm font-semibold text-white/70">
            正在查找作品和剧集
          </p>
        </div>
        <div v-else-if="results.length" class="space-y-3">
          <article v-for="anime in results" :key="anime.animeId" class="rounded-2xl border border-white/8 bg-white/5 p-4">
            <header class="mb-3 flex items-baseline justify-between gap-3">
              <h3 class="min-w-0 truncate text-sm font-bold text-white">
                {{ anime.animeTitle }}
              </h3>
              <span v-if="anime.typeDescription" class="shrink-0 text-xs text-white/36">{{ anime.typeDescription }}</span>
            </header>
            <div class="grid gap-2 sm:grid-cols-2">
              <button v-for="item in anime.episodes" :key="item.episodeId" type="button" class="flex min-h-11 items-center justify-between gap-3 rounded-xl border border-white/8 bg-white/5 px-3 py-2 text-left text-sm text-white/66 transition-colors hover:border-primary/38 hover:bg-primary/10 hover:text-white disabled:cursor-wait disabled:opacity-50" :disabled="selectingEpisodeId !== null" @click="emit('select', anime, item)">
                <span class="min-w-0 truncate">{{ item.episodeTitle || `剧集 ${item.episodeId}` }}</span>
                <span class="shrink-0 text-xs font-semibold text-white/38">{{ selectingEpisodeId === item.episodeId ? '加载中…' : '选择' }}</span>
              </button>
            </div>
          </article>
        </div>
        <p v-else class="px-3 py-8 text-center text-sm leading-6 text-white/42">
          输入作品名称开始搜索；若结果太多，可填写集数缩小范围。
        </p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.danmaku-search-spinner { animation: danmaku-search-spin 720ms linear infinite; }
@keyframes danmaku-search-spin { to { transform: rotate(360deg); } }
.danmaku-search-overlay.is-mobile { align-items: stretch; padding: 0; background: rgba(5,7,11,.98); backdrop-filter: none; }
.danmaku-search-overlay.is-mobile .danmaku-search-sheet { width: 100%; max-width: none; max-height: none; height: 100%; border: 0; border-radius: 0; padding-top: env(safe-area-inset-top); padding-bottom: env(safe-area-inset-bottom); background: rgba(7,9,14,.98); box-shadow: none; }
.danmaku-search-overlay.is-mobile .danmaku-search-header { padding: .8rem max(1rem,env(safe-area-inset-right)) .7rem max(1rem,env(safe-area-inset-left)); }
.danmaku-search-overlay.is-mobile .danmaku-search-header p:first-child { display: none; }
.danmaku-search-overlay.is-mobile .danmaku-search-header h2 { margin-top: 0; font-size: 1.05rem; }
.danmaku-search-overlay.is-mobile .danmaku-search-header h2 + p { margin-top: .2rem; font-size: .72rem; line-height: 1.2rem; }
.danmaku-search-overlay.is-mobile .danmaku-search-header button { width: 2.75rem; height: 2.75rem; flex: 0 0 auto; }
.danmaku-search-overlay.is-mobile .danmaku-search-controls { padding: .8rem max(1rem,env(safe-area-inset-right)); }
.danmaku-search-overlay.is-mobile .danmaku-search-results { min-height: 0; padding: .8rem max(1rem,env(safe-area-inset-right)) calc(1rem + env(safe-area-inset-bottom)) max(1rem,env(safe-area-inset-left)); }
@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .danmaku-search-overlay:not(.is-mobile) { align-items: stretch; padding: 0; }
  .danmaku-search-overlay:not(.is-mobile) .danmaku-search-sheet { width: 100%; max-width: none; max-height: none; height: 100%; border: 0; border-radius: 0; }
}
</style>
