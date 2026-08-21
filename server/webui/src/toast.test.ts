import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearToasts, dismissToast, notify, toasts } from '@/toast'

afterEach(() => { clearToasts(); vi.useRealTimers() })

describe('global toast notifications', () => {
  it('keeps a bounded queue and supports manual dismissal', () => {
    for (let index = 0; index < 6; index++) notify(`消息 ${index}`)
    expect(toasts.value.map(item => item.message)).toEqual(['消息 2', '消息 3', '消息 4', '消息 5'])
    dismissToast(toasts.value[0]!.id)
    expect(toasts.value).toHaveLength(3)
  })

  it('disappears automatically after the requested duration', () => {
    vi.useFakeTimers()
    notify('连接失败', 'error', 1200)
    expect(toasts.value).toHaveLength(1)
    vi.advanceTimersByTime(1200)
    expect(toasts.value).toHaveLength(0)
  })
})
