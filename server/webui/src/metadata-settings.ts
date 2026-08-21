import type { MetadataSettings } from '@/types/api'

export const defaultTMDBAPIBaseURL = 'https://api.tmdb.org/3'
export const defaultTMDBImageBaseURL = 'https://image.tmdb.org/t/p'

export function credentialSourceLabel(source: MetadataSettings['credential_source']): string {
  return { custom: '自定义凭据', deployment: '部署凭据', builtin: '内置应用通道', none: '没有可用凭据' }[source]
}

export function credentialKindLabel(kind: MetadataSettings['credential_kind']): string {
  return { read_access_token: 'API 读访问令牌', api_key: 'API 密钥', '': '未配置' }[kind]
}
