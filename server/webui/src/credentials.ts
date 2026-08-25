import { api } from '@/api/client'

export type RevealCredentialResource = 'connection' | 'downloader' | 'site' | 'cookiecloud' | 'metadata' | 'plugin_connection'

export interface RevealCredentialRequest {
  resourceType: RevealCredentialResource
  resourceID: string | number
  field: string
}

export async function revealCredential(request: RevealCredentialRequest): Promise<string> {
  const response = await api<{ value: string }>('/api/v1/credentials/reveal', {
    method: 'POST',
    body: JSON.stringify({
      resource_type: request.resourceType,
      resource_id: String(request.resourceID),
      field: request.field,
    }),
  })
  return response.value
}

export function credentialLoader(request: RevealCredentialRequest): () => Promise<string> {
  return () => revealCredential(request)
}
