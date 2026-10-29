<script setup lang="ts">
import type { ServerResourceRecognition } from '@/services/serverResourceRecognition'
import { serverTitleEpisodeLabel } from '@/services/serverResourceRecognition'
import { shareContentGroupLabel } from '@/services/serverShareContent'

defineProps<{ cloud: boolean, titleOnly?: boolean, result?: ServerResourceRecognition, loading?: boolean, error?: string }>()
</script>

<template>
  <section class="mt-3 rounded-xl border border-white/8 bg-white/[.025] px-3 py-2 text-xs leading-5 text-[var(--color-text-secondary)]" :aria-label="cloud && !titleOnly ? '分享文件分析' : titleOnly ? '标题识别' : 'BT 标题识别'" role="status">
    <template v-if="cloud && !titleOnly && result?.shareAnalysis">
      <p class="font-semibold text-white/85">
        {{ result.shareAnalysis.complete ? '已读取分享内容' : '分享内容读取未完成' }}<template v-if="result.shareAnalysis.complete">
          · {{ result.shareAnalysis.video_count }} 个视频
        </template>
      </p>
      <p v-for="(group, index) in result.shareAnalysis.groups" :key="`${group.tmdb_id}:${group.season}:${index}`" class="mt-1 break-words">
        {{ shareContentGroupLabel(group) }}<span v-if="group.status !== 'matched'"> · 作品待确认</span>
      </p>
      <p v-if="result.shareAnalysis.complete && result.shareAnalysis.ignored_count" class="mt-1">
        已忽略 {{ result.shareAnalysis.ignored_count }} 个非视频文件
      </p>
      <p v-if="result.shareAnalysis.unresolved_count" class="mt-1">
        另有 {{ result.shareAnalysis.unresolved_count }} 个视频的作品或集数待确认
      </p>
      <p v-if="result.shareAnalysis.message" class="mt-1">
        {{ result.shareAnalysis.message }}
      </p>
    </template>
    <template v-else-if="(!cloud || titleOnly) && result">
      <p>{{ titleOnly ? result.status === 'matched' ? '标题识别' : '标题待确认' : result.status === 'matched' ? 'BT 标题识别' : 'BT 标题待确认' }} · {{ result.title }}</p>
      <p v-if="serverTitleEpisodeLabel(result)" class="mt-1">
        {{ serverTitleEpisodeLabel(result) }}
      </p>
    </template>
    <p v-else>
      {{ error || (loading ? cloud && !titleOnly ? '正在读取分享文件…' : '正在识别 BT 标题…' : result && cloud ? '当前 Server 未返回分享文件分析；标题仅供参考，请升级 Server 后重新检测。' : cloud ? '等待读取分享文件…' : '等待识别 BT 标题…') }}
    </p>
  </section>
</template>
