<script setup lang="ts">
import type { DataSourceConfig, DataSourceType, MediaLibrary } from '@/services/datasource/types'
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { hasPersistentCredentialStorageWarning } from '@/services/datasource/credentialStore'
import { loginEmbyAndCreateConfig } from '@/services/datasource/emby'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { useDataSourceStore } from '@/stores/datasource'

interface DataSourceFormState {
  id: string | null
  type: DataSourceType
  displayName: string
  url: string
  username: string
  password: string
}

const store = useDataSourceStore()
const route = useRoute()
const router = useRouter()
const form = reactive<DataSourceFormState>({
  id: null,
  type: 'emby',
  displayName: 'Emby',
  url: '',
  username: '',
  password: '',
})
const mode = ref<'manage' | 'add' | 'edit'>('manage')
const isSaving = ref(false)
const clearingCacheSourceId = ref<string | null>(null)
const feedback = ref<{ type: 'success' | 'error' | 'info', message: string } | null>(null)
const lastFetchedLibraries = ref<MediaLibrary[]>([])
const persistentCredentialWarning = computed(() => hasPersistentCredentialStorageWarning())

const configuredSources = computed(() => store.orderedConfigs)
const isEditing = computed(() => mode.value === 'edit')

onMounted(() => {
  store.loadConfigs()
  mode.value = route.query.action === 'add' ? 'add' : 'manage'
})

function goManage(options: { preserveFeedback?: boolean } = {}) {
  mode.value = 'manage'
  if (!options.preserveFeedback)
    feedback.value = null
  lastFetchedLibraries.value = []
  void router.replace({ name: 'settings', query: { section: 'datasources' } })
}

function goAdd() {
  resetForm()
  mode.value = 'add'
  void router.replace({ name: 'settings', query: { section: 'datasources', action: 'add' } })
}

function resetForm() {
  form.id = null
  form.type = 'emby'
  form.displayName = 'Emby'
  form.url = ''
  form.username = ''
  form.password = ''
  feedback.value = null
  lastFetchedLibraries.value = []
}

function editSource(config: DataSourceConfig) {
  form.id = config.id
  form.type = config.type
  form.displayName = config.displayName ?? config.name
  form.url = config.url
  form.username = ''
  form.password = ''
  feedback.value = {
    type: 'info',
    message: '可修改显示名称、URL 与启用状态；如 URL 或账号变化，请输入 Emby 账号密码重新登录。',
  }
  lastFetchedLibraries.value = []
  mode.value = 'edit'
  void router.replace({ name: 'settings', query: { section: 'datasources', action: 'edit', id: config.id } })
}

async function toggleSource(config: DataSourceConfig) {
  await store.updateConfig(config.id, { enabled: config.enabled === false })
}

async function removeSource(id: string) {
  await store.removeConfig(id)
  if (form.id === id)
    goManage()
}

async function clearSourceCache(source: DataSourceConfig) {
  clearingCacheSourceId.value = source.id
  feedback.value = null
  try {
    await store.clearSourceCache(source.id)
    feedback.value = { type: 'success', message: `已清除「${source.displayName ?? source.name}」的媒体库、列表与详情缓存，凭证和配置未受影响。` }
  }
  catch (error) {
    feedback.value = {
      type: 'error',
      message: toSafeErrorMessage(error, '清除缓存失败，请稍后重试。'),
    }
  }
  finally {
    clearingCacheSourceId.value = null
  }
}

async function saveSource() {
  isSaving.value = true
  feedback.value = null
  lastFetchedLibraries.value = []
  try {
    if (mode.value === 'edit' && form.id) {
      await saveEditedSource(form.id)
      return
    }

    const id = `emby-${Date.now()}`
    const result = await loginEmbyAndCreateConfig({
      id,
      url: form.url,
      displayName: form.displayName,
      username: form.username,
      password: form.password,
      order: store.configs.length,
    })
    await store.replaceConfig(result.config)
    const libraryCount = result.libraries.length
    resetForm()
    feedback.value = { type: 'success', message: `Emby 登录成功，已获取 ${libraryCount} 个媒体库。新数据源已添加到左侧侧边栏。` }
    goManage({ preserveFeedback: true })
  }
  catch (error) {
    feedback.value = {
      type: 'error',
      message: toSafeErrorMessage(error, '添加数据源失败，请检查 Emby URL、账号和密码。'),
    }
  }
  finally {
    isSaving.value = false
  }
}

