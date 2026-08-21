<script setup lang="ts">
import { computed } from 'vue'
import type { MediaLibraryDraft } from '@/media-libraries'
import type { DownloaderSummary } from '@/types/api'

const props = defineProps<{ disabled?: boolean; storageType?: string; ingestDownloaders?: DownloaderSummary[] }>()
const emit = defineEmits<{ browseIngest: [] }>()
const model = defineModel<MediaLibraryDraft>({ required: true })

const cloud115 = computed(() => props.storageType === 'pan115')
</script>

<template>
  <div>
    <section class="semantic-inset mb-5 p-4">
      <h3 class="m-0 text-base">入库与文件整理</h3>
      <p class="text-subtle mb-4 mt-1 text-sm">{{ cloud115 ? '115 离线下载完成后，会在同一账号内按这里的策略重命名并整理到当前媒体库。' : '下载仍进入全局暂存目录；完成后按这里的策略重命名并转移到当前媒体库。' }}</p>
      <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <div><label class="label">转移方式</label><select v-model="model.transfer_mode" class="input" :disabled="disabled"><option value="move">{{ cloud115 ? '云端移动' : '移动' }}</option><option value="copy">{{ cloud115 ? '云端复制' : '复制' }}</option><option v-if="!cloud115" value="symlink">软链接（保留暂存源）</option></select></div>
        <div><label class="label">同名冲突</label><select v-model="model.conflict_policy" class="input" :disabled="disabled"><option value="ask">每次询问</option><option value="overwrite">直接覆盖</option><option value="skip">跳过</option><option value="rename">自动改名</option></select></div>
      </div>
      <p class="text-subtle mb-0 mt-4 text-sm">识别预处理、电影命名和剧集命名由当前媒体库选择的规则 Profile 统一提供。<RouterLink class="semantic-link ml-1" to="/system/media-rules">打开规则管理</RouterLink></p>
      <p v-if="model.transfer_mode === 'symlink'" class="semantic-warning mb-0 mt-4 p-3 text-sm">软链接依赖暂存源，Server 不会自动清理对应下载数据；Windows 还需要允许创建符号链接。</p>
      <p v-if="model.conflict_policy === 'overwrite'" class="semantic-error mb-0 mt-4 p-3 text-sm">{{ cloud115 ? '覆盖会先把媒体库中的同名目标送入 115 回收站，再放置新文件。' : '覆盖会直接替换媒体库中的同名目标文件。' }}</p>
    </section>
    <section v-if="cloud115" class="semantic-inset mb-5 p-4">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div><h3 class="m-0 text-base">115 分享与转存接管</h3><p class="text-subtle mb-0 mt-1 text-sm">分享链接和 115 App 手工转存内容先进入独立中转目录，再复用统一识别、命名和云端整理流水线。</p></div>
        <label class="text-muted flex items-center gap-3 text-sm"><input v-model="model.ingest_enabled" type="checkbox" :disabled="disabled" />启用自动摄取</label>
      </div>
      <div v-if="model.ingest_enabled" class="mt-4 grid gap-4 md:grid-cols-2">
        <div><label class="label">绑定 115 下载器</label><select v-model="model.ingest_downloader_id" class="input" :disabled="disabled" required><option value="" disabled>请选择同账号下载器</option><option v-for="item in ingestDownloaders ?? []" :key="item.id" :value="item.id">{{ item.name }} · {{ item.provider_directory_path || '/' }}</option></select></div>
        <div><label class="label">中转目录</label><div class="flex gap-2"><input class="input font-mono" :value="model.ingest_path" readonly placeholder="请选择独立于最终媒体库的目录" /><button class="btn-secondary" type="button" :disabled="disabled" @click="emit('browseIngest')">浏览</button></div></div>
      </div>
      <p v-if="model.ingest_enabled && (ingestDownloaders ?? []).length === 0" class="semantic-warning mb-0 mt-3 p-3 text-xs">没有同一 115 账号下已启用的原生离线下载器，请先在下载管理中创建。</p>
      <p v-if="model.ingest_enabled" class="text-subtle mb-0 mt-3 text-xs">中转目录不能与最终媒体库目录或其它媒体库中转目录重叠。生活事件只负责唤醒，Server 会重新扫描目录事实并自动去重。</p>
    </section>
    <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
      <div><label class="label">周期全量间隔（小时）</label><input v-model.number="model.full_scan_interval_hours" class="input" type="number" min="1" max="720" :disabled="disabled" /></div>
      <div><label class="label">定时增量间隔（分钟）</label><input v-model.number="model.incremental_minutes" class="input" type="number" min="1" max="1440" :disabled="disabled" /></div>
      <div><label class="label">Provider 请求 / 秒</label><input v-model.number="model.provider_rate_per_second" class="input" type="number" min="1" max="1000" :disabled="disabled" /></div>
      <div><label class="label">Provider 并发</label><input v-model.number="model.provider_concurrency" class="input" type="number" min="1" max="32" :disabled="disabled" /></div>
      <div><label class="label">Metadata 请求 / 秒</label><input v-model.number="model.metadata_rate_per_second" class="input" type="number" min="1" max="100" :disabled="disabled" /></div>
      <div><label class="label">Metadata 并发</label><input v-model.number="model.metadata_concurrency" class="input" type="number" min="1" max="16" :disabled="disabled" /></div>
      <div><label class="label">TMDB 语言</label><input v-model="model.metadata_language" class="input" maxlength="16" :disabled="disabled" /></div>
      <div><label class="label">TMDB 地区</label><input v-model="model.metadata_region" class="input" maxlength="8" :disabled="disabled" /></div>
      <div><label class="label">匹配策略</label><select v-model="model.match_strategy" class="input" :disabled="disabled"><option value="balanced">平衡</option><option value="strict">严格</option><option value="lenient">宽松</option></select></div>
      <div class="md:col-span-2"><label class="label">固定视频格式（17 种）</label><textarea v-model="model.video_extensions_text" class="input" rows="3" disabled readonly></textarea><p class="text-subtle mb-0 mt-2 text-xs">视频识别集合由系统统一维护，避免不同媒体库遗漏常用格式。</p></div>
      <div class="md:col-span-2"><label class="label">伴随文件扩展</label><input v-model="model.strm_asset_extra_extensions_text" class="input" :disabled="disabled" maxlength="175" placeholder="例如 png, xml（不含点号）" /><p class="text-subtle mb-0 mt-2 text-xs">默认始终包含 srt、ssa、ass、jpg；可追加最多 16 个小写字母/数字扩展，不能填写视频格式、路径或通配符。</p></div>
      <div class="md:col-span-2"><label class="label">忽略规则（每行一个）</label><textarea v-model="model.ignore_patterns_text" class="input" rows="3" :disabled="disabled"></textarea></div>
    </div>
  </div>
</template>
