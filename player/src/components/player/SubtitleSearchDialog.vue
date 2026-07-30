<script setup lang="ts">
import type { SubtitleSearchOrigin, SubtitleSearchResult } from '@/services/datasource/types'
import type { SubtitleLanguage } from '@/services/subtitle'
import { ref, watch } from 'vue'

const props = defineProps<{
  open: boolean
  requiresSourceChoice: boolean
  origin: SubtitleSearchOrigin | null
  defaultLanguage: SubtitleLanguage
  results: readonly SubtitleSearchResult[]
  loading: boolean
  downloadingId: string | null
  error: string | null
}>()

const emit = defineEmits<{
  close: []
  selectOrigin: [origin: SubtitleSearchOrigin]
  search: [language: SubtitleLanguage]
  download: [result: SubtitleSearchResult]
  back: []
}>()

const language = ref<SubtitleLanguage>(props.defaultLanguage)

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
    class="fixed inset-0 z-[1200] flex items-center justify-center bg-black/60 p-5 backdrop-blur-md"
    role="presentation"
    @pointerdown.self="emit('close')"
  >
    <section
      class="glass-panel flex max-h-[min(46rem,calc(100vh-2.5rem))] w-full max-w-2xl flex-col overflow-hidden rounded-3xl border border-white/14 bg-black/76 shadow-2xl"
      role="dialog"
      aria-modal="true"
      aria-label="搜索字幕"
      @pointerdown.stop
    >
      <header class="flex items-start justify-between gap-4 border-b border-white/10 px-6 py-5">
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
        <button type="button" class="flex h-9 w-9 items-center justify-center rounded-full bg-white/8 text-xl text-white/58 transition-colors hover:bg-white/14 hover:text-white" aria-label="关闭字幕搜索" @click="emit('close')">
          ×
        </button>
      </header>

      <div v-if="requiresSourceChoice && !origin" class="grid gap-3 p-6 sm:grid-cols-2">
        <button type="button" class="rounded-2xl border border-white/10 bg-white/6 p-5 text-left transition-colors hover:border-primary/45 hover:bg-primary/14" @click="emit('selectOrigin', 'emby')">
          <span class="block text-base font-bold text-white">Emby 搜索</span>
          <span class="mt-2 block text-sm leading-6 text-white/46">调用当前 Emby 服务器安装并配置的字幕提供器，下载后由 Emby 保存和刷新媒体轨道。</span>
        </button>
        <button type="button" class="rounded-2xl border border-white/10 bg-white/6 p-5 text-left transition-colors hover:border-primary/45 hover:bg-primary/14" @click="emit('selectOrigin', 'local')">
          <span class="block text-base font-bold text-white">本地搜索</span>
          <span class="mt-2 block text-sm leading-6 text-white/46">调用 Player 自己配置的 OpenSubtitles 等提供器，不经过 Emby 服务器。</span>
        </button>
      </div>

      <template v-else>
        <div class="flex flex-wrap items-end gap-3 border-b border-white/8 px-6 py-4">
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
          <button type="button" class="rounded-xl bg-primary/80 px-5 py-2.5 text-sm font-bold text-white transition-colors hover:bg-primary disabled:cursor-wait disabled:opacity-55" :disabled="loading || downloadingId !== null" @click="emit('search', language)">
            {{ loading ? '搜索中…' : '开始搜索' }}
          </button>
        </div>

        <div class="min-h-48 flex-1 overflow-y-auto p-4">
          <p v-if="error" class="rounded-2xl border border-red-400/20 bg-red-400/10 px-4 py-3 text-sm leading-6 text-red-100">
            {{ error }}
          </p>
          <p v-else-if="loading" class="px-3 py-8 text-center text-sm text-white/46">
            正在搜索可用字幕…
          </p>
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
