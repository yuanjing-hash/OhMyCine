import type { AddressInfo } from 'node:net'
import type { DataSourceConfig, ProviderPlaybackProgressInput } from '../src/services/datasource/types'
import assert from 'node:assert/strict'
import { Buffer } from 'node:buffer'
import { createServer } from 'node:http'
import playerPackage from '../package.json'
import { saveEmbyCredential } from '../src/services/datasource/credentialStore'
import { EmbyDataSource } from '../src/services/datasource/emby'

interface RecordedRequest {
  readonly method: string
  readonly path: string
  readonly authorization: string
  readonly token: string
  readonly range: string
  readonly body: Record<string, unknown> | null
}

const token = 'integration-test-token'
const userId = 'integration-user'
const itemId = 'integration-item'
const mediaSourceId = 'integration-media-source'
const playSessionId = 'integration-play-session'
const credentialRef = 'datasource:integration-emby:emby-credential'
const requests: RecordedRequest[] = []

const server = createServer(async (request, response) => {
  const chunks: Buffer[] = []
  for await (const chunk of request)
    chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk))

  const bodyText = Buffer.concat(chunks).toString('utf8')
  const body = bodyText ? JSON.parse(bodyText) as Record<string, unknown> : null
  const url = new URL(request.url ?? '/', 'http://127.0.0.1')
  requests.push({
    method: request.method ?? '',
    path: url.pathname,
    authorization: request.headers.authorization ?? request.headers['x-emby-authorization']?.toString() ?? '',
    token: request.headers['x-emby-token']?.toString() ?? '',
    range: request.headers.range ?? '',
    body,
  })

  response.setHeader('content-type', 'application/json')
  if (request.method === 'GET' && url.pathname === '/System/Info') {
    response.end(JSON.stringify({ ServerName: 'Integration Emby' }))
    return
  }
  if (request.method === 'GET' && url.pathname === `/Users/${userId}/Items/${itemId}`) {
    response.end(JSON.stringify({
      Id: itemId,
      Name: 'Integration Movie',
      Type: 'Movie',
      RunTimeTicks: 3_600 * 10_000_000,
      MediaSources: [{
        Id: mediaSourceId,
        Protocol: 'Http',
        SupportsDirectPlay: true,
        DirectStreamUrl: `/media/${itemId}`,
      }],
    }))
    return
  }
  if (request.method === 'POST' && url.pathname === `/Items/${itemId}/PlaybackInfo`) {
    response.end(JSON.stringify({
      PlaySessionId: playSessionId,
      MediaSources: [{
        Id: mediaSourceId,
        Protocol: 'Http',
        SupportsDirectPlay: true,
        DirectStreamUrl: `/media/${itemId}`,
      }],
    }))
    return
  }
  if (request.method === 'GET' && url.pathname === `/media/${itemId}`) {
    assert.equal(url.searchParams.get('api_key'), token)
    assert.equal(request.headers.range, 'bytes=0-0')
    response.statusCode = 206
    response.setHeader('content-type', 'application/octet-stream')
    response.setHeader('content-range', 'bytes 0-0/1')
    response.setHeader('content-length', '1')
    response.end(Buffer.from([0]))
    return
  }
  if (request.method === 'POST' && [
    '/Sessions/Playing',
    '/Sessions/Playing/Progress',
    '/Sessions/Playing/Stopped',
  ].includes(url.pathname)) {
    response.statusCode = 204
    response.end()
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
  const baseUrl = `http://127.0.0.1:${address.port}`
  await saveEmbyCredential(credentialRef, {
    accessToken: token,
    username: 'integration-user-name',
    password: 'integration-password',
  })

  const config: DataSourceConfig = {
    id: 'integration-emby',
    type: 'emby',
    name: 'Integration Emby',
    order: 0,
    url: baseUrl,
    enabled: true,
    extra: {
      credentialRef,
      userId,
      deviceId: 'integration-device',
    },
  }
  const source = new EmbyDataSource()
  await source.init(config)
  assert.equal(await source.test(), true)

  const stream = await source.getStreamRequest({ itemId, mediaSourceId })
  const streamResponse = await fetch(stream.url, { headers: { Range: 'bytes=0-0' } })
  assert.equal(streamResponse.status, 206)
  assert.equal((await streamResponse.arrayBuffer()).byteLength, 1)

  const sync = (input: Pick<ProviderPlaybackProgressInput, 'event' | 'position' | 'isPaused'>) => source.syncPlaybackProgress({
    itemId,
    mediaSourceId,
    mediaType: 'movie',
    duration: 3_600,
    startPosition: 123,
    completed: false,
    playbackRate: 1.25,
    ...input,
  })

  await sync({ event: 'started', position: 123, isPaused: false })
  await sync({ event: 'paused', position: 130, isPaused: true })
  await sync({ event: 'resumed', position: 130, isPaused: false })
  await sync({ event: 'progress', position: 140, isPaused: false })
  await sync({ event: 'stopped', position: 150, isPaused: true })

  const sessionRequests = requests.filter(request => request.path.startsWith('/Sessions/Playing'))
  assert.deepEqual(sessionRequests.map(request => request.path), [
    '/Sessions/Playing',
    '/Sessions/Playing/Progress',
    '/Sessions/Playing/Progress',
    '/Sessions/Playing/Progress',
    '/Sessions/Playing/Stopped',
  ])
  assert.deepEqual(sessionRequests.map(request => request.body?.EventName ?? null), [
    'StateChange',
    'Pause',
    'Unpause',
    'TimeUpdate',
    'StateChange',
  ])
  assert.deepEqual(sessionRequests.map(request => request.body?.PositionTicks), [
    1_230_000_000,
    1_300_000_000,
    1_300_000_000,
    1_400_000_000,
    1_500_000_000,
  ])
  for (const request of sessionRequests) {
    assert.equal(request.body?.ItemId, itemId)
    assert.equal(request.body?.MediaSourceId, mediaSourceId)
    assert.equal(request.body?.PlaySessionId, playSessionId)
    assert.equal(request.body?.RunTimeTicks, 36_000_000_000)
    assert.equal(request.body?.PlaybackRate, 1.25)
    assert.equal(request.token, token)
    assert.match(request.authorization, new RegExp(`Version="${playerPackage.version.replaceAll('.', '\\.')}"`))
  }

  const streamRequest = requests.find(request => request.path === `/media/${itemId}`)
  assert.equal(streamRequest?.range, 'bytes=0-0')
  assert.equal(source.getPlaybackSyncDiagnostics().some(item => item.event === 'stopped' && item.ok), true)

  console.log(JSON.stringify({
    systemProbe: true,
    playbackInfoNegotiated: true,
    streamRangeRead: true,
    mediaSourcePreserved: true,
    playSessionPreserved: true,
    startedReported: true,
    pauseResumeReported: true,
    periodicProgressReported: true,
    stoppedReported: true,
    secondsConvertedToTicks: true,
    clientVersion: playerPackage.version,
  }, null, 2))
}
finally {
  await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()))
}
