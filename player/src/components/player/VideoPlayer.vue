<script setup lang="ts">
defineProps<{
  isPlaying: boolean
  hasMedia: boolean
}>()

const emit = defineEmits<{
  fileDrop: [path: string]
}>()

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
        <p class="text-base font-semibold text-white">
          视频内嵌渲染仍在接入中
        </p>
        <p class="mt-3 text-sm leading-6 text-white/54">
          本地文件已交给播放后端加载；当前版本已阻止弹出独立播放窗口。窗口内视频画面还在接入中，本页控制条可用于验证播放、暂停、进度和音量通路。
        </p>
        <p v-if="!isPlaying" class="mt-4 text-xs uppercase tracking-[0.22em] text-white/34">
          Paused
        </p>
      </div>
    </div>
  </div>
</template>
