import type { TmdbCredentialValue } from '../datasource/credentialStore'

export interface TmdbRequestDescriptor {
  readonly url: string
  readonly headers: Record<string, string>
}

export interface BuildTmdbRequestInput {
  readonly baseUrl: string
  readonly path: string
  readonly params: Record<string, string>
  readonly credential: TmdbCredentialValue
}

export function buildTmdbRequestDescriptor(input: BuildTmdbRequestInput): TmdbRequestDescriptor {
  const url = new URL(`${input.baseUrl}${input.path}`)
  for (const [key, value] of Object.entries(input.params)) {
    if (key.toLowerCase() === 'api_key')
      continue
    if (value)
      url.searchParams.set(key, value)
  }

  const headers: Record<string, string> = {
    Accept: 'application/json',
  }

  if (input.credential.authType === 'apiKey') {
    url.searchParams.set('api_key', input.credential.value)
  }
  else {
    headers.Authorization = `Bearer ${input.credential.value}`
  }

  return {
    url: url.toString(),
    headers,
  }
}

export function tmdbCredentialAuthTypeMismatchMessage(authType: TmdbCredentialValue['authType']): string {
  return authType === 'readAccessToken'
    ? '当前按读访问令牌验证失败，请确认填的是 API 读访问令牌；若填 API 密钥请切换类型。'
    : '当前按 API 密钥验证失败，请确认填的是 API 密钥；若填 API 读访问令牌请切换类型。'
}

export function tmdbHttpFailureMessage(authType: TmdbCredentialValue['authType'], status: number, responseText: string): string {
  if (isAuthFailureStatus(status) || responseMentionsExpiredToken(responseText))
    return tmdbCredentialAuthTypeMismatchMessage(authType)

  return `TMDB 请求失败（HTTP ${status}）。`
}

function isAuthFailureStatus(status: number): boolean {
  return status === 401 || status === 403
}

function responseMentionsExpiredToken(value: string): boolean {
  return /\btoken\b/i.test(value) && /\bexpired\b/i.test(value)
}
