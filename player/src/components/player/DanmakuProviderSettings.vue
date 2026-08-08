<script setup lang="ts">
import type { DanmakuSettings } from '@/services/danmaku/types'
import { ref } from 'vue'
import { loadDanmakuSettings, saveDanmakuSettings } from '@/services/danmaku/settings'
import { toSafeErrorMessage } from '@/services/datasource/errors'

const form = ref<DanmakuSettings>(loadDanmakuSettings())
const saving = ref(false)
const feedback = ref('')

async function save() {
  saving.value = true
  feedback.value = ''
  try {
    form.value = await saveDanmakuSettings(form.value)
    feedback.value = '弹幕服务设置已保存。'
  }
  catch (error) {
    feedback.value = toSafeErrorMessage(error, '弹幕服务设置保存失败。')
  }
  finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="mt-5 border-b border-white/8 pb-5">
    <p class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">
      弹幕服务
    </p>
    <p class="mt-2 text-xs leading-5 text-white/38">
      默认使用弹弹play 官方 API，也可切换到兼容相同接口和标准格式的自建服务。
    </p>
    <div class="mt-4 flex flex-wrap gap-2">
      <button type="button" class="provider-choice" :class="{ active: form.provider === 'official' }" @click="form.provider = 'official'">
        官方 API
      </button>
      <button type="button" class="provider-choice" :class="{ active: form.provider === 'custom' }" @click="form.provider = 'custom'">
        自定义兼容 API
      </button>
    </div>
    <label v-if="form.provider === 'custom'" class="mt-4 block rounded-2xl bg-black/16 p-4">
      <span class="text-xs font-semibold uppercase tracking-[0.18em] text-white/42">API 根地址</span>
      <input v-model.trim="form.customBaseUrl" type="url" class="mt-3 w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white outline-none focus:border-primary/60" placeholder="https://danmaku.example.com">
    </label>
    <p class="mt-3 text-xs leading-5 text-white/38">
      隐私说明：匹配只发送媒体标题或文件名和时长，不发送本地路径、播放地址、Emby API Key、请求头或其他凭据。
    </p>
    <div class="mt-4 flex items-center gap-3">
      <button type="button" class="rounded-2xl bg-primary px-5 py-2.5 text-sm font-semibold text-white disabled:opacity-50" :disabled="saving" @click="save">
        {{ saving ? '保存中…' : '保存弹幕服务' }}
      </button>
      <span v-if="feedback" class="text-xs text-white/56">{{ feedback }}</span>
    </div>
  </section>
</template>

<style scoped>
.provider-choice{border:1px solid rgb(255 255 255/.1);border-radius:999px;background:rgb(255 255 255/.06);padding:.6rem 1rem;color:rgb(255 255 255/.62);font-size:.82rem;font-weight:700}.provider-choice.active{border-color:transparent;background:var(--color-primary);color:#fff}
</style>
