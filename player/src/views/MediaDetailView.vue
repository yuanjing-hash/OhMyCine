<script setup lang="ts">
import type { AudioTrack, DataSource, MediaDetail, MediaItem, MediaLibrary, MediaSourceOption, SubtitleTrack } from '@/services/datasource/types'
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
const seasons = ref<MediaItem[]>([])
const episodes = ref<MediaItem[]>([])
const selectedSeasonId = ref<string>('')
const isLoading = ref(false)
const isSeriesContentLoading = ref(false)
const isPlaying = ref(false)
const errorMessage = ref<string | null>(null)
const seriesErrorMessage = ref<string | null>(null)
const selectedMediaSourceId = ref<string>('')
const selectedAudioIndex = ref<number | null>(null)
const selectedSubtitleIndex = ref<number | null>(null)

const heroStyle = computed(() => {
  const backdrop = detail.value?.backdropUrl
  return backdrop ? { backgroundImage: `url(${backdrop})` } : {}
})
const isSeriesDetail = computed(() => detail.value?.type === 'series')
const isPlayableDetail = computed(() => detail.value != null && !['series', 'season', 'folder'].includes(detail.value.type))
const mediaSources = computed(() => detail.value?.mediaSources ?? [])
const visibleMediaSources = computed(() => isSeriesDetail.value ? [] : mediaSources.value.filter(hasMeaningfulMediaSource))
const selectedMediaSource = computed(() => visibleMediaSources.value.find(source => source.id === selectedMediaSourceId.value) ?? visibleMediaSources.value[0])
const audioTracks = computed(() => isPlayableDetail.value ? (detail.value?.audioTracks ?? []) : [])
const subtitleTracks = computed(() => isPlayableDetail.value ? (detail.value?.subtitles ?? []) : [])
const runtimeLabel = computed(() => detail.value?.duration ? `${Math.round(detail.value.duration / 60)} 分钟` : '')
const sourceLabel = computed(() => selectedMediaSource.value ? describeMediaSource(selectedMediaSource.value) : '默认版本')
const mediaInfoRows = computed(() => {
  const current = detail.value
  if (!current)
    return []

  return [
    current.codec ? ['视频', current.codec] : undefined,
    current.audioCodec ? ['音频', current.audioCodec] : undefined,
    current.imdbId ? ['IMDB', current.imdbId] : undefined,
    current.tmdbId ? ['TMDB', String(current.tmdbId)] : undefined,
  ].filter((row): row is [string, string] => Boolean(row))
})
const selectedSeason = computed(() => seasons.value.find(season => season.id === selectedSeasonId.value))

onMounted(loadDetail)

watch([sourceId, itemId], loadDetail)

async function loadDetail() {
  isLoading.value = true
  errorMessage.value = null
  seriesErrorMessage.value = null
  detail.value = null
  seasons.value = []
  episodes.value = []
  selectedSeasonId.value = ''
  selectedMediaSourceId.value = ''
  selectedAudioIndex.value = null
  selectedSubtitleIndex.value = null

  try {
    const source = await resolveSource()
    const nextDetail = await source.getDetail(itemId.value)
    detail.value = nextDetail
    const selectableMediaSources = (nextDetail.mediaSources ?? []).filter(hasMeaningfulMediaSource)
    selectedMediaSourceId.value = selectableMediaSources[0]?.id ?? ''
    selectedAudioIndex.value = nextDetail.audioTracks?.find(track => track.isDefault)?.index ?? nextDetail.audioTracks?.[0]?.index ?? null
    selectedSubtitleIndex.value = nextDetail.subtitles?.find(track => track.isDefault)?.index ?? nextDetail.subtitles?.[0]?.index ?? null

    if (nextDetail.type === 'series')
      await loadSeriesSeasons(source, nextDetail.id)
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, '媒体详情加载失败。')
  }
  finally {
    isLoading.value = false
  }
}

async function resolveSource(): Promise<DataSource> {
  store.loadConfigs()
  await store.syncManager()
  const source = store.getSource(sourceId.value)
  if (!source)
    throw new Error('数据源不可用，请检查设置或重新登录。')
  return source
}

