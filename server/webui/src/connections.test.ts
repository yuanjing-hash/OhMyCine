import { describe, expect, it } from 'vitest'
import {
  buildEmbyCreatePayload,
  buildEmbyGatewayUpdatePayload,
  buildEmbyUpdatePayload,
  canBrowseProviderDirectory,
  canEnableEmbyGateway,
  connectionListPath,
  consumeEmbyCreatePayload,
  consumeEmbyUpdatePayload,
  isLoopbackURL,
} from '@/connections'

describe('Emby connection form boundary', () => {
  it('uses provider-filtered list endpoints so storage never receives Emby records', () => {
    expect(connectionListPath('pan115')).toBe('/api/v1/connections?provider=pan115')
    expect(connectionListPath('emby')).toBe('/api/v1/connections?provider=emby')
    expect(connectionListPath('jellyfin')).toBe('/api/v1/connections?provider=jellyfin')
  })
  it('sends the API Key only while creating the encrypted credential', () => {
    expect(buildEmbyCreatePayload({ provider: 'emby', name: ' 家庭 Emby ', endpoint: ' http://nas.example.test:8096/ ', apiKey: ' secret ', enabled: true })).toEqual({
      name: '家庭 Emby',
      provider: 'emby',
      endpoint: 'http://nas.example.test:8096/',
      api_key: 'secret',
      enabled: true,
    })
  })

  it('does not replace or retain the credential when the edit field is blank', () => {
    expect(buildEmbyUpdatePayload({ provider: 'emby', name: '家庭 Emby', endpoint: 'http://nas.example.test:8096', apiKey: '   ', enabled: true }, 7)).toEqual({
      name: '家庭 Emby',
      endpoint: 'http://nas.example.test:8096',
      enabled: true,
      revision: 7,
    })
  })

  it('clears a create credential from page state before the request starts', () => {
    const draft = { provider: 'emby' as const, name: '家庭 Emby', endpoint: 'http://nas.example.test:8096', apiKey: 'secret', enabled: true }
    const payload = consumeEmbyCreatePayload(draft)

    expect(payload.api_key).toBe('secret')
    expect(draft.apiKey).toBe('')
  })

  it('clears an optional replacement credential from page state before the request starts', () => {
    const draft = { provider: 'emby' as const, name: '家庭 Emby', endpoint: 'http://nas.example.test:8096', apiKey: 'replacement', enabled: true }
    const payload = consumeEmbyUpdatePayload(draft, 7)

    expect(payload.api_key).toBe('replacement')
    expect(draft.apiKey).toBe('')
  })

  it('binds a gateway toggle to the revision currently shown to the administrator', () => {
    expect(buildEmbyGatewayUpdatePayload(true, ' Home-Cinema ', false, true, 9)).toEqual({ enabled: true, alias: 'Home-Cinema', external_player_enabled: false, fanart_enabled: true, revision: 9 })
  })

  it('allows gateway enablement only for an enabled and online connection', () => {
    expect(canEnableEmbyGateway(true, 'online')).toBe(true)
    expect(canEnableEmbyGateway(false, 'online')).toBe(false)
    expect(canEnableEmbyGateway(true, 'offline')).toBe(false)
    expect(canEnableEmbyGateway(true, 'unknown')).toBe(false)
  })

  it('requires connection read, storage browse, and the matching storage write permission', () => {
    expect(canBrowseProviderDirectory(true, true, true)).toBe(true)
    expect(canBrowseProviderDirectory(false, true, true)).toBe(false)
    expect(canBrowseProviderDirectory(true, false, true)).toBe(false)
    expect(canBrowseProviderDirectory(true, true, false)).toBe(false)
  })

  it('warns for every supported loopback gateway host only', () => {
    for (const value of ['http://localhost:3000/emby/id', 'http://127.0.0.1:3000/emby/id', 'http://[::1]:3000/emby/id']) {
      expect(isLoopbackURL(value)).toBe(true)
    }
    expect(isLoopbackURL('https://media.example.test/emby/id')).toBe(false)
    expect(isLoopbackURL('not a url')).toBe(false)
  })

})
