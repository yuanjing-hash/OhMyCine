<script setup lang="ts">
import type { ServerSearchSite } from '@/services/serverDiscovery'
import { computed } from 'vue'

const props = defineProps<{
  open: boolean
  sites: ServerSearchSite[]
  selectedSiteIds: number[]
  loading: boolean
  mode: 'aggregate' | 'direct'
  directKind: 'title' | 'tmdb'
  directQuery: string
  tmdbId?: number
}>()

const emit = defineEmits<{
  close: []
  search: []
  toggleSite: [id: number]
  toggleAll: []
  updateDirectKind: [value: 'title' | 'tmdb']
  updateDirectQuery: [value: string]
}>()

const searchableCount = computed(() => props.sites.filter(site => site.searchable).length)
const selectedAll = computed(() => searchableCount.value > 0 && props.sites.filter(site => site.searchable).every(site => props.selectedSiteIds.includes(site.id)))
const canSubmit = computed(() => props.selectedSiteIds.length > 0 && (props.mode !== 'direct' || props.directKind !== 'title' || props.directQuery.trim().length > 0))
</script>

<template>
  <Teleport to="body">
    <Transition name="discovery-dialog">
      <div v-if="open" class="dialog-layer fixed inset-0 z-[1210] grid place-items-center p-4" @click.self="emit('close')">
        <section class="dialog-card glass-panel w-full max-w-3xl overflow-hidden rounded-[28px]">
          <header class="flex items-start justify-between gap-5 border-b border-white/8 px-6 py-5">
            <div>
              <p class="text-[11px] font-bold tracking-[.22em] text-white/35 uppercase">
                步骤 1 · 搜索范围
              </p>
              <h2 class="mt-1 text-xl font-bold text-white">
                选择搜索站点
              </h2>
              <p class="mt-1 text-sm text-white/45">
                只请求你选中的站点；结果会按站点陆续出现。
              </p>
            </div>
            <button class="icon-button" type="button" aria-label="关闭" @click="emit('close')">
              ×
            </button>
          </header>

          <div class="max-h-[68vh] overflow-y-auto px-6 py-5 cinema-scrollbar">
            <div v-if="mode === 'direct'" class="mb-5 rounded-2xl border border-white/9 bg-black/18 p-4">
              <div class="flex flex-wrap gap-2">
                <button class="mode-chip" :class="{ active: directKind === 'title' }" type="button" @click="emit('updateDirectKind', 'title')">
                  按标题
                </button>
                <button class="mode-chip" :class="{ active: directKind === 'tmdb' }" type="button" :disabled="!tmdbId" @click="emit('updateDirectKind', 'tmdb')">
                  按 TMDB ID
                </button>
              </div>
              <input v-if="directKind === 'title'" :value="directQuery" class="glass-input mt-3 w-full rounded-2xl px-4 py-3 text-sm" maxlength="160" placeholder="输入要直接搜索的标题" @input="emit('updateDirectQuery', ($event.target as HTMLInputElement).value)">
              <p v-else class="mt-3 text-sm text-white/55">
                将使用 TMDB {{ tmdbId }} 直接检索，并把结果绑定到当前作品。
              </p>
            </div>

            <div class="mb-3 flex items-center justify-between gap-4">
              <button class="glass-button rounded-full px-4 py-2 text-sm" type="button" @click="emit('toggleAll')">
                {{ selectedAll ? '取消全选' : '快速全选' }}
              </button>
              <span class="text-xs text-white/42">已选 {{ selectedSiteIds.length }} / {{ searchableCount }}</span>
            </div>
            <div v-if="loading" class="py-14 text-center text-sm text-white/45">
              正在读取可搜索站点…
            </div>
            <div v-else class="grid gap-3 sm:grid-cols-2">
              <button v-for="site in sites" :key="site.id" class="site-card" :class="{ selected: selectedSiteIds.includes(site.id) }" type="button" :disabled="!site.searchable" @click="emit('toggleSite', site.id)">
                <span class="min-w-0 text-left">
                  <strong class="block truncate text-sm text-white/90">{{ site.name }}</strong>
                  <small class="mt-1 block truncate text-xs text-white/38">{{ site.siteType.toUpperCase() }}<template v-if="site.reason"> · {{ site.reason }}</template></small>
                </span>
                <span class="check-mark">{{ selectedSiteIds.includes(site.id) ? '✓' : '' }}</span>
              </button>
            </div>
          </div>

          <footer class="flex items-center justify-between gap-4 border-t border-white/8 px-6 py-4">
            <span class="text-xs text-white/35">可随时取消正在进行的搜索</span>
            <button class="primary-button" type="button" :disabled="!canSubmit || loading" @click="emit('search')">
              搜索
            </button>
          </footer>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.dialog-layer { background: color-mix(in srgb, var(--color-bg) 72%, transparent); backdrop-filter: blur(16px); }
.dialog-card { border: 1px solid var(--chrome-border); background: color-mix(in srgb, var(--chrome-surface-translucent) 94%, transparent); box-shadow: var(--chrome-shadow), inset 0 1px rgba(255,255,255,.1); }
.icon-button { display:grid; width:2.4rem; height:2.4rem; place-items:center; border-radius:999px; color:var(--color-text-secondary); background:var(--surface-soft); font-size:1.35rem; }
.mode-chip { border:1px solid var(--color-border); border-radius:999px; padding:.55rem .9rem; color:var(--color-text-secondary); background:var(--surface-soft); font-size:.78rem; font-weight:700; }
.mode-chip.active { border-color:color-mix(in srgb,var(--color-primary) 58%,transparent); color:#fff; background:color-mix(in srgb,var(--color-primary) 74%,transparent); }
.mode-chip:disabled { opacity:.4; }
.glass-input { border:1px solid var(--control-border); color:var(--control-text); background:var(--control-bg); outline:none; }
.glass-input:focus { border-color:var(--control-border-hover); box-shadow:0 0 0 3px var(--control-focus-ring); }
.site-card { display:flex; min-height:4.3rem; align-items:center; justify-content:space-between; gap:1rem; border:1px solid var(--color-border); border-radius:18px; padding:.9rem 1rem; background:var(--surface-soft); transition:160ms ease; }
.site-card:hover { border-color:var(--control-border-hover); background:var(--surface-soft-hover); transform:translateY(-1px); }
.site-card.selected { border-color:color-mix(in srgb,var(--color-primary) 62%,transparent); background:color-mix(in srgb,var(--color-primary) 18%,var(--surface-soft)); }
.site-card:disabled { cursor:not-allowed; opacity:.42; transform:none; }
.check-mark { display:grid; width:1.55rem; height:1.55rem; flex:0 0 auto; place-items:center; border-radius:50%; color:#fff; background:color-mix(in srgb,var(--color-primary) 74%,transparent); font-size:.72rem; }
.primary-button { min-width:7rem; border-radius:999px; padding:.7rem 1.4rem; color:#05070b; background:#fff; font-size:.85rem; font-weight:800; box-shadow:0 10px 28px rgba(0,0,0,.24); }
.primary-button:disabled { cursor:not-allowed; opacity:.45; }
.discovery-dialog-enter-active,.discovery-dialog-leave-active { transition:opacity 180ms ease; }
.discovery-dialog-enter-active .dialog-card,.discovery-dialog-leave-active .dialog-card { transition:transform 180ms ease,opacity 180ms ease; }
.discovery-dialog-enter-from,.discovery-dialog-leave-to { opacity:0; }
.discovery-dialog-enter-from .dialog-card,.discovery-dialog-leave-to .dialog-card { opacity:0; transform:translateY(14px) scale(.985); }
</style>
