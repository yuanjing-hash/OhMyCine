<script setup lang="ts">
import type { MpvRenderStatus } from '@/composables/useMpv'
import { computed } from 'vue'

const props = defineProps<{
  isPlaying: boolean
  hasMedia: boolean
  renderStatus: MpvRenderStatus
  renderError: string | null
}>()

const emit = defineEmits<{
  fileDrop: [path: string]
}>()

const renderTitle = computed(() => {
  switch (props.renderStatus) {
    case 'initializing':
      return '正在初始化内嵌渲染'
    case 'ready':
      return '内嵌渲染已就绪'
    case 'unsupported':
      return '当前平台暂未启用内嵌渲染'
    case 'error':
      return '内嵌渲染初始化失败'
    case 'idle':
    default:
      return '内嵌渲染基础已接入'
  }
})

const renderDescription = computed(() => {
  if (props.renderError)
    return props.renderError

  switch (props.renderStatus) {
    case 'initializing':
      return '正在查询 True libmpv render API 后端状态。'
    case 'ready':
      return '后续播放将使用应用自有渲染目标；播放、暂停、进度和音量控制仍由当前控制通路处理。'
    case 'unsupported':
      return 'OhMyCine 保留跨平台渲染后端架构；此平台的实际 native surface/render loop 会在后续里程碑接入，当前仍安全禁止外部 mpv 窗口。'
    case 'error':
      return '播放器已保留 no-external-window 安全保护，请稍后重试或查看运行日志。'
    case 'idle':
    default:
      return 'True render API 绑定与状态通路已经接入；下一步将实现平台 native surface 与 OpenGL render loop。'
  }
})

function handleDrop(event: DragEvent) {
  const file = event.dataTransfer?.files.item(0) as (File & { path?: string }) | null
  if (!file?.path)
    return
  emit('fileDrop', file.path)
}
</script>

<template>
  <div
    class="relative h-full w-full overflow-hidden bg-black"
    @dragover.prevent
    @drop.prevent="handleDrop"
  >
    <div class="absolute inset-0 bg-[radial-gradient(circle_at_center,rgba(74,158,255,0.14),transparent_34%),linear-gradient(135deg,#050509,#090911_52%,#030305)]" />
    <div class="absolute inset-0 flex items-center justify-center px-8">
      <div v-if="!hasMedia" class="glass-panel max-w-md rounded-3xl p-8 text-center">
        <p class="text-lg font-semibold text-white">
          拖拽文件到此处播放
        </p>
        <p class="mt-3 text-sm leading-6 text-white/50">
          支持 libmpv 可播放的本地视频文件。加载后可使用底部控制条播放、暂停、拖动进度和调节音量。
        </p>
      </div>
      <div v-else class="glass-panel max-w-xl rounded-3xl p-8 text-center">
        <p class="text-xs uppercase tracking-[0.24em] text-white/35">
          {{ renderStatus }}
        </p>
        <p class="mt-3 text-base font-semibold text-white">
          {{ renderTitle }}
        </p>
        <p class="mt-3 text-sm leading-6 text-white/54">
          {{ renderDescription }}
        </p>
        <p v-if="!isPlaying" class="mt-4 text-xs uppercase tracking-[0.22em] text-white/34">
          Paused
        </p>
      </div>
    </div>
  </div>
</template>
