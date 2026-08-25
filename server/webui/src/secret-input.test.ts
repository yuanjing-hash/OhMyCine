import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

function vueFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name)
    return entry.isDirectory() ? vueFiles(path) : entry.name.endsWith('.vue') ? [path] : []
  })
}

describe('secret input policy', () => {
  it('routes every protected Server field through the shared reveal control', () => {
    const violations: string[] = []
    for (const file of vueFiles(new URL('.', import.meta.url).pathname.slice(1))) {
      const source = readFileSync(file, 'utf8')
      for (const tag of source.match(/<(?:input|textarea)\b[\s\S]*?>/g) ?? []) {
        const model = /v-model=["']([^"']+)["']/.exec(tag)?.[1] ?? ''
        const field = model.split('.').at(-1) ?? ''
        const protectedField = /(?:password|cookie|passkey|api[_-]?key|apiToken|accessToken|authHeader|credential|recyclePassword|secret|tmdbToken|uuid|(?:^|[._-])token$)/i.test(field)
          && !/^(?:clear|remove|delete|configured|credentialMode|credentialScope)/i.test(field)
        if (/type=["']password["']/.test(tag) || protectedField)
          violations.push(`${file}: ${tag.replace(/\s+/g, ' ')}`)
      }
    }
    expect(violations).toEqual([])
  })

  it('shows configured state and exposes only the current replacement value', () => {
    const source = readFileSync(new URL('./components/SecretInput.vue', import.meta.url), 'utf8')
    expect(source).toContain('••••••••（已配置）')
    expect(source).toContain("revealed ? 'text' : 'password'")
    expect(source).toContain('显示当前输入内容')
    expect(source).toContain('已保存凭据不会回传')
    expect(source).toContain("attrs.disabled !== undefined && attrs.disabled !== false")
    expect(source).toMatch(/if \(!value\)\s+revealed\.value = false/)
    expect(source).toContain("'secret-input--masked': !revealed")
    const storage = readFileSync(new URL('./views/StorageView.vue', import.meta.url), 'utf8')
    const sites = readFileSync(new URL('./views/SitesView.vue', import.meta.url), 'utf8')
    expect(storage).toMatch(/<SecretInput[^>]+v-model="cloudEdit\.cookie"[^>]+multiline/)
    expect(sites).toMatch(/<SecretInput[^>]+v-model="form\.cookie"[^>]+multiline/)
  })
})
