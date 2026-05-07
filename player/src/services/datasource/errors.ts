const SENSITIVE_QUERY_KEYS = ['api_key', 'apikey', 'access_token', 'token', 'x-emby-token']

export function redactSensitiveText(value: unknown): string {
  const input = value instanceof Error ? value.message : String(value ?? 'Unknown error')
  let redacted = input

  for (const key of SENSITIVE_QUERY_KEYS) {
    redacted = redacted.replace(new RegExp(`([?&]${key}=)\\S+`, 'gi'), '$1[redacted]')
    redacted = redacted.replace(new RegExp(`(${key}[=:]\\s*)\\S+`, 'gi'), '$1[redacted]')
  }

  redacted = redacted.replace(/(Token=")[^"]+(")/gi, '$1[redacted]$2')
  redacted = redacted.replace(/(Authorization:\s*Bearer\s+)\S+/gi, '$1[redacted]')
  redacted = redacted.replace(/(X-Emby-Token:\s*)\S+/gi, '$1[redacted]')

  return redacted
}

export function toSafeErrorMessage(error: unknown, fallback = '操作失败，请检查数据源配置。'): string {
  const redacted = redactSensitiveText(error)
  return redacted && redacted !== 'undefined' && redacted !== 'null' ? redacted : fallback
}
