<script setup lang="ts">
import { computed } from 'vue'
import { isNativeAndroidRuntime } from '@/services/runtimePlatform'
import { useUpdaterStore } from '@/stores/updater'

const updater = useUpdaterStore()
const isAndroid = isNativeAndroidRuntime()

const updateTitle = computed(() => updater.availableUpdate?.version
  ? `OhMyCine ${updater.availableUpdate.version}`
  : '发现新版本')
const channelLabel = computed(() => (updater.availableUpdate?.channel ?? updater.settings.channel) === 'beta' ? 'Beta' : '正式版')
const showProgress = computed(() => updater.isBusy || updater.downloadedBytes > 0 || updater.status === 'ready' || updater.status === 'paused')

async function runPrimaryAction() {
  if (updater.canInstall)
    await updater.installAvailableUpdate().catch(() => undefined)
  else
    await updater.downloadAvailableUpdate().catch(() => undefined)
}
</script>

<template>
  <Teleport to="body">
    <div v-if="updater.promptOpen && updater.availableUpdate" class="fixed inset-0 z-[1400] flex items-center justify-center bg-black/62 p-5 backdrop-blur-md" @pointerdown.self="updater.dismissPrompt()" @keydown.esc.stop.prevent="updater.dismissPrompt()">
      <section class="theme-adaptive glass-panel max-h-[calc(100dvh-2.5rem)] w-full max-w-xl flex flex-col overflow-hidden rounded-3xl border border-white/14 shadow-2xl" role="dialog" aria-modal="true" aria-label="软件更新">
        <header class="flex items-start justify-between gap-4 border-b border-white/10 px-6 py-5">
          <div>
            <p class="text-xs font-semibold uppercase tracking-[0.2em] text-white/36">
              {{ isAndroid ? 'Verified APK' : 'Signed Update' }} · {{ channelLabel }}
            </p>
            <h2 class="mt-1 text-xl font-bold text-white">
              {{ updateTitle }}
            </h2>
            <p class="mt-2 text-sm text-white/48">
              当前版本 {{ updater.availableUpdate.currentVersion }}
              <span v-if="updater.availableUpdate.date"> · 发布于 {{ updater.availableUpdate.date }}</span>
            </p>
          </div>
          <button type="button" class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-white/8 text-xl text-white/58 transition-colors hover:bg-white/14 hover:text-white disabled:opacity-35" aria-label="关闭更新窗口" :disabled="!updater.canDismiss" @click="updater.dismissPrompt()">
            ×
          </button>
        </header>

        <div class="min-h-0 overflow-y-auto p-6 space-y-4">
          <div v-if="updater.availableUpdate.body" class="max-h-52 overflow-y-auto whitespace-pre-wrap rounded-2xl border border-white/8 bg-white/5 px-4 py-3 text-sm leading-6 text-white/58">
            {{ updater.availableUpdate.body }}
          </div>
          <p v-else class="text-sm leading-6 text-white/48">
            {{ isAndroid ? '该版本未提供额外发布说明。APK 会通过 SHA-256 校验后交给系统安装器。' : '该版本未提供额外发布说明。更新包将使用内置公钥验证签名后安装。' }}
          </p>

          <div v-if="showProgress" class="rounded-2xl border border-primary/20 bg-primary/10 p-4" role="status" aria-live="polite">
            <div class="flex items-center justify-between gap-3 text-sm font-semibold text-white/78">
              <span>{{ updater.statusLabel }}</span>
              <span v-if="updater.progressPercent != null" class="shrink-0">{{ Math.round(updater.progressPercent) }}%</span>
            </div>
            <div class="mt-3 h-2 overflow-hidden rounded-full bg-white/10" role="progressbar" aria-label="更新下载进度" :aria-valuenow="updater.progressPercent ?? undefined" :aria-valuemin="0" :aria-valuemax="100">
              <div class="h-full rounded-full bg-primary transition-[width] duration-200" :class="updater.progressPercent == null ? 'w-1/3 animate-pulse' : ''" :style="updater.progressPercent == null ? undefined : { width: `${updater.progressPercent}%` }" />
            </div>
            <p v-if="updater.progressText" class="mt-2 text-xs text-white/48">
              {{ updater.progressText }}
            </p>
          </div>

          <p v-if="updater.error" class="rounded-2xl border border-red-400/20 bg-red-400/10 px-4 py-3 text-sm leading-6 text-red-100">
            {{ updater.error }}
          </p>

          <p class="text-xs leading-5 text-white/36">
            关闭窗口后下载会继续，可在设置中查看进度。下载完成后，点击“立即安装”开始更新。
          </p>
          <p class="text-xs leading-5 text-white/36">
            {{ isAndroid ? '首次安装更新需要允许 OhMyCine 安装未知应用。' : '开始安装后应用会退出。便携版会保留便携数据并更新到当前目录。' }}
          </p>
        </div>

        <footer class="flex justify-end gap-3 border-t border-white/10 px-6 py-4">
          <button type="button" class="rounded-xl bg-white/8 px-4 py-2.5 text-sm font-semibold text-white/68 transition-colors hover:bg-white/14 disabled:opacity-40" :disabled="!updater.canDismiss" @click="updater.dismissPrompt()">
            {{ updater.status === 'downloading' || updater.status === 'verifying' ? '后台下载' : '稍后' }}
          </button>
          <button type="button" class="rounded-xl bg-primary/82 px-5 py-2.5 text-sm font-bold text-white transition-colors hover:bg-primary disabled:cursor-wait disabled:opacity-55" :disabled="!updater.canDownload && !updater.canInstall" @click="runPrimaryAction">
            {{ updater.primaryActionLabel }}
          </button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>
