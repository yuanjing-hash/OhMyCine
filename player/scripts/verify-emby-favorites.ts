import type { AddressInfo } from 'node:net'
import type { DataSourceConfig } from '../src/services/datasource/types'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { saveEmbyCredential } from '../src/services/datasource/credentialStore'
import { EmbyDataSource } from '../src/services/datasource/emby'

const userId = 'favorite-user'
const itemId = 'favorite-series'
const credentialRef = 'datasource:favorite-test:emby-credential'
let favorite = true
let staleFavoriteListOnce = false
const favoriteQueries: URL[] = []

const item = {
  Id: itemId,
  Name: 'Favorite Series',
  Type: 'Series',
  ImageTags: { Primary: 'poster-tag' },
}

const server = createServer((request, response) => {
  const url = new URL(request.url ?? '/', 'http://127.0.0.1')
  response.setHeader('content-type', 'application/json')

  if (url.pathname === `/Users/${userId}/FavoriteItems/${itemId}` && request.method === 'POST') {
    favorite = true
    response.statusCode = 204
    response.end()
    return
  }
  if (url.pathname === `/Users/${userId}/FavoriteItems/${itemId}` && request.method === 'DELETE') {
    favorite = false
    response.statusCode = 204
    response.end()
    return
  }
  if (url.pathname === `/Users/${userId}/Items/${itemId}` && request.method === 'GET') {
    // Some Emby versions do not include UserData in this response unless explicitly enabled.
    response.end(JSON.stringify(item))
    return
  }
  if (url.pathname === `/Users/${userId}/Items` && request.method === 'GET') {
    favoriteQueries.push(url)
    const isFavoriteFilter = url.searchParams.get('Filters')?.split(',').includes('IsFavorite') === true
    const ids = url.searchParams.get('Ids')
    const visible = isFavoriteFilter && favorite && !staleFavoriteListOnce && (!ids || ids === itemId)
    staleFavoriteListOnce = false
    response.end(JSON.stringify({ Items: visible ? [item] : [], TotalRecordCount: visible ? 1 : 0 }))
    return
  }

  response.statusCode = 404
  response.end(JSON.stringify({ error: 'not found' }))
})

await new Promise<void>((resolve, reject) => {
  server.once('error', reject)
  server.listen(0, '127.0.0.1', resolve)
})

try {
  const address = server.address() as AddressInfo
  await saveEmbyCredential(credentialRef, {
    accessToken: 'favorite-token',
    username: 'favorite-user',
    password: 'favorite-password',
  })
  const config: DataSourceConfig = {
    id: 'favorite-test',
    type: 'emby',
    name: 'Favorite Test',
    url: `http://127.0.0.1:${address.port}`,
    order: 0,
    enabled: true,
    extra: { credentialRef, userId, deviceId: 'favorite-device' },
  }
  const source = new EmbyDataSource()
  await source.init(config)

  assert.equal(await source.getFavoriteState(itemId), true)
  assert.deepEqual((await source.listFavorites()).map(entry => entry.id), [itemId])

  await source.setFavorite(itemId, false)
  assert.equal(await source.getFavoriteState(itemId), false, 'unfavorite must be visible immediately after the write')
  assert.deepEqual(await source.listFavorites(), [])

  staleFavoriteListOnce = true
  await source.setFavorite(itemId, true)
  assert.equal(await source.getFavoriteState(itemId), true, 'favorite must be visible immediately after the write')
  assert.deepEqual((await source.listFavorites()).map(entry => entry.id), [itemId], 'the favorites page must survive short provider indexing delay')

  assert.equal(favoriteQueries.every(url => url.searchParams.get('Filters')?.includes('IsFavorite')), true)
  assert.equal(favoriteQueries.every(url => url.searchParams.get('Fields') !== 'UserData'), true)
  assert.equal(favoriteQueries.some(url => url.searchParams.get('Ids') === itemId), true)
}
finally {
  server.close()
}

console.log('Emby favorite write/read/list integration verification passed')
