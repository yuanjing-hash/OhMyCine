<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { DirectoryItem, DirectoryListing } from '@/types/api'
import { directoryRootLabel, FILESYSTEM_ROOTS_ENDPOINT } from '@/directory-navigation'

const props = defineProps<{ open: boolean; storageId?: number | null; restrictToStorage?: boolean }>()
const emit = defineEmits<{ close: []; select: [value: { path: string; token: string }] }>()

const dialog = ref<HTMLElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)
const listing = ref<DirectoryListing | null>(null)
const loading = ref(false)
const error = ref('')
let previousFocus: HTMLElement | null = null
let activeRequest: AbortController | null = null
const restrictedHistory: string[] = []

watch(() => props.open, async open => {
	if (!open) { cancelRequest(); await nextTick(); previousFocus?.focus(); return }
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  await nextTick()
  closeButton.value?.focus()
	await loadInitial()
})

onBeforeUnmount(() => { cancelRequest(); previousFocus?.focus() })

async function loadInitial() {
  restrictedHistory.length = 0
  if (!props.storageId) { await loadRoots(); return }
  loading.value = true; error.value = ''
  const controller = beginRequest()
  try { listing.value = await api<DirectoryListing>(`/api/v1/storages/${props.storageId}/directory`, { signal: controller.signal }) }
  catch (reason) {
	if (controller.signal.aborted) return
    const staleMessage = reason instanceof Error ? reason.message : '原目录已不可用'
    try {
		listing.value = await api<DirectoryListing>(FILESYSTEM_ROOTS_ENDPOINT, { signal: controller.signal })
      error.value = `${staleMessage}；请重新选择可用目录。`
    } catch (fallbackReason) { error.value = fallbackReason instanceof Error ? fallbackReason.message : '无法读取 Server 目录' }
	} finally { if (activeRequest === controller) { activeRequest = null; loading.value = false } }
}

const withinStorage = () => Boolean(props.restrictToStorage && props.storageId)

async function browse(token: string) {
  await load('/api/v1/filesystem/directories?token=' + encodeURIComponent(token))
}

async function enter(token: string) {
  if (withinStorage() && listing.value?.current_token) restrictedHistory.push(listing.value.current_token)
  await browse(token)
}

async function backWithinStorage() {
  const token = restrictedHistory.pop()
  if (token) await browse(token)
}

async function loadRoots() { await load(FILESYSTEM_ROOTS_ENDPOINT) }

async function load(path: string) {
	const controller = beginRequest()
  loading.value = true
  error.value = ''
	try { listing.value = await api<DirectoryListing>(path, { signal: controller.signal }) }
	catch (reason) { if (!controller.signal.aborted) error.value = reason instanceof Error ? reason.message : '无法读取 Server 目录' }
	finally { if (activeRequest === controller) { activeRequest = null; loading.value = false } }
}

function beginRequest() {
	cancelRequest()
	const controller = new AbortController()
	activeRequest = controller
	return controller
}

function cancelRequest() {
	activeRequest?.abort()
	activeRequest = null
}

function choose(path: string, token: string) {
  if (!path || !token) return
  emit('select', { path, token })
  close()
}

function close() {
	cancelRequest()
  emit('close')
  nextTick(() => previousFocus?.focus())
}

