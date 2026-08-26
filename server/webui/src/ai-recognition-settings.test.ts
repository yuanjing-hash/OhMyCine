import { describe, expect, it } from 'vitest'
import { aiProviderLabel, aiRuntimeNotice, defaultOpenAIBaseURL, effectiveAIBaseURL, googleAIStudioBaseURL } from '@/ai-recognition-settings'

describe('AI recognition settings presentation', () => {
  it('keeps Google on the fixed official origin and OpenAI on an explicit HTTPS default', () => {
    expect(effectiveAIBaseURL('google_ai_studio', 'https://evil.test')).toBe(googleAIStudioBaseURL)
    expect(effectiveAIBaseURL('openai_compatible', '')).toBe(defaultOpenAIBaseURL)
  })
  it('makes the opt-in runtime boundary explicit', () => {
    expect(aiRuntimeNotice(false)).toContain('不会向 AI Provider 发起请求')
    expect(aiRuntimeNotice(true)).toContain('人工确认结果永远优先')
  })
  it('labels both supported provider protocols', () => {
    expect(aiProviderLabel('openai_compatible')).toBe('OpenAI-compatible')
    expect(aiProviderLabel('google_ai_studio')).toBe('Google AI Studio')
  })
})
