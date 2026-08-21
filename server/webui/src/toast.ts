import { readonly, ref } from 'vue'

export type ToastKind = 'success' | 'error' | 'warning' | 'info'
export interface ToastMessage { id: number; kind: ToastKind; message: string }

const messages = ref<ToastMessage[]>([])
const timers = new Map<number, ReturnType<typeof setTimeout>>()
let nextID = 1

export const toasts = readonly(messages)

export function dismissToast(id: number) {
  const timer = timers.get(id)
  if (timer) clearTimeout(timer)
  timers.delete(id)
  messages.value = messages.value.filter(item => item.id !== id)
}

export function notify(message: string, kind: ToastKind = 'info', duration = kind === 'error' ? 6000 : 4000) {
  const normalized = message.trim() || '操作已完成'
  const item = { id: nextID++, kind, message: normalized }
  messages.value = [...messages.value.slice(-3), item]
  timers.set(item.id, setTimeout(() => dismissToast(item.id), Math.max(1000, duration)))
  return item.id
}

export function clearToasts() {
  for (const timer of timers.values()) clearTimeout(timer)
  timers.clear()
  messages.value = []
}
