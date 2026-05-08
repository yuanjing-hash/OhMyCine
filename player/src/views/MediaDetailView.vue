<script setup lang="ts">
import type { AudioTrack, MediaDetail, MediaItem, MediaLibrary, MediaSourceOption, SubtitleTrack } from '@/services/datasource/types'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import MediaGrid from '@/components/media/MediaGrid.vue'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { useDataSourceStore } from '@/stores/datasource'

const route = useRoute()
const router = useRouter()
const store = useDataSourceStore()

const sourceId = computed(() => route.params.sourceId as string)
const itemId = computed(() => route.params.itemId as string)
const detail = ref<MediaDetail | null>(null)
const isLoading = ref(false)
const errorMessage = ref<string | null>(null)
const selectedMediaSourceId = ref<string>('')
const selectedAudioIndex = ref<number | null>(null)
const selectedSubtitleIndex = ref<number | null>(null)

const heroStyle = computed(() => {
  const backdrop = detail.value?.backdropUrl
  return backdrop ? { backgroundImage: `url(${backdrop})` } : {}
})
const mediaSources = computed(() => detail.value?.mediaSources ?? [])
const selectedMediaSource = computed(() => mediaSources.value.find(source => source.id === selectedMediaSourceId.value) ?? mediaSources.value[0])
const audioTracks = computed(() => detail.value?.audioTracks ?? [])
const subtitleTracks = computed(() => detail.value?.subtitles ?? [])
const runtimeLabel = computed(() => detail.value?.duration ? `${Math.round(detail.value.duration / 60)} 分钟` : '')
const sourceLabel = computed(() => selectedMediaSource.value ? describeMediaSource(selectedMediaSource.value) : '默认版本')

onMounted(loadDetail)

watch(itemId, loadDetail)

async function loadDetail() {
  isLoading.value = true
  errorMessage.value = null
  detail.value = null
  selectedMediaSourceId.value = ''
  selectedAudioIndex.value = null
  selectedSubtitleIndex.value = null

  try {
    store.loadConfigs()
    await store.syncManager()
    const source = store.getSource(sourceId.value)
    if (!source)
      throw new Error('数据源不可用，请检查设置或重新登录。')

    const nextDetail = await source.getDetail(itemId.value)
    detail.value = nextDetail
    selectedMediaSourceId.value = nextDetail.mediaSources?.[0]?.id ?? ''
    selectedAudioIndex.value = nextDetail.audioTracks?.find(track => track.isDefault)?.index ?? nextDetail.audioTracks?.[0]?.index ?? null
    selectedSubtitleIndex.value = nextDetail.subtitles?.find(track => track.isDefault)?.index ?? nextDetail.subtitles?.[0]?.index ?? null
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, '媒体详情加载失败。')
  }
  finally {
    isLoading.value = false
  }
}

async function play() {
  if (!detail.value)
    return

  isLoading.value = true
  errorMessage.value = null
  try {
    const source = store.getSource(sourceId.value)
    if (!source)
      throw new Error('数据源不可用，请检查设置或重新登录。')
    const path = await source.getStreamURL(detail.value.id)
    await router.push({
      name: 'player',
      query: {
        title: detail.value.name,
        path,
        sourceId: sourceId.value,
        itemId: detail.value.id,
        mediaSourceId: selectedMediaSource.value?.id,
        audioIndex: selectedAudioIndex.value ?? undefined,
        subtitleIndex: selectedSubtitleIndex.value ?? undefined,
      },
    })
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, '无法获取播放地址。')
  }
  finally {
    isLoading.value = false
  }
}

function openRelated(item: MediaItem | MediaLibrary) {
  if (!('path' in item))
    return
  void router.push({ name: 'media-detail', params: { sourceId: sourceId.value, itemId: item.id } })
}

function describeMediaSource(source: MediaSourceOption): string {
  return [source.container?.toUpperCase(), source.size ? formatBytes(source.size) : undefined, source.isStrm ? 'STRM' : undefined, source.isRemote ? '远程' : undefined]
    .filter(Boolean)
    .join(' · ') || source.name
}

