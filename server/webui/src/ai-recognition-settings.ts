import type { AIRecognitionSettings } from '@/types/api'

export const defaultOpenAIBaseURL = 'https://api.openai.com'
export const googleAIStudioBaseURL = 'https://generativelanguage.googleapis.com'

export function aiProviderLabel(provider: AIRecognitionSettings['provider_type']): string {
  return provider === 'google_ai_studio' ? 'Google AI Studio' : 'OpenAI-compatible'
}

export function effectiveAIBaseURL(provider: AIRecognitionSettings['provider_type'], current: string): string {
  if (provider === 'google_ai_studio') return googleAIStudioBaseURL
  return current.trim() || defaultOpenAIBaseURL
}

export function aiRuntimeNotice(enabled: boolean): string {
  return enabled ? '低置信度或候选冲突时允许 AI 辅助；人工确认结果永远优先。' : 'AI 辅助默认关闭；当前所有识别任务都不会向 AI Provider 发起请求。'
}
