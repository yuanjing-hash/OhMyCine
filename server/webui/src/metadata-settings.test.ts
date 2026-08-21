import { describe, expect, it } from 'vitest'
import { credentialKindLabel, credentialSourceLabel, defaultTMDBAPIBaseURL, defaultTMDBImageBaseURL } from '@/metadata-settings'
import type { MetadataSettings } from '@/types/api'

describe('TMDB settings presentation', () => {
  it('shows every safe credential source without exposing a token', () => {
    const sources: MetadataSettings['credential_source'][] = ['custom', 'deployment', 'builtin', 'none']
    expect(sources.map(credentialSourceLabel)).toEqual(['自定义凭据', '部署凭据', '内置应用通道', '没有可用凭据'])
  })
  it('shows the explicit TMDB authentication kind', () => {
    const kinds: MetadataSettings['credential_kind'][] = ['read_access_token', 'api_key', '']
    expect(kinds.map(credentialKindLabel)).toEqual(['API 读访问令牌', 'API 密钥', '未配置'])
  })
  it('keeps Player-aligned default API and image routes distinct', () => {
    expect(defaultTMDBAPIBaseURL).toBe('https://api.tmdb.org/3')
    expect(defaultTMDBImageBaseURL).toBe('https://image.tmdb.org/t/p')
  })
})