function formatBytes(value: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  return `${size.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

function trackLabel(track: AudioTrack | SubtitleTrack): string {
  const codec = 'codec' in track ? ` · ${track.codec}` : ''
  const channels = 'channels' in track && track.channels ? ` · ${track.channels}ch` : ''
  const title = 'title' in track ? track.title : undefined
  return `${title ?? track.language}${codec}${channels}${track.isDefault ? ' · 默认' : ''}`
}
</script>

<template>
  <div class="detail-view min-h-screen bg-[var(--color-bg)] text-white">
    <div v-if="isLoading && !detail" class="flex min-h-screen items-center justify-center text-white/45">
      正在加载媒体详情…
    </div>

    <div v-else-if="errorMessage && !detail" class="flex min-h-screen flex-col items-center justify-center gap-4 px-6 text-center">
      <p class="text-lg font-semibold text-white">
        详情不可用
      </p>
      <p class="max-w-xl text-sm leading-6 text-white/50">
        {{ errorMessage }}
      </p>
      <button class="rounded-2xl bg-white/10 px-5 py-3 text-sm text-white transition-colors hover:bg-white/16" @click="router.back()">
        返回
      </button>
    </div>

    <template v-else-if="detail">
      <section class="relative min-h-[68vh] overflow-hidden bg-cover bg-center" :style="heroStyle">
        <div class="absolute inset-0 bg-gradient-to-r from-black/94 via-black/62 to-black/20" />
        <div class="absolute inset-0 bg-gradient-to-t from-[var(--color-bg)] via-transparent to-black/40" />
        <div class="relative flex min-h-[68vh] items-end gap-8 px-6 pb-12 pl-24 pt-24 lg:px-12 lg:pl-28">
          <div class="hidden w-56 flex-shrink-0 overflow-hidden rounded-[1.8rem] border border-white/12 bg-white/6 shadow-2xl md:block">
            <img v-if="detail.posterUrl" :src="detail.posterUrl" :alt="detail.name" class="aspect-[2/3] w-full object-cover" loading="eager" decoding="async">
            <div v-else class="flex aspect-[2/3] items-center justify-center p-6 text-center text-sm text-white/45">
              {{ detail.name }}
            </div>
          </div>

          <div class="max-w-4xl">
            <p class="text-xs uppercase tracking-[0.28em] text-white/42">
              OhMyCine Detail
            </p>
            <h1 class="mt-3 text-4xl font-bold leading-tight drop-shadow-2xl lg:text-6xl">
              {{ detail.name }}
            </h1>
            <div class="mt-4 flex flex-wrap items-center gap-3 text-sm text-white/68">
              <span v-if="detail.rating" class="rounded-full bg-yellow-400/16 px-3 py-1 text-yellow-100">★ {{ detail.rating.toFixed(1) }}</span>
              <span v-if="detail.year">{{ detail.year }}</span>
              <span v-if="runtimeLabel">{{ runtimeLabel }}</span>
              <span v-if="detail.resolution">{{ detail.resolution }}</span>
              <span v-if="detail.genres?.length">{{ detail.genres.slice(0, 4).join(' / ') }}</span>
            </div>
            <p v-if="detail.overview" class="mt-5 max-w-3xl text-base leading-8 text-white/68 line-clamp-5">
              {{ detail.overview }}
            </p>
            <div class="mt-7 flex flex-wrap items-center gap-3">
              <button class="flex items-center gap-2 rounded-full bg-white px-7 py-3 text-sm font-bold text-black shadow-xl transition-transform hover:scale-105" @click="play">
                <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M4 2l10 6-10 6V2z" /></svg>
                播放
              </button>
              <span class="rounded-full border border-white/12 bg-white/8 px-4 py-3 text-xs text-white/58">{{ sourceLabel }}</span>
            </div>
          </div>
        </div>
      </section>

      <main class="space-y-10 px-6 pb-14 pl-24 lg:px-12 lg:pl-28">
        <div v-if="errorMessage" class="rounded-2xl border border-red-400/20 bg-red-400/10 px-5 py-4 text-sm text-red-100">
          {{ errorMessage }}
        </div>

        <section class="grid gap-5 lg:grid-cols-3">
          <div class="glass-panel rounded-[1.6rem] p-5">
            <h2 class="text-base font-semibold">
              版本
            </h2>
            <div class="mt-4 space-y-2">
              <button
                v-for="source in mediaSources"
                :key="source.id"
                class="w-full rounded-2xl border px-4 py-3 text-left text-sm transition-colors"
                :class="selectedMediaSourceId === source.id ? 'border-white/34 bg-white/14 text-white' : 'border-white/8 bg-white/5 text-white/62 hover:bg-white/10'"
                @click="selectedMediaSourceId = source.id"
              >
                <span class="block font-medium">版本 {{ mediaSources.findIndex(candidate => candidate.id === source.id) + 1 }}</span>
                <span class="mt-1 block text-xs text-white/42">{{ describeMediaSource(source) }}</span>
              </button>
              <p v-if="!mediaSources.length" class="text-sm text-white/42">
                使用 Emby 默认媒体版本。
              </p>
            </div>
          </div>

          <div class="glass-panel rounded-[1.6rem] p-5">
            <h2 class="text-base font-semibold">
              音轨
            </h2>
            <select v-model="selectedAudioIndex" class="mt-4 w-full rounded-2xl border border-white/10 bg-black/20 px-4 py-3 text-sm text-white outline-none">
              <option v-for="track in audioTracks" :key="track.index" :value="track.index">
                {{ trackLabel(track) }}
              </option>
            </select>
            <p v-if="!audioTracks.length" class="mt-4 text-sm text-white/42">
              未返回音轨信息。
            </p>
          </div>

          <div class="glass-panel rounded-[1.6rem] p-5">
            <h2 class="text-base font-semibold">
              字幕
            </h2>
            <select v-model="selectedSubtitleIndex" class="mt-4 w-full rounded-2xl border border-white/10 bg-black/20 px-4 py-3 text-sm text-white outline-none">
              <option :value="null">
                不预选字幕
              </option>
              <option v-for="track in subtitleTracks" :key="track.index" :value="track.index">
                {{ trackLabel(track) }}
              </option>
            </select>
            <p v-if="!subtitleTracks.length" class="mt-4 text-sm text-white/42">
              未返回字幕信息。
            </p>
          </div>
        </section>

        <section v-if="detail.stills?.length">
          <h2 class="mb-4 text-xl font-bold">
            剧照 / 截图
          </h2>
          <div class="flex gap-4 overflow-x-auto cinema-scrollbar">
            <img v-for="still in detail.stills" :key="still" :src="still" :alt="detail.name" class="h-40 w-72 flex-shrink-0 rounded-3xl object-cover" loading="lazy" decoding="async">
          </div>
        </section>

        <section class="grid gap-6 lg:grid-cols-[1.5fr_1fr]">
          <div class="glass-panel rounded-[1.6rem] p-6">
            <h2 class="text-xl font-bold">
              演职员
            </h2>
            <p class="mt-4 text-sm leading-7 text-white/58">
              <span v-if="detail.directors?.length">导演：{{ detail.directors.join('、') }}</span>
              <br v-if="detail.directors?.length && detail.cast?.length">
              <span v-if="detail.cast?.length">演员：{{ detail.cast.slice(0, 12).join('、') }}</span>
              <span v-if="!detail.directors?.length && !detail.cast?.length">暂无演职员信息。</span>
            </p>
          </div>
          <div class="glass-panel rounded-[1.6rem] p-6">
            <h2 class="text-xl font-bold">
              媒体信息
            </h2>
            <dl class="mt-4 space-y-2 text-sm text-white/58">
              <div class="flex justify-between gap-4">
                <dt>视频</dt><dd>{{ detail.codec ?? '未知' }}</dd>
              </div>
              <div class="flex justify-between gap-4">
                <dt>音频</dt><dd>{{ detail.audioCodec ?? '未知' }}</dd>
              </div>
              <div class="flex justify-between gap-4">
                <dt>IMDB</dt><dd>{{ detail.imdbId ?? '—' }}</dd>
              </div>
              <div class="flex justify-between gap-4">
                <dt>TMDB</dt><dd>{{ detail.tmdbId ?? '—' }}</dd>
              </div>
            </dl>
          </div>
        </section>

        <section v-if="detail.collections?.length">
          <h2 class="mb-4 text-xl font-bold">
            合集
          </h2>
          <MediaGrid :items="detail.collections" @select="openRelated" @play="play" />
        </section>

        <section v-if="detail.similarItems?.length">
          <h2 class="mb-4 text-xl font-bold">
            相似内容
          </h2>
          <MediaGrid :items="detail.similarItems" @select="openRelated" @play="play" />
        </section>
      </main>
    </template>
  </div>
</template>
