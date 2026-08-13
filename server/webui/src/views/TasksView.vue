<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { Permissions } from "@/auth/generated-permissions";
import { useAuthStore } from "@/stores/auth";
import { APIError } from "@/api/client";
import {
  controlJob,
  getAttempts,
  getJob,
  getTimeline,
  listJobs,
  reorderLane,
  respondAction,
  statusLabels,
  unknown,
  type Job,
  type JobAttempt,
  type JobEvent,
  type JobStatus,
} from "@/jobs";
const auth = useAuthStore(),
  jobs = ref<Job[]>([]),
  total = ref(0),
  page = ref(1),
  pageSize = ref(50),
  loading = ref(true),
  error = ref(""),
  notice = ref(""),
  status = ref(""),
  jobType = ref(""),
  priority = ref(""),
  provider = ref(""),
  laneOnly = ref(false),
  selected = ref<Job | null>(null),
  attempts = ref<JobAttempt[]>([]),
  timeline = ref<JobEvent[]>([]),
  dragged = ref<number | null>(null);
let drawerTrigger: HTMLElement | null = null;
const canControl = computed(() =>
    auth.canAny([Permissions.JobsControlOwn, Permissions.JobsControlAll]),
  ),
  canRespond = computed(() => auth.can(Permissions.JobsRespond)),
  canReorder = computed(() => auth.can(Permissions.JobsReorder)),
  counts = computed(() =>
    jobs.value.reduce<Record<string, number>>((r, j) => {
      r[j.status] = (r[j.status] ?? 0) + 1;
      return r;
    }, {}),
  ),
  laneReady = computed(
    () =>
      laneOnly.value &&
      status.value === "queued" &&
      jobType.value !== "" &&
      priority.value !== "" &&
      jobs.value.length > 1,
  );
