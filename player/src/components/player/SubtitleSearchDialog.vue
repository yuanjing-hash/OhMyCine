<script setup lang="ts">
import type { SubtitleSearchOrigin, SubtitleSearchResult } from '@/services/datasource/types'
import type { SubtitleKeywordMode, SubtitleLanguage } from '@/services/subtitle'
import { computed, ref, watch } from 'vue'

const props = defineProps<{
  open: boolean
  requiresSourceChoice: boolean
  origin: SubtitleSearchOrigin | null
  defaultLanguage: SubtitleLanguage
  mediaTitle: string
  fileName: string
  results: readonly SubtitleSearchResult[]
  loading: boolean
  downloadingId: string | null
  error: string | null
}>()

const emit = defineEmits<{
  close: []
  selectOrigin: [origin: SubtitleSearchOrigin]
  search: [language: SubtitleLanguage, keyword: string, keywordMode: SubtitleKeywordMode]
  download: [result: SubtitleSearchResult]
  back: []
}>()

const language = ref<SubtitleLanguage>(props.defaultLanguage)
const keywordMode = ref<SubtitleKeywordMode>('mediaTitle')
const customKeyword = ref('')
const selectedKeyword = computed(() => {
  if (keywordMode.value === 'custom')
    return customKeyword.value.trim()
  return keywordMode.value === 'fileName' ? props.fileName.trim() : props.mediaTitle.trim()
})
const effectiveKeyword = computed(() => props.origin === 'local' ? selectedKeyword.value : props.mediaTitle.trim())
const effectiveKeywordMode = computed<SubtitleKeywordMode>(() => props.origin === 'local' ? keywordMode.value : 'mediaTitle')
const resultSummary = computed(() => props.loading
  ? '正在搜索字幕'
  : props.results.length > 0
    ? `找到 ${props.results.length} 条字幕`
    : '等待搜索')

const languages: Array<{ value: SubtitleLanguage, label: string }> = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'zh-TW', label: '繁体中文' },
  { value: 'en', label: 'English' },
  { value: 'ja', label: '日本語' },
  { value: 'ko', label: '한국어' },
]

watch(() => props.open, (open) => {
  if (open)
    language.value = props.defaultLanguage
  if (open) {
    keywordMode.value = 'mediaTitle'
    customKeyword.value = ''
  }
})

function resultMeta(result: SubtitleSearchResult): string {
  const details = [
    result.providerName,
    result.language.toUpperCase(),
    result.format?.toUpperCase(),
    result.isHashMatch ? '哈希匹配' : undefined,
    typeof result.rating === 'number' ? `${result.rating.toFixed(1)} 分` : undefined,
    typeof result.downloadCount === 'number' ? `${result.downloadCount} 次下载` : undefined,
  ].filter(Boolean)
  return details.join(' · ')
}

function resultFlags(result: SubtitleSearchResult): string[] {
  return [
    result.hearingImpaired ? '听障字幕' : undefined,
    result.forced ? '强制字幕' : undefined,
    result.aiTranslated ? 'AI 翻译' : undefined,
    result.machineTranslated ? '机器翻译' : undefined,
  ].filter((value): value is string => Boolean(value))
}
</script>

