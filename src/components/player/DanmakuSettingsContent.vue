<script setup lang="ts">
import type { DanmakuSettings } from '@/services/danmaku/types'

const props = defineProps<{
  settings: DanmakuSettings
  loading: boolean
  error: string | null
  commentCount: number
  showProvider?: boolean
}>()

const emit = defineEmits<{
  update: [settings: DanmakuSettings]
  reload: []
  search: []
}>()

function update<K extends keyof DanmakuSettings>(key: K, value: DanmakuSettings[K]) {
  emit('update', { ...props.settings, [key]: value })
}

function updateNumber(key: 'opacity' | 'fontScale' | 'speed' | 'density', event: Event) {
  update(key, Number.parseFloat((event.target as HTMLInputElement).value))
}

function updateKeywords(event: Event) {
  update('blockKeywords', (event.target as HTMLInputElement).value.split(/[,，]/).map(item => item.trim()).filter(Boolean))
}
</script>

<template>
  <div class="danmaku-settings-content">
    <div class="danmaku-status-row">
      <span>{{ loading ? '正在匹配…' : error || `已加载 ${commentCount} 条弹幕` }}</span>
      <span class="status-actions">
        <button type="button" @click="emit('search')">搜索</button>
        <button type="button" :disabled="loading" @click="emit('reload')">刷新</button>
      </span>
    </div>
    <div class="mode-pills" role="group" aria-label="弹幕类型">
      <button type="button" :class="{ active: settings.showScroll }" @click="update('showScroll', !settings.showScroll)">
        滚动
      </button>
      <button type="button" :class="{ active: settings.showTop }" @click="update('showTop', !settings.showTop)">
        顶部
      </button>
      <button type="button" :class="{ active: settings.showBottom }" @click="update('showBottom', !settings.showBottom)">
        底部
      </button>
    </div>
    <label class="slider-row"><span>不透明度 <output>{{ Math.round(settings.opacity * 100) }}%</output></span><input type="range" min="0.1" max="1" step="0.05" :value="settings.opacity" @input="updateNumber('opacity', $event)"></label>
    <label class="slider-row"><span>字号 <output>{{ Math.round(settings.fontScale * 100) }}%</output></span><input type="range" min="0.7" max="1.6" step="0.05" :value="settings.fontScale" @input="updateNumber('fontScale', $event)"></label>
    <label class="slider-row"><span>速度 <output>{{ settings.speed.toFixed(1) }}x</output></span><input type="range" min="0.5" max="2" step="0.1" :value="settings.speed" @input="updateNumber('speed', $event)"></label>
    <label class="slider-row"><span>密度 <output>{{ Math.round(settings.density * 100) }}%</output></span><input type="range" min="0.2" max="1" step="0.1" :value="settings.density" @input="updateNumber('density', $event)"></label>
    <div class="setting-row">
      <span>显示区域</span>
      <select :value="settings.displayArea" @change="update('displayArea', Number(($event.target as HTMLSelectElement).value))">
        <option :value="0.25">
          1/4 屏
        </option><option :value="0.5">
          半屏
        </option><option :value="0.75">
          3/4 屏
        </option><option :value="1">
          全屏
        </option>
      </select>
    </div>
    <label class="check-row"><input type="checkbox" :checked="settings.bold" @change="update('bold', ($event.target as HTMLInputElement).checked)">描边粗体</label>
    <label class="keyword-row"><span>屏蔽关键词（逗号分隔）</span><input type="text" :value="settings.blockKeywords.join('，')" placeholder="剧透，广告" @change="updateKeywords"></label>
    <template v-if="showProvider">
      <div class="provider-divider" />
      <div class="mode-pills">
        <button type="button" :class="{ active: settings.provider === 'official' }" @click="update('provider', 'official')">
          官方 API
        </button>
        <button type="button" :class="{ active: settings.provider === 'custom' }" @click="update('provider', 'custom')">
          自定义兼容 API
        </button>
      </div>
      <label v-if="settings.provider === 'custom'" class="keyword-row"><span>API 根地址</span><input type="url" :value="settings.customBaseUrl" placeholder="https://danmaku.example.com" @change="update('customBaseUrl', ($event.target as HTMLInputElement).value)"></label>
      <p class="privacy-note">
        匹配只发送媒体标题或文件名和时长，不发送本地路径、播放地址、请求头或媒体服务器凭据。
      </p>
    </template>
  </div>
</template>

<style scoped>
.danmaku-settings-content{display:grid;gap:.85rem;color:var(--color-text);font-size:.82rem}.danmaku-status-row,.setting-row{display:flex;align-items:center;justify-content:space-between;gap:.75rem}.danmaku-status-row{color:var(--color-text-secondary)}.status-actions{display:flex;gap:.35rem}button,select,input{font:inherit}.danmaku-status-row button,.mode-pills button{border:1px solid var(--control-border);border-radius:999px;background:var(--surface-soft);color:var(--color-text-secondary);padding:.42rem .72rem}.mode-pills{display:flex;gap:.4rem}.mode-pills button.active{background:var(--color-primary);color:white;border-color:transparent}.slider-row,.keyword-row{display:grid;gap:.42rem}.slider-row span{display:flex;justify-content:space-between}.slider-row output{color:var(--color-text-secondary)}input[type=range]{width:100%;accent-color:var(--color-primary)}select,.keyword-row input{min-width:0;border:1px solid var(--control-border);border-radius:.7rem;background:var(--surface-soft);color:var(--color-text);padding:.5rem .65rem}.check-row{display:flex;align-items:center;gap:.5rem}.provider-divider{height:1px;background:var(--control-border)}.privacy-note{margin:0;color:var(--color-text-tertiary);font-size:.75rem;line-height:1.55}
</style>
