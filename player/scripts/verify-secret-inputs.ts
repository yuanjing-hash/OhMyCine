import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')

function vueFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name)
    return entry.isDirectory() ? vueFiles(path) : entry.name.endsWith('.vue') ? [path] : []
  })
}

const violations: string[] = []
for (const file of vueFiles(root)) {
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
assert.deepEqual(violations, [], 'Protected Player fields must use SecretInput')

const component = readFileSync(join(root, 'components', 'SecretInput.vue'), 'utf8')
assert.match(component, /••••••••（已配置）/)
assert.match(component, /revealed \? 'text' : 'password'/)
assert.match(component, /loadSecret\?: \(\) => Promise<string>/)
assert.match(component, /revealedValue/)
assert.match(component, /generation !== revealGeneration/)
assert.match(component, /onBeforeUnmount\(clearReveal\)/)
assert.match(component, /attrs\.disabled !== undefined && attrs\.disabled !== false/)
assert.match(component, /if \(!value\)\s+revealed\.value = false/)
assert.match(component, /'secret-input--masked': !revealed/)

const settings = readFileSync(join(root, 'views', 'SettingsView.vue'), 'utf8')
assert.doesNotMatch(settings, /:configured="isEditing"/)
assert.match(settings, /sourceCredentialConfigured/)
assert.match(settings, /loadStoredTmdbCredentialValue/)
assert.match(settings, /loadOpenSubtitlesCredentialField/)
assert.match(settings, /loadEditedSourceCredentialField/)
assert.match(settings, /source\.type === 'server' \|\| source\.type === 'local'/)
assert.match(settings, /:load-secret="sourceCredentialLoader\('apiToken'\)"/)
assert.match(settings, /:load-secret="sourceCredentialLoader\('cookie'\)"/)
assert.match(settings, /:load-secret="sourceCredentialLoader\('password'\)"/)

console.log('Secret input policy verified')
