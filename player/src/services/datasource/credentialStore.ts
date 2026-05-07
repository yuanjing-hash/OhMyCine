const CREDENTIAL_PREFIX = 'ohmycine-session-credential:'

export interface CredentialRecord {
  readonly token: string
}

export function createCredentialRef(sourceId: string): string {
  return `datasource:${sourceId}:access-token`
}

export function saveSessionCredential(ref: string, token: string): void {
  sessionStorage.setItem(`${CREDENTIAL_PREFIX}${ref}`, JSON.stringify({ token }))
}

export function readSessionCredential(ref: string): string | null {
  const raw = sessionStorage.getItem(`${CREDENTIAL_PREFIX}${ref}`)
  if (!raw)
    return null

  try {
    const parsed = JSON.parse(raw) as unknown
    if (isCredentialRecord(parsed))
      return parsed.token
  }
  catch {
    return null
  }

  return null
}

export function removeSessionCredential(ref: string): void {
  sessionStorage.removeItem(`${CREDENTIAL_PREFIX}${ref}`)
}

function isCredentialRecord(value: unknown): value is CredentialRecord {
  if (typeof value !== 'object' || value == null)
    return false

  const record = value as Record<string, unknown>
  return typeof record.token === 'string' && record.token.length > 0
}
