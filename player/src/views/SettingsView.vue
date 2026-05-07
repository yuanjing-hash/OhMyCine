<script setup lang="ts">
import type { DataSourceConfig } from '@/services/datasource/types'
import { computed, onMounted, reactive, ref } from 'vue'
import { createCredentialRef, removeSessionCredential, saveSessionCredential } from '@/services/datasource/credentialStore'
import { toSafeErrorMessage } from '@/services/datasource/errors'
import { createDataSource } from '@/services/datasource/manager'
import { useDataSourceStore } from '@/stores/datasource'

interface EmbyFormState {
  id: string | null
  displayName: string
  url: string
  userId: string
  token: string
}

const store = useDataSourceStore()
const form = reactive<EmbyFormState>({
  id: null,
  displayName: 'Emby',
  url: '',
  userId: '',
  token: '',
})
const isTesting = ref(false)
const isSaving = ref(false)
const feedback = ref<{ type: 'success' | 'error' | 'info', message: string } | null>(null)

const embySources = computed(() => store.orderedConfigs.filter(config => config.type === 'emby'))
const isEditing = computed(() => form.id != null)

onMounted(() => {
  store.loadConfigs()
})

function resetForm() {
  form.id = null
  form.displayName = 'Emby'
  form.url = ''
  form.userId = ''
  form.token = ''
  feedback.value = null
}

function editSource(config: DataSourceConfig) {
  form.id = config.id
  form.displayName = config.displayName ?? config.name
  form.url = config.url
  form.userId = typeof config.extra?.userId === 'string' ? config.extra.userId : ''
  form.token = ''
  feedback.value = {
    type: 'info',
    message: '访问令牌只保存在当前会话中；如需重新测试或浏览，请重新输入令牌。',
  }
}

async function testConnection() {
  isTesting.value = true
  feedback.value = null
  try {
    const config = buildConfig('emby-test')
    const source = createDataSource('emby')
    await source.init(config)
    const ok = await source.test()
    source.destroy()
    feedback.value = {
      type: 'success',
      message: ok ? 'Emby 连接测试成功。' : 'Emby 连接测试未通过。',
    }
  }
  catch (error) {
    feedback.value = {
      type: 'error',
      message: toSafeErrorMessage(error, 'Emby 连接测试失败，请检查 URL、用户 ID 与访问令牌。'),
    }
  }
  finally {
    isTesting.value = false
  }
}

async function saveSource() {
  isSaving.value = true
  feedback.value = null
  try {
    if (form.id) {
      const config = buildConfig(form.id)
      await store.updateConfig(form.id, config)
      feedback.value = { type: 'success', message: 'Emby 数据源已更新。' }
    }
    else {
      const id = `emby-${Date.now()}`
      const credentialRef = createCredentialRef(id)
      if (form.token.trim())
        saveSessionCredential(credentialRef, form.token.trim())
      try {
        await store.addConfig({
          id,
          type: 'emby',
          name: form.displayName.trim() || 'Emby',
          displayName: form.displayName.trim() || 'Emby',
          url: form.url.trim(),
          extra: {
            userId: form.userId.trim(),
            credentialRef,
            deviceId: `ohmycine-${id}`,
          },
        })
      }
      catch (error) {
        removeSessionCredential(credentialRef)
        throw error
      }
      feedback.value = { type: 'success', message: 'Emby 数据源已添加，并会出现在左侧侧边栏。' }
      resetForm()
    }
  }
  catch (error) {
    feedback.value = {
      type: 'error',
      message: toSafeErrorMessage(error, '保存 Emby 数据源失败。'),
    }
  }
  finally {
    isSaving.value = false
  }
}

async function removeSource(id: string) {
  await store.removeConfig(id)
  if (form.id === id)
    resetForm()
}

function buildConfig(id: string): DataSourceConfig {
  const trimmedUrl = form.url.trim()
  const trimmedUserId = form.userId.trim()
  const credentialRef = createCredentialRef(id)
  const existing = store.configs.find(config => config.id === id)
  const existingCredentialRef = typeof existing?.extra?.credentialRef === 'string'
    ? existing.extra.credentialRef
    : credentialRef
  const ref = existingCredentialRef

  if (!trimmedUrl)
    throw new Error('请输入 Emby 服务器 URL。')
  if (!trimmedUserId)
    throw new Error('请输入 Emby 用户 ID。')
  if (form.token.trim())
    saveSessionCredential(ref, form.token.trim())

  return {
    id,
    type: 'emby',
    name: form.displayName.trim() || 'Emby',
    displayName: form.displayName.trim() || 'Emby',
    order: existing?.order ?? store.configs.length,
    url: trimmedUrl,
    extra: {
      userId: trimmedUserId,
      credentialRef: ref,
      deviceId: typeof existing?.extra?.deviceId === 'string' ? existing.extra.deviceId : `ohmycine-${id}`,
    },
  }
}
</script>

