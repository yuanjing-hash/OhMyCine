<script setup lang="ts">
import type { MpvPlaybackDiagnostics } from '@/composables/useMpv'
import type { PlayerFrameInterpolationQuality, PlayerFrameInterpolationSettings, PlayerFrameInterpolationTarget } from '@/services/playerInteractionSettings'
import { computed } from 'vue'

const props = defineProps<{
  settings: PlayerFrameInterpolationSettings
  diagnostics: MpvPlaybackDiagnostics | null
  error?: string | null
}>()

const emit = defineEmits<{
  update: [patch: Partial<PlayerFrameInterpolationSettings>]
}>()

const targets: ReadonlyArray<{ value: PlayerFrameInterpolationTarget, label: string }> = [
  { value: 'auto', label: '自动' },
  { value: '48', label: '48 FPS' },
  { value: '60', label: '60 FPS' },
  { value: '120', label: '120 FPS' },
]

const qualities: ReadonlyArray<{ value: PlayerFrameInterpolationQuality, label: string, flow: string }> = [
  { value: 'auto', label: '自动', flow: '动态调节' },
  { value: 'quality', label: '质量', flow: 'Flow 1.0' },
  { value: 'balanced', label: '均衡', flow: 'Flow 0.67' },
  { value: 'performance', label: '性能', flow: 'Flow 0.5' },
]

const capabilityAvailable = computed(() => props.diagnostics?.frameInterpolationCapability.supported === true)
const controlsEnabled = computed(() => capabilityAvailable.value && props.settings.frameInterpolationMode === 'auto')
const statusLabel = computed(() => {
  const state = props.diagnostics?.frameInterpolationEffectiveState
  if (state === 'active')
    return '运行中'
  if (state === 'probing')
    return '正在检测'
  if (state === 'temporary-bypass')
    return '暂时旁路'
  if (state === 'disabled' || !state)
    return '已关闭'
  return '不可用'
})
const statusReason = computed(() => props.diagnostics?.frameInterpolationReason
  || props.diagnostics?.frameInterpolationCapability.reason
  || (capabilityAvailable.value ? '硬解与当前视频对应的高精度输出链通过后自动启用。' : '正在等待原生 GPU 能力检测。'))
</script>

<template>
  <div class="frame-interpolation-settings">
    <div class="frame-status" :class="{ 'is-ready': capabilityAvailable }">
      <div>
        <strong>{{ statusLabel }}</strong>
        <small>{{ statusReason }}</small>
      </div>
      <span>{{ diagnostics?.frameInterpolationBackend || 'GPU bypass' }}</span>
    </div>

    <div class="mode-grid" role="radiogroup" aria-label="视频插帧模式">
      <button
        type="button"
        class="choice"
        :class="{ 'is-selected': settings.frameInterpolationMode === 'off' }"
        role="radio"
        :aria-checked="settings.frameInterpolationMode === 'off'"
        @click="emit('update', { frameInterpolationMode: 'off' })"
      >
        <strong>关闭</strong><small>保持原生 mpv 输出</small>
      </button>
      <button
        type="button"
        class="choice"
        :class="{ 'is-selected': settings.frameInterpolationMode === 'auto' }"
        role="radio"
        :aria-checked="settings.frameInterpolationMode === 'auto'"
        :disabled="!capabilityAvailable"
        @click="emit('update', { frameInterpolationMode: 'auto' })"
      >
        <strong>自动</strong><small>硬解、FP16 与性能通过后启用</small>
      </button>
    </div>

    <div>
      <p class="section-label">
        目标帧率
      </p>
      <div class="target-grid" role="radiogroup" aria-label="视频插帧目标帧率">
        <button
          v-for="option in targets"
          :key="option.value"
          type="button"
          class="compact-choice"
          :class="{ 'is-selected': settings.frameInterpolationTarget === option.value }"
          :disabled="!controlsEnabled"
          @click="emit('update', { frameInterpolationTarget: option.value })"
        >
          {{ option.label }}
        </button>
      </div>
    </div>

    <div>
      <p class="section-label">
        生成质量
      </p>
      <div class="quality-grid" role="radiogroup" aria-label="视频插帧生成质量">
        <button
          v-for="option in qualities"
          :key="option.value"
          type="button"
          class="choice quality-choice"
          :class="{ 'is-selected': settings.frameInterpolationQuality === option.value }"
          :disabled="!controlsEnabled"
          @click="emit('update', { frameInterpolationQuality: option.value })"
        >
          <strong>{{ option.label }}</strong><small>{{ option.flow }}</small>
        </button>
      </div>
    </div>

    <p class="help">
      SDR、HDR10/HLG/HDR10+ 与 Dolby Vision 都使用硬解和统一 FP16 合成；SDR 会在线性光中插帧后正常输出 SDR。条件不满足时立即恢复原始画面，不使用 CPU 插帧，也不会把 HDR 静默转成 SDR。
    </p>
    <p v-if="error" class="error" role="status">
      {{ error }}
    </p>
  </div>
</template>

<style scoped>
.frame-interpolation-settings { display: grid; gap: .85rem; }
.frame-status { display: flex; align-items: flex-start; justify-content: space-between; gap: .75rem; border: 1px solid rgba(251,191,36,.18); border-radius: 10px; padding: .7rem; background: rgba(251,191,36,.08); }
.frame-status.is-ready { border-color: var(--control-border-hover); background: var(--surface-soft); }
.frame-status strong,.frame-status small { display: block; }
.frame-status strong { color: var(--color-text); font-size: .76rem; }
.frame-status small { margin-top: .2rem; color: var(--color-text-tertiary); font-size: .62rem; line-height: 1.45; }
.frame-status span { flex: 0 0 auto; color: var(--color-text-tertiary); font-family: ui-monospace, monospace; font-size: .58rem; }
.mode-grid,.quality-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .45rem; }
.quality-grid { grid-template-columns: repeat(4, minmax(0, 1fr)); }
.choice,.compact-choice { border: 1px solid var(--control-border); border-radius: 10px; color: var(--color-text-secondary); background: var(--surface-soft); }
.choice { min-height: 3.7rem; padding: .6rem; text-align: left; }
.choice strong,.choice small { display: block; }
.choice strong { color: var(--color-text); font-size: .72rem; }
.choice small { margin-top: .2rem; color: var(--color-text-tertiary); font-size: .58rem; line-height: 1.35; }
.choice.is-selected,.compact-choice.is-selected { border-color: var(--control-border-hover); background: var(--surface-soft-hover); }
.target-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: .4rem; }
.compact-choice { min-height: 2.5rem; padding: .35rem; font-size: .68rem; font-weight: 700; }
.section-label { margin-bottom: .45rem; color: var(--color-text-tertiary); font-size: .66rem; font-weight: 800; }
.help { color: var(--color-text-tertiary); font-size: .62rem; line-height: 1.55; }
.error { border: 1px solid rgba(251,191,36,.2); border-radius: 8px; padding: .65rem; color: #fef3c7; background: rgba(251,191,36,.1); font-size: .68rem; line-height: 1.45; }
button:disabled { cursor: not-allowed; opacity: .42; }
@media (max-width: 520px) { .quality-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>
