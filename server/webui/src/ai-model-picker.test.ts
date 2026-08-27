// @vitest-environment happy-dom

import { readFileSync } from 'node:fs'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { filterAIModels } from '@/ai-model-picker'
import AIModelPickerDialog from '@/components/AIModelPickerDialog.vue'
import type { AIProviderModel } from '@/types/api'

const models: AIProviderModel[] = [
  { id: 'openai/gpt-4.1', display_name: 'GPT 4.1' },
  { id: 'google/gemini-2.5-flash', display_name: 'Gemini Flash' },
  { id: 'deepseek/deepseek-chat', display_name: 'DeepSeek V3' },
]

describe('AI model picker', () => {
  let wrapper: VueWrapper | null = null

  beforeEach(() => {
    document.body.innerHTML = '<button id="model-picker-trigger">获取模型列表</button>'
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
  })

  it('filters every model by ID or display name without case sensitivity', () => {
    expect(filterAIModels(models, '')).toEqual(models)
    expect(filterAIModels(models, '  GEMINI  ').map(model => model.id)).toEqual(['google/gemini-2.5-flash'])
    expect(filterAIModels(models, 'v3').map(model => model.id)).toEqual(['deepseek/deepseek-chat'])
    expect(filterAIModels(models, 'missing')).toEqual([])
  })

  it('mounts the teleported dialog, searches, marks the current model, and selects a whole row', async () => {
    const trigger = document.querySelector<HTMLButtonElement>('#model-picker-trigger')!
    trigger.focus()
    wrapper = mount(AIModelPickerDialog, {
      attachTo: document.body,
      props: { open: true, models, selectedModel: models[1].id },
    })
    await nextTick()

    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!
    const search = document.querySelector<HTMLInputElement>('#ai-model-search')!
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(document.activeElement).toBe(search)
    expect(document.querySelector('[aria-pressed="true"]')?.textContent).toContain(models[1].id)

    search.value = '  v3  '
    search.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    const rows = [...document.querySelectorAll<HTMLButtonElement>('.model-picker-item')]
    expect(rows).toHaveLength(1)
    expect(rows[0].textContent).toContain('deepseek/deepseek-chat')

    rows[0].click()
    expect(wrapper.emitted('select')).toEqual([[models[2].id]])
    expect(wrapper.emitted('close')).toHaveLength(1)

    await wrapper.setProps({ open: false })
    await nextTick()
    expect(document.activeElement).toBe(trigger)
  })

  it('closes through Escape, backdrop, and close button and traps Tab focus', async () => {
    wrapper = mount(AIModelPickerDialog, {
      attachTo: document.body,
      props: { open: true, models, selectedModel: '' },
    })
    await nextTick()

    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!
    const closeButton = dialog.querySelector<HTMLButtonElement>('button[aria-label="关闭 AI 模型选择器"]')!
    const lastRow = [...dialog.querySelectorAll<HTMLButtonElement>('.model-picker-item')].at(-1)!

    closeButton.focus()
    closeButton.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(lastRow)
    lastRow.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(closeButton)

    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    expect(wrapper.emitted('close')).toHaveLength(1)
    document.querySelector<HTMLElement>('.model-picker-overlay')!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    expect(wrapper.emitted('close')).toHaveLength(2)
    closeButton.click()
    expect(wrapper.emitted('close')).toHaveLength(3)
  })

  it('renders loading, empty-list, and no-search-result states from live props', async () => {
    wrapper = mount(AIModelPickerDialog, {
      attachTo: document.body,
      props: { open: true, models: [], selectedModel: '', loading: true },
    })
    await nextTick()
    expect(document.body.textContent).toContain('正在读取模型列表…')

    await wrapper.setProps({ loading: false })
    expect(document.body.textContent).toContain('Provider 没有返回可用模型')

    await wrapper.setProps({ models })
    const search = document.querySelector<HTMLInputElement>('#ai-model-search')!
    search.value = 'missing-model'
    search.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    expect(document.body.textContent).toContain('没有匹配“missing-model”的模型')
  })

  it('wires successful loading to the dialog without auto-saving or datalist fallback', () => {
    const source = readFileSync('src/views/SettingsView.vue', 'utf8')
    expect(source).toContain('aiModelPickerOpen.value = true')
    expect(source).toContain('aiModel.value = modelID')
    expect(source).toContain("notify('模型 ID 已回填，尚未保存'")
    expect(source).toContain('<AIModelPickerDialog')
    expect(source).not.toContain('<datalist')
    expect(source).not.toContain('aiModel.value = result.list[0].id')
  })
})
