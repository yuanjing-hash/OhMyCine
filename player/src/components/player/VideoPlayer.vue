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
    <div class="absolute inset-0 flex items-center justify-center">
      <div v-if="!hasMedia" class="glass-panel max-w-md rounded-3xl p-8 text-center">
        <p class="text-lg font-semibold text-white">
          拖拽文件到此处播放
        </p>
        <p class="mt-3 text-sm leading-6 text-white/50">
          支持 libmpv 可播放的本地视频文件。加载后可使用底部控制条播放、暂停、拖动进度和调节音量。
        </p>
      </div>
      <div v-else-if="!isPlaying" class="text-sm text-white/35">
        已暂停
      </div>
    </div>
  </div>
</template>
