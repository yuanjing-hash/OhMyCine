import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { requestTmdbJsonWithFallback, TMDB_API_BASE_URLS } from '../src/services/scraper/tmdb.ts'
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

assert.deepEqual(TMDB_API_BASE_URLS, [
  'https://api.tmdb.org/3',
  'https://api.themoviedb.org/3',
])

const fallbackRequests: string[] = []
const fallbackResult = await requestTmdbJsonWithFallback({
  path: '/configuration',
  params: {},
  credential: { authType: 'apiKey', value: 'fake-api-key-for-routing-test' },
  timeoutMs: 1_000,
  fetcher: (async (input) => {
    const url = String(input)
    fallbackRequests.push(url)
    if (url.startsWith(TMDB_API_BASE_URLS[0]))
      throw new TypeError('simulated primary network failure')
    return new Response(JSON.stringify({ ok: true }), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }) as typeof fetch,
})
assert.deepEqual(fallbackResult, { ok: true })
assert.equal(fallbackRequests.length, 2)
assert.equal(fallbackRequests[0]?.startsWith('https://api.tmdb.org/3/'), true)
assert.equal(fallbackRequests[1]?.startsWith('https://api.themoviedb.org/3/'), true)

let authFailureRequests = 0
await assert.rejects(() => requestTmdbJsonWithFallback({
  path: '/configuration',
  params: {},
  credential: { authType: 'apiKey', value: 'invalid-key' },
  timeoutMs: 1_000,
  fetcher: (async () => {
    authFailureRequests += 1
    return new Response(JSON.stringify({ status_code: 7 }), { status: 401, headers: { 'Content-Type': 'application/json' } })
  }) as typeof fetch,
}), /当前按 API 密钥验证失败/)
assert.equal(authFailureRequests, 1)

const viteConfig = await readFile(new URL('../vite.config.ts', import.meta.url), 'utf8')
const tmdbService = await readFile(new URL('../src/services/scraper/tmdb.ts', import.meta.url), 'utf8')
const settingsView = await readFile(new URL('../src/views/SettingsView.vue', import.meta.url), 'utf8')
const playerWorkflow = await readFile(new URL('../../.github/workflows/player.yml', import.meta.url), 'utf8')
const manualWorkflow = await readFile(new URL('../../.github/workflows/manual-build.yml', import.meta.url), 'utf8')
const releaseWorkflow = await readFile(new URL('../../.github/workflows/player-beta-release.yml', import.meta.url), 'utf8')
assert.match(viteConfig, /process\.env\.OHMYCINE_TMDB_READ_ACCESS_TOKEN/)
assert.match(tmdbService, /resolveEffectiveTmdbCredential\(matchingUserCredential, BUILT_IN_TMDB_READ_ACCESS_TOKEN\)/)
assert.match(tmdbService, /https:\/\/api\.tmdb\.org\/3/)
assert.match(tmdbService, /https:\/\/api\.themoviedb\.org\/3/)
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
  shortDomainPreferred: TMDB_API_BASE_URLS[0] === 'https://api.tmdb.org/3',
  legacyDomainNetworkFallback: fallbackRequests.length === 2,
  authFailureDoesNotFallback: authFailureRequests === 1,
  releaseBuildSecretInjection: true,
}, null, 2))
