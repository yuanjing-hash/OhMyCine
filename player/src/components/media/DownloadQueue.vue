<script setup lang="ts">
import type { DownloadTask } from '@/services/downloads'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { cancelDownload, listDownloadTasks, listenDownloadProgress, retryDownload } from '@/services/downloads'

const tasks = ref<DownloadTask[]>([])
const open = ref(false)
const error = ref('')
let unlisten: (() => void) | undefined
const activeCount = computed(() => tasks.value.filter(task => task.status === 'queued' || task.status === 'running' || task.status === 'cancelling').length)

onMounted(async () => {
  try {
    tasks.value = await listDownloadTasks()
    unlisten = await listenDownloadProgress(upsert)
  }
  catch {
    // Browser development mode has no native queue.
  }
})
onBeforeUnmount(() => unlisten?.())

function upsert(task: DownloadTask) {
  const index = tasks.value.findIndex(item => item.id === task.id)
  if (index >= 0)
    tasks.value[index] = task
  else
    tasks.value.unshift(task)
}

function percent(task: DownloadTask): number {
  return task.totalBytes ? Math.min(100, (task.bytesDownloaded / task.totalBytes) * 100) : 0
}

function bytes(value?: number): string {
  if (!value)
    return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(units.length - 1, Math.floor(Math.log(value) / Math.log(1024)))
  return `${(value / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index]}`
}

async function cancel(task: DownloadTask) {
  try {
    await cancelDownload(task.id)
  }
  catch (reason) {
    error.value = toSafeErrorMessage(reason, '取消下载失败。')
  }
}

async function retry(task: DownloadTask) {
  try {
    upsert(await retryDownload(task.id))
  }
  catch (reason) {
    error.value = toSafeErrorMessage(reason, '重试下载失败。')
  }
}
</script>

<template>
  <div v-if="tasks.length" class="download-queue">
    <button class="download-queue-trigger" type="button" :aria-expanded="open" aria-label="下载队列" @click="open = !open">
      <span>↓</span><b v-if="activeCount">{{ activeCount }}</b>
    </button>
    <section v-if="open" class="download-queue-panel glass-panel" aria-label="下载队列">
      <header>
        <strong>下载队列</strong><button type="button" aria-label="关闭下载队列" @click="open = false">
          ×
        </button>
      </header>
      <p v-if="error" class="download-error">
        {{ error }}
      </p>
      <div class="download-list">
        <article v-for="task in tasks" :key="task.id" class="download-task">
          <div class="download-task-head">
            <strong>{{ task.destinationName }}</strong><span>{{ task.status }}</span>
          </div>
          <div class="download-progress">
            <i :style="{ width: `${percent(task)}%` }" />
          </div>
          <small>{{ bytes(task.bytesDownloaded) }}<template v-if="task.totalBytes"> / {{ bytes(task.totalBytes) }}</template></small>
          <p v-if="task.errorMessage">
            {{ task.errorMessage }}
          </p>
          <div class="download-actions">
            <button v-if="task.status === 'queued' || task.status === 'running'" type="button" @click="cancel(task)">
              取消
            </button>
            <button v-if="task.status === 'failed' || task.status === 'cancelled' || task.status === 'paused'" type="button" @click="retry(task)">
              重试
            </button>
          </div>
        </article>
      </div>
    </section>
  </div>
</template>

<style scoped>
.download-queue { position: fixed; right: 1.25rem; bottom: max(1.25rem, env(safe-area-inset-bottom)); z-index: 9100; }
.download-queue-trigger { position: relative; display:flex; width:2.75rem; height:2.75rem; align-items:center; justify-content:center; border:1px solid var(--color-border); border-radius:50%; color:var(--color-text); background:var(--chrome-surface); box-shadow:var(--chrome-shadow); backdrop-filter:blur(24px); font-size:1.25rem; }
.download-queue-trigger b { position:absolute; right:-.2rem; top:-.2rem; min-width:1.1rem; padding:.12rem .3rem; border-radius:999px; background:var(--color-primary); font-size:.62rem; }
.download-queue-panel { position:absolute; right:0; bottom:3.4rem; width:min(24rem, calc(100vw - 2rem)); max-height:min(34rem, 70vh); overflow:hidden; border-radius:14px; color:var(--color-text); }
.download-queue-panel header { display:flex; align-items:center; justify-content:space-between; padding:.85rem 1rem; border-bottom:1px solid var(--color-divider); }
.download-queue-panel header button { font-size:1.35rem; color:var(--color-text-secondary); }
.download-list { max-height:29rem; overflow:auto; padding:.55rem; }
.download-task { padding:.7rem; border-radius:10px; background:var(--surface-soft); }
.download-task + .download-task { margin-top:.45rem; }
.download-task-head { display:flex; gap:.7rem; justify-content:space-between; }
.download-task-head strong { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:.78rem; }
.download-task-head span,.download-task small { color:var(--color-text-tertiary); font-size:.65rem; }
.download-progress { height:.24rem; margin:.55rem 0 .35rem; overflow:hidden; border-radius:999px; background:var(--color-divider); }
.download-progress i { display:block; height:100%; background:var(--color-primary); transition:width .2s ease; }
.download-task p,.download-error { margin-top:.4rem; color:var(--color-error); font-size:.66rem; }
.download-error { padding:0 1rem; }
.download-actions { display:flex; justify-content:flex-end; gap:.4rem; }
.download-actions button { border-radius:8px; padding:.3rem .6rem; color:var(--color-text-secondary); background:var(--surface-soft-hover); font-size:.68rem; }
</style>
