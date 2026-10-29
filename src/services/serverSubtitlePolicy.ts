export const serverSubtitleLanguages = [
  { value: 'zh-Hans', label: '简体中文' },
  { value: 'zh-Hant', label: '繁体中文' },
  { value: 'en', label: '英语' },
  { value: 'ja', label: '日语' },
  { value: 'ko', label: '韩语' },
] as const
export type ServerSubtitleLanguage = typeof serverSubtitleLanguages[number]['value']
export interface ServerSubtitlePolicy {
  mode: 'ignore' | 'prefer' | 'require'
  languages: ServerSubtitleLanguage[]
}
export function parseServerSubtitlePolicy(value: unknown): ServerSubtitlePolicy | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    return
  const data = value as Record<string, unknown>
  if (!['ignore', 'prefer', 'require'].includes(String(data.mode)) || !Array.isArray(data.languages) || data.languages.length > 5 || data.languages.some(item => !serverSubtitleLanguages.some(language => language.value === item)) || new Set(data.languages).size !== data.languages.length || (data.mode !== 'ignore' && !data.languages.length))
    return
  return { mode: data.mode as ServerSubtitlePolicy['mode'], languages: [...data.languages] as ServerSubtitleLanguage[] }
}
