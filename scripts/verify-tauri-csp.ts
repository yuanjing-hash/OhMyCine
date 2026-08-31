import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)
const config = JSON.parse(await readFile(fileURLToPath(new URL('src-tauri/tauri.conf.json', root)), 'utf8')) as {
  app?: { security?: { csp?: unknown, devCsp?: unknown } }
}
const sourceFiles = await Promise.all([
  'src/main.ts',
  'src/router/index.ts',
  'src/views/PlayerView.vue',
  'src/views/SettingsView.vue',
  'src/views/SourceLibraryView.vue',
].map(path => readFile(fileURLToPath(new URL(path, root)), 'utf8')))

const csp = config.app?.security?.csp
const devCsp = config.app?.security?.devCsp
assert.equal(typeof csp, 'string')
assert.equal(typeof devCsp, 'string')

const directives = parseDirectives(csp)
const devDirectives = parseDirectives(devCsp)

assert.deepEqual(directives.get('default-src'), ["'self'"])
assert.deepEqual(directives.get('script-src'), ["'self'"])
assert.deepEqual(directives.get('object-src'), ["'none'"])
assert.deepEqual(directives.get('frame-src'), ["'none'"])
assert.deepEqual(directives.get('base-uri'), ["'self'"])
assert.deepEqual(directives.get('form-action'), ["'self'"])
assert.deepEqual(directives.get('frame-ancestors'), ["'none'"])
assertIncludesAll(directives.get('connect-src'), ["'self'", 'ipc:', 'http://ipc.localhost', 'http:', 'https:'])
assertIncludesAll(directives.get('img-src'), ["'self'", 'asset:', 'data:', 'blob:', 'http:', 'https:'])
assertIncludesAll(devDirectives.get('connect-src'), ['ipc:', 'http:', 'https:', 'ws:', 'wss:'])
assert.equal(csp.includes("'unsafe-eval'"), false)
assert.equal(csp.includes(' *'), false)

for (const source of sourceFiles) {
  assert.doesNotMatch(source, /\bv-html\b/)
  assert.doesNotMatch(source, /\beval\s*\(/)
  assert.doesNotMatch(source, /new\s+Function\s*\(/)
}

console.log(JSON.stringify({
  cspEnabled: true,
  scriptsRestrictedToSelf: true,
  evalBlocked: true,
  framesAndObjectsBlocked: true,
  datasourceHttpAllowed: true,
  controlledArtworkSchemesAllowed: true,
  devHmrWebSocketAllowed: true,
}, null, 2))

function parseDirectives(value: unknown): Map<string, string[]> {
  assert.equal(typeof value, 'string')
  return new Map(value.split(';').map((directive) => {
    const [name, ...sources] = directive.trim().split(/\s+/)
    return [name, sources]
  }).filter(([name]) => Boolean(name)))
}

function assertIncludesAll(actual: string[] | undefined, expected: string[]) {
  assert.ok(actual)
  for (const value of expected)
    assert.ok(actual.includes(value), `missing CSP source ${value}`)
}
