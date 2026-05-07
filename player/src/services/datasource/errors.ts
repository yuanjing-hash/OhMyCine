const SENSITIVE_QUERY_KEYS = ['api_key', 'apikey', 'access_token', 'token', 'x-emby-token', 'password', 'pw']

export function redactSensitiveText(value: unknown): string {
  const input = value instanceof Error ? value.message : String(value ?? 'Unknown error')
  let redacted = input

  for (const key of SENSITIVE_QUERY_KEYS) {
    redacted = redacted.replace(new RegExp(`([?&]${key}=)[^\\s&]+`, 'gi'), '$1[redacted]')
    redacted = redacted.replace(new RegExp(`(${key}[=:]\\s*)[^\\s,;&]+`, 'gi'), '$1[redacted]')
    redacted = redacted.replace(new RegExp(`(["']${key}["']\\s*:\\s*["'])[^"']+(["'])`, 'gi'), '$1[redacted]$2')
  }

  redacted = redacted.replace(/(Token=")[^"]+(")/gi, '$1[redacted]$2')
  redacted = redacted.replace(/(Authorization:\s*Bearer\s+)\S+/gi, '$1[redacted]')
  redacted = redacted.replace(/(X-Emby-Token:\s*)\S+/gi, '$1[redacted]')

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
