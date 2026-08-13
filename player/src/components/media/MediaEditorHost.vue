<script setup lang="ts">
import type { EditableArtworkKind, EditableMediaMetadata, MediaDetail, SubtitleSearchResult, SubtitleTrack } from '@/services/datasource/types'
import type { MediaItemActionTarget } from '@/services/mediaActions'
import type { RawEditableContext } from '@/services/mediaEditing'
import type { SubtitleLanguage } from '@/services/subtitle'
import { computed, ref, watch } from 'vue'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { closeMediaEditor, publishFeedback, useMediaEditorRuntime } from '@/services/mediaActions'
import {
  clearSelectedLocalSubtitle,
  describeMediaSubtitleProviders,
  downloadAndSelectLocalSubtitle,
  importAndSelectLocalSubtitle,
  loadRawEditableContext,
  saveRawArtwork,
  saveRawMetadata,
  searchMediaSubtitles,
} from '@/services/mediaEditing'
import { getRawScannedMediaDetail } from '@/services/scraper'
import { loadSubtitleSearchSettings } from '@/services/subtitle'
import { useDataSourceStore } from '@/stores/datasource'

const runtime = useMediaEditorRuntime()
const store = useDataSourceStore()
const loading = ref(false)
const saving = ref(false)
const error = ref<string | null>(null)
const detail = ref<MediaDetail | null>(null)
const rawContext = ref<RawEditableContext | null>(null)
const name = ref('')
const originalTitle = ref('')
const overview = ref('')
const tagline = ref('')
const year = ref('')
const rating = ref('')
const genres = ref('')
const artworkKind = ref<EditableArtworkKind>('Primary')
const artworkUrl = ref('')
const subtitleLanguage = ref<SubtitleLanguage>(loadSubtitleSearchSettings().defaultLanguage)
const subtitleKeyword = ref('')
const subtitleResults = ref<SubtitleSearchResult[]>([])
const subtitleProviderSummary = ref('')
const workingSubtitleId = ref<string | null>(null)

const request = computed(() => runtime.request.value)
const target = computed(() => request.value?.target ?? null)
const source = computed(() => target.value ? store.getSource(target.value.sourceId) : null)
const providerNative = computed(() => target.value?.sourceType === 'emby' || target.value?.sourceType === 'jellyfin')
const title = computed(() => request.value?.kind === 'editMetadata'
  ? '编辑元数据'
  : request.value?.kind === 'editArtwork' ? '编辑图像' : '编辑字幕')
const existingSubtitles = computed<SubtitleTrack[]>(() => detail.value?.subtitles ?? [])

watch(request, request => void loadEditor(request?.target ?? null), { immediate: true })

async function loadEditor(nextTarget: MediaItemActionTarget | null) {
  detail.value = null
  rawContext.value = null
  error.value = null
  subtitleResults.value = []
  if (!nextTarget)
    return
  loading.value = true
  try {
    await store.syncManager()
    const config = store.orderedConfigs.find(entry => entry.id === nextTarget.sourceId)
    rawContext.value = await loadRawEditableContext(nextTarget, config)
    detail.value = rawContext.value
      ? getRawScannedMediaDetail(rawContext.value.cache, nextTarget.itemId)
      ?? getRawScannedMediaDetail(rawContext.value.cache, rawContext.value.candidate.record.providerPath)
      : await store.getSource(nextTarget.sourceId)?.getDetail(nextTarget.itemId) ?? null
    const current = detail.value
    const metadata = rawContext.value?.candidate.scrapeMetadata
      ?? rawContext.value?.cache.scrapedItems?.find(item => item.recordId === rawContext.value?.candidate.record.id)?.metadata
    name.value = current?.name ?? metadata?.title ?? nextTarget.display.name
    originalTitle.value = current?.originalTitle ?? metadata?.originalTitle ?? ''
    overview.value = current?.overview ?? metadata?.overview ?? ''
    tagline.value = current?.tagline ?? ''
    year.value = String(current?.year ?? metadata?.releaseYear ?? '')
    rating.value = String(current?.rating ?? metadata?.rating ?? '')
    genres.value = (current?.genres ?? metadata?.genres ?? []).join('、')
    subtitleKeyword.value = current?.seriesName ?? current?.name ?? nextTarget.display.name
    artworkKind.value = 'Primary'
    artworkUrl.value = current?.posterUrl ?? metadata?.posterUrl ?? ''
    subtitleProviderSummary.value = providerNative.value ? '使用媒体服务原生字幕提供器；下载和删除会写回服务器。' : await describeMediaSubtitleProviders()
  }
  catch (reason) {
    error.value = toSafeErrorMessage(reason, '无法读取媒体编辑信息。')
  }
  finally {
    loading.value = false
  }
}

