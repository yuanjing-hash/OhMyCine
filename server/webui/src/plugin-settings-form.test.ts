import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('declarative plugin settings login surface', () => {
  it('renders Host-owned QR login inside the plugin-declared credential component', () => {
    const form = readFileSync(new URL('./components/PluginSettingsForm.vue', import.meta.url), 'utf8')
    const view = readFileSync(new URL('./views/PluginsView.vue', import.meta.url), 'utf8')

    expect(form).toContain("field.type === 'credential-status'")
    expect(form).toContain("emit('start-auth')")
    expect(form).toContain('qrAuthState.qrDataURL')
    expect(form).toContain("props.qrAuthState?.state === 'pending' || props.qrAuthState?.state === 'scanned'")
    expect(view).toContain('@start-auth="startConnectionAuth(plugin, connection)"')
    expect(view).toContain(':qr-auth-state="canManage ? connectionAuth[connection.id] : undefined"')
    expect(view).not.toContain('connectionAuth[connection.id].qrDataURL')
    expect(view).not.toContain('connection.credential_mode === \'cookie\' && plugin.capabilities.includes(\'site.interaction\')')
  })
})