async function loadSeriesSeasons(source: DataSource, seriesId: string) {
  isSeriesContentLoading.value = true
  seriesErrorMessage.value = null
  try {
    const children = await source.list(seriesId)
    const seasonItems = children.filter(item => item.type === 'season' || item.type === 'folder')
    const episodeItems = children.filter(item => item.type === 'episode')
    seasons.value = seasonItems

    if (seasonItems.length > 0) {
      selectedSeasonId.value = seasonItems[0].id
      await loadSeasonEpisodes(source, seasonItems[0].id)
      return
    }

    episodes.value = episodeItems
  }
  catch (error) {
    seriesErrorMessage.value = toSafeErrorMessage(error, '剧集季/集信息加载失败。')
  }
  finally {
    isSeriesContentLoading.value = false
  }
}

async function selectSeason(season: MediaItem) {
  if (season.id === selectedSeasonId.value && episodes.value.length > 0)
    return

  selectedSeasonId.value = season.id
  isSeriesContentLoading.value = true
  seriesErrorMessage.value = null
  try {
    episodes.value = []
    const source = await resolveSource()
    await loadSeasonEpisodes(source, season.id)
  }
  catch (error) {
    seriesErrorMessage.value = toSafeErrorMessage(error, '分集列表加载失败。')
  }
  finally {
    isSeriesContentLoading.value = false
  }
}

async function loadSeasonEpisodes(source: DataSource, seasonId: string) {
  const items = await source.list(seasonId)
  episodes.value = items.filter(item => item.type === 'episode' || item.type === 'file' || item.type === 'movie')
}