<template>
  <div class="settings-view min-h-full p-6 pl-20 pt-16">
    <div class="mx-auto max-w-6xl space-y-8">
      <header>
        <p class="text-xs uppercase tracking-[0.28em] text-white/38">
          Settings
        </p>
        <h1 class="mt-2 text-3xl font-bold text-white">
          数据源设置
        </h1>
        <p class="mt-3 max-w-2xl text-sm leading-6 text-white/48">
          Player 可直接连接 Emby 浏览和播放媒体，不依赖 OhMyCine Server。当前 MVP 将访问令牌保存在会话存储中，避免写入 localStorage；重启应用后需要重新输入令牌。
        </p>
      </header>

      <div class="grid gap-6 lg:grid-cols-[1fr_1.2fr]">
        <section class="glass-panel rounded-[1.75rem] p-6">
          <div class="mb-5 flex items-center justify-between">
            <div>
              <h2 class="text-xl font-bold text-white">
                已配置数据源
              </h2>
              <p class="mt-1 text-xs text-white/38">
                左侧侧边栏会按此列表展示
              </p>
            </div>
            <button class="rounded-2xl bg-white/8 px-4 py-2 text-xs font-semibold text-white transition-colors hover:bg-white/14" @click="resetForm">
              新增 Emby
            </button>
          </div>

          <div v-if="embySources.length" class="space-y-3">
            <article
              v-for="source in embySources"
              :key="source.id"
              class="rounded-2xl border border-white/10 bg-white/5 p-4"
            >
              <div class="flex items-start justify-between gap-4">
                <div class="min-w-0">
                  <p class="truncate text-sm font-semibold text-white">
                    {{ source.displayName ?? source.name }}
                  </p>
                  <p class="mt-1 truncate text-xs text-white/40">
                    {{ source.url }}
                  </p>
                  <p class="mt-1 text-xs text-white/28">
                    User ID: {{ source.extra?.userId ?? '未设置' }}
                  </p>
                </div>
                <div class="flex shrink-0 gap-2">
                  <button class="rounded-xl bg-white/8 px-3 py-2 text-xs text-white/72 transition-colors hover:bg-white/14" @click="editSource(source)">
                    编辑
                  </button>
                  <button class="rounded-xl bg-red-500/14 px-3 py-2 text-xs text-red-100 transition-colors hover:bg-red-500/24" @click="removeSource(source.id)">
                    删除
                  </button>
                </div>
              </div>
            </article>
          </div>

          <div v-else class="rounded-2xl border border-dashed border-white/12 p-8 text-center">
            <p class="text-sm font-medium text-white/72">
              还没有 Emby 数据源
            </p>
            <p class="mt-2 text-xs leading-5 text-white/38">
              在右侧填写服务器 URL、用户 ID 与访问令牌后即可添加。
            </p>
          </div>
        </section>

        <section class="glass-panel rounded-[1.75rem] p-6">
          <h2 class="text-xl font-bold text-white">
            {{ isEditing ? '编辑 Emby 数据源' : '添加 Emby 数据源' }}
          </h2>
          <p class="mt-2 text-sm leading-6 text-white/45">
            访问令牌来自 Emby 用户设置或 API Key。JSON 接口请求使用授权头；图片/播放 URL 仍需 tokenized URL，错误提示会做脱敏处理。
          </p>

          <form class="mt-6 space-y-5" @submit.prevent="saveSource">
            <label class="block">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">显示名称</span>
              <input
                v-model="form.displayName"
                class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                placeholder="家庭 Emby"
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
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">用户 ID</span>
              <input
                v-model="form.userId"
                class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                placeholder="Emby User.Id"
                autocomplete="off"
              >
            </label>

            <label class="block">
              <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">访问令牌 / API Key</span>
              <input
                v-model="form.token"
                class="mt-2 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none transition-colors placeholder:text-white/25 focus:border-primary/60"
                placeholder="只保存在当前应用会话"
                type="password"
                autocomplete="off"
              >
            </label>

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

            <div class="flex flex-wrap gap-3">
              <button
                type="button"
                class="rounded-2xl bg-white/8 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/14 disabled:cursor-not-allowed disabled:opacity-45"
                :disabled="isTesting"
                @click="testConnection"
              >
                {{ isTesting ? '测试中…' : '测试连接' }}
              </button>
              <button
                class="rounded-2xl bg-primary/80 px-5 py-3 text-sm font-semibold text-white transition-colors hover:bg-primary disabled:cursor-not-allowed disabled:opacity-45"
                :disabled="isSaving"
              >
                {{ isSaving ? '保存中…' : (isEditing ? '保存修改' : '添加数据源') }}
              </button>
              <button
                v-if="isEditing"
                type="button"
                class="rounded-2xl bg-white/6 px-5 py-3 text-sm font-semibold text-white/70 transition-colors hover:bg-white/12"
                @click="resetForm"
              >
                取消编辑
              </button>
            </div>
          </form>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.settings-view {
  background: var(--color-bg);
}
</style>