async function saveMetadata() {
  const currentTarget = target.value
  if (!currentTarget)
    return
  saving.value = true
  error.value = null
  try {
    const metadata: EditableMediaMetadata = {
      name: name.value.trim(),
      originalTitle: originalTitle.value.trim() || undefined,
      overview: overview.value.trim() || undefined,
      tagline: tagline.value.trim() || undefined,
      year: optionalNumber(year.value, true),
      rating: optionalNumber(rating.value, false),
      genres: genres.value.split(/[、,，]/).map(value => value.trim()).filter(Boolean),
    }
    if (rawContext.value)
      await saveRawMetadata(rawContext.value, metadata)
    else if (source.value?.updateMetadata)
      await source.value.updateMetadata(currentTarget.itemId, metadata)
    else
      throw new Error('当前来源不支持元数据编辑。')
    complete('元数据已保存')
  }
  catch (reason) {
    error.value = toSafeErrorMessage(reason, '元数据保存失败。')
  }
  finally {
    saving.value = false
  }
}

async function saveArtwork(remove = false) {
  const currentTarget = target.value
  if (!currentTarget)
    return
  saving.value = true
  error.value = null
  try {
    if (rawContext.value) {
      await saveRawArtwork(rawContext.value, mapArtworkKind(artworkKind.value), remove ? undefined : artworkUrl.value)
    }
    else if (remove && source.value?.deleteArtwork) {
      await source.value.deleteArtwork(currentTarget.itemId, artworkKind.value)
    }
    else if (!remove && source.value?.updateArtworkFromUrl) {
      await source.value.updateArtworkFromUrl(currentTarget.itemId, artworkKind.value, artworkUrl.value)
    }
    else {
      throw new Error('当前来源不支持该图片操作。')
    }
    complete(remove ? '图片已删除' : '图片已更新')
  }
  catch (reason) {
    error.value = toSafeErrorMessage(reason, '图片操作失败。')
  }
  finally {
    saving.value = false
  }
}

async function searchSubtitles() {
  const currentTarget = target.value
  const currentDetail = detail.value
  if (!currentTarget || !currentDetail)
    return
  saving.value = true
  error.value = null
  subtitleResults.value = []
  try {
    if (providerNative.value && source.value?.searchSubtitles) {
      subtitleResults.value = await source.value.searchSubtitles({
        itemId: currentTarget.itemId,
        language: subtitleLanguage.value,
        title: subtitleKeyword.value,
        year: currentDetail.year,
        mediaType: currentDetail.type,
        seasonNumber: currentDetail.seasonNumber,
        episodeNumber: currentDetail.episodeNumber,
        imdbId: currentDetail.imdbId,
        tmdbId: currentDetail.tmdbId,
      })
    }
    else {
      subtitleResults.value = await searchMediaSubtitles(currentDetail, subtitleLanguage.value, subtitleKeyword.value)
    }
    if (!subtitleResults.value.length)
      error.value = '没有找到符合条件的字幕，可以更换语言或关键词重试。'
  }
  catch (reason) {
    error.value = toSafeErrorMessage(reason, '字幕搜索失败。')
  }
  finally {
    saving.value = false
  }
}