async function saveEditedSource(id: string) {
  const existing = store.configs.find(config => config.id === id)
  if (!existing)
    throw new Error('数据源不存在。')

  const username = form.username.trim()
  const shouldRelogin = Boolean(username || form.password)
  if (shouldRelogin && (!username || !form.password))
    throw new Error('重新登录时必须同时填写 Emby 账号和密码。')

  if (shouldRelogin) {
    const result = await loginEmbyAndCreateConfig({
      id,
      url: form.url,
      displayName: form.displayName,
      username,
      password: form.password,
      order: existing.order,
    })
    await store.replaceConfig({ ...result.config, enabled: existing.enabled !== false })
    feedback.value = { type: 'success', message: `Emby 已重新登录，并刷新 ${result.libraries.length} 个媒体库。` }
    form.password = ''
    goManage({ preserveFeedback: true })
    return
  }

  await store.updateConfig(id, {
    name: form.displayName.trim() || existing.name,
    displayName: form.displayName.trim() || existing.displayName,
    url: form.url.trim() || existing.url,
  })
  form.password = ''
  feedback.value = { type: 'success', message: '数据源已更新。若会话凭证已过期，请再次编辑并输入账号密码登录。' }
  goManage({ preserveFeedback: true })
}
</script>

<template>
  <div class="settings-view min-h-full p-6 pl-20 pt-16">
    <div
      v-if="feedback && mode === 'manage'"
      class="fixed right-6 top-20 z-50 max-w-md rounded-2xl border px-4 py-3 text-sm shadow-2xl backdrop-blur-xl"
      :class="{
        'border-emerald-400/20 bg-emerald-400/10 text-emerald-100': feedback.type === 'success',
        'border-red-400/20 bg-red-400/10 text-red-100': feedback.type === 'error',
        'border-white/12 bg-black/50 text-white/72': feedback.type === 'info',
      }"
    >
      {{ feedback.message }}
    </div>
    <div class="mx-auto max-w-6xl space-y-8">
      <header>
        <p class="text-xs uppercase tracking-[0.28em] text-white/38">
          Settings
        </p>
        <h1 class="mt-2 text-3xl font-bold text-white">
          设置
        </h1>
        <p class="mt-3 max-w-2xl text-sm leading-6 text-white/48">
          Player 可直接连接 Emby 浏览和播放媒体，不依赖 OhMyCine Server。当前 MVP 通过 Emby 账号密码登录，账号、密码和访问令牌保存到 Tauri app data 下的 SQLite 凭证边界中，DataSource 配置和 localStorage 只保留 credentialRef 等非敏感字段。
        </p>
      </header>

      <div
        v-if="persistentCredentialWarning"
        class="rounded-2xl border border-amber-300/20 bg-amber-300/10 px-5 py-4 text-sm leading-6 text-amber-100"
      >
        当前运行环境不可用 Tauri SQLite 凭证命令，Emby 账号、密码和 token 仅保存在内存中。请使用 Tauri 桌面应用运行以跨重启保留登录状态。
      </div>

      <section v-if="mode === 'manage'" class="glass-panel rounded-[1.75rem] p-6">
        <div class="mb-6 flex flex-wrap items-center justify-between gap-4">
          <div>
            <p class="text-xs uppercase tracking-[0.24em] text-white/34">
              Data Sources
            </p>
            <h2 class="mt-1 text-2xl font-bold text-white">
              管理数据源
            </h2>
            <p class="mt-2 text-sm text-white/42">
              已启用的数据源会显示在左侧侧边栏；停用后保留配置但不再初始化或浏览。
            </p>
          </div>
          <button class="rounded-2xl bg-primary/80 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-primary" @click="goAdd">
            添加数据源
          </button>
        </div>

        <div v-if="configuredSources.length" class="space-y-3">
          <article
            v-for="source in configuredSources"
            :key="source.id"
            class="rounded-2xl border border-white/10 bg-white/5 p-4"
          >
            <div class="flex flex-wrap items-center justify-between gap-4">
              <div class="min-w-0">
                <div class="flex items-center gap-3">
                  <span class="flex h-10 w-10 items-center justify-center rounded-2xl bg-primary/16 text-sm font-bold text-primary">{{ source.type[0].toUpperCase() }}</span>
                  <div class="min-w-0">
                    <p class="truncate text-sm font-semibold text-white">
                      {{ source.displayName ?? source.name }}
                    </p>
                    <p class="mt-1 truncate text-xs text-white/40">
                      {{ source.url }}
                    </p>
                  </div>
                </div>
                <p class="mt-3 text-xs text-white/34">
                  状态：{{ source.enabled === false ? '已停用' : '已启用' }} · 用户：{{ source.extra?.userId ?? '登录后自动获取' }}
                </p>
              </div>

              <div class="flex flex-wrap gap-2">
                <button class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/72 transition-colors hover:bg-white/14" @click="toggleSource(source)">
                  {{ source.enabled === false ? '启用' : '停用' }}
                </button>
                <button class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/72 transition-colors hover:bg-white/14" @click="editSource(source)">
                  编辑
                </button>
                <button
                  class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/72 transition-colors hover:bg-white/14 disabled:cursor-not-allowed disabled:opacity-35"
                  :disabled="source.enabled === false"
                  :title="source.enabled === false ? '请先启用该数据源再浏览' : '浏览媒体库'"
                  @click="source.enabled === false ? undefined : router.push(`/source/${source.id}`)"
                >
                  浏览
                </button>
                <button
                  class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/72 transition-colors hover:bg-white/14 disabled:cursor-not-allowed disabled:opacity-35"
                  :disabled="clearingCacheSourceId === source.id"
                  title="仅清除该数据源已加载的媒体库、列表与详情缓存，不删除凭证或配置"
                  @click="clearSourceCache(source)"
                >
                  {{ clearingCacheSourceId === source.id ? '清除中…' : '清除缓存' }}
                </button>
                <button class="rounded-xl bg-red-500/14 px-3 py-2 text-xs text-red-100 transition-colors hover:bg-red-500/24" @click="removeSource(source.id)">
                  删除
                </button>
              </div>
            </div>
          </article>
        </div>

        <div v-else class="rounded-2xl border border-dashed border-white/12 p-10 text-center">
          <p class="text-base font-semibold text-white">
            还没有数据源
          </p>
          <p class="mt-2 text-sm leading-6 text-white/42">
            添加 Emby 数据源后，它会出现在左侧侧边栏，并可进入详细媒体库浏览页。
          </p>
          <button class="mt-5 rounded-2xl bg-primary/80 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-primary" @click="goAdd">
            添加数据源
          </button>
        </div>
      </section>

      <section v-else class="glass-panel rounded-[1.75rem] p-6">
        <div class="mb-6 flex items-start justify-between gap-4">
          <div>
            <p class="text-xs uppercase tracking-[0.24em] text-white/34">
              {{ isEditing ? 'Edit Source' : 'Add Source' }}
            </p>
            <h2 class="mt-1 text-2xl font-bold text-white">
              {{ isEditing ? '编辑数据源' : '添加数据源' }}
            </h2>
            <p class="mt-2 text-sm leading-6 text-white/42">
              先选择数据源类型，再填写对应登录信息。当前仅开放 Emby；点击添加时会先登录测试，成功后在 SQLite 凭证边界保存账号、密码、token 与用户信息并拉取媒体库。
            </p>
          </div>
          <button class="rounded-2xl bg-white/8 px-4 py-2 text-sm text-white/70 transition-colors hover:bg-white/14" @click="() => goManage()">
            返回管理
          </button>
        </div>

        <form class="space-y-5" @submit.prevent="saveSource">
          <label class="block">
            <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">数据源类型</span>
            <select
              v-model="form.type"
              class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors focus:border-primary/60"
              :disabled="isEditing"
            >
              <option value="emby">
                Emby
              </option>
            </select>
          </label>

          <template v-if="form.type === 'emby'">
            <label class="block">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">显示名称</span>
              <input
                v-model="form.displayName"
                class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                placeholder="家庭 Emby"
                autocomplete="off"
              >
            </label>

            <label class="block">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">服务器 URL</span>
              <input
                v-model="form.url"
                class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                placeholder="http://192.168.1.2:8096"
                autocomplete="off"
              >
            </label>

            <label class="block">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">账号 / 用户名</span>
              <input
                v-model="form.username"
                class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                placeholder="Emby 登录账号"
                autocomplete="username"
              >
            </label>

            <label class="block">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">密码</span>
              <input
                v-model="form.password"
                class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                :placeholder="isEditing ? '留空则不重新登录' : '将保存到 SQLite 凭证边界'"
                type="password"
                autocomplete="current-password"
              >
            </label>
          </template>

          <div
            v-if="feedback"
            class="rounded-2xl border px-4 py-3 text-sm"
            :class="{
              'border-emerald-400/20 bg-emerald-400/10 text-emerald-100': feedback.type === 'success',
              'border-red-400/20 bg-red-400/10 text-red-100': feedback.type === 'error',
              'border-white/12 bg-white/6 text-white/58': feedback.type === 'info',
            }"
          >
            {{ feedback.message }}
          </div>

          <div v-if="lastFetchedLibraries.length" class="rounded-2xl border border-white/10 bg-white/5 p-4">
            <p class="text-sm font-semibold text-white">
              已获取媒体库
            </p>
            <div class="mt-3 flex flex-wrap gap-2">
              <span v-for="library in lastFetchedLibraries" :key="library.id" class="rounded-full bg-white/8 px-3 py-1 text-xs text-white/60">
                {{ library.name }}
              </span>
            </div>
          </div>

          <div class="flex justify-end gap-3 border-t border-white/8 pt-5">
            <button
              type="button"
              class="rounded-2xl bg-white/8 px-5 py-3 text-sm font-semibold text-white/70 transition-colors hover:bg-white/14"
              @click="() => goManage()"
            >
              取消
            </button>
            <button
              class="rounded-2xl bg-primary/80 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-primary disabled:cursor-not-allowed disabled:opacity-45"
              :disabled="isSaving"
            >
              {{ isSaving ? '登录测试中…' : (isEditing ? '保存' : '添加') }}
            </button>
          </div>
        </form>
      </section>
    </div>
  </div>
</template>

<style scoped>
.settings-view {
  background: var(--color-bg);
}

select option {
  color: #111827;
}
</style>
