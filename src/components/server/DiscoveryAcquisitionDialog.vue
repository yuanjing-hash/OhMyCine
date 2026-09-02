<script setup lang="ts">
import type { ServerDownloadOption, ServerLibraryOption, ServerProfileOption, ServerResourceItem } from '@/services/serverDiscovery'
import { computed, ref, watch } from 'vue'

const props = defineProps<{
  open: boolean
  resource: ServerResourceItem | null
  downloaders: ServerDownloadOption[]
  libraries: ServerLibraryOption[]
  profiles: ServerProfileOption[]
  loading: boolean
  submitting: boolean
}>()

const emit = defineEmits<{
  close: []
  confirm: [value: { downloaderId: string, libraryId: number, profileId: number }]
}>()

const step = ref<1 | 2 | 3>(1)
const downloaderId = ref('')
const libraryId = ref(0)
const selectedDownloader = computed(() => props.downloaders.find(item => item.id === downloaderId.value))
const selectedLibrary = computed(() => props.libraries.find(item => item.id === libraryId.value))

watch(() => props.open, (open) => {
  if (!open)
    return
  step.value = 1
  downloaderId.value = props.downloaders[0]?.id ?? ''
  libraryId.value = props.libraries[0]?.id ?? 0
}, { immediate: true })

watch(() => props.downloaders, (items) => {
  if (!downloaderId.value)
    downloaderId.value = items[0]?.id ?? ''
})
watch(() => props.libraries, (items) => {
  if (!libraryId.value)
    libraryId.value = items[0]?.id ?? 0
})

function confirm() {
  if (!downloaderId.value || !libraryId.value)
    return
  emit('confirm', { downloaderId: downloaderId.value, libraryId: libraryId.value, profileId: props.profiles[0]?.id ?? 0 })
}
</script>