async function downloadSubtitle(result: SubtitleSearchResult) {
  const currentTarget = target.value
  if (!currentTarget)
    return
  workingSubtitleId.value = result.id
  error.value = null
  try {
    if (result.origin === 'emby' && source.value?.downloadSubtitle) {
      await source.value.downloadSubtitle({ itemId: currentTarget.itemId, result })
    }
    else {
      await downloadAndSelectLocalSubtitle(currentTarget, result)
    }
    await loadEditor(currentTarget)
    publishFeedback({ id: Date.now(), kind: 'success', message: '字幕已下载并应用' })
  }
  catch (reason) {
    error.value = toSafeErrorMessage(reason, '字幕下载失败。')
  }
  finally {
    workingSubtitleId.value = null
  }
}

async function importSubtitle() {
  const currentTarget = target.value
  if (!currentTarget)
    return
  try {
    if (await importAndSelectLocalSubtitle(currentTarget))
      publishFeedback({ id: Date.now(), kind: 'success', message: '字幕已导入 Player 缓存并应用' })
  }
  catch (reason) {
    error.value = toSafeErrorMessage(reason, '字幕导入失败。')
  }
}

async function deleteSubtitle(track: SubtitleTrack) {
  const currentTarget = target.value
  if (!currentTarget)
    return
  workingSubtitleId.value = `track:${track.index}`
  error.value = null
  try {
    if (providerNative.value && source.value?.deleteSubtitle)
      await source.value.deleteSubtitle(currentTarget.itemId, track.index)
    else
      await clearSelectedLocalSubtitle(currentTarget)
    await loadEditor(currentTarget)
    publishFeedback({ id: Date.now(), kind: 'success', message: providerNative.value ? '服务器字幕已删除' : '已取消该媒体的本地默认字幕' })
  }
  catch (reason) {
    error.value = toSafeErrorMessage(reason, '字幕移除失败。')
  }
  finally {
    workingSubtitleId.value = null
  }
}

function complete(message: string) {
  const currentTarget = target.value
  if (currentTarget) {
    source.value?.clearCache?.()
    store.invalidateSourceRootSnapshot(currentTarget.sourceId)
    store.invalidateHomeCache()
    void store.loadHomeSections({ force: true, background: true })
  }
  publishFeedback({ id: Date.now(), kind: 'success', message })
  closeMediaEditor()
}

function optionalNumber(value: string, integer: boolean): number | undefined {
  if (!value.trim())
    return undefined
  const parsed = Number(value)
  if (!Number.isFinite(parsed) || (integer && !Number.isInteger(parsed)))
    throw new Error(integer ? '年份必须是整数。' : '评分必须是数字。')
  return parsed
}

function mapArtworkKind(kind: EditableArtworkKind): 'poster' | 'backdrop' | 'logo' {
  return kind === 'Primary' ? 'poster' : kind === 'Backdrop' ? 'backdrop' : 'logo'
}
</script>

