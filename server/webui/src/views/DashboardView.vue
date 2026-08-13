<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api/client'
import { getVisibleDashboardCards } from '@/dashboard/cards'
import { useAuthStore } from '@/stores/auth'

interface Summary {
  initialized: boolean
  recovery_required: boolean
  users: number
  active_users: number
  roles: number
  audit_events: number
}

const auth = useAuthStore()
const summary = ref<Summary | null>(null)
const loading = ref(true)
const error = ref('')
const updatedAt = ref<Date | null>(null)
const cards = computed(() => getVisibleDashboardCards(auth.user?.permissions ?? []))

async function loadBaseline() {
  loading.value = true
  error.value = ''
  try {
    summary.value = await api<Summary>('/api/v1/dashboard')
    updatedAt.value = new Date()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : 'Server 基线加载失败'
  } finally {
    loading.value = false
  }
}

function formatTime(value: Date | null) {
  return value ? value.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '尚未更新'
}

onMounted(loadBaseline)
</script>

<template>
  <section>
    <header class="dashboard-heading">
      <div>
        <h1 class="m-0 text-3xl font-800">欢迎回来，{{ auth.user?.display_name }}</h1>
        <p class="page-description mt-2">先确认状态与告警，再处理任务、流水线和订阅；发现内容始终后置。</p>
      </div>
      <div class="baseline-time"><span>管理基线</span><strong>更新于 {{ formatTime(updatedAt) }}</strong></div>
    </header>

    <div class="dashboard-grid mt-7" aria-label="Server 仪表盘卡片">
      <article
        v-for="card in cards"
        :key="card.id"
        class="dashboard-card"
        :class="`dashboard-card--span-${card.span}`"
        :data-card-id="card.id"
        :data-section="card.section"
      >
        <template v-if="card.id === 'server-status'">
          <div class="server-card__heading">
            <div>
              <span class="card-kicker">管理基线</span>
              <h2>{{ card.title }}</h2>
            </div>
            <span v-if="loading" class="status-chip">读取中</span>
            <span v-else-if="error" class="status-chip status-chip--error">局部错误</span>
            <span v-else-if="summary?.recovery_required" class="status-chip status-chip--warning">需要恢复</span>
            <span v-else class="status-chip status-chip--ready">API 已响应</span>
          </div>

          <div v-if="loading" class="baseline-state" aria-live="polite">正在读取现有 `/api/v1/dashboard` 管理基线…</div>
          <div v-else-if="error" class="baseline-state baseline-state--error" role="alert">
            <div><strong>Server 基线暂时不可用</strong><p>{{ error }}</p></div>
            <button class="btn-secondary" type="button" @click="loadBaseline">重试此卡片</button>
          </div>
          <div v-else-if="summary" class="server-card__content">
            <div class="server-state-mark" :class="summary.recovery_required ? 'server-state-mark--warning' : ''">
              <span></span>
              <div><strong>{{ summary.recovery_required ? '管理数据需要安全恢复' : summary.initialized ? '管理数据库已初始化' : '初始化尚未完成' }}</strong><small>这里只报告现有管理 API 的真实状态，不推断媒体、存储或调度器健康。</small></div>
            </div>
            <dl class="administration-baseline">
              <div><dt>有效账户</dt><dd>{{ summary.active_users }} / {{ summary.users }}</dd></div>
              <div><dt>角色</dt><dd>{{ summary.roles }}</dd></div>
              <div><dt>审计事件</dt><dd>{{ summary.audit_events }}</dd></div>
            </dl>
          </div>
        </template>

        <template v-else>
          <div class="planned-card__heading">
            <span class="card-kicker">{{ card.owner }}</span>
            <span class="status-chip status-chip--planned">规划 / 未配置</span>
          </div>
          <h2>{{ card.title }}</h2>
          <p>{{ card.description }}</p>
          <footer>数据 owner：{{ card.owner }}</footer>
        </template>
      </article>
    </div>
  </section>
</template>