<template>
  <div
    v-if="open"
    class="subtitle-search-overlay fixed inset-0 z-[1200] flex items-center justify-center bg-black/60 p-5 backdrop-blur-md"
    role="presentation"
    @pointerdown.self="emit('close')"
  >
    <section
      class="subtitle-search-sheet theme-immersive-dark glass-panel flex max-h-[min(46rem,calc(100vh-2.5rem))] w-full max-w-2xl flex-col overflow-hidden rounded-3xl border border-white/14 bg-black/76 shadow-2xl"
      role="dialog"
      aria-modal="true"
      aria-label="搜索字幕"
      @pointerdown.stop
    >
      <header class="subtitle-search-header flex shrink-0 items-start justify-between gap-4 border-b border-white/10 px-6 py-5">
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.2em] text-white/36">
            Subtitle Search
          </p>
          <h2 class="mt-1 text-xl font-bold text-white">
            搜索字幕
          </h2>
          <p class="mt-2 text-sm leading-6 text-white/48">
            {{ origin === 'emby' ? '使用 Emby 服务器已配置的字幕提供器。' : origin === 'local' ? '使用 Player 本地字幕提供器，字幕只下载到应用缓存。' : '选择本次搜索使用的字幕来源。' }}
          </p>
        </div>
        <button type="button" class="flex h-9 w-9 items-center justify-center rounded-full bg-white/8 text-white/58 transition-colors hover:bg-white/14 hover:text-white" aria-label="关闭字幕搜索" @click="emit('close')">
          <svg width="17" height="17" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="m5 5 10 10M15 5 5 15" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" /></svg>
        </button>
      </header>

      <div v-if="requiresSourceChoice && !origin" class="grid gap-3 p-6 sm:grid-cols-2">
        <button type="button" class="rounded-2xl border border-white/10 bg-white/6 p-5 text-left transition-colors hover:border-primary/45 hover:bg-primary/14" @click="emit('selectOrigin', 'emby')">
          <span class="block text-base font-bold text-white">Emby 搜索</span>
          <span class="mt-2 block text-sm leading-6 text-white/46">调用当前 Emby 服务器安装并配置的字幕提供器，下载后由 Emby 保存和刷新媒体轨道。</span>
        </button>
        <button type="button" class="rounded-2xl border border-white/10 bg-white/6 p-5 text-left transition-colors hover:border-primary/45 hover:bg-primary/14" @click="emit('selectOrigin', 'local')">
          <span class="block text-base font-bold text-white">本地搜索</span>
          <span class="mt-2 block text-sm leading-6 text-white/46">调用 Player 自己配置的字幕提供器，适用于本地文件和远程媒体源；字幕只保存到 Player 缓存。</span>
        </button>
      </div>

      <template v-else>
        <div class="subtitle-search-controls shrink-0 border-b border-white/8 px-6 py-4">
          <div v-if="origin === 'local'" class="mb-4">
            <span class="text-xs font-semibold uppercase tracking-[0.16em] text-white/38">搜索关键词</span>
            <div class="mt-2 grid grid-cols-3 gap-1 rounded-xl bg-white/5 p-1" role="group" aria-label="字幕搜索关键词来源">
              <button type="button" class="rounded-lg px-3 py-2 text-xs font-semibold transition-colors" :class="keywordMode === 'mediaTitle' ? 'bg-white/14 text-white' : 'text-white/46 hover:text-white/76'" @click="keywordMode = 'mediaTitle'">
                媒体名称
              </button>
              <button type="button" class="rounded-lg px-3 py-2 text-xs font-semibold transition-colors disabled:cursor-not-allowed disabled:opacity-35" :class="keywordMode === 'fileName' ? 'bg-white/14 text-white' : 'text-white/46 hover:text-white/76'" :disabled="!fileName" @click="keywordMode = 'fileName'">
                原始文件名
              </button>
              <button type="button" class="rounded-lg px-3 py-2 text-xs font-semibold transition-colors" :class="keywordMode === 'custom' ? 'bg-white/14 text-white' : 'text-white/46 hover:text-white/76'" @click="keywordMode = 'custom'">
                自定义
              </button>
            </div>
            <input
              v-if="keywordMode === 'custom'"
              v-model="customKeyword"
              class="mt-2 w-full rounded-xl border border-white/10 bg-white/8 px-3 py-2.5 text-sm text-white outline-none placeholder:text-white/28 focus:border-primary/55"
              type="text"
              maxlength="160"
              placeholder="输入片名、年份或发布版本"
              @keydown.enter.prevent="effectiveKeyword && emit('search', language, effectiveKeyword, effectiveKeywordMode)"
            >
            <div v-else class="mt-2 truncate rounded-xl border border-white/8 bg-white/5 px-3 py-2.5 text-sm text-white/66" :title="selectedKeyword">
              {{ selectedKeyword || '当前没有可用关键词' }}
            </div>
          </div>

          <div class="subtitle-search-actions flex flex-wrap items-end gap-3">
            <button v-if="requiresSourceChoice" type="button" class="rounded-xl bg-white/8 px-3 py-2 text-sm font-semibold text-white/68 transition-colors hover:bg-white/14 hover:text-white" @click="emit('back')">
              返回选择来源
            </button>
            <label class="min-w-44 flex-1">
              <span class="text-xs font-semibold uppercase tracking-[0.16em] text-white/38">字幕语言</span>
              <select v-model="language" class="mt-2 w-full rounded-xl border border-white/10 bg-white/8 px-3 py-2.5 text-sm text-white outline-none focus:border-primary/55">
                <option v-for="option in languages" :key="option.value" :value="option.value">
                  {{ option.label }}
                </option>
              </select>
            </label>
            <button type="button" class="rounded-xl bg-primary/80 px-5 py-2.5 text-sm font-bold text-white transition-colors hover:bg-primary disabled:cursor-wait disabled:opacity-55" :disabled="loading || downloadingId !== null || !effectiveKeyword" @click="emit('search', language, effectiveKeyword, effectiveKeywordMode)">
              {{ loading ? '搜索中…' : '开始搜索' }}
            </button>
          </div>
        </div>

        <div class="subtitle-search-results min-h-48 flex-1 overflow-y-auto p-4">
          <div class="mb-3 flex items-center justify-between gap-3 px-1">
            <span class="text-xs font-semibold text-white/48">{{ resultSummary }}</span>
            <span v-if="downloadingId" class="text-xs text-white/36">正在下载并载入</span>
          </div>
          <p v-if="error" class="rounded-2xl border border-red-400/20 bg-red-400/10 px-4 py-3 text-sm leading-6 text-red-100">
            {{ error }}
          </p>
          <div v-else-if="loading" class="subtitle-search-loading flex min-h-52 flex-col items-center justify-center gap-4 px-3 py-8 text-center">
            <span class="subtitle-search-spinner h-7 w-7 rounded-full border-2 border-white/16 border-t-white/78" />
            <div>
              <p class="text-sm font-semibold text-white/76">
                正在搜索可用字幕
              </p>
              <p class="mt-2 text-xs text-white/36">
                结果会在这里直接出现
              </p>
            </div>
          </div>
          <div v-else-if="results.length" class="space-y-2">
            <button
              v-for="result in results"
              :key="result.id"
              type="button"
              class="w-full rounded-2xl border border-white/8 bg-white/5 px-4 py-3 text-left transition-colors hover:border-white/18 hover:bg-white/10 disabled:cursor-wait disabled:opacity-55"
              :disabled="downloadingId !== null"
              @click="emit('download', result)"
            >
              <span class="flex items-start justify-between gap-3">
                <span class="min-w-0">
                  <span class="block truncate text-sm font-bold text-white">{{ result.title }}</span>
                  <span class="mt-1 block text-xs leading-5 text-white/42">{{ resultMeta(result) }}</span>
                  <span v-if="result.comments" class="mt-1 block line-clamp-2 text-xs leading-5 text-white/34">{{ result.comments }}</span>
                  <span v-if="resultFlags(result).length" class="mt-2 flex flex-wrap gap-1.5">
                    <small v-for="flag in resultFlags(result)" :key="flag" class="rounded-full bg-white/8 px-2 py-1 text-[10px] font-semibold text-white/52">{{ flag }}</small>
                  </span>
                </span>
                <span class="shrink-0 rounded-xl bg-white/8 px-3 py-2 text-xs font-semibold text-white/68">
                  {{ downloadingId === result.id ? '加载中…' : '下载并使用' }}
                </span>
              </span>
            </button>
          </div>
          <p v-else class="px-3 py-8 text-center text-sm leading-6 text-white/42">
            选择语言后开始搜索。没有结果时可切换语言或返回更换搜索来源。
          </p>
        </div>
      </template>
    </section>
  </div>
