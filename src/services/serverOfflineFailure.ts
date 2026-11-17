/** A versioned, public-safe prompt. Never retain arbitrary error-envelope data. */
export interface ServerOfflineFailureWarning {
  version: 1
  reason: 'offline_deadline_exceeded'
  timeoutMinutes: 30
  confirmationToken: string
}

export const serverOfflineConfirmationCode = 'offline_resource_confirmation_required'

export function parseServerOfflineFailureWarning(value: unknown): ServerOfflineFailureWarning | undefined {
  if (typeof value !== 'object' || value === null)
    return undefined
  const version = Reflect.get(value, 'version')
  const reason = Reflect.get(value, 'reason')
  const timeoutMinutes = Reflect.get(value, 'timeout_minutes')
  const confirmationToken = Reflect.get(value, 'confirmation_token')
  if (version !== 1 || reason !== 'offline_deadline_exceeded' || timeoutMinutes !== 30 || typeof confirmationToken !== 'string' || !/^[0-9a-f]{64}$/.test(confirmationToken))
    return undefined
  return { version, reason, timeoutMinutes, confirmationToken }
}
