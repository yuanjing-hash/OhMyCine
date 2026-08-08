import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { safeMediaName } from '../src/services/danmaku/client'
import { parseDanmakuComments, parseDanmakuMatches } from '../src/services/danmaku/parser'
import { sanitizeDanmakuSettings } from '../src/services/danmaku/settings'

const root = resolve(import.meta.dirname, '..')
const read = (path: string) => readFileSync(resolve(root, path), 'utf8')

const matches = parseDanmakuMatches({ success: true, matches: [{ episodeId: 123, animeId: 9, animeTitle: '作品', episodeTitle: '第1话', shift: 1.5 }] })
assert.equal(matches.length, 1)
assert.equal(matches[0].episodeId, 123)

const comments = parseDanmakuComments({ comments: [
  { cid: 1, p: '12.5,1,16777215,user', m: '滚动' },
  { cid: 2, p: '20,4,16711680,user', m: '底部' },
  { cid: 3, p: '30,5,255,user', m: '顶部' },
  { cid: 4, p: 'bad,7,0,user', m: '无效' },
] }, 1.5)
assert.deepEqual(comments.map(item => [item.time, item.mode, item.color]), [
  [14, 'scroll', '#ffffff'], [21.5, 'bottom', '#ff0000'], [31.5, 'top', '#0000ff'],
])

assert.equal(safeMediaName('/mnt/media/Show.S01E01.mkv'), 'Show.S01E01.mkv')
assert.equal(safeMediaName('https://server/video?api_key=secret'), '未命名影片')
const sanitized = sanitizeDanmakuSettings({ opacity: 99, speed: -1, displayArea: 0.33, blockKeywords: ['剧透', '剧透'] })
assert.equal(sanitized.opacity, 1)
assert.equal(sanitized.speed, 0.5)
assert.equal(sanitized.displayArea, 0.75)
assert.deepEqual(sanitized.blockKeywords, ['剧透'])

const rust = read('src-tauri/src/commands/danmaku.rs')
assert.match(rust, /MAX_RESPONSE_BYTES/)
assert.match(rust, /redirect\(redirect::Policy::none\(\)\)/)
assert.match(rust, /X-Signature/)
assert.match(rust, /拒绝了不安全跳转/)
assert.doesNotMatch(rust, /mobile_proxy/)

const player = read('src/views/PlayerView.vue')
assert.match(player, /<DanmakuOverlay/)
assert.match(player, /loadDanmakuForMedia/)
assert.doesNotMatch(player, /danmaku.*route\.query/i)

const desktop = read('src/components/player/PlayerControls.vue')
const mobile = read('src/components/player/MobilePlayerControls.vue')
for (const source of [desktop, mobile]) {
  assert.match(source, /toggleDanmaku/)
  assert.match(source, /DanmakuSettingsContent/)
}

const mobileProxy = read('src-tauri/src/mpv/mobile_proxy.rs')
assert.match(mobileProxy, /AndroidStreamProxyState/)
assert.match(mobileProxy, /redirect/)

console.log('Danmaku API parsing, privacy boundary, desktop/mobile controls, and Android 302 isolation verified.')