function folderReason(item: DirectoryItem) {
  if (item.unavailable_reason === 'link_not_allowed') return '符号链接或 Reparse Point 不允许进入或选择'
  if (item.unavailable_reason === 'root_unavailable') return '该根位置当前不可用'
  return item.unavailable_reason || '该目录不可用'
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') { event.preventDefault(); close(); return }
  if (event.key !== 'Tab' || !dialog.value) return
  const focusable = [...dialog.value.querySelectorAll<HTMLElement>('button:not([disabled]), [href], [tabindex]:not([tabindex="-1"])')]
  if (focusable.length === 0) return
  const first = focusable[0]; const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="directory-overlay fixed inset-0 z-100 flex items-center justify-center p-4" @mousedown.self="close">
      <section ref="dialog" role="dialog" aria-modal="true" aria-labelledby="directory-picker-title" class="panel flex max-h-[min(46rem,92vh)] w-full max-w-4xl flex-col overflow-hidden" @keydown="onKeydown">
        <header class="flex items-start justify-between gap-4">
          <div><h2 id="directory-picker-title" class="m-0 text-xl">选择 Server 目录</h2><p class="page-description mb-0 mt-2 text-sm">这里显示运行 OhMyCine Server 的设备，不是当前浏览器所在设备。</p></div>
          <button ref="closeButton" type="button" class="btn-secondary" aria-label="关闭目录选择器" @click="close">关闭</button>
        </header>

        <div class="semantic-divider mt-5 flex flex-wrap items-center gap-2 border-y py-3" aria-label="目录面包屑">
          <button v-if="listing && !withinStorage()" type="button" class="root-navigation" :disabled="loading || !listing.breadcrumbs.length" @click="loadRoots">{{ directoryRootLabel(listing.platform) }}</button>
          <span v-else-if="listing" class="text-muted text-sm">来源 Storage</span>
          <span v-if="listing?.breadcrumbs.length && !withinStorage()" aria-hidden="true" class="text-subtle">/</span>
          <button v-for="crumb in withinStorage() ? [] : listing?.breadcrumbs ?? []" :key="crumb.token" type="button" class="breadcrumb-button" :disabled="loading" @click="browse(crumb.token)">{{ crumb.name }}</button>
          <span v-if="listing && !listing.breadcrumbs.length" class="text-muted text-sm">选择一个 Server 根位置开始浏览</span>
        </div>

        <div class="mt-4 flex flex-wrap gap-2">
          <button v-if="withinStorage() && restrictedHistory.length" type="button" class="btn-secondary" :disabled="loading" @click="backWithinStorage">返回上级</button>
          <button v-else-if="listing?.parent_token && !withinStorage()" type="button" class="btn-secondary" :disabled="loading" @click="browse(listing.parent_token)">返回上级</button>
          <button type="button" class="btn-secondary" :disabled="loading" @click="listing?.current_token ? browse(listing.current_token) : loadRoots()">刷新</button>
        </div>

        <p v-if="error" role="alert" class="semantic-error p-3 text-sm">{{ error }}</p>
        <div v-if="loading" role="status" class="text-muted flex min-h-48 items-center justify-center">正在读取当前层目录…</div>
        <div v-else class="semantic-inset mt-4 min-h-48 flex-1 overflow-y-auto p-2">
          <div v-if="listing && listing.items.length === 0" class="text-subtle flex min-h-44 items-center justify-center text-sm">当前层没有可显示的子目录</div>
          <div v-for="(item, index) in listing?.items ?? []" :key="item.token || item.name" class="directory-item mb-1 flex items-center gap-2 p-2">
            <button type="button" class="min-w-0 flex-1 rounded-2 px-2 py-2 text-left" :disabled="!item.enterable" :aria-describedby="item.unavailable_reason ? `directory-reason-${index}` : undefined" @click="item.token && enter(item.token)">
              <strong class="block truncate">{{ item.name }}</strong><span class="text-subtle text-xs">{{ item.kind || 'directory' }}</span>
            </button>
            <button v-if="item.selectable && item.selection_token" type="button" class="btn-secondary" @click="choose(item.location, item.selection_token)">选择</button>
            <span v-if="item.unavailable_reason" :id="`directory-reason-${index}`" class="semantic-warning-text max-w-52 text-xs">{{ folderReason(item) }}</span>
          </div>
        </div>
        <p v-if="listing?.truncated" role="status" class="semantic-warning-text mb-0 mt-3 text-xs">当前目录项目过多，仅显示前 500 个目录。</p>

        <footer class="semantic-divider mt-4 flex flex-wrap items-center justify-between gap-3 border-t pt-4">
          <div class="min-w-0"><span class="text-subtle text-xs">当前位置</span><div class="truncate font-mono text-sm">{{ listing?.location || 'Server 根位置' }}</div></div>
          <button type="button" class="btn-primary" :disabled="!listing?.current_selection_token" @click="listing && choose(listing.location, listing.current_selection_token)">选择当前目录</button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.directory-overlay { background: var(--overlay); }
.root-navigation, .breadcrumb-button { border: 0; border-radius: var(--radius-sm); background: transparent; padding: .3rem .45rem; color: var(--accent); font-size: .875rem; font-weight: 650; }
.root-navigation:hover:not(:disabled), .breadcrumb-button:hover:not(:disabled) { background: var(--accent-soft); }
.root-navigation:disabled { cursor: default; color: var(--text-muted); opacity: 1; }
.directory-item { border-radius: var(--radius-md); }
.directory-item:hover { background: var(--surface-hover); }
</style>
