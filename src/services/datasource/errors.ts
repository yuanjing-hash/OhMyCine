const SENSITIVE_QUERY_KEYS = [
  'Authorization',
  'X-Emby-Authorization',
  'X-MediaBrowser-Authorization',
  'UserId',
  'userId',
  'api_key',
  'apikey',
  'access_key',
  'accessKeyId',
  'access_token',
  'accessToken',
  'AWSAccessKeyId',
  'Expires',
  'expires',
  'OSSAccessKeyId',
  'password',
  'passwd',
  'pwd',
  'pw',
  'security-token',
  'sig',
  'sign',
  'signature',
  'token',
  'X-Amz-Credential',
  'X-Amz-Security-Token',
  'X-Amz-Signature',
  'x-emby-token',
  'x-mediabrowser-token',
]

export function redactSensitiveText(value: unknown): string {
  const input = value instanceof Error ? value.message : String(value ?? 'Unknown error')
  let redacted = input

  for (const key of SENSITIVE_QUERY_KEYS) {
    redacted = redacted.replace(new RegExp(`([?&]${key}=)[^\\s&]+`, 'gi'), '$1[redacted]')
    redacted = redacted.replace(new RegExp(`(${key}[=:/]\\s*)[^\\s,;&]+`, 'gi'), '$1[redacted]')
    redacted = redacted.replace(new RegExp(`(["']${key}["']\\s*:\\s*["'])[^"']+(["'])`, 'gi'), '$1[redacted]$2')
  }

  redacted = redacted.replace(/(Token=")[^"]+(")/gi, '$1[redacted]$2')
  redacted = redacted.replace(/\b(UserId\s*=\s*")[^"]+(")/gi, '$1[redacted]$2')
  redacted = redacted.replace(/\b(UserId\s*=\s*')[^']+(')/gi, '$1[redacted]$2')
  redacted = redacted.replace(/(Authorization:)\s*[^\n\r]+/gi, '$1 [redacted]')
  redacted = redacted.replace(/(X-Emby-Authorization:)\s*[^\n\r]+/gi, '$1 [redacted]')
  redacted = redacted.replace(/(X-Emby-Token:\s*)\S+/gi, '$1[redacted]')
  redacted = redacted.replace(/(X-MediaBrowser-Token:\s*)\S+/gi, '$1[redacted]')
  redacted = redacted.replace(/X-MediaBrowser-Authorization:[^\n\r]+/gi, 'X-MediaBrowser-Authorization: [redacted]')
  redacted = redacted.replace(/\b0x[0-9a-f]{6,}\b/gi, '[native-handle]')
  redacted = redacted.replace(/\b((?:owner|mpv)?_?hwnd|hglrc|hdc|handle|pointer|ptr)\s*[:=]\s*-?\d+\b/gi, '$1=[native-handle]')
  redacted = redacted.replace(/https?:\/\/[^/\s"')]+/gi, match => `${match.startsWith('https://') ? 'https' : 'http'}://[redacted-host]`)
  redacted = redacted.replace(/(\/Users\/)[^/\s?#"')]+/gi, '$1[redacted-user]')

  return redacted
}

export function toSafeErrorMessage(error: unknown, fallback = '操作失败，请检查数据源配置。'): string {
  const redacted = redactSensitiveText(error)
  if (!redacted || redacted === 'undefined' || redacted === 'null')
    return fallback

  const normalized = redacted.toLowerCase()
  if (normalized.includes('failed to fetch') || normalized.includes('networkerror'))
    return fallback

  return redacted
}