async function playItem(item?: MediaItem) {
  const target = item ?? detail.value
  if (!target || ['series', 'season', 'folder'].includes(target.type))
    return

  isPlaying.value = true
  errorMessage.value = null
  try {
    const source = await resolveSource()
    const path = await source.getStreamURL(target.id)
    await router.push({
      name: 'player',
      query: {
        title: target.name,
        path,
        sourceId: sourceId.value,
        itemId: target.id,
        mediaSourceId: item ? undefined : selectedMediaSource.value?.id,
        audioIndex: item ? undefined : (selectedAudioIndex.value ?? undefined),
        subtitleIndex: item ? undefined : (selectedSubtitleIndex.value ?? undefined),
      },
    })
  }
  catch (error) {
    errorMessage.value = toSafeErrorMessage(error, '无法获取播放地址。')
  }
  finally {
    isPlaying.value = false
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

function hasMeaningfulMediaSource(source: MediaSourceOption): boolean {
  return Boolean(source.container || source.size || source.bitrate || source.isRemote || source.isStrm || (source.name && !/^default$|^source-\d+$/i.test(source.name)))
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
  const language = track.language && track.language !== 'Unknown' ? track.language : undefined
  const codec = 'codec' in track && track.codec !== 'unknown' ? ` · ${track.codec}` : ''
  const channels = 'channels' in track && track.channels ? ` · ${track.channels}ch` : ''
  const title = 'title' in track ? track.title : undefined
  return `${title ?? language ?? '未命名轨道'}${codec}${channels}${track.isDefault ? ' · 默认' : ''}`
}

function episodeTitle(item: MediaItem): string {
  return item.episodeNumber == null ? item.name : `第 ${item.episodeNumber} 集 · ${item.name}`
}

function itemRuntime(item: MediaItem): string {
  return item.duration ? `${Math.round(item.duration / 60)} 分钟` : ''
}
</script>

<template>
  <div class="detail-view min-h-screen bg-[var(--color-bg)] text-white">
    <div v-if="isLoading && !detail" class="pointer-events-none flex min-h-screen items-center justify-center text-white/45" aria-live="polite">
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
        <div class="pointer-events-none absolute inset-0 bg-gradient-to-r from-black/94 via-black/62 to-black/20" />
        <div class="pointer-events-none absolute inset-0 bg-gradient-to-t from-[var(--color-bg)] via-transparent to-black/40" />
        <div class="relative flex min-h-[68vh] items-end gap-8 px-6 pb-12 pl-24 pt-24 lg:px-12 lg:pl-28">
          <div class="hidden w-56 flex-shrink-0 overflow-hidden rounded-[1.8rem] border border-white/12 bg-white/6 shadow-2xl md:block">
            <img v-if="detail.posterUrl" :src="detail.posterUrl" :alt="detail.name" class="aspect-[2/3] w-full object-cover" loading="eager" decoding="async">
            <div v-else class="flex aspect-[2/3] items-center justify-center p-6 text-center text-sm text-white/45">
              {{ detail.name }}
            </div>
          </div>

          <div class="max-w-4xl">
            <p class="text-xs uppercase tracking-[0.28em] text-white/42">
              {{ isSeriesDetail ? 'OhMyCine Series' : 'OhMyCine Detail' }}
            </p>
            <h1 class="mt-3 text-4xl font-bold leading-tight drop-shadow-2xl lg:text-6xl">
              {{ detail.name }}
            </h1>
            <div class="mt-4 flex flex-wrap items-center gap-3 text-sm text-white/68">
              <span v-if="detail.rating" class="rounded-full bg-yellow-400/16 px-3 py-1 text-yellow-100">★ {{ detail.rating.toFixed(1) }}</span>
              <span v-if="detail.year">{{ detail.year }}</span>
              <span v-if="runtimeLabel">{{ runtimeLabel }}</span>
              <span v-if="detail.resolution && !isSeriesDetail">{{ detail.resolution }}</span>
              <span v-if="detail.genres?.length">{{ detail.genres.slice(0, 4).join(' / ') }}</span>
            </div>
            <p v-if="detail.overview" class="mt-5 max-w-3xl text-base leading-8 text-white/68 line-clamp-5">
              {{ detail.overview }}
            </p>
            <div class="mt-7 flex flex-wrap items-center gap-3">
              <button v-if="isPlayableDetail" class="flex items-center gap-2 rounded-full bg-white px-7 py-3 text-sm font-bold text-black shadow-xl transition-transform hover:scale-105 disabled:cursor-not-allowed disabled:opacity-60" :disabled="isPlaying" @click="playItem()">
                <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M4 2l10 6-10 6V2z" /></svg>
                {{ isPlaying ? '正在准备…' : '播放' }}
              </button>
              <span v-if="isSeriesDetail" class="rounded-full border border-white/12 bg-white/8 px-4 py-3 text-xs text-white/58">选择季和分集后播放</span>
              <span v-else-if="visibleMediaSources.length" class="rounded-full border border-white/12 bg-white/8 px-4 py-3 text-xs text-white/58">{{ sourceLabel }}</span>
            </div>
          </div>
        </div>
      </section>

      <main class="space-y-10 px-6 pb-14 pl-24 lg:px-12 lg:pl-28">
        <div v-if="errorMessage" class="rounded-2xl border border-red-400/20 bg-red-400/10 px-5 py-4 text-sm text-red-100">
          {{ errorMessage }}
        </div>

        <section v-if="isSeriesDetail" class="glass-panel rounded-[1.8rem] p-6">
          <div class="flex flex-wrap items-end justify-between gap-4">
            <div>
              <p class="text-xs uppercase tracking-[0.24em] text-white/36">
                Seasons & Episodes
              </p>
              <h2 class="mt-2 text-2xl font-bold">
                季与分集
              </h2>
            </div>
            <p v-if="selectedSeason" class="text-sm text-white/46">
              {{ selectedSeason.name }} · {{ episodes.length }} 集
            </p>
          </div>

          <div v-if="seriesErrorMessage" class="mt-5 rounded-2xl border border-red-400/20 bg-red-400/10 px-4 py-3 text-sm text-red-100">
            {{ seriesErrorMessage }}
          </div>

          <div v-if="seasons.length" class="mt-6 flex gap-3 overflow-x-auto cinema-scrollbar pb-2">
            <button
              v-for="season in seasons"
              :key="season.id"
              class="flex-shrink-0 rounded-full border px-5 py-3 text-sm transition-colors"
              :class="selectedSeasonId === season.id ? 'border-white/36 bg-white/16 text-white' : 'border-white/10 bg-white/6 text-white/58 hover:bg-white/10'"
              @click="selectSeason(season)"
            >
              {{ season.name }}
            </button>
          </div>

          <div v-if="isSeriesContentLoading" class="pointer-events-none mt-6 grid gap-4 lg:grid-cols-2" aria-hidden="true">
            <div v-for="i in 6" :key="i" class="h-36 animate-pulse rounded-[1.4rem] bg-white/6" />
          </div>

          <div v-else-if="episodes.length" class="mt-6 grid gap-4 lg:grid-cols-2">
            <article
              v-for="episode in episodes"
              :key="episode.id"
              class="group grid gap-4 overflow-hidden rounded-[1.5rem] border border-white/10 bg-white/[0.045] p-3 transition-colors hover:border-white/24 hover:bg-white/[0.075] sm:grid-cols-[11rem_1fr]"
              @click="openRelated(episode)"
            >
              <div class="relative overflow-hidden rounded-[1.1rem] bg-white/6">
                <img v-if="episode.backdropUrl || episode.posterUrl" :src="episode.backdropUrl ?? episode.posterUrl" :alt="episode.name" class="aspect-video h-full w-full object-cover transition-transform duration-500 group-hover:scale-105" loading="lazy" decoding="async">
                <div v-else class="flex aspect-video h-full w-full items-center justify-center p-4 text-center text-xs text-white/42">
                  {{ episodeTitle(episode) }}
                </div>
                <div class="absolute inset-0 bg-gradient-to-t from-black/70 to-transparent" />
              </div>
              <div class="flex min-w-0 flex-col justify-between gap-3 py-1">
                <div>
                  <p class="text-xs text-white/38">
                    {{ itemRuntime(episode) }}
                  </p>
                  <h3 class="mt-1 line-clamp-2 text-base font-semibold text-white">
                    {{ episodeTitle(episode) }}
                  </h3>
                  <p v-if="episode.overview" class="mt-2 line-clamp-3 text-sm leading-6 text-white/52">
                    {{ episode.overview }}
                  </p>
                </div>
                <div class="flex gap-2">
                  <button class="rounded-full bg-white px-4 py-2 text-xs font-bold text-black transition-transform hover:scale-105" @click.stop="playItem(episode)">
                    播放本集
                  </button>
                  <button class="rounded-full border border-white/10 bg-white/6 px-4 py-2 text-xs text-white/62 transition-colors hover:bg-white/10" @click.stop="openRelated(episode)">
                    详情
                  </button>
                </div>
              </div>
            </article>
          </div>

          <div v-else class="mt-6 rounded-[1.5rem] border border-white/10 bg-white/5 p-8 text-center text-sm text-white/45">
            Emby 暂未返回可选择的分集。
          </div>
        </section>

        <section v-if="!isSeriesDetail && (visibleMediaSources.length || audioTracks.length || subtitleTracks.length)" class="grid gap-5 lg:grid-cols-3">
          <div v-if="visibleMediaSources.length" class="glass-panel rounded-[1.6rem] p-5">
            <h2 class="text-base font-semibold">
              版本
            </h2>
            <div class="mt-4 space-y-2">
              <button
                v-for="source in visibleMediaSources"
                :key="source.id"
                class="w-full rounded-2xl border px-4 py-3 text-left text-sm transition-colors"
                :class="selectedMediaSourceId === source.id ? 'border-white/34 bg-white/14 text-white' : 'border-white/8 bg-white/5 text-white/62 hover:bg-white/10'"
                @click="selectedMediaSourceId = source.id"
              >
                <span class="block font-medium">版本 {{ mediaSources.findIndex(candidate => candidate.id === source.id) + 1 }}</span>
                <span class="mt-1 block text-xs text-white/42">{{ describeMediaSource(source) }}</span>
              </button>
            </div>
          </div>

          <div v-if="audioTracks.length" class="glass-panel rounded-[1.6rem] p-5">
            <h2 class="text-base font-semibold">
              音轨
            </h2>
            <select v-model="selectedAudioIndex" class="mt-4 w-full rounded-2xl border border-white/10 bg-black/20 px-4 py-3 text-sm text-white outline-none">
              <option v-for="track in audioTracks" :key="track.index" :value="track.index">
                {{ trackLabel(track) }}
              </option>
            </select>
          </div>

          <div v-if="subtitleTracks.length" class="glass-panel rounded-[1.6rem] p-5">
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
          <div v-if="mediaInfoRows.length" class="glass-panel rounded-[1.6rem] p-6">
            <h2 class="text-xl font-bold">
              媒体信息
            </h2>
            <dl class="mt-4 space-y-2 text-sm text-white/58">
              <div v-for="row in mediaInfoRows" :key="row[0]" class="flex justify-between gap-4">
                <dt>{{ row[0] }}</dt><dd>{{ row[1] }}</dd>
              </div>
            </dl>
          </div>
        </section>

        <section v-if="detail.collections?.length">
          <h2 class="mb-4 text-xl font-bold">
            合集
          </h2>
          <MediaGrid :items="detail.collections" @select="openRelated" @play="playItem" />
        </section>

        <section v-if="detail.similarItems?.length">
          <h2 class="mb-4 text-xl font-bold">
            相似内容
          </h2>
          <MediaGrid :items="detail.similarItems" @select="openRelated" @play="playItem" />
        </section>
      </main>
    </template>
  </div>
</template>
