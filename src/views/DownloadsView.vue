<script setup lang="ts">
import type { DownloadTask } from '@/services/downloads'
import { open } from '@tauri-apps/plugin-dialog'
import { open as openPath } from '@tauri-apps/plugin-shell'
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { pickAndroidDownloadDirectory, resolveCompletedDownload } from '@/services/downloads'
import { savePlaybackMediaContext } from '@/services/playbackContext'
import { createPlaybackRouteQuery } from '@/services/playbackRoute'
import { isNativeAndroidRuntime } from '@/services/runtimePlatform'
import { useDownloadStore } from '@/stores/downloads'

type Tab = 'active' | 'completed' | 'failed' | 'settings'

const store = useDownloadStore()
const router = useRouter()
const tab = ref<Tab>('active')
const busyId = ref('')
const feedback = ref('')
const speedLimitMb = ref(0)

const tabs: Array<{ id: Tab, label: string, count?: () => number }> = [
  { id: 'active', label: '进行中', count: () => store.activeTasks.length },
  { id: 'completed', label: '已完成', count: () => store.completedTasks.length },
  { id: 'failed', label: '失败', count: () => store.failedTasks.length },
  { id: 'settings', label: '设置' },
]
const visibleTasks = computed(() => tab.value === 'active' ? store.activeTasks : tab.value === 'completed' ? store.completedTasks : store.failedTasks)

onMounted(async () => {
  await store.initialize()
  speedLimitMb.value = store.settings.globalSpeedLimitBytesPerSecond
    ? store.settings.globalSpeedLimitBytesPerSecond / 1024 / 1024
    : 0
})

function percent(task: DownloadTask) {
  return task.totalBytes ? Math.min(100, task.bytesDownloaded / task.totalBytes * 100) : 0
}

function bytes(value?: number) {
  if (!value)
    return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(units.length - 1, Math.floor(Math.log(value) / Math.log(1024)))
  return `${(value / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index]}`
}

function eta(seconds?: number) {
  if (!seconds || !Number.isFinite(seconds))
    return '计算中'
  if (seconds < 60)
    return `${Math.ceil(seconds)} 秒`
  if (seconds < 3600)
    return `${Math.ceil(seconds / 60)} 分钟`
  return `${Math.floor(seconds / 3600)} 小时 ${Math.ceil(seconds % 3600 / 60)} 分钟`
}

async function run(task: DownloadTask, action: () => Promise<void>) {
  busyId.value = task.id
  feedback.value = ''
  try {
    await action()
  }
  catch (cause) {
    feedback.value = cause instanceof Error ? cause.message : '操作失败，请稍后重试。'
  }
  finally {
    busyId.value = ''
  }
}

async function chooseDirectory() {
  const selected = isNativeAndroidRuntime()
    ? await pickAndroidDownloadDirectory(false)
    : await open({ directory: true, multiple: false, defaultPath: store.defaultDirectory || undefined })
  if (typeof selected === 'string')
    await store.saveDirectory(selected)
}

async function saveSettings() {
  feedback.value = ''
  try {
    await store.saveSettings({
      concurrentTasks: store.settings.concurrentTasks,
      segmentsPerTask: store.settings.segmentsPerTask,
      globalSpeedLimitBytesPerSecond: speedLimitMb.value > 0 ? Math.round(speedLimitMb.value * 1024 * 1024) : undefined,
    })
    feedback.value = '下载设置已保存。'
  }
  catch (cause) {
    feedback.value = cause instanceof Error ? cause.message : '下载设置保存失败。'
  }
}

async function playOffline(task: DownloadTask) {
  await run(task, async () => {
    const path = await resolveCompletedDownload({
      sourceId: task.sourceId,
      itemId: task.itemId,
      mediaSourceId: task.mediaSourceId,
      variantId: task.variantId,
    })
    if (!path)
      throw new Error('本地离线文件不存在，请重新下载。')
    const contextId = savePlaybackMediaContext({
      sourceId: task.sourceId,
      itemId: task.itemId,
      title: task.displayName,
      locator: { kind: 'localPath', path },
    })
    await router.push({
      name: 'player',
      query: createPlaybackRouteQuery({ sourceId: task.sourceId, itemId: task.itemId, contextId }),
    })
  })
}