const completeLane = computed(() => laneReady.value && total.value === jobs.value.length);
async function load() {
  loading.value = true;
  error.value = "";
  try {
    const q = new URLSearchParams({ page: String(page.value), page_size: String(laneOnly.value ? 200 : pageSize.value) });
    if (status.value) q.set("status", status.value);
    if (jobType.value) q.set("job_type", jobType.value);
    if (priority.value) q.set("priority", priority.value);
    if (provider.value) q.set("provider", provider.value);
    const r = await listJobs(q);
    jobs.value = r.list;
    total.value = r.total;
  } catch (c) {
    error.value = c instanceof Error ? c.message : "任务加载失败";
  } finally {
    loading.value = false;
  }
}
async function open(job: Job) {
  drawerTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const [detail, a, t] = await Promise.all([getJob(job.id), getAttempts(job.id), getTimeline(job.id)]);
  selected.value = detail;
  attempts.value = a.list;
  timeline.value = t.list;
  window.setTimeout(() => document.querySelector<HTMLElement>(".task-drawer .icon-button")?.focus());
}
function closeDrawer(){selected.value=null;window.setTimeout(()=>drawerTrigger?.focus())}
function handleEscape(event:KeyboardEvent){if(event.key==="Escape"&&selected.value)closeDrawer()}
async function action(job: Job, name: "pause" | "resume" | "cancel" | "retry") {
  if (
    (name === "cancel" || name === "retry") &&
    !window.confirm(
      `确认${name === "cancel" ? "取消" : "重试"}“${job.display_name}”？取消不会删除真实文件。`,
    )
  )
    return;
  try {
    await controlJob(job.id, name);
    await load();
  } catch (c) {
    error.value = c instanceof Error ? c.message : "操作失败";
  }
}
async function respond(job: Job, response: string) {
  try {
    await respondAction(job, response);
    await load();
    selected.value = null;
  } catch (c) {
    error.value = c instanceof Error ? c.message : "响应失败";
  }
}
async function move(index: number, offset: number) {
  if (!completeLane.value) return;
  const next = index + offset;
  if (next < 0 || next >= jobs.value.length) return;
  const ordered = [...jobs.value];
  [ordered[index], ordered[next]] = [ordered[next], ordered[index]];
  try {
    jobs.value = (
      await reorderLane(ordered[0].job_type, ordered[0].priority, ordered)
    ).list;
    notice.value = "队列顺序已保存";
  } catch (c) {
    notice.value =
      c instanceof APIError && c.errorCode === "queue_order_conflict"
        ? "队列已变化，已刷新真实顺序"
        : "";
    error.value = notice.value
      ? ""
      : c instanceof Error
        ? c.message
        : "排序失败";
    await load();
  }
}
async function drop(index: number) {
  if (!completeLane.value || dragged.value === null || dragged.value === index) return;
  const ordered = [...jobs.value],
    item = ordered.splice(dragged.value, 1)[0];
  ordered.splice(index, 0, item);
  dragged.value = null;
  try {
    jobs.value = (
      await reorderLane(ordered[0].job_type, ordered[0].priority, ordered)
    ).list;
  } catch {
    notice.value = "队列已变化，已刷新真实顺序";
    await load();
  }
}
onMounted(load);
let refreshTimer: number | undefined;
let socket: WebSocket | undefined;
function startLiveUpdates() {
  refreshTimer = window.setInterval(load, 15_000);
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  socket = new WebSocket(`${protocol}//${window.location.host}/api/v1/jobs/events/ws`);
  socket.onmessage = () => load();
  socket.onclose = () => { socket = undefined; };
}
onMounted(startLiveUpdates);
onMounted(()=>window.addEventListener("keydown",handleEscape));
onBeforeUnmount(() => { if (refreshTimer) window.clearInterval(refreshTimer); socket?.close(); window.removeEventListener("keydown",handleEscape); });
</script>
<template>
  <section class="mx-auto max-w-[96rem]">
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 class="m-0 text-3xl font-800">任务中心</h1>
        <p class="page-description mt-2">
          持久化任务的全局观察与控制面；媒体库 watcher 和 reconciliation
          不进入此队列。
        </p>
      </div>
      <button class="btn-secondary" @click="load">刷新</button>
    </header>
    <div class="task-summary mt-6">
      <article
        v-for="item in [
          'queued',
          'running',
          'waiting_user_action',
          'retry_wait',
          'failed',
        ]"
        :key="item"
        class="panel"
      >
        <small>本页 · {{ statusLabels[item as JobStatus] }}</small><strong>{{ counts[item] ?? 0 }}</strong>
      </article>
    </div>
    <div class="panel mt-4 task-filters">
      <label><span class="label">状态</span><select v-model="status" class="input" @change="load">
        <option value="">全部</option>
        <option v-for="(label, key) in statusLabels" :key="key" :value="key">
          {{ label }}
        </option>
      </select></label><label><span class="label">任务类型</span><input
        v-model.trim="jobType"
        class="input"
        placeholder="download / transfer"
        @change="load"
      /></label><label><span class="label">优先级</span><input
        v-model.trim="priority"
        class="input"
        inputmode="numeric"
        placeholder="10"
        @change="load"
      /></label><label><span class="label">Provider</span><input
        v-model.trim="provider"
        class="input"
        placeholder="provider"
        @change="load"
      /></label><label class="task-checkbox"><input
        v-model="laneOnly"
        type="checkbox"
        @change="
          status = laneOnly ? 'queued' : status;
          load();
        "
      />
        单 lane 排序</label>
    </div>
    <p v-if="notice" class="semantic-warning mt-4 p-3" role="status">
      {{ notice }}
    </p>
    <p v-if="error" class="semantic-error mt-4 p-3" role="alert">{{ error }}</p>
    <div class="panel mt-4 overflow-x-auto p-0">
      <p v-if="laneOnly && !laneReady" class="semantic-warning m-3 p-3">
        排序模式需要排队状态、任务类型和优先级三个精确筛选。
      </p>
      <p v-else-if="laneReady && !completeLane" class="semantic-warning m-3 p-3">
        当前 lane 超过 200 条，无法加载完整顺序，因此已禁用排序。
      </p>
      <p v-if="loading" class="p-6 text-muted">正在读取持久化队列…</p>
      <p v-else-if="!jobs.length" class="p-6 text-muted">
        查询成功，当前筛选范围内没有任务。
      </p>
      <table v-else class="semantic-table task-table w-full">
        <thead>
          <tr>
            <th>顺序</th>
            <th>任务</th>
            <th>状态</th>
            <th>进度 / 处理量</th>
            <th>速度 / ETA</th>
            <th>错误 / 重试</th>
            <th>更新</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(job, index) in jobs"
            :key="job.id"
            :class="{
              'task-action-required': job.status === 'waiting_user_action',
            }"
            :draggable="canReorder && completeLane"
            @dragstart="dragged = index"
            @dragover.prevent
            @drop="drop(index)"
          >
            <td>
              #{{ job.lane_rank ?? "—" }}
              <span v-if="canReorder && completeLane" aria-hidden="true">⠿</span>
              <div v-if="canReorder && completeLane" class="flex gap-1">
                <button
                  class="task-order"
                  :disabled="index === 0"
                  :aria-label="`上移 ${job.display_name}`"
                  @click="move(index, -1)"
                >
                  ↑</button><button
                  class="task-order"
                  :disabled="index === jobs.length - 1"
                  :aria-label="`下移 ${job.display_name}`"
                  @click="move(index, 1)"
                >
                  ↓
                </button>
              </div>
            </td>
            <td>
              <button class="semantic-link text-left" @click="open(job)">
                <strong>{{ job.display_name }}</strong><small class="block text-subtle">{{ job.job_type }} · P{{ job.priority }} ·
                  {{ job.provider || job.resource_key || "无资源标签" }}</small>
              </button>
            </td>
            <td>
              <span
                class="status-chip"
                :class="{
                  'status-chip--warning': [
                    'waiting_user_action',
                    'retry_wait',
                  ].includes(job.status),
                  'status-chip--error': job.status === 'failed',
                  'status-chip--ready': job.status === 'completed',
                }"
              >{{ statusLabels[job.status] }}</span><small
                v-if="job.interrupt_pending"
                class="block mt-1 semantic-warning-text"
              >控制请求处理中</small>
            </td>
            <td>
              {{ unknown(job.progress, "%")
              }}<small class="block text-subtle">{{ unknown(job.processed_items) }} /
                {{ unknown(job.total_items) }}</small>
            </td>
            <td>
              {{ unknown(job.speed, "/s")
              }}<small class="block text-subtle">ETA {{ unknown(job.eta_seconds, "s") }}</small>
            </td>
            <td>
              {{ job.last_error_message || "—"
              }}<small class="block text-subtle">{{
                job.next_attempt_at
                  ? new Date(job.next_attempt_at).toLocaleString()
                  : "—"
              }}</small>
            </td>
            <td>{{ new Date(job.updated_at).toLocaleString() }}</td>
            <td>
              <div v-if="canControl" class="flex flex-wrap gap-1">
                <button
                  v-if="
                    ['queued', 'running', 'retry_wait'].includes(job.status)
                  "
                  class="btn-secondary"
                  @click="action(job, 'pause')"
                >
                  暂停</button><button
                  v-if="job.status === 'paused'"
                  class="btn-secondary"
                  @click="action(job, 'resume')"
                >
                  恢复</button><button
                  v-if="job.status === 'failed'"
                  class="btn-secondary"
                  @click="action(job, 'retry')"
                >
                  重试</button><button
                  v-if="!['completed', 'cancelled'].includes(job.status)"
                  class="btn-danger"
                  @click="action(job, 'cancel')"
                >
                  取消
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
      <footer
        v-if="jobs.length"
        class="border-t border-[var(--border)] p-3 text-sm text-muted"
      >
        显示 {{ jobs.length }} / {{ total }} 条
        <span v-if="!laneOnly" class="ml-4 inline-flex items-center gap-2">
          <button class="btn-secondary" :disabled="page === 1" @click="page--; load()">上一页</button>
          <span>第 {{ page }} 页</span>
          <button class="btn-secondary" :disabled="page * pageSize >= total" @click="page++; load()">下一页</button>
        </span>
      </footer>
    </div>
    <div
      v-if="selected"
      class="task-drawer-backdrop"
      @click.self="closeDrawer()"
    >
      <aside
        class="task-drawer"
        role="dialog"
        aria-modal="true"
        :aria-label="`${selected.display_name} 详情`"
      >
        <header>
          <div>
            <small>{{ selected.job_type }}</small>
            <h2>{{ selected.display_name }}</h2>
          </div>
          <button
            class="icon-button"
            aria-label="关闭详情"
            @click="closeDrawer()"
          >
            ×
          </button>
        </header>
        <section
          v-if="selected.action_request"
          class="semantic-warning m-4 p-4"
        >
          <strong>{{ selected.action_request.prompt }}</strong>
          <div class="mt-3 flex flex-wrap gap-2">
            <button
              v-for="option in selected.action_request.options"
              :key="option"
              class="btn-primary"
              :disabled="!canRespond"
              @click="respond(selected, option)"
            >
              {{ option }}
            </button>
          </div>
        </section>
        <section class="p-4">
          <h3>状态时间线</h3>
          <ol class="task-timeline">
            <li v-for="event in timeline" :key="event.id">
              <strong>{{ event.event_type }}</strong><span>{{ event.from_status || "开始" }} →
                {{ event.to_status || "—" }}</span><time>{{ new Date(event.created_at).toLocaleString() }}</time>
            </li>
          </ol>
          <h3 class="mt-6">执行尝试</h3>
          <ol class="task-timeline">
            <li v-for="attempt in attempts" :key="attempt.id">
              <strong>第 {{ attempt.attempt_number }} 次 ·
                {{ attempt.status }}</strong><span>{{ attempt.error_message || "无错误" }}</span><time>{{ new Date(attempt.started_at).toLocaleString() }}</time>
            </li>
          </ol>
        </section>
      </aside>
    </div>
  </section>
</template>

