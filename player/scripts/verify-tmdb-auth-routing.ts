import assert from 'node:assert/strict'
import { buildTmdbRequestDescriptor } from '../src/services/scraper/tmdbAuth.ts'

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

console.log(JSON.stringify({
  apiKeyUsesQuery: apiKeyUrl.searchParams.has('api_key'),
  apiKeyUsesAuthorization: Object.hasOwn(apiKeyRequest.headers, 'Authorization'),
  readAccessTokenUsesQuery: readAccessTokenUrl.searchParams.has('api_key'),
  readAccessTokenUsesAuthorization: readAccessTokenRequest.headers.Authorization?.startsWith('Bearer ') === true,
}, null, 2))