async function openOfflineDetail(task: DownloadTask) {
  await router.push({ name: 'media-detail', params: { sourceId: task.sourceId, itemId: task.itemId } })
}

async function openDownloadLocation(task: DownloadTask) {
  await run(task, async () => {
    if (isNativeAndroidRuntime())
      throw new Error('Android 下载由系统文件提供器管理，请在系统文件应用中打开下载目录。')
    await openPath(task.destinationDirectory)
  })
}
</script>

<template>
  <main class="downloads-view mx-auto w-full max-w-6xl px-5 pb-24 pt-24 md:px-10">
    <header class="mb-7">
      <p class="text-sm text-primary">
        离线中心
      </p>
      <h1 class="mt-1 text-3xl font-semibold text-[var(--color-text)]">
        下载管理
      </h1>
      <p class="mt-2 text-sm text-[var(--color-text-secondary)]">
        下载完成后优先播放本地文件；取消会立即从队列移除。
      </p>
    </header>

    <nav class="mb-6 flex gap-2 overflow-x-auto rounded-2xl border border-[var(--color-border)] bg-[var(--color-surface)] p-1.5" aria-label="下载管理分页">
      <button v-for="item in tabs" :key="item.id" type="button" class="min-w-max rounded-xl px-4 py-2 text-sm transition" :class="tab === item.id ? 'bg-primary text-white' : 'text-[var(--color-text-secondary)] hover:bg-[var(--color-surface-hover)]'" @click="tab = item.id">
        {{ item.label }}<span v-if="item.count" class="ml-1 opacity-70">{{ item.count() }}</span>
      </button>
    </nav>

    <p v-if="feedback" class="mb-4 rounded-xl bg-[var(--color-surface)] px-4 py-3 text-sm text-[var(--color-text-secondary)]">
      {{ feedback }}
    </p>

    <section v-if="tab !== 'settings'" class="space-y-3">
      <div v-if="visibleTasks.length === 0" class="rounded-3xl border border-dashed border-[var(--color-border)] px-6 py-16 text-center text-[var(--color-text-tertiary)]">
        这里暂时没有任务
      </div>
      <article v-for="task in visibleTasks" :key="task.id" class="rounded-2xl border border-[var(--color-border)] bg-[var(--color-surface)] p-5 shadow-sm">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0">
            <h2 class="truncate font-semibold text-[var(--color-text)]">
              {{ task.displayName }}
            </h2>
            <p class="mt-1 text-xs text-[var(--color-text-tertiary)]">
              {{ task.groupName || task.destinationName }} · {{ task.status }}
            </p>
          </div>
          <span v-if="task.attachmentState === 'partial' || task.attachmentState === 'pending'" class="rounded-full bg-amber-500/14 px-3 py-1 text-xs text-amber-500">视频可离线 · 附件待补全</span>
          <span v-else-if="task.attachmentState === 'syncing'" class="rounded-full bg-primary/14 px-3 py-1 text-xs text-primary">正在保存离线附件</span>
        </div>
        <div v-if="task.status !== 'failed'" class="mt-4 h-1.5 overflow-hidden rounded-full bg-[var(--color-divider)]">
          <i class="block h-full rounded-full bg-primary transition-all" :style="{ width: `${percent(task)}%` }" />
        </div>
        <div class="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-xs text-[var(--color-text-secondary)]">
          <span>{{ bytes(task.bytesDownloaded) }}<template v-if="task.totalBytes"> / {{ bytes(task.totalBytes) }}</template></span>
          <span v-if="task.speedBytesPerSecond">{{ bytes(task.speedBytesPerSecond) }}/s</span>
          <span v-if="task.status === 'downloading'">剩余 {{ eta(task.etaSeconds) }}</span>
          <span v-if="task.activeSegments">{{ task.activeSegments }} 个活动分段</span>
        </div>
        <p v-if="task.errorMessage" class="mt-3 text-sm text-red-400">
          {{ task.errorMessage }}
        </p>
        <div class="mt-4 flex flex-wrap justify-end gap-2">
          <button v-if="['queued', 'interrupted', 'resolving', 'downloading', 'finalizing'].includes(task.status)" type="button" class="task-button" :disabled="busyId === task.id" @click="run(task, () => store.pause(task.id))">
            暂停
          </button>
          <button v-if="task.status === 'paused'" type="button" class="task-button" :disabled="busyId === task.id" @click="run(task, () => store.resume(task.id))">
            继续
          </button>
          <button v-if="tab === 'active'" type="button" class="task-button danger" :disabled="busyId === task.id" @click="run(task, () => store.cancel(task.id))">
            取消并清理
          </button>
          <button v-if="task.status === 'failed'" type="button" class="task-button" :disabled="busyId === task.id" @click="run(task, () => store.retry(task.id))">
            重试
          </button>
          <button v-if="task.status === 'failed'" type="button" class="task-button danger" :disabled="busyId === task.id" @click="run(task, () => store.remove(task.id, false))">
            删除记录
          </button>
          <button v-if="task.status === 'completed'" type="button" class="task-button" :disabled="busyId === task.id" @click="run(task, () => store.remove(task.id, false))">
            仅删记录
          </button>
          <button v-if="task.status === 'completed'" type="button" class="task-button" :disabled="busyId === task.id" @click="playOffline(task)">
            离线播放
          </button>
          <button v-if="task.status === 'completed'" type="button" class="task-button" :disabled="busyId === task.id" @click="openOfflineDetail(task)">
            查看媒体详情
          </button>
          <button v-if="task.status === 'completed' && !isNativeAndroidRuntime()" type="button" class="task-button" :disabled="busyId === task.id" @click="openDownloadLocation(task)">
            打开文件位置
          </button>
          <button v-if="task.status === 'completed' && task.attachmentState !== 'complete'" type="button" class="task-button" :disabled="busyId === task.id" @click="run(task, () => store.retryAttachments(task))">
            重试附件
          </button>
          <button v-if="task.status === 'completed'" type="button" class="task-button danger" :disabled="busyId === task.id" @click="run(task, () => store.remove(task.id, true))">
            删除记录和文件
          </button>
        </div>
      </article>
    </section>

    <section v-else class="space-y-4 rounded-3xl border border-[var(--color-border)] bg-[var(--color-surface)] p-6">
      <label class="setting-row"><span><b>下载位置</b><small>{{ store.defaultDirectory || '尚未选择' }}</small></span><button type="button" class="task-button" @click="chooseDirectory">选择目录</button></label>
      <label class="setting-row"><span><b>同时下载数量</b><small>排队任务按照创建顺序公平启动</small></span><input v-model.number="store.settings.concurrentTasks" type="number" min="1" max="8"></label>
      <label class="setting-row"><span><b>单任务下载线程</b><small>不支持安全 Range 时自动退化为单线程</small></span><input v-model.number="store.settings.segmentsPerTask" type="number" min="1" max="16"></label>
      <label class="setting-row"><span><b>全局最快速度</b><small>填写 0 表示不限速，单位 MB/s</small></span><input v-model.number="speedLimitMb" type="number" min="0" step="0.5"></label>
      <div class="flex justify-end">
        <button type="button" class="rounded-xl bg-primary px-5 py-2.5 text-sm font-semibold text-white" @click="saveSettings">
          保存设置
        </button>
      </div>
    </section>
  </main>
</template>

<style scoped>
.task-button { border-radius:.75rem; padding:.55rem .85rem; color:var(--color-text-secondary); background:var(--color-surface-hover); font-size:.78rem; }
.task-button:hover { color:var(--color-text); }
.task-button:disabled { opacity:.5; }
.task-button.danger { color:rgb(248 113 113); }
.setting-row { display:flex; align-items:center; justify-content:space-between; gap:1rem; border-bottom:1px solid var(--color-divider); padding:1rem 0; }
.setting-row span { min-width:0; display:flex; flex-direction:column; gap:.25rem; }
.setting-row b { color:var(--color-text); font-size:.9rem; }
.setting-row small { color:var(--color-text-tertiary); overflow-wrap:anywhere; }
.setting-row input { width:7rem; border:1px solid var(--color-border); border-radius:.75rem; background:var(--color-surface-hover); padding:.6rem .75rem; color:var(--color-text); }
@media (max-width:640px) { .setting-row { align-items:flex-start; flex-direction:column; } .setting-row input { width:100%; } }
</style>
