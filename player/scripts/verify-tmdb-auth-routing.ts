import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { buildTmdbRequestDescriptor, resolveEffectiveTmdbCredential } from '../src/services/scraper/tmdbAuth.ts'

const apiKeyRequest = buildTmdbRequestDescriptor({
  baseUrl: 'https://api.themoviedb.org/3',
  path: '/search/movie',
  params: { query: 'Inception', language: 'zh-CN' },
  credential: { authType: 'apiKey', value: 'fake-api-key-for-routing-test' },
})
const apiKeyUrl = new URL(apiKeyRequest.url)
assert.equal(apiKeyUrl.searchParams.has('api_key'), true)
assert.equal(apiKeyUrl.searchParams.get('api_key'), 'fake-api-key-for-routing-test')
assert.equal(Object.hasOwn(apiKeyRequest.headers, 'Authorization'), false)

const readAccessTokenRequest = buildTmdbRequestDescriptor({
  baseUrl: 'https://api.themoviedb.org/3',
  path: '/search/movie',
  params: { query: 'Inception', language: 'zh-CN', api_key: 'legacy-key-must-not-leak' },
  credential: { authType: 'readAccessToken', value: 'fake.jwt.read-access-token' },
})
const readAccessTokenUrl = new URL(readAccessTokenRequest.url)
assert.equal(readAccessTokenUrl.searchParams.has('api_key'), false)
assert.equal(readAccessTokenRequest.headers.Authorization, 'Bearer fake.jwt.read-access-token')

const builtInCredential = resolveEffectiveTmdbCredential(null, 'built-in.read-access-token')
assert.deepEqual(builtInCredential, {
  authType: 'readAccessToken',
  value: 'built-in.read-access-token',
})

const userCredential = resolveEffectiveTmdbCredential({
  authType: 'apiKey',
  value: 'user-api-key',
}, 'built-in.read-access-token')
assert.deepEqual(userCredential, {
  authType: 'apiKey',
  value: 'user-api-key',
})
assert.equal(resolveEffectiveTmdbCredential(null, ''), null)
assert.equal(resolveEffectiveTmdbCredential(null, 'invalid token with spaces'), null)

const viteConfig = await readFile(new URL('../vite.config.ts', import.meta.url), 'utf8')
const tmdbService = await readFile(new URL('../src/services/scraper/tmdb.ts', import.meta.url), 'utf8')
const settingsView = await readFile(new URL('../src/views/SettingsView.vue', import.meta.url), 'utf8')
const playerWorkflow = await readFile(new URL('../../.github/workflows/player.yml', import.meta.url), 'utf8')
const manualWorkflow = await readFile(new URL('../../.github/workflows/manual-build.yml', import.meta.url), 'utf8')
const releaseWorkflow = await readFile(new URL('../../.github/workflows/player-beta-release.yml', import.meta.url), 'utf8')
assert.match(viteConfig, /process\.env\.OHMYCINE_TMDB_READ_ACCESS_TOKEN/)
assert.match(tmdbService, /resolveEffectiveTmdbCredential\(matchingUserCredential, BUILT_IN_TMDB_READ_ACCESS_TOKEN\)/)
assert.match(settingsView, /内置通道可用/)
assert.match(settingsView, /This product uses the TMDB API but is not endorsed or certified by TMDB\./)
assert.match(playerWorkflow, /OHMYCINE_TMDB_READ_ACCESS_TOKEN: \$\{\{ secrets\.OHMYCINE_TMDB_READ_ACCESS_TOKEN \}\}/)
assert.match(manualWorkflow, /OHMYCINE_TMDB_READ_ACCESS_TOKEN: \$\{\{ secrets\.OHMYCINE_TMDB_READ_ACCESS_TOKEN \}\}/)
assert.equal((releaseWorkflow.match(/OHMYCINE_TMDB_READ_ACCESS_TOKEN: \$\{\{ secrets\.OHMYCINE_TMDB_READ_ACCESS_TOKEN \}\}/g) ?? []).length >= 2, true)
assert.match(releaseWorkflow, /OHMYCINE_TMDB_READ_ACCESS_TOKEN GitHub Secret is required for the default TMDB metadata channel\./)

console.log(JSON.stringify({
  apiKeyUsesQuery: apiKeyUrl.searchParams.has('api_key'),
  apiKeyUsesAuthorization: Object.hasOwn(apiKeyRequest.headers, 'Authorization'),
  readAccessTokenUsesQuery: readAccessTokenUrl.searchParams.has('api_key'),
  readAccessTokenUsesAuthorization: readAccessTokenRequest.headers.Authorization?.startsWith('Bearer ') === true,
  builtInCredentialAvailable: builtInCredential?.authType === 'readAccessToken',
  userCredentialOverridesBuiltIn: userCredential?.value === 'user-api-key',
  releaseBuildSecretInjection: true,
}, null, 2))