<style scoped>
.dashboard-heading { display: flex; align-items: flex-end; justify-content: space-between; gap: 1.5rem; }
.baseline-time { flex: 0 0 auto; border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--surface-muted); padding: .65rem .8rem; text-align: right; }
.baseline-time span, .baseline-time strong { display: block; }
.baseline-time span { color: var(--text-subtle); font-size: .68rem; }
.baseline-time strong { margin-top: .15rem; color: var(--text); font-size: .72rem; }
.dashboard-grid { display: grid; grid-template-columns: repeat(12, minmax(0, 1fr)); gap: 1rem; }
.dashboard-card { min-width: 0; overflow: hidden; border: 1px solid var(--border); border-radius: var(--radius-lg); background: var(--surface); padding: 1rem; }
.dashboard-card--span-3 { grid-column: span 3; }
.dashboard-card--span-4 { grid-column: span 4; }
.dashboard-card--span-5 { grid-column: span 5; }
.dashboard-card--span-7 { grid-column: span 7; }
.dashboard-card--span-12 { grid-column: span 12; }
.dashboard-card h2 { margin: .65rem 0 0; font-size: 1.02rem; }
.dashboard-card p { margin: .65rem 0 0; color: var(--text-muted); font-size: .8rem; line-height: 1.65; }
.dashboard-card footer { margin-top: 1rem; border-top: 1px solid var(--border); padding-top: .7rem; color: var(--text-subtle); font-size: .68rem; }
.card-kicker { overflow: hidden; color: var(--text-subtle); font-size: .68rem; font-weight: 700; text-overflow: ellipsis; white-space: nowrap; }
.server-card__heading, .planned-card__heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; }
.server-card__heading h2 { margin: .35rem 0 0; font-size: 1.15rem; }
.server-card__content { display: grid; grid-template-columns: minmax(16rem, 1fr) minmax(22rem, 1.4fr); align-items: center; gap: 1.25rem; margin-top: 1.2rem; }
.server-state-mark { display: flex; align-items: center; gap: .8rem; }
.server-state-mark > span { height: .7rem; width: .7rem; flex: 0 0 auto; border-radius: 99px; background: var(--success); }
.server-state-mark--warning > span { background: var(--warning); }
.server-state-mark strong, .server-state-mark small { display: block; }
.server-state-mark small { margin-top: .3rem; color: var(--text-subtle); font-size: .7rem; line-height: 1.5; }
.administration-baseline { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: .55rem; margin: 0; }
.administration-baseline div { border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--surface-muted); padding: .7rem .8rem; }
.administration-baseline dt { color: var(--text-subtle); font-size: .65rem; }
.administration-baseline dd { margin: .2rem 0 0; color: var(--text); font-size: 1rem; font-weight: 750; }
.baseline-state { margin-top: 1.2rem; border: 1px dashed var(--border-strong); border-radius: var(--radius-md); padding: 1rem; color: var(--text-muted); font-size: .8rem; }
.baseline-state--error { display: flex; align-items: center; justify-content: space-between; gap: 1rem; border-color: var(--danger-border); background: var(--danger-soft); }
.baseline-state--error strong, .baseline-state--error p { color: var(--danger); }

@media (min-width: 768px) and (max-width: 1279px) {
  .dashboard-grid { grid-template-columns: repeat(6, minmax(0, 1fr)); }
  .dashboard-card { grid-column: span 3; }
  .dashboard-card--span-5, .dashboard-card--span-7, .dashboard-card--span-12 { grid-column: span 6; }
  .server-card__content { grid-template-columns: 1fr; }
}

@media (max-width: 767px) {
  .dashboard-heading { align-items: flex-start; flex-direction: column; }
  .baseline-time { width: 100%; text-align: left; }
  .dashboard-grid { grid-template-columns: minmax(0, 1fr); }
  .dashboard-card { grid-column: span 1; }
  .server-card__content { grid-template-columns: 1fr; }
  .administration-baseline { grid-template-columns: 1fr; }
  .baseline-state--error { align-items: stretch; flex-direction: column; }
}
</style>