<template>
  <Teleport to="body">
    <div v-if="request" class="fixed inset-0 z-[1250] flex items-center justify-center bg-black/70 p-4 backdrop-blur-md" @pointerdown.self="closeMediaEditor">
      <section class="theme-immersive-dark glass-panel flex max-h-[min(50rem,calc(100vh-2rem))] w-full max-w-3xl flex-col overflow-hidden rounded-3xl border border-white/14 bg-black/82 shadow-2xl" role="dialog" aria-modal="true" :aria-label="title">
        <header class="flex items-start justify-between gap-4 border-b border-white/10 px-6 py-5">
          <div class="min-w-0">
            <p class="text-xs font-semibold uppercase tracking-[0.18em] text-white/36">
              {{ providerNative ? 'Provider Native Editor' : 'Player Local Override' }}
            </p>
            <h2 class="mt-1 text-xl font-bold text-white">
              {{ title }}
            </h2>
            <p class="mt-1 truncate text-sm text-white/48">
              {{ target?.display.name }}
            </p>
          </div>
          <button type="button" class="flex h-10 w-10 items-center justify-center rounded-full bg-white/8 text-xl text-white/60 hover:bg-white/14 hover:text-white" aria-label="关闭编辑器" @click="closeMediaEditor">
            ×
          </button>
        </header>

        <div class="min-h-0 flex-1 overflow-y-auto p-6">
          <p v-if="!providerNative" class="mb-4 rounded-2xl border border-primary/20 bg-primary/8 px-4 py-3 text-xs leading-5 text-white/58">
            修改只保存在 Player 数据库和受控缓存，不会写入或改名源目录中的文件。
          </p>
          <p v-if="error" class="mb-4 rounded-2xl border border-red-400/20 bg-red-400/10 px-4 py-3 text-sm text-red-100">
            {{ error }}
          </p>
          <p v-if="loading" class="py-16 text-center text-sm text-white/45">
            正在读取编辑信息…
          </p>

          <form v-else-if="request.kind === 'editMetadata'" class="space-y-4" @submit.prevent="saveMetadata">
            <div class="grid gap-4 md:grid-cols-2">
              <label><span class="editor-label">标题</span><input v-model="name" class="editor-input" maxlength="500" required></label>
              <label><span class="editor-label">原始标题</span><input v-model="originalTitle" class="editor-input" maxlength="500"></label>
              <label><span class="editor-label">年份</span><input v-model="year" class="editor-input" inputmode="numeric" placeholder="可选"></label>
              <label><span class="editor-label">评分</span><input v-model="rating" class="editor-input" inputmode="decimal" placeholder="可选"></label>
            </div>
            <label class="block"><span class="editor-label">类型 / 标签</span><input v-model="genres" class="editor-input" placeholder="用逗号或顿号分隔"></label>
            <label class="block"><span class="editor-label">标语</span><input v-model="tagline" class="editor-input" maxlength="1000"></label>
            <label class="block"><span class="editor-label">简介</span><textarea v-model="overview" class="editor-input min-h-36 resize-y" maxlength="20000" /></label>
            <div class="flex justify-end gap-3">
              <button type="button" class="editor-secondary" @click="closeMediaEditor">
                取消
              </button><button class="editor-primary" :disabled="saving">
                {{ saving ? '保存中…' : '保存元数据' }}
              </button>
            </div>
          </form>

          <div v-else-if="request.kind === 'editArtwork'" class="space-y-5">
            <div class="grid gap-4 md:grid-cols-[12rem_1fr]">
              <div class="aspect-[2/3] overflow-hidden rounded-2xl bg-white/6">
                <img v-if="artworkUrl" :src="artworkUrl" alt="图片预览" class="h-full w-full object-cover"><div v-else class="flex h-full items-center justify-center text-sm text-white/32">
                  暂无预览
                </div>
              </div>
              <div class="space-y-4">
                <label class="block"><span class="editor-label">图片类型</span><select v-model="artworkKind" class="editor-input"><option value="Primary">海报</option><option value="Backdrop">背景图</option><option value="Logo">标题 Logo</option></select></label>
                <label class="block"><span class="editor-label">HTTP(S) 图片地址</span><input v-model="artworkUrl" class="editor-input" type="url" placeholder="https://…"></label>
                <p class="text-xs leading-5 text-white/40">
                  {{ providerNative ? '由媒体服务下载并保存图片，实际可用性受账号权限和服务端版本限制。' : '地址经安全校验后写入本地元数据覆盖层；带令牌或签名参数的 URL 会被拒绝。' }}
                </p>
              </div>
            </div>
            <div class="flex flex-wrap justify-end gap-3">
              <button type="button" class="editor-danger" :disabled="saving" @click="saveArtwork(true)">
                删除此图片
              </button><button type="button" class="editor-secondary" @click="closeMediaEditor">
                取消
              </button><button type="button" class="editor-primary" :disabled="saving || !artworkUrl.trim()" @click="saveArtwork(false)">
                {{ saving ? '保存中…' : '应用图片' }}
              </button>
            </div>
          </div>

          <div v-else class="space-y-5">
            <p class="rounded-2xl border border-white/8 bg-white/5 px-4 py-3 text-xs leading-5 text-white/52">
              {{ subtitleProviderSummary }}
            </p>
            <div v-if="existingSubtitles.length" class="space-y-2">
              <h3 class="text-sm font-bold text-white">
                现有字幕
              </h3>
              <div v-for="track in existingSubtitles" :key="track.index" class="flex items-center justify-between gap-3 rounded-2xl border border-white/8 bg-white/5 px-4 py-3">
                <div>
                  <p class="text-sm font-semibold text-white">
                    {{ track.title || track.language || `字幕 ${track.index}` }}
                  </p><p class="mt-1 text-xs text-white/38">
                    {{ track.language }} · {{ track.codec || '未知格式' }} · {{ track.source === 'embedded' ? '内嵌' : '外挂' }}
                  </p>
                </div>
                <button v-if="providerNative && track.source === 'external'" type="button" class="editor-danger" :disabled="workingSubtitleId !== null" @click="deleteSubtitle(track)">
                  删除
                </button>
              </div>
            </div>
            <div class="grid gap-3 md:grid-cols-[1fr_10rem_auto]">
              <label><span class="editor-label">搜索词</span><input v-model="subtitleKeyword" class="editor-input" @keydown.enter.prevent="searchSubtitles"></label>
              <label><span class="editor-label">语言</span><select v-model="subtitleLanguage" class="editor-input"><option value="zh-CN">简体中文</option><option value="zh-TW">繁体中文</option><option value="en">English</option><option value="ja">日本語</option><option value="ko">한국어</option></select></label>
              <button type="button" class="editor-primary self-end" :disabled="saving || !subtitleKeyword.trim()" @click="searchSubtitles">
                {{ saving ? '搜索中…' : '搜索' }}
              </button>
            </div>
            <div v-if="subtitleResults.length" class="space-y-2">
              <button v-for="result in subtitleResults" :key="result.id" type="button" class="flex w-full items-center justify-between gap-4 rounded-2xl border border-white/8 bg-white/5 px-4 py-3 text-left hover:border-primary/45 hover:bg-primary/8 disabled:opacity-50" :disabled="workingSubtitleId !== null" @click="downloadSubtitle(result)">
                <span class="min-w-0"><span class="block truncate text-sm font-bold text-white">{{ result.title }}</span><span class="mt-1 block text-xs text-white/40">{{ result.providerName }} · {{ result.language }} · {{ result.format || '字幕' }}</span></span><span class="shrink-0 text-xs font-semibold text-primary">{{ workingSubtitleId === result.id ? '处理中…' : '下载并应用' }}</span>
              </button>
            </div>
            <div class="flex flex-wrap justify-between gap-3">
              <button v-if="!providerNative" type="button" class="editor-secondary" @click="importSubtitle">
                导入本地字幕
              </button><button v-if="!providerNative" type="button" class="editor-secondary" @click="target && clearSelectedLocalSubtitle(target)">
                取消本地默认字幕
              </button><button type="button" class="editor-secondary ml-auto" @click="closeMediaEditor">
                完成
              </button>
            </div>
          </div>
        </div>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.editor-label { display: block; margin-bottom: .45rem; font-size: .75rem; font-weight: 650; color: rgba(255,255,255,.48); }
.editor-input { width: 100%; border: 1px solid rgba(255,255,255,.11); border-radius: 1rem; background: rgba(255,255,255,.07); padding: .72rem .9rem; color: white; outline: none; }
.editor-input:focus { border-color: color-mix(in srgb, var(--color-primary, #8bd5ff) 60%, transparent); }
.editor-primary,.editor-secondary,.editor-danger { border-radius: .9rem; padding: .68rem 1rem; font-size: .82rem; font-weight: 700; transition: background .15s, opacity .15s; }
.editor-primary { background: var(--color-primary, #8bd5ff); color: #05070a; }
.editor-secondary { background: rgba(255,255,255,.09); color: rgba(255,255,255,.72); }
.editor-danger { border: 1px solid rgba(248,113,113,.22); background: rgba(248,113,113,.1); color: rgb(254 202 202); }
button:disabled { cursor: wait; opacity: .5; }
@media (max-width: 767px) { section[role="dialog"] { max-height: 100vh; height: 100%; border-radius: 0; } }
</style>