<template>
  <Teleport to="body">
    <Transition name="acquisition-dialog">
      <div v-if="open" class="dialog-layer fixed inset-0 z-[1220] grid place-items-center p-4" @click.self="!submitting && emit('close')">
        <section class="dialog-card glass-panel flex max-h-[88vh] w-full max-w-3xl flex-col overflow-hidden rounded-[28px]">
          <header class="flex items-start justify-between gap-5 border-b border-white/8 px-6 py-5">
            <div class="min-w-0">
              <p class="text-[11px] font-bold tracking-[.22em] text-white/35 uppercase">
                步骤 3 · 确认入库
              </p><h2 class="mt-1 text-xl font-bold text-white">
                {{ step === 1 ? '选择下载器' : step === 2 ? '选择媒体库' : '确认任务' }}
              </h2><p class="mt-1 truncate text-sm text-white/45">
                {{ resource?.title }}
              </p>
            </div>
            <button class="icon-button" type="button" :disabled="submitting" aria-label="关闭" @click="emit('close')">
              ×
            </button>
          </header>

          <div class="step-rail px-6 pt-5">
            <span v-for="value in 3" :key="value" :class="{ active: value <= step }" /><small>{{ step }} / 3</small>
          </div>
          <main class="min-h-0 flex-1 overflow-y-auto px-6 py-5 cinema-scrollbar">
            <div v-if="loading" class="py-16 text-center text-sm text-white/45">
              正在读取 Server 入库选项…
            </div>
            <template v-else-if="step === 1">
              <button v-for="(item, index) in downloaders" :key="item.id" class="option-card" :class="{ selected: downloaderId === item.id }" type="button" @click="downloaderId = item.id">
                <span><b class="block text-sm text-white/90">{{ index === 0 ? `默认下载 · ${item.name}` : item.name }}</b><small class="mt-1 block text-xs text-white/38">{{ item.type || '下载器' }}</small></span><span class="radio-mark">{{ downloaderId === item.id ? '✓' : '' }}</span>
              </button>
              <p v-if="!downloaders.length" class="py-12 text-center text-sm text-white/45">
                当前账号没有可用下载器。
              </p>
            </template>
            <template v-else-if="step === 2">
              <button v-for="(item, index) in libraries" :key="item.id" class="option-card" :class="{ selected: libraryId === item.id }" type="button" @click="libraryId = item.id">
                <span><b class="block text-sm text-white/90">{{ index === 0 ? `默认媒体库 · ${item.name}` : item.name }}</b><small class="mt-1 block text-xs text-white/38">Server 将使用该媒体库自己的分类与命名规则</small></span><span class="radio-mark">{{ libraryId === item.id ? '✓' : '' }}</span>
              </button>
              <p v-if="!libraries.length" class="py-12 text-center text-sm text-white/45">
                当前账号没有可用媒体库。
              </p>
            </template>
            <template v-else>
              <div class="summary-card rounded-2xl border border-white/9 bg-white/[.035] p-5">
                <p class="text-xs text-white/38">
                  资源
                </p><p class="mt-1 break-words text-sm font-semibold text-white/88">
                  {{ resource?.title }}
                </p>
                <div class="my-4 h-px bg-white/8" />
                <dl class="grid gap-4 sm:grid-cols-2">
                  <div>
                    <dt class="text-xs text-white/38">
                      下载器
                    </dt><dd class="mt-1 text-sm font-semibold text-white/85">
                      {{ selectedDownloader?.name }}
                    </dd>
                  </div><div>
                    <dt class="text-xs text-white/38">
                      目标媒体库
                    </dt><dd class="mt-1 text-sm font-semibold text-white/85">
                      {{ selectedLibrary?.name }}
                    </dd>
                  </div>
                </dl>
                <p class="mt-4 rounded-xl bg-white/[.04] p-3 text-xs leading-5 text-white/48">
                  Server 会在下载前验证该资源与当前 TMDB 海报身份；明显不匹配时不会开始下载或错误入库。
                </p>
              </div>
            </template>
          </main>

          <footer class="flex items-center justify-between gap-3 border-t border-white/8 px-6 py-4">
            <button class="glass-button rounded-full px-4 py-2 text-sm" type="button" :disabled="submitting" @click="step === 1 ? emit('close') : step = step === 3 ? 2 : 1">
              {{ step === 1 ? '取消' : '上一步' }}
            </button>
            <button v-if="step < 3" class="primary-button" type="button" :disabled="loading || step === 1 && !downloaderId || step === 2 && !libraryId" @click="step = step === 1 ? 2 : 3">
              下一步
            </button>
            <button v-else class="primary-button" type="button" :disabled="submitting || !downloaderId || !libraryId" @click="confirm">
              {{ submitting ? '正在创建任务…' : '确认入库' }}
            </button>
          </footer>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.dialog-layer{background:color-mix(in srgb,var(--color-bg) 72%,transparent);backdrop-filter:blur(18px)}.dialog-card{border:1px solid var(--chrome-border);background:color-mix(in srgb,var(--chrome-surface-translucent) 96%,transparent);box-shadow:var(--chrome-shadow),inset 0 1px rgba(255,255,255,.1)}.icon-button{display:grid;width:2.4rem;height:2.4rem;place-items:center;border-radius:50%;color:var(--color-text-secondary);background:var(--surface-soft);font-size:1.35rem}.step-rail{display:flex;align-items:center;gap:.45rem}.step-rail span{height:.22rem;flex:1;border-radius:999px;background:rgba(255,255,255,.08)}.step-rail span.active{background:color-mix(in srgb,var(--color-primary) 78%,white 12%)}.step-rail small{margin-left:.45rem;color:var(--color-text-tertiary);font-size:.68rem}.option-card{display:flex;width:100%;min-height:4.6rem;align-items:center;justify-content:space-between;gap:1rem;border:1px solid var(--color-border);border-radius:20px;padding:1rem 1.1rem;background:var(--surface-soft);text-align:left;transition:160ms ease}.option-card+.option-card{margin-top:.65rem}.option-card:hover{border-color:var(--control-border-hover);background:var(--surface-soft-hover)}.option-card.selected{border-color:color-mix(in srgb,var(--color-primary) 62%,transparent);background:color-mix(in srgb,var(--color-primary) 18%,var(--surface-soft))}.radio-mark{display:grid;width:1.65rem;height:1.65rem;place-items:center;border-radius:50%;color:#fff;background:color-mix(in srgb,var(--color-primary) 74%,transparent);font-size:.72rem}.primary-button{min-width:7rem;border-radius:999px;padding:.7rem 1.4rem;color:#05070b;background:#fff;font-size:.85rem;font-weight:800}.primary-button:disabled{cursor:not-allowed;opacity:.45}.acquisition-dialog-enter-active,.acquisition-dialog-leave-active{transition:opacity 180ms ease}.acquisition-dialog-enter-from,.acquisition-dialog-leave-to{opacity:0}
</style>