</template>

<style scoped>
.subtitle-search-spinner { animation: subtitle-search-spin 720ms linear infinite; }
@keyframes subtitle-search-spin { to { transform: rotate(360deg); } }

@media (max-width: 767px), (hover: none) and (pointer: coarse) {
  .subtitle-search-overlay { align-items: stretch; padding: 0; background: rgba(5,7,11,.96); backdrop-filter: none; }
  .subtitle-search-sheet { width: 100%; max-width: none; max-height: none; height: 100%; border: 0; border-radius: 0; padding-top: env(safe-area-inset-top); padding-bottom: env(safe-area-inset-bottom); background: rgba(7,9,14,.97); box-shadow: none; backdrop-filter: blur(28px) saturate(1.25); }
  .subtitle-search-header { padding: .85rem 1rem .75rem; background: rgba(7,9,14,.88); }
  .subtitle-search-header p:first-child { display: none; }
  .subtitle-search-header h2 { margin-top: 0; font-size: 1.05rem; }
  .subtitle-search-header h2 + p { margin-top: .3rem; max-width: calc(100vw - 5rem); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: .72rem; line-height: 1.2rem; }
  .subtitle-search-controls { padding: .8rem 1rem; background: rgba(7,9,14,.78); }
  .subtitle-search-actions { display: grid; grid-template-columns: minmax(0,1fr) auto; align-items: end; gap: .65rem; }
  .subtitle-search-actions > button:first-child { grid-column: 1 / -1; justify-self: start; }
  .subtitle-search-actions label { min-width: 0; }
  .subtitle-search-actions > button:last-child { min-width: 7.2rem; min-height: 2.7rem; padding-right: 1rem; padding-left: 1rem; }
  .subtitle-search-results { min-height: 0; padding: .8rem 1rem calc(1rem + env(safe-area-inset-bottom)); }
  .subtitle-search-results button { border-radius: 8px; padding: .85rem; }
  .subtitle-search-results button > span { gap: .65rem; }
  .subtitle-search-results button > span > span:last-child { border-radius: 7px; padding: .55rem .7rem; white-space: nowrap; }
  .subtitle-search-sheet > .grid { grid-template-columns: 1fr; overflow-y: auto; padding: 1rem; }
  .subtitle-search-sheet > .grid button { border-radius: 8px; padding: 1rem; }
  select,input { border-radius: 8px !important; }
}
</style>
