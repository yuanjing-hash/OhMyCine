import assert from 'node:assert/strict'
import { ServerDataSource, ServerRequestError } from '../src/services/datasource/server.ts'

const token = `omc_player_${'a'.repeat(43)}`
const calls: Array<{ path: string, method: string, accessToken?: string }> = []
let reply = { status: 200, body: { code: 0, message: 'success', data: { version: 1, ready: true } } } as { status: number, body: unknown }
let finish: ((value: typeof reply) => void) | undefined
let delay = false
let markStarted!: () => void
const started = new Promise<void>((resolve) => {
  markStarted = resolve
})
async function fixture(capabilities: string[]) {
  const source = new ServerDataSource({ readCredential: async () => ({ accessToken: token }), bridge: { async request(request) {
    calls.push(request)
    if (delay) {
      return new Promise((resolve) => {
        finish = resolve
        markStarted()
      })
    }
    return reply
  } } })
  await source.init({ id: 'fixture', name: 'Server', type: 'server', enabled: true, order: 0, url: 'http://127.0.0.1:3000', extra: { credentialRef: 'fixture-only', capabilities } })
  return source
}
const request = { itemId: 'physical-fixture', mediaSourceId: '7' }
const legacy = await fixture([])
assert.equal((await legacy.getStreamRequest(request)).url, 'http://127.0.0.1:3000/api/v1/player/media-entries/7/stream')
assert.equal(calls.length, 0, 'old Server must not receive an unsupported check')
const source = await fixture(['playback_check_v1'])
const stream = await source.getStreamRequest(request)
assert.deepEqual(stream, { url: 'http://127.0.0.1:3000/api/v1/player/media-entries/7/stream', headers: { Authorization: `Bearer ${token}` }, mediaSourceId: '7' })
assert.equal(calls.length, 1)
assert.equal(calls[0]?.method, 'GET')
assert.equal(calls[0]?.path, '/api/v1/player/media-entries/7/stream?check=1')
assert.equal(calls[0]?.accessToken, token)
reply = { status: 200, body: { code: 0, data: { version: 1, ready: true, http_read_profile: 'bounded_ranges_v1' } } }
assert.deepEqual(await source.getStreamRequest(request), { ...stream, httpReadProfile: 'bounded_ranges_v1' })
for (const http_read_profile of ['curl-max-request-size=1', 'future_profile', null, {}, 1]) {
  reply = { status: 200, body: { code: 0, data: { version: 1, ready: true, http_read_profile } } }
  await assert.rejects(source.getStreamRequest(request), /播放读取配置无效/)
}
reply = { status: 200, body: { code: 0, data: { version: 1, ready: true } } }
assert.equal((await source.getStreamRequest(request)).httpReadProfile, undefined, 'hint cannot leak into another unhinted entry')
for (const [status, code, message] of [[503, 'quark_auth_expired', '夸克播放授权已失效，请管理员重新授权。'], [429, 'quark_rate_limited', '夸克请求受到限流，请在 2 秒后重试。'], [502, 'quark_response_invalid', '夸克返回的播放信息无效。']] as const) {
  reply = { status, body: { code: status * 100 + 1, message, data: { error_code: code } } }
  await assert.rejects(source.getStreamRequest(request), error => error instanceof ServerRequestError && error.status === status && error.code === code && error.message === message)
}
for (const data of [{ version: 2, ready: true }, { version: 1, ready: false }, null, { url: 'https://unused.invalid/video' }]) {
  reply = { status: 200, body: { code: 0, data } }
  await assert.rejects(source.getStreamRequest(request), /播放检查结果无效/)
}
reply = { status: 404, body: { code: 40401, message: 'Server 暂不支持播放检查', data: { error_code: 'NOT_FOUND' } } }
await assert.rejects(source.getStreamRequest(request), /暂不支持播放检查/)
reply = { status: 503, body: { code: 'legacy_unavailable', message: '暂不可用', data: {} } }
await assert.rejects(source.getStreamRequest(request), error => error instanceof ServerRequestError && error.code === 'legacy_unavailable')
assert.ok(calls.every(call => call.path.endsWith('?check=1')), 'never request media or silently fall back after failed check')
delay = true
const pending = source.getStreamRequest(request)
await started
source.destroy()
finish!({ status: 200, body: { code: 0, data: { version: 1, ready: true } } })
await assert.rejects(pending, /连接已变化/)
const count = calls.length
await assert.rejects(source.getStreamRequest(request), /连接已变化/)
assert.equal(calls.length, count)
legacy.destroy()
console.log('Playback check: optional capability, safe typed errors, unchanged stream URL, malformed/failure rejection and retired-source fence passed.')
