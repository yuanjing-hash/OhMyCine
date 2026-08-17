<script setup lang="ts">
import type { PlayerFsrMode, PlayerFsrSettings, PlayerFsrTarget } from '@/services/playerInteractionSettings'

defineProps<{
  settings: PlayerFsrSettings
  error?: string | null
}>()

const emit = defineEmits<{
  update: [patch: Partial<PlayerFsrSettings>]
}>()

const modeOptions: ReadonlyArray<{ value: PlayerFsrMode, label: string, description: string }> = [
  { value: 'off', label: '关闭', description: '使用 mpv 普通缩放' },
  { value: 'auto', label: '自动', description: '兼容且确实放大时启用' },
  { value: 'force', label: '强制', description: '跳过保守能力预判，失败仍回退' },
]

const targetOptions: ReadonlyArray<{ value: PlayerFsrTarget, label: string }> = [
  { value: 'auto', label: '跟随画面' },
  { value: '1080p', label: '1080p' },
  { value: '1440p', label: '1440p' },
  { value: '2160p', label: '2160p' },
]
</script>

<template>
  <div class="fsr-settings-content">
    <div class="fsr-mode-grid" role="radiogroup" aria-label="FSR 模式">
      <button
        v-for="option in modeOptions"
        :key="option.value"
        type="button"
        class="fsr-choice"
        :class="{ 'is-selected': settings.fsrMode === option.value }"
        role="radio"
        :aria-checked="settings.fsrMode === option.value"
        :title="option.description"
        @click="emit('update', { fsrMode: option.value })"
      >
        <strong>{{ option.label }}</strong>
        <small>{{ option.description }}</small>
      </button>
    </div>

    <label class="fsr-range-row">
      <span>RCAS 锐化</span>
      <strong>{{ Math.round(settings.fsrSharpness) }}%</strong>
      <input
        type="range"
        min="0"
        max="100"
        step="1"
        :value="settings.fsrSharpness"
        :disabled="settings.fsrMode === 'off'"
        @input="emit('update', { fsrSharpness: Number(($event.target as HTMLInputElement).value) })"
      >
    </label>

    <div class="fsr-toggle-row">
      <div>
        <strong>RCAS 降噪</strong>
        <small>降低噪点区域的过度锐化</small>
      </div>
      <button
        type="button"
        class="fsr-switch"
        :class="{ 'is-enabled': settings.fsrDenoise }"
        :aria-pressed="settings.fsrDenoise"
        :disabled="settings.fsrMode === 'off'"
        @click="emit('update', { fsrDenoise: !settings.fsrDenoise })"
      >
        <span />
      </button>
    </div>

    <div>
      <p class="fsr-section-label">
        目标分辨率上限
      </p>
      <div class="fsr-target-grid" role="radiogroup" aria-label="FSR 目标分辨率上限">
        <button
          v-for="option in targetOptions"
          :key="option.value"
          type="button"
          class="fsr-target-choice"
          :class="{ 'is-selected': settings.fsrTarget === option.value }"
          role="radio"
          :aria-checked="settings.fsrTarget === option.value"
          :disabled="settings.fsrMode === 'off'"
          @click="emit('update', { fsrTarget: option.value })"
        >
          {{ option.label }}
        </button>
      </div>
      <p class="fsr-help">
        数值档按输出画面的短边限制 FSR 中间分辨率，并保持当前画面比例；不会强制切换显示器分辨率。
      </p>
    </div>

    <p v-if="error" class="fsr-error" role="status">
      {{ error }}
    </p>
  </div>
</template>

<style scoped>
.fsr-settings-content { display: grid; gap: .85rem; }
.fsr-mode-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: .45rem; }
.fsr-choice,.fsr-target-choice { border: 1px solid var(--control-border); border-radius: 10px; color: var(--color-text-secondary); background: var(--surface-soft); }
.fsr-choice { min-height: 4.5rem; padding: .65rem .55rem; text-align: left; }
.fsr-choice strong,.fsr-choice small { display: block; }
.fsr-choice strong { color: var(--color-text); font-size: .76rem; }
.fsr-choice small { margin-top: .25rem; color: var(--color-text-tertiary); font-size: .6rem; line-height: 1.35; }
.fsr-choice.is-selected,.fsr-target-choice.is-selected { border-color: var(--control-border-hover); background: var(--surface-soft-hover); }
.fsr-range-row { display: grid; grid-template-columns: 1fr auto; gap: .55rem; border-top: 1px solid var(--color-divider); padding-top: .8rem; color: var(--color-text-secondary); font-size: .75rem; }
.fsr-range-row input { grid-column: 1 / -1; width: 100%; }
.fsr-toggle-row { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
.fsr-toggle-row strong,.fsr-toggle-row small { display: block; }
.fsr-toggle-row strong { color: var(--color-text); font-size: .78rem; }
.fsr-toggle-row small { margin-top: .15rem; color: var(--color-text-tertiary); font-size: .65rem; }
.fsr-switch { width: 2.8rem; height: 1.55rem; flex: 0 0 auto; border-radius: 999px; padding: .18rem; background: var(--surface-soft-hover); }
.fsr-switch span { display: block; width: 1.18rem; height: 1.18rem; border-radius: 999px; background: var(--color-text-tertiary); transition: transform 160ms ease, background 160ms ease; }
.fsr-switch.is-enabled span { background: var(--color-text); transform: translateX(1.25rem); }
.fsr-section-label { margin-bottom: .45rem; color: var(--color-text-tertiary); font-size: .66rem; font-weight: 800; }
.fsr-target-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: .4rem; }
.fsr-target-choice { min-height: 2.5rem; padding: .35rem; font-size: .7rem; font-weight: 700; }
.fsr-help { margin-top: .5rem; color: var(--color-text-tertiary); font-size: .62rem; line-height: 1.5; }
.fsr-error { border: 1px solid rgba(251,191,36,.2); border-radius: 8px; padding: .65rem; color: #fef3c7; background: rgba(251,191,36,.1); font-size: .68rem; line-height: 1.45; }
button:disabled,input:disabled { cursor: not-allowed; opacity: .45; }
@media (max-width: 520px) { .fsr-mode-grid { grid-template-columns: 1fr; } .fsr-choice { min-height: 3.6rem; } }
</style>
