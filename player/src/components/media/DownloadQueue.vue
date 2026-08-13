<script setup lang="ts">
import type { DownloadTask } from '@/services/downloads'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { cancelDownload, listDownloadTasks, listenDownloadProgress, retryDownload } from '@/services/downloads'

const tasks = ref<DownloadTask[]>([])
const open = ref(false)
const error = ref('')
const expandedGroups = ref(new Set<string>())
let unlisten: (() => void) | undefined
const topLevelEntries = computed(() => {
  const grouped = new Map<string, DownloadTask[]>()
  const singles: DownloadQueueEntry[] = []
  for (const task of tasks.value) {
    if (!task.parentId) {
      singles.push({ ...task, children: [] })
      continue
    }
    const children = grouped.get(task.parentId) ?? []
    children.push(task)
    grouped.set(task.parentId, children)
  }
  const groups = [...grouped.entries()].map(([id, children]) => aggregateGroup(id, children))
  return [...groups, ...singles].sort((left, right) => right.createdAt - left.createdAt)
})
const activeCount = computed(() => topLevelEntries.value.filter(entry => entry.status === 'queued' || entry.status === 'running' || entry.status === 'cancelling').length)

interface DownloadQueueEntry extends DownloadTask {
  children: DownloadTask[]
}

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

function aggregateGroup(id: string, children: DownloadTask[]): DownloadQueueEntry {
  const bytesDownloaded = children.reduce((sum, child) => sum + child.bytesDownloaded, 0)
  const allTotalsKnown = children.every(child => child.totalBytes != null)
  const totalBytes = allTotalsKnown ? children.reduce((sum, child) => sum + (child.totalBytes ?? 0), 0) : undefined
  const statuses = new Set(children.map(child => child.status))
  let status: DownloadTask['status'] = 'completed'
  for (const candidate of ['running', 'queued', 'cancelling', 'failed', 'paused', 'cancelled'] as const) {
    if (statuses.has(candidate)) {
      status = candidate
      break
    }
  }
  const first = children[0]
  return {
    ...first,
    id,
    displayName: first.groupName ?? first.displayName,
    destinationName: first.groupName ?? first.displayName,
    status,
    bytesDownloaded,
    totalBytes,
    errorMessage: statuses.has('failed') ? `${children.filter(child => child.status === 'failed').length} 个文件失败` : undefined,
    children: [...children].sort((left, right) => left.createdAt - right.createdAt),
  }
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

async function cancelEntry(entry: DownloadQueueEntry) {
  if (!entry.children.length)
    return cancel(entry)
  await Promise.all(entry.children.filter(child => ['queued', 'running', 'cancelling'].includes(child.status)).map(cancel))
}

async function retry(task: DownloadTask) {
  try {
    upsert(await retryDownload(task.id))
  }
  catch (reason) {
    error.value = toSafeErrorMessage(reason, '重试下载失败。')
  }
}

async function retryEntry(entry: DownloadQueueEntry) {
  if (!entry.children.length)
    return retry(entry)
  await Promise.all(entry.children.filter(child => ['failed', 'cancelled', 'paused'].includes(child.status)).map(retry))
}

function toggleGroup(id: string) {
  const next = new Set(expandedGroups.value)
  if (next.has(id))
    next.delete(id)
  else
    next.add(id)
  expandedGroups.value = next
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
        <article v-for="task in topLevelEntries" :key="task.id" class="download-task">
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
          <button v-if="task.children.length" class="download-group-toggle" type="button" @click="toggleGroup(task.id)">
            {{ task.children.filter(child => child.status === 'completed').length }} / {{ task.children.length }} 个文件 · {{ expandedGroups.has(task.id) ? '收起' : '展开' }}
          </button>
          <div v-if="task.children.length && expandedGroups.has(task.id)" class="download-group-children">
            <div v-for="child in task.children" :key="child.id">
              <span>{{ child.destinationName }}</span><small>{{ child.status }} · {{ percent(child).toFixed(0) }}%</small>
            </div>
          </div>
          <div class="download-actions">
            <button v-if="task.status === 'queued' || task.status === 'running'" type="button" @click="cancelEntry(task)">
              取消
            </button>
            <button v-if="task.status === 'failed' || task.status === 'cancelled' || task.status === 'paused'" type="button" @click="retryEntry(task)">
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
.download-group-toggle { margin-top:.45rem; color:var(--color-primary); font-size:.68rem; }
.download-group-children { margin-top:.45rem; padding:.45rem; border-radius:8px; background:rgb(0 0 0 / 12%); }
.download-group-children div { display:flex; gap:.5rem; justify-content:space-between; padding:.24rem 0; font-size:.65rem; }
.download-group-children span { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
</style>
